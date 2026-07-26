package codexbridge

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	maxMediaCount         = 9
	maxMediaBytes         = 20 * 1024 * 1024
	maxTotalMediaBytes    = 50 * 1024 * 1024
	maxConcurrentDownload = 4
)

type MaterializedMedia struct {
	Kind      string
	Path      string
	MIMEType  string
	SizeBytes int
}

type MaterializedBatch struct {
	Directory string
	Items     []MaterializedMedia
	once      sync.Once
}

func (batch *MaterializedBatch) Cleanup() {
	batch.once.Do(func() { _ = os.RemoveAll(batch.Directory) })
}

type MediaStore struct {
	Root              string
	MaxMediaCount     int
	MaxMediaBytes     int
	MaxTotalBytes     int
	Timeout           time.Duration
	Attempts          int
	AllowPrivateHosts bool
	Resolver          *net.Resolver
	HTTPClient        *http.Client
	semaphore         chan struct{}
}

func NewMediaStore(root string) *MediaStore {
	return &MediaStore{
		Root: root, MaxMediaCount: maxMediaCount, MaxMediaBytes: maxMediaBytes,
		MaxTotalBytes: maxTotalMediaBytes, Timeout: 30 * time.Second, Attempts: 3,
		Resolver: net.DefaultResolver, semaphore: make(chan struct{}, maxConcurrentDownload),
	}
}

func (store *MediaStore) Prepare() error {
	if err := os.MkdirAll(store.Root, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(store.Root, 0o700); err != nil {
		return err
	}
	cutoff := time.Now().Add(-24 * time.Hour)
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

func (store *MediaStore) Materialize(ctx context.Context, sources []IncomingMedia) (*MaterializedBatch, error) {
	select {
	case store.semaphore <- struct{}{}:
		defer func() { <-store.semaphore }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("cannot materialize an empty media list")
	}
	limit := store.MaxMediaCount
	if limit == 0 {
		limit = maxMediaCount
	}
	if len(sources) > limit {
		return nil, fmt.Errorf("message contains more than %d media items", limit)
	}
	if err := store.Prepare(); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(store.Root, "message-")
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(directory, 0o700)
	batch := &MaterializedBatch{Directory: directory}
	ok := false
	defer func() {
		if !ok {
			batch.Cleanup()
		}
	}()
	total := 0
	for index, source := range sources {
		data, mimeType, extension, err := store.download(ctx, source.URL)
		if err != nil {
			return nil, err
		}
		total += len(data)
		totalLimit := store.MaxTotalBytes
		if totalLimit == 0 {
			totalLimit = maxTotalMediaBytes
		}
		if total > totalLimit {
			return nil, fmt.Errorf("message media exceeds the %d-byte total limit", totalLimit)
		}
		path := filepath.Join(directory, fmt.Sprintf("%d%s", index+1, extension))
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return nil, err
		}
		absolute, _ := filepath.Abs(path)
		batch.Items = append(batch.Items, MaterializedMedia{
			Kind: source.Kind, Path: absolute, MIMEType: mimeType, SizeBytes: len(data),
		})
	}
	ok = true
	return batch, nil
}

func SafeURLForLog(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parsed.RawQuery, parsed.Fragment, parsed.RawFragment = "", "", ""
	return parsed.String()
}

func (store *MediaStore) validateHTTPSURL(ctx context.Context, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return fmt.Errorf("media URLs must be absolute https:// URLs without credentials")
	}
	resolver := store.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addresses, err := resolver.LookupIPAddr(ctx, parsed.Hostname())
	if err != nil {
		return fmt.Errorf("could not resolve media host %s", parsed.Hostname())
	}
	if len(addresses) == 0 {
		return fmt.Errorf("media host %s did not resolve", parsed.Hostname())
	}
	if store.AllowPrivateHosts {
		return nil
	}
	for _, address := range addresses {
		if !isPublicIP(address.IP) {
			return fmt.Errorf("media URL resolved to a non-public address")
		}
	}
	return nil
}

func isPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	reserved := []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
		"2001:db8::/32", "2001:10::/28", "fc00::/7", "fe80::/10", "ff00::/8",
	}
	for _, raw := range reserved {
		_, network, _ := net.ParseCIDR(raw)
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func (store *MediaStore) download(ctx context.Context, rawURL string) ([]byte, string, string, error) {
	if err := store.validateHTTPSURL(ctx, rawURL); err != nil {
		return nil, "", "", err
	}
	attempts := store.Attempts
	if attempts == 0 {
		attempts = 3
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		data, mime, extension, retryable, err := store.downloadOnce(ctx, rawURL)
		if err == nil {
			return data, mime, extension, nil
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
				return nil, "", "", err
			}
		}
	}
	return nil, "", "", fmt.Errorf("could not download media from %s: %w", SafeURLForLog(rawURL), lastErr)
}

func (store *MediaStore) downloadOnce(ctx context.Context, rawURL string) ([]byte, string, string, bool, error) {
	timeout := store.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, _ := http.NewRequestWithContext(requestCtx, http.MethodGet, rawURL, nil)
	request.Header.Set("Accept", "image/png,image/jpeg,image/gif,image/webp")
	request.Header.Set("User-Agent", "agenrena-codex-bridge/"+Version)
	httpClient := store.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return store.validateHTTPSURL(req.Context(), req.URL.String())
			},
		}
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, "", "", true, err
	}
	defer response.Body.Close()
	if err := store.validateHTTPSURL(requestCtx, response.Request.URL.String()); err != nil {
		return nil, "", "", false, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", "", response.StatusCode == 429 || response.StatusCode >= 500, fmt.Errorf("media download returned HTTP %d", response.StatusCode)
	}
	limit := store.MaxMediaBytes
	if limit == 0 {
		limit = maxMediaBytes
	}
	if response.ContentLength > int64(limit) {
		return nil, "", "", false, fmt.Errorf("media exceeds the %d-byte size limit", limit)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if err != nil {
		return nil, "", "", true, err
	}
	if len(data) > limit {
		return nil, "", "", false, fmt.Errorf("media exceeds the %d-byte size limit", limit)
	}
	mime, extension, ok := detectedImageType(data)
	if !ok {
		return nil, "", "", false, fmt.Errorf("downloaded media is not a supported image")
	}
	return data, mime, extension, false, nil
}

func detectedImageType(data []byte) (string, string, bool) {
	prefix := func(value string) bool { return len(data) >= len(value) && string(data[:len(value)]) == value }
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
