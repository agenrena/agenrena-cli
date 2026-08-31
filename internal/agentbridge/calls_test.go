package agentbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNormalizeIncomingCallEvent(t *testing.T) {
	event, handled, err := normalizeCallEvent(map[string]any{
		"domain": "calls",
		"action": "incoming",
		"payload": map[string]any{
			"call_id":         "call-1",
			"conversation_id": "conversation-1",
			"caller": map[string]any{
				"id":           "identity-123",
				"display_name": "Kai",
			},
			"expires_at": "2026-08-12T12:00:30+00:00",
			"rtc": map[string]any{
				"server_url":        "wss://rtc.example",
				"participant_token": "agent-token",
			},
		},
	})
	if err != nil || !handled || event == nil || event.Method != "calls/incoming" {
		t.Fatalf("event=%+v handled=%v err=%v", event, handled, err)
	}
	want := IncomingCall{
		CallID: "call-1", ConversationID: "conversation-1", ExpiresAt: "2026-08-12T12:00:30+00:00",
		Caller: &Sender{ID: "identity-123", Name: "Kai"},
		RTC:    CallRTC{ServerURL: "wss://rtc.example", ParticipantToken: "agent-token"},
	}
	if !reflect.DeepEqual(event.Params, want) {
		t.Fatalf("params=%+v want=%+v", event.Params, want)
	}
	encoded, err := json.Marshal(event.Params)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"callId":"call-1","conversationId":"conversation-1","caller":{"id":"identity-123","name":"Kai"},"expiresAt":"2026-08-12T12:00:30+00:00","rtc":{"serverUrl":"wss://rtc.example","participantToken":"agent-token"}}` {
		t.Fatalf("encoded params=%s", encoded)
	}
}

func TestNormalizeCancelledCallEvent(t *testing.T) {
	event, handled, err := normalizeCallEvent(map[string]any{
		"domain":  "calls",
		"action":  "cancelled",
		"payload": map[string]any{"call_id": "call-1"},
	})
	if err != nil || !handled || event == nil || event.Method != "calls/cancelled" {
		t.Fatalf("event=%+v handled=%v err=%v", event, handled, err)
	}
	if !reflect.DeepEqual(event.Params, CancelledCall{CallID: "call-1"}) {
		t.Fatalf("params=%+v", event.Params)
	}
}

func TestNormalizeCallEventIgnoresOtherDomainsAndUnknownActions(t *testing.T) {
	event, handled, err := normalizeCallEvent(map[string]any{"message_type": "text"})
	if err != nil || handled || event != nil {
		t.Fatalf("non-call event=%+v handled=%v err=%v", event, handled, err)
	}
	event, handled, err = normalizeCallEvent(map[string]any{
		"domain": "calls", "action": "future_action", "payload": map[string]any{},
	})
	if err != nil || !handled || event != nil {
		t.Fatalf("unknown action event=%+v handled=%v err=%v", event, handled, err)
	}
}

func TestNormalizeCallEventRejectsMissingFieldsWithoutExposingCredentials(t *testing.T) {
	_, handled, err := normalizeCallEvent(map[string]any{
		"domain": "calls",
		"action": "incoming",
		"payload": map[string]any{
			"call_id": "call-1", "conversation_id": "conversation-1", "expires_at": "2026-08-12T12:00:30Z",
			"rtc": map[string]any{"participant_token": "secret-token"},
		},
	})
	if !handled || err == nil || !strings.Contains(err.Error(), "server_url is missing") {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("error exposes participant token: %v", err)
	}
}

func TestServiceCarriesCallEventsFromWebSocket(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	events := make(chan Event, 2)
	service := &Service{
		ctx:    context.Background(),
		notify: func(event Event) { events <- event },
		config: Config{PingInterval: time.Hour},
	}
	socket := NewWebSocketConnection(clientConn, bufio.NewReader(clientConn), 2*1024*1024)
	done := make(chan error, 1)
	go func() { done <- service.consumeConnection(socket) }()

	incoming := []byte(`{"domain":"calls","action":"incoming","payload":{"call_id":"call-1","conversation_id":"conversation-1","caller":{"id":"identity-123","display_name":"Kai"},"expires_at":"2026-08-12T12:00:30+00:00","rtc":{"server_url":"wss://rtc.example","participant_token":"agent-token"}}}`)
	cancelled := []byte(`{"domain":"calls","action":"cancelled","payload":{"call_id":"call-1"}}`)
	writeExtendedServerFrame(serverConn, incoming)
	writeExtendedServerFrame(serverConn, cancelled)

	for _, method := range []string{"calls/incoming", "calls/cancelled"} {
		select {
		case event := <-events:
			if event.Method != method {
				t.Fatalf("event method=%q want=%q", event.Method, method)
			}
			if method == "calls/incoming" {
				call, ok := event.Params.(IncomingCall)
				if !ok || call.Route == "" || call.RTC.ParticipantToken != "" || call.Caller == nil || call.Caller.ID != "identity-123" {
					t.Fatalf("public incoming call must have an opaque route without credentials: %+v", event.Params)
				}
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for %s", method)
		}
	}

	_ = serverConn.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("consumeConnection did not stop after the WebSocket closed")
	}
}
