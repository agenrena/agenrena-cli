package codexbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type AgenrenaAPIError struct {
	Message    string
	Status     int
	RetryAfter time.Duration
}

func (err *AgenrenaAPIError) Error() string { return err.Message }
func (err *AgenrenaAPIError) Retryable() bool {
	return err.Status == 0 || err.Status == http.StatusTooManyRequests || err.Status >= 500
}

type AgenrenaAPIClient struct {
	APIBase     string
	APIKey      string
	UserAgent   string
	HTTPClient  *http.Client
	MaxAttempts int
}

func (client *AgenrenaAPIClient) SendReply(ctx context.Context, reply PendingReply) (map[string]any, error) {
	attempts := client.MaxAttempts
	if attempts == 0 {
		attempts = 4
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		result, err := client.sendReplyOnce(ctx, reply)
		if err == nil {
			return result, nil
		}
		apiErr, ok := err.(*AgenrenaAPIError)
		if !ok || !apiErr.Retryable() || attempt == attempts {
			return nil, err
		}
		delay := apiErr.RetryAfter
		if delay == 0 {
			delay = time.Duration(1<<(attempt-1)) * time.Second
			if delay > 8*time.Second {
				delay = 8 * time.Second
			}
		}
		log.Printf("temporary Agenrena reply API failure; retrying in %.1fs (attempt %d/%d)", delay.Seconds(), attempt, attempts)
		if err := waitContext(ctx, delay); err != nil {
			return nil, err
		}
	}
	panic("unreachable")
}

func (client *AgenrenaAPIClient) sendReplyOnce(ctx context.Context, reply PendingReply) (map[string]any, error) {
	body, _ := json.Marshal(map[string]any{
		"source": "agenrena", "conversation_id": reply.ConversationID,
		"text": reply.Text, "message_id": reply.OutboundMessageID(),
		"reply_to_message_id": reply.InboundMessageID,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, client.APIBase+"/channels/messages/send/", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+client.APIKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", client.UserAgent)
	httpClient := client.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	response, err := httpClient.Do(req)
	if err != nil {
		return nil, &AgenrenaAPIError{Message: "could not reach the Agenrena reply API: " + err.Error()}
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		retryAfter := time.Duration(0)
		if seconds, err := strconv.ParseFloat(response.Header.Get("Retry-After"), 64); err == nil && seconds >= 0 {
			retryAfter = time.Duration(seconds * float64(time.Second))
		}
		detail := string(bytes.TrimSpace(raw))
		if detail == "" {
			detail = response.Status
		}
		return nil, &AgenrenaAPIError{
			Message: fmt.Sprintf("Agenrena reply API returned HTTP %d: %s", response.StatusCode, detail),
			Status:  response.StatusCode, RetryAfter: retryAfter,
		}
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if object, ok := result.(map[string]any); ok {
		return object, nil
	}
	return map[string]any{"result": result}, nil
}

func AuthenticatedWSURL(wsURL, apiKey string) (string, error) {
	parsed, err := url.Parse(wsURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("token", apiKey)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

type AgenrenaWebSocketClient struct {
	WSURL        string
	APIKey       string
	MaxBackoff   time.Duration
	PingInterval time.Duration
	PingTimeout  time.Duration
}

func (client *AgenrenaWebSocketClient) Next(ctx context.Context) (map[string]any, error) {
	return nil, fmt.Errorf("Next must be called through Stream")
}

func (client *AgenrenaWebSocketClient) Stream(ctx context.Context, output chan<- map[string]any) error {
	backoff := time.Second
	maxBackoff := client.MaxBackoff
	if maxBackoff == 0 {
		maxBackoff = 30 * time.Second
	}
	pingInterval := client.PingInterval
	if pingInterval == 0 {
		pingInterval = 20 * time.Second
	}
	pingTimeout := client.PingTimeout
	if pingTimeout == 0 {
		pingTimeout = 20 * time.Second
	}
	wsURL, err := AuthenticatedWSURL(client.WSURL, client.APIKey)
	if err != nil {
		return err
	}
	for ctx.Err() == nil {
		socket, err := DialWebSocket(ctx, wsURL, 2*1024*1024)
		if err == nil {
			log.Printf("connected to the Agenrena Agent WebSocket")
			backoff = time.Second
			err = client.consume(ctx, socket, output, pingInterval, pingTimeout)
			_ = socket.Close()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		jitterMax := minDuration(time.Second, backoff/5)
		jitter := time.Duration(0)
		if jitterMax > 0 {
			jitter = time.Duration(rand.Int63n(int64(jitterMax)))
		}
		delay := minDuration(maxBackoff, backoff+jitter)
		log.Printf("Agenrena WebSocket disconnected (%T); reconnecting in %.1fs", err, delay.Seconds())
		if err := waitContext(ctx, delay); err != nil {
			return err
		}
		backoff = minDuration(maxBackoff, backoff*2)
	}
	return ctx.Err()
}

func (client *AgenrenaWebSocketClient) consume(ctx context.Context, socket *WebSocketConnection, output chan<- map[string]any, pingInterval, pingTimeout time.Duration) error {
	for {
		readCtx, cancel := context.WithTimeout(ctx, pingInterval)
		raw, err := socket.ReceiveEvent(readCtx)
		cancel()
		if err == context.DeadlineExceeded {
			if err := socket.Ping(ctx); err != nil {
				return err
			}
			readCtx, cancel = context.WithTimeout(ctx, pingTimeout)
			raw, err = socket.ReceiveEvent(readCtx)
			cancel()
		}
		if err != nil {
			return err
		}
		if raw == nil {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil || payload == nil {
			log.Printf("ignored a non-JSON or non-object Agenrena WebSocket event")
			continue
		}
		select {
		case output <- payload:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
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

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
