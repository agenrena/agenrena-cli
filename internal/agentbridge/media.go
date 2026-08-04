package agentbridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultMaxMediaCount      = 9
	defaultMaxMediaBytes      = 20 * 1024 * 1024
	defaultMaxTotalMediaBytes = 50 * 1024 * 1024
)

type MediaStore struct {
	Root              string
	MaxMediaCount     int
	MaxMediaBytes     int
	MaxTotalBytes     int
	Timeout           time.Duration
	Attempts          int
	Retention         time.Duration
	AllowPrivateHosts bool
	AllowHTTP         bool
	Resolver          *net.Resolver
	HTTPClient        *http.Client
}

func NewMediaStore(root string) *MediaStore {
	return &MediaStore{
		Root: root, MaxMediaCount: defaultMaxMediaCount,
		MaxMediaBytes: defaultMaxMediaBytes, MaxTotalBytes: defaultMaxTotalMediaBytes,
		Timeout: 30 * time.Second, Attempts: 3, Retention: 24 * time.Hour,
		Resolver: net.DefaultResolver,
	}
}

func (store *MediaStore) Prepare() error {
	if strings.TrimSpace(store.Root) == "" {
		return fmt.Errorf("media store root is empty")
	}
	if err := os.MkdirAll(store.Root, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(store.Root, 0o700); err != nil {
		return err
	}
	retention := store.Retention
	if retention <= 0 {
		retention = 24 * time.Hour
	}
	cutoff := time.Now().Add(-retention)
	entries, _ := os.ReadDir(store.Root)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "message-") {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(store.Root, entry.Name()))
		}
	}
	return nil
}

func (store *MediaStore) Materialize(ctx context.Context, messageID string, sources []rawInboundMedia) ([]MaterializedMedia, error) {
	if len(sources) == 0 {
		return []MaterializedMedia{}, nil
	}
	limit := store.MaxMediaCount
	if limit <= 0 {
		limit = defaultMaxMediaCount
	}
	if len(sources) > limit {
		return nil, bridgeError("MEDIA_INVALID", fmt.Sprintf("message contains more than %d media items", limit), false)
	}
	if err := store.Prepare(); err != nil {
		return nil, wrapBridgeError("MEDIA_INVALID", "could not prepare the bridge media directory", false, err)
	}
	digest := sha256.Sum256([]byte(messageID + time.Now().UTC().Format(time.RFC3339Nano)))
	directory := filepath.Join(store.Root, "message-"+hex.EncodeToString(digest[:8]))
	if err := os.Mkdir(directory, 0o700); err != nil {
		return nil, wrapBridgeError("MEDIA_INVALID", "could not create a bridge media directory", false, err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(directory)
		}
	}()

	total := 0
	result := make([]MaterializedMedia, 0, len(sources))
	for index, source := range sources {
		data, mimeType, extension, err := store.Download(ctx, source.URL)
		if err != nil {
			return nil, err
		}
		total += len(data)
		totalLimit := store.MaxTotalBytes
		if totalLimit <= 0 {
			totalLimit = defaultMaxTotalMediaBytes
		}
		if total > totalLimit {
			return nil, bridgeError("MEDIA_INVALID", fmt.Sprintf("message media exceeds the %d-byte total limit", totalLimit), false)
		}
		path := filepath.Join(directory, fmt.Sprintf("%d%s", index+1, extension))
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return nil, wrapBridgeError("MEDIA_INVALID", "could not write inbound media", false, err)
		}
		absolute, _ := filepath.Abs(path)
		width, height := imageDimensions(data)
		if source.Width > 0 {
			width = source.Width
		}
		if source.Height > 0 {
			height = source.Height
		}
		kind := strings.TrimSpace(source.Kind)
		if kind == "" {
			kind = "image"
		}
		result = append(result, MaterializedMedia{
			Kind: kind, Path: absolute, MIMEType: mimeType, SizeBytes: len(data),
			Width: width, Height: height,
		})
	}
	ok = true
	return result, nil
}

func (store *MediaStore) Download(ctx context.Context, rawURL string) ([]byte, string, string, error) {
	if err := store.validateURL(ctx, rawURL); err != nil {
		return nil, "", "", err
	}
	attempts := store.Attempts
	if attempts <= 0 {
		attempts = 3
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		data, mimeType, extension, retryable, err := store.downloadOnce(ctx, rawURL)
		if err == nil {
			return data, mimeType, extension, nil
		}
		lastErr = err
		if !retryable {
			return nil, "", "", err
		}
		if attempt < attempts {
			delay := time.Duration(1<<(attempt-1)) * time.Second
			if delay > 4*time.Second {
				delay = 4 * time.Second
			}
			if err := waitContext(ctx, delay); err != nil {
				return nil, "", "", wrapBridgeError("NETWORK_ERROR", "media download was cancelled", true, err)
			}
		}
	}
	return nil, "", "", wrapBridgeError("NETWORK_ERROR", "could not download media", true, lastErr)
}

func (store *MediaStore) validateURL(ctx context.Context, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil {
		return bridgeError("MEDIA_INVALID", "media URL must be absolute and must not contain credentials", false)
	}
	if parsed.Scheme != "https" && !(store.AllowHTTP && parsed.Scheme == "http") {
		return bridgeError("MEDIA_INVALID", "media URL must use HTTPS", false)
	}
	resolver := store.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addresses, err := resolver.LookupIPAddr(ctx, parsed.Hostname())
	if err != nil || len(addresses) == 0 {
		return bridgeError("MEDIA_INVALID", "media host could not be resolved", false)
	}
	if store.AllowPrivateHosts {
		return nil
	}
	for _, address := range addresses {
		if !isPublicIP(address.IP) {
			return bridgeError("MEDIA_INVALID", "media URL resolved to a non-public address", false)
		}
	}
	return nil
}

func (store *MediaStore) downloadOnce(ctx context.Context, rawURL string) ([]byte, string, string, bool, error) {
	timeout := store.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, _ := http.NewRequestWithContext(requestCtx, http.MethodGet, rawURL, nil)
	request.Header.Set("Accept", "image/png,image/jpeg,image/gif,image/webp")
	request.Header.Set("User-Agent", "agenrena-agent-bridge")
	client := store.HTTPClient
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.DialContext = store.dialValidated
		client = &http.Client{
			Timeout:   timeout,
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return store.validateURL(req.Context(), req.URL.String())
			},
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, "", "", true, wrapBridgeError("NETWORK_ERROR", "media download failed", true, err)
	}
	defer response.Body.Close()
	if err := store.validateURL(requestCtx, response.Request.URL.String()); err != nil {
		return nil, "", "", false, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		retryable := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		return nil, "", "", retryable, bridgeError("MEDIA_INVALID", fmt.Sprintf("media download returned HTTP %d", response.StatusCode), retryable)
	}
	limit := store.MaxMediaBytes
	if limit <= 0 {
		limit = defaultMaxMediaBytes
	}
	if response.ContentLength > int64(limit) {
		return nil, "", "", false, bridgeError("MEDIA_INVALID", fmt.Sprintf("media exceeds the %d-byte size limit", limit), false)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if err != nil {
		return nil, "", "", true, wrapBridgeError("NETWORK_ERROR", "could not read downloaded media", true, err)
	}
	if len(data) > limit {
		return nil, "", "", false, bridgeError("MEDIA_INVALID", fmt.Sprintf("media exceeds the %d-byte size limit", limit), false)
	}
	mimeType, extension, ok := detectedImageType(data)
	if !ok {
		return nil, "", "", false, bridgeError("MEDIA_INVALID", "downloaded media is not a supported image", false)
	}
	return data, mimeType, extension, false, nil
}

func (store *MediaStore) dialValidated(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	resolver := store.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addresses, err := resolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, fmt.Errorf("media host could not be resolved")
	}
	var lastErr error
	dialer := &net.Dialer{}
	for _, candidate := range addresses {
		if !store.AllowPrivateHosts && !isPublicIP(candidate.IP) {
			lastErr = fmt.Errorf("media host resolved to a non-public address")
			continue
		}
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("media host has no permitted address")
	}
	return nil, lastErr
}

func SafeURLForLog(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "<invalid-url>"
	}
	parsed.User = nil
	parsed.RawQuery, parsed.Fragment, parsed.RawFragment = "", "", ""
	return parsed.String()
}

func detectedImageType(data []byte) (string, string, bool) {
	prefix := func(value string) bool {
		return len(data) >= len(value) && string(data[:len(value)]) == value
	}
	switch {
	case prefix("\x89PNG\r\n\x1a\n"):
		return "image/png", ".png", true
	case prefix("\xff\xd8\xff"):
		return "image/jpeg", ".jpg", true
	case prefix("GIF87a"), prefix("GIF89a"):
		return "image/gif", ".gif", true
	case len(data) >= 12 && prefix("RIFF") && string(data[8:12]) == "WEBP":
		return "image/webp", ".webp", true
	default:
		return "", "", false
	}
}

func imageDimensions(data []byte) (int, int) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return config.Width, config.Height
}

func isPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	reserved := []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24",
		"224.0.0.0/4", "240.0.0.0/4", "2001:db8::/32", "2001:10::/28",
		"fc00::/7", "fe80::/10", "ff00::/8",
	}
	for _, raw := range reserved {
		_, network, _ := net.ParseCIDR(raw)
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
