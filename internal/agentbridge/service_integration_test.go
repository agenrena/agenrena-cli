package agentbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestServiceCarriesImageMessageFromWebSocketToRESTReply(t *testing.T) {
	pngBytes := testPNG(t)
	var mu sync.Mutex
	var registered map[string]any
	var sent []map[string]any
	uploads := map[string][]byte{}
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Host + request.URL.Path {
		case "api.example/agents/me/":
			if request.Method != http.MethodPatch || request.Header.Get("Authorization") != "Bearer test-key" {
				return testHTTPResponse(request, http.StatusUnauthorized, `{"detail":"bad registration"}`, "application/json"), nil
			}
			registered = decodeTestJSONBody(t, request.Body)
			return testJSONResponse(request, http.StatusOK, map[string]any{}), nil
		case "1.1.1.1/media/inbound.png":
			return testHTTPResponseBytes(request, http.StatusOK, pngBytes, "image/png"), nil
		case "api.example/hub/media/presign/":
			return testJSONResponse(request, http.StatusOK, map[string]any{"media": []any{map[string]any{
				"id":                   "media-1",
				"image_upload_url":     "https://uploads.example/image",
				"thumbnail_upload_url": "https://uploads.example/thumbnail",
			}}}), nil
		case "uploads.example/image", "uploads.example/thumbnail":
			data, _ := io.ReadAll(request.Body)
			mu.Lock()
			uploads[request.URL.Path] = data
			mu.Unlock()
			return testHTTPResponse(request, http.StatusNoContent, "", ""), nil
		case "api.example/channels/messages/send/":
			mu.Lock()
			sent = append(sent, decodeTestJSONBody(t, request.Body))
			messageID := "outbound-text"
			if len(sent) == 2 {
				messageID = "outbound-image"
			}
			mu.Unlock()
			return testJSONResponse(request, http.StatusOK, map[string]any{"message_id": messageID}), nil
		default:
			return testHTTPResponse(request, http.StatusNotFound, "not found", "text/plain"), nil
		}
	})
	httpClient := &http.Client{Transport: transport}

	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	dialed := make(chan string, 1)
	service := NewService(Config{
		APIBase: "https://api.example", WSURL: "wss://api.example/ws/agent/events/", StateDir: t.TempDir(),
		ServerVersion: "test", UserAgent: "bridge-test",
		APIKeyLoader: func() (string, error) { return "test-key", nil },
		HTTPClient:   httpClient, MediaHTTPClient: httpClient,
		PingInterval: time.Hour, PingTimeout: time.Second,
		WebSocketDialer: func(_ context.Context, rawURL string, maxSize int, _ string) (*WebSocketConnection, error) {
			dialed <- rawURL
			return NewWebSocketConnection(clientConn, bufio.NewReader(clientConn), maxSize), nil
		},
	})
	defer service.Close()

	events := make(chan Event, 8)
	result, err := service.Initialize(context.Background(), InitializeParams{
		ProtocolVersion: ProtocolVersion,
		ClientInfo:      ClientInfo{Name: "hermes", Version: "1.0"},
		Agent: AgentInfo{Type: "hermes", SlashCommands: []SlashCommand{{
			Name: "help", Description: "Show help",
		}}},
		Capabilities: ClientCapabilities{InboundMedia: true, OutboundMedia: true},
	}, func(event Event) { events <- event })
	if err != nil {
		t.Fatal(err)
	}
	if authenticatedURL := <-dialed; !strings.Contains(authenticatedURL, "token=test-key") {
		t.Fatalf("dialed URL is not authenticated: %s", SafeURLForLog(authenticatedURL))
	}
	if result.State != "connected" || !result.Capabilities.InboundMedia || !result.Capabilities.OutboundMedia {
		t.Fatalf("initialize result=%+v", result)
	}
	if registered["agent_type"] != "hermes" {
		t.Fatalf("registered metadata=%v", registered)
	}

	payload, _ := json.Marshal(map[string]any{
		"id": "inbound-1", "source": "telegram", "chat_id": "chat-1",
		"conversation_id": "conversation-1", "message_type": "image", "text": "look",
		"sender": map[string]any{"id": "user-1", "display_name": "Alice"},
		"images": []any{map[string]any{
			"url": "https://1.1.1.1/media/inbound.png", "mime_type": "image/png", "width": 2, "height": 1,
		}},
	})
	go func() {
		writeExtendedServerFrame(serverConn, payload)
		_, _ = io.Copy(io.Discard, serverConn)
	}()
	incoming := waitForIncomingMessage(t, events)
	if incoming.ID != "inbound-1" || incoming.Text != "look" || len(incoming.Media) != 1 {
		t.Fatalf("incoming=%+v", incoming)
	}
	if incoming.Media[0].MIMEType != "image/png" || incoming.Media[0].Width != 2 || incoming.Media[0].Height != 1 {
		t.Fatalf("materialized media=%+v", incoming.Media[0])
	}
	data, err := os.ReadFile(incoming.Media[0].Path)
	if err != nil || !bytes.Equal(data, pngBytes) {
		t.Fatalf("materialized image mismatch err=%v", err)
	}
	route, err := DecodeRoute(incoming.Route)
	if err != nil || route.Source != "telegram" || route.ChatID != "chat-1" || route.ConversationID != "conversation-1" {
		t.Fatalf("route=%+v err=%v", route, err)
	}

	sendResult, err := service.Send(context.Background(), SendParams{
		Route: incoming.Route, ReplyTo: incoming.ID, ClientMessageID: "hermes-inbound-1",
		Text: "received", Format: "markdown", Media: []SendMedia{{Path: incoming.Media[0].Path}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sendResult.MessageID != "outbound-image" ||
		!reflect.DeepEqual(sendResult.MessageIDs, []string{"outbound-text", "outbound-image"}) ||
		sendResult.ClientMessageID != "hermes-inbound-1" {
		t.Fatalf("send result=%+v", sendResult)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 2 {
		t.Fatalf("sent %d platform messages: %v", len(sent), sent)
	}
	textBody, imageBody := sent[0], sent[1]
	if textBody["source"] != "telegram" || textBody["chat_id"] != "chat-1" || textBody["conversation_id"] != nil ||
		textBody["reply_to_message_id"] != "inbound-1" || textBody["message_id"] != "hermes-inbound-1" ||
		textBody["message_type"] != "text" || textBody["text"] != "received" {
		t.Fatalf("text body=%v", textBody)
	}
	if imageBody["source"] != "telegram" || imageBody["chat_id"] != "chat-1" || imageBody["conversation_id"] != nil ||
		imageBody["message_id"] != derivedClientMessageID("hermes-inbound-1", "image") || imageBody["message_type"] != "image" {
		t.Fatalf("image body=%v", imageBody)
	}
	if _, exists := imageBody["text"]; exists {
		t.Fatalf("image body unexpectedly contains text: %v", imageBody)
	}
	if _, exists := imageBody["reply_to_message_id"]; exists {
		t.Fatalf("image body unexpectedly contains reply_to_message_id: %v", imageBody)
	}
	if len(uploads["/image"]) == 0 || len(uploads["/thumbnail"]) == 0 {
		t.Fatalf("uploads=%v", uploads)
	}
	if images, ok := imageBody["images"].([]any); !ok || len(images) != 1 {
		t.Fatalf("sent images=%v", imageBody["images"])
	}
}

func writeExtendedServerFrame(writer io.Writer, payload []byte) {
	header := []byte{0x81}
	switch {
	case len(payload) < 126:
		header = append(header, byte(len(payload)))
	case len(payload) < 1<<16:
		header = append(header, 126, byte(len(payload)>>8), byte(len(payload)))
	default:
		header = append(header, 127)
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(payload)))
		header = append(header, length[:]...)
	}
	_, _ = writer.Write(append(header, payload...))
}

func waitForIncomingMessage(t *testing.T, events <-chan Event) *IncomingMessage {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if event.Method == "messages/received" {
				message, ok := event.Params.(*IncomingMessage)
				if !ok {
					t.Fatalf("incoming params type=%T", event.Params)
				}
				return message
			}
		case <-timer.C:
			t.Fatal("timed out waiting for an incoming message")
		}
	}
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, 2, 1))
	value.Set(0, 0, color.RGBA{R: 255, A: 255})
	value.Set(1, 0, color.RGBA{B: 255, A: 255})
	var output bytes.Buffer
	if err := png.Encode(&output, value); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func testJSONResponse(request *http.Request, status int, value any) *http.Response {
	data, _ := json.Marshal(value)
	return testHTTPResponseBytes(request, status, data, "application/json")
}

func testHTTPResponse(request *http.Request, status int, body, contentType string) *http.Response {
	return testHTTPResponseBytes(request, status, []byte(body), contentType)
}

func testHTTPResponseBytes(request *http.Request, status int, body []byte, contentType string) *http.Response {
	header := make(http.Header)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode: status, Status: http.StatusText(status), Header: header,
		Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: request,
	}
}

func decodeTestJSONBody(t *testing.T, reader io.Reader) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.NewDecoder(reader).Decode(&value); err != nil {
		t.Errorf("decode request body: %v", err)
	}
	return value
}
