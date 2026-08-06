package agentbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Config struct {
	APIBase           string
	WSURL             string
	StateDir          string
	ServerVersion     string
	UserAgent         string
	APIKeyLoader      func() (string, error)
	HTTPClient        *http.Client
	MediaHTTPClient   *http.Client
	AllowPrivateMedia bool
	AllowHTTPMedia    bool
	MaxBackoff        time.Duration
	PingInterval      time.Duration
	PingTimeout       time.Duration
	WebSocketDialer   func(context.Context, string, int, string) (*WebSocketConnection, error)
}

type Service struct {
	config Config

	mu          sync.Mutex
	initialized bool
	closed      bool
	api         *APIClient
	media       *MediaStore
	lock        *CredentialLock
	socket      *WebSocketConnection
	ctx         context.Context
	cancel      context.CancelFunc
	notify      func(Event)
	fatal       chan *RPCError
	wg          sync.WaitGroup
}

func NewService(config Config) *Service {
	return &Service{config: config, fatal: make(chan *RPCError, 1)}
}

func (service *Service) Initialize(ctx context.Context, params InitializeParams, notify func(Event)) (InitializeResult, error) {
	service.mu.Lock()
	if service.initialized {
		service.mu.Unlock()
		return InitializeResult{}, bridgeError("ALREADY_INITIALIZED", "bridge is already initialized", false)
	}
	if service.closed {
		service.mu.Unlock()
		return InitializeResult{}, bridgeError("INTERNAL_ERROR", "bridge is closed", false)
	}
	service.mu.Unlock()

	if params.ProtocolVersion != ProtocolVersion {
		return InitializeResult{}, bridgeError("PROTOCOL_UNSUPPORTED", fmt.Sprintf("protocol version %d is unsupported", params.ProtocolVersion), false)
	}
	if strings.TrimSpace(params.ClientInfo.Name) == "" || strings.TrimSpace(params.ClientInfo.Version) == "" {
		return InitializeResult{}, bridgeError("MESSAGE_INVALID", "clientInfo name and version are required", false)
	}
	if strings.TrimSpace(params.Agent.Type) == "" {
		return InitializeResult{}, bridgeError("MESSAGE_INVALID", "agent type is required", false)
	}
	if service.config.APIKeyLoader == nil {
		return InitializeResult{}, bridgeError("AUTH_REQUIRED", "Agenrena credential loader is unavailable", false)
	}
	apiKey, err := service.config.APIKeyLoader()
	if err != nil || strings.TrimSpace(apiKey) == "" {
		return InitializeResult{}, bridgeError("AUTH_REQUIRED", "not logged in; run `agenrena auth login`", false)
	}

	credentialLock, err := AcquireCredentialLock(service.config.StateDir, apiKey)
	if err != nil {
		return InitializeResult{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = credentialLock.Close()
		}
	}()

	media := NewMediaStore(filepath.Join(service.config.StateDir, "media"))
	media.AllowPrivateHosts = service.config.AllowPrivateMedia
	media.AllowHTTP = service.config.AllowHTTPMedia
	media.HTTPClient = service.config.MediaHTTPClient
	if err := media.Prepare(); err != nil {
		return InitializeResult{}, wrapBridgeError("MEDIA_INVALID", "could not prepare bridge media storage", false, err)
	}
	apiClient := &APIClient{
		BaseURL: strings.TrimRight(service.config.APIBase, "/"), APIKey: apiKey,
		UserAgent: service.config.UserAgent, HTTPClient: service.config.HTTPClient,
		MediaStore: media,
	}
	warnings := []string{}
	if err := apiClient.RegisterAgent(ctx, params.Agent); err != nil {
		if isAuthenticationError(err) {
			return InitializeResult{}, bridgeError("AUTH_INVALID", "Agenrena authentication was rejected", false)
		}
		warnings = append(warnings, "could not register agent metadata: "+err.Error())
	}

	if notify != nil {
		notify(Event{Method: "bridge/status", Params: Status{State: "connecting"}})
	}
	authenticatedURL, err := AuthenticatedWebSocketURL(service.config.WSURL, apiKey)
	if err != nil {
		return InitializeResult{}, bridgeError("INTERNAL_ERROR", err.Error(), false)
	}
	connectCtx, connectCancel := context.WithTimeout(ctx, 20*time.Second)
	socket, err := service.dialWebSocket(connectCtx, authenticatedURL)
	connectCancel()
	if err != nil {
		if isAuthenticationError(err) {
			return InitializeResult{}, bridgeError("AUTH_INVALID", "Agenrena WebSocket authentication was rejected", false)
		}
		return InitializeResult{}, wrapBridgeError("NETWORK_ERROR", "could not connect to the Agenrena WebSocket", true, err)
	}

	serviceCtx, cancel := context.WithCancel(ctx)
	service.mu.Lock()
	service.initialized = true
	service.api = apiClient
	service.media = media
	service.lock = credentialLock
	service.socket = socket
	service.ctx = serviceCtx
	service.cancel = cancel
	service.notify = notify
	service.mu.Unlock()
	cleanup = false
	if notify != nil {
		notify(Event{Method: "bridge/status", Params: Status{State: "connected"}})
	}
	service.wg.Add(1)
	go service.connectionLoop(authenticatedURL, socket)

	return InitializeResult{
		ProtocolVersion: ProtocolVersion,
		ServerInfo:      ServerInfo{Name: "agenrena-agent-bridge", Version: service.config.ServerVersion},
		State:           "connected",
		Capabilities: ServerCapabilities{
			InboundMedia: true, OutboundMedia: true,
			MessageTypes: []string{"text", "image", "sticker"},
			Handoff:      true,
		},
		Warnings: warnings,
	}, nil
}

func (service *Service) Send(ctx context.Context, params SendParams) (SendResult, error) {
	service.mu.Lock()
	apiClient := service.api
	initialized := service.initialized && !service.closed
	service.mu.Unlock()
	if !initialized || apiClient == nil {
		return SendResult{}, bridgeError("NOT_INITIALIZED", "bridge is not initialized", false)
	}
	return apiClient.SendMessage(ctx, params)
}

func (service *Service) Handoff(ctx context.Context, params HandoffParams) (HandoffResult, error) {
	service.mu.Lock()
	apiClient := service.api
	initialized := service.initialized && !service.closed
	service.mu.Unlock()
	if !initialized || apiClient == nil {
		return HandoffResult{}, bridgeError("NOT_INITIALIZED", "bridge is not initialized", false)
	}
	return apiClient.Handoff(ctx, params)
}

func (service *Service) Fatal() <-chan *RPCError { return service.fatal }

func (service *Service) Close() error {
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return nil
	}
	service.closed = true
	cancel, socket, credentialLock := service.cancel, service.socket, service.lock
	service.cancel, service.socket, service.lock = nil, nil, nil
	notify := service.notify
	service.mu.Unlock()
	if notify != nil {
		notify(Event{Method: "bridge/status", Params: Status{State: "stopping"}})
	}
	if cancel != nil {
		cancel()
	}
	if socket != nil {
		_ = socket.Close()
	}
	service.wg.Wait()
	if credentialLock != nil {
		return credentialLock.Close()
	}
	return nil
}

func (service *Service) connectionLoop(authenticatedURL string, initial *WebSocketConnection) {
	defer service.wg.Done()
	socket := initial
	attempt := 0
	for {
		_ = service.consumeConnection(socket)
		_ = socket.Close()
		service.clearSocket(socket)
		if service.contextDone() {
			return
		}
		attempt++
		backoff := service.reconnectDelay(attempt)
		service.emit(Event{Method: "bridge/status", Params: Status{
			State: "reconnecting", Attempt: attempt, RetryInMS: backoff.Milliseconds(),
		}})
		if err := waitContext(service.ctx, backoff); err != nil {
			return
		}
		connectCtx, cancel := context.WithTimeout(service.ctx, 20*time.Second)
		next, dialErr := service.dialWebSocket(connectCtx, authenticatedURL)
		cancel()
		if dialErr != nil {
			if isAuthenticationError(dialErr) {
				fatal := bridgeError("AUTH_INVALID", "Agenrena WebSocket authentication is no longer valid", false)
				service.emit(Event{Method: "bridge/status", Params: Status{State: "fatal", Error: fatal}})
				select {
				case service.fatal <- fatal:
				default:
				}
				return
			}
			log.Printf("Agenrena Agent Bridge WebSocket reconnect failed: %v", dialErr)
			continue
		}
		attempt = 0
		socket = next
		service.setSocket(socket)
		service.emit(Event{Method: "bridge/status", Params: Status{State: "connected"}})
	}
}

func (service *Service) dialWebSocket(ctx context.Context, rawURL string) (*WebSocketConnection, error) {
	if service.config.WebSocketDialer != nil {
		return service.config.WebSocketDialer(ctx, rawURL, 2*1024*1024, service.config.UserAgent)
	}
	return DialWebSocket(ctx, rawURL, 2*1024*1024, service.config.UserAgent)
}

func (service *Service) consumeConnection(socket *WebSocketConnection) error {
	pingInterval := service.config.PingInterval
	if pingInterval <= 0 {
		pingInterval = 20 * time.Second
	}
	pingTimeout := service.config.PingTimeout
	if pingTimeout <= 0 {
		pingTimeout = 20 * time.Second
	}
	pingDone := make(chan struct{})
	defer close(pingDone)
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				pingCtx, cancel := context.WithTimeout(service.ctx, pingTimeout)
				err := socket.Ping(pingCtx)
				cancel()
				if err != nil {
					_ = socket.Close()
					return
				}
			case <-pingDone:
				return
			case <-service.ctx.Done():
				return
			}
		}
	}()
	for {
		readCtx, cancel := context.WithTimeout(service.ctx, pingInterval+pingTimeout)
		raw, err := socket.ReceiveEvent(readCtx)
		cancel()
		if err != nil {
			return err
		}
		if raw == nil {
			continue
		}
		var payload map[string]any
		if json.Unmarshal(raw, &payload) != nil || payload == nil {
			log.Printf("ignored a non-JSON or non-object Agenrena WebSocket event")
			continue
		}
		message, err := service.normalizeIncoming(service.ctx, payload)
		if err != nil {
			log.Printf("ignored an invalid Agenrena WebSocket event: %v", err)
			continue
		}
		if message != nil {
			service.emit(Event{Method: "messages/received", Params: message})
		}
	}
}

func (service *Service) normalizeIncoming(ctx context.Context, payload map[string]any) (*IncomingMessage, error) {
	messageType := valueString(payload["message_type"])
	if messageType != "text" && messageType != "image" && messageType != "sticker" {
		return nil, nil
	}
	messageID := valueString(payload["id"])
	if messageID == "" {
		return nil, fmt.Errorf("message id is missing")
	}
	route := Route{
		ChatID: valueString(payload["chat_id"]), ConversationID: valueString(payload["conversation_id"]),
		Source: valueString(payload["source"]), Version: 1,
	}
	routeValue, err := EncodeRoute(route)
	if err != nil {
		return nil, err
	}
	senderValue, _ := payload["sender"].(map[string]any)
	senderID := valueString(senderValue["id"])
	if senderID == "" {
		return nil, fmt.Errorf("sender id is missing")
	}
	senderName := firstNonEmpty(valueString(senderValue["display_name"]), valueString(senderValue["name"]))

	rawMedia := inboundMediaFromPayload(payload, messageType)
	media, err := service.media.Materialize(ctx, messageID, rawMedia)
	if err != nil {
		return nil, err
	}
	contextItems, err := service.normalizeContext(ctx, messageID, payload["context"])
	if err != nil {
		return nil, err
	}
	text := valueString(payload["text"])
	if text == "" && len(media) == 0 {
		return nil, nil
	}
	var replyTo any
	if value := valueString(payload["reply_to_message_id"]); value != "" {
		replyTo = value
	}
	return &IncomingMessage{
		ID: messageID, Route: routeValue, MessageType: messageType,
		Sender: Sender{ID: senderID, Name: senderName}, Text: text,
		Media: media, ReplyTo: replyTo, Context: contextItems,
		CreatedAt: valueString(payload["created_at"]),
	}, nil
}

func inboundMediaFromPayload(payload map[string]any, messageType string) []rawInboundMedia {
	result := []rawInboundMedia{}
	if images, ok := payload["images"].([]any); ok {
		for _, value := range images {
			imageValue, ok := value.(map[string]any)
			if !ok || valueString(imageValue["url"]) == "" {
				continue
			}
			result = append(result, rawInboundMedia{
				Kind: "image", URL: valueString(imageValue["url"]), MIMEType: valueString(imageValue["mime_type"]),
				Width: intValue(imageValue["width"]), Height: intValue(imageValue["height"]),
			})
		}
	}
	if messageType == "sticker" {
		if sticker, ok := payload["sticker"].(map[string]any); ok {
			if imageURL := valueString(sticker["image_url"]); imageURL != "" {
				result = append(result, rawInboundMedia{Kind: "sticker", URL: imageURL, MIMEType: "image/png"})
			}
		}
	}
	return result
}

func (service *Service) normalizeContext(ctx context.Context, messageID string, raw any) ([]ContextItem, error) {
	contextValue, ok := raw.(map[string]any)
	if !ok {
		return []ContextItem{}, nil
	}
	values, ok := contextValue["items"].([]any)
	if !ok {
		return []ContextItem{}, nil
	}
	result := make([]ContextItem, 0, len(values))
	for index, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		metadata, _ := item["metadata"].(map[string]any)
		messageType := valueString(metadata["message_type"])
		if messageType == "" {
			messageType = "text"
		}
		mediaPayload := map[string]any{"images": item["media"]}
		mediaSources := inboundMediaFromPayload(mediaPayload, messageType)
		media, err := service.media.Materialize(ctx, fmt.Sprintf("%s-context-%d", messageID, index+1), mediaSources)
		if err != nil {
			return nil, err
		}
		result = append(result, ContextItem{
			Label: valueString(item["label"]), MessageType: messageType,
			Text:  firstNonEmpty(valueString(item["content"]), valueString(item["text"])),
			Media: media, Metadata: map[string]any{"message_type": messageType},
		})
	}
	return result, nil
}

func AuthenticatedWebSocketURL(rawURL, apiKey string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "ws" && parsed.Scheme != "wss") {
		return "", fmt.Errorf("WebSocket URL must be an absolute ws:// or wss:// URL")
	}
	query := parsed.Query()
	query.Set("token", apiKey)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func DeriveWebSocketURL(apiBase string) (string, error) {
	parsed, err := url.Parse(apiBase)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("Agenrena API base must be an absolute URL")
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		if !isLoopbackHost(parsed.Hostname()) {
			return "", fmt.Errorf("insecure Agenrena API base is only allowed for loopback hosts")
		}
		parsed.Scheme = "ws"
	default:
		return "", fmt.Errorf("Agenrena API base must use HTTP or HTTPS")
	}
	parsed.Path, parsed.RawPath, parsed.RawQuery, parsed.Fragment = "/ws/agent/events/", "", "", ""
	return parsed.String(), nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isAuthenticationError(err error) bool {
	if apiErr, ok := err.(*APIError); ok {
		return apiErr.Status == 401 || apiErr.Status == 403
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "http 401") || strings.Contains(text, "http 403")
}

func intValue(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		result, _ := typed.Int64()
		return int(result)
	default:
		return 0
	}
}

func (service *Service) emit(event Event) {
	service.mu.Lock()
	notify := service.notify
	service.mu.Unlock()
	if notify != nil {
		notify(event)
	}
}

func (service *Service) reconnectDelay(attempt int) time.Duration {
	maximum := service.config.MaxBackoff
	if maximum <= 0 {
		maximum = 30 * time.Second
	}
	delay := time.Second
	for index := 1; index < attempt && delay < maximum; index++ {
		delay *= 2
	}
	if delay > maximum {
		delay = maximum
	}
	jitterMax := delay / 5
	if jitterMax > time.Second {
		jitterMax = time.Second
	}
	if jitterMax > 0 {
		delay += time.Duration(rand.Int63n(int64(jitterMax)))
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

func (service *Service) contextDone() bool {
	service.mu.Lock()
	ctx := service.ctx
	service.mu.Unlock()
	return ctx == nil || ctx.Err() != nil
}

func (service *Service) setSocket(socket *WebSocketConnection) {
	service.mu.Lock()
	if !service.closed {
		service.socket = socket
	}
	service.mu.Unlock()
}

func (service *Service) clearSocket(socket *WebSocketConnection) {
	service.mu.Lock()
	if service.socket == socket {
		service.socket = nil
	}
	service.mu.Unlock()
}
