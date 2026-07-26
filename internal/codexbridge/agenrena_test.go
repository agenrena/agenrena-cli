package codexbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestAgenrenaReplyContract(t *testing.T) {
	var observed map[string]any
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/agent-api/channels/messages/send/" {
			t.Errorf("path=%s", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer agr_secret" {
			t.Errorf("authorization=%s", request.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(request.Body).Decode(&observed)
		return &http.Response{
			StatusCode: 200, Header: http.Header{},
			Body: io.NopCloser(bytes.NewBufferString(`{"message_id":"server-1"}`)),
		}, nil
	})
	client := &AgenrenaAPIClient{
		APIBase: "https://api.example/api/agent-api", APIKey: "agr_secret",
		UserAgent: DefaultUserAgent, HTTPClient: &http.Client{Transport: transport}, MaxAttempts: 1,
	}
	_, err := client.SendReply(context.Background(), PendingReply{
		InboundMessageID: "inbound-1", ConversationID: "conversation-1", Text: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed["message_id"] != "codex-inbound-1" || observed["reply_to_message_id"] != "inbound-1" {
		t.Fatalf("unexpected body: %#v", observed)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestAuthenticatedWSURLPreservesAndEncodesQuery(t *testing.T) {
	value, err := AuthenticatedWSURL("wss://api.example/ws/?existing=1", "agr_key+with/symbols")
	if err != nil {
		t.Fatal(err)
	}
	if value != "wss://api.example/ws/?existing=1&token=agr_key%2Bwith%2Fsymbols" {
		t.Fatalf("url=%s", value)
	}
}
