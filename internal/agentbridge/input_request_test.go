package agentbridge

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

func testInputRequest() map[string]any {
	return map[string]any{"question": "請補充訂位需求", "fields": []any{map[string]any{"key": "party_size", "type": "number", "label": "用餐人數", "required": true}}}
}

func TestSendInputRequestPreservesPayload(t *testing.T) {
	calls := 0
	client := &APIClient{BaseURL: "https://api.example", HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Path != "/channels/messages/send/" || req.Method != http.MethodPost {
			t.Fatalf("unexpected endpoint: %s %s", req.Method, req.URL.Path)
		}
		body := decodeTestJSONBody(t, req.Body)
		spec := body["input_request"].(map[string]any)
		if body["message_type"] != "input_request" || body["text"] != spec["question"] || body["text_format"] != "plain" || body["message_id"] != "stable-form" || body["chat_id"] != "conversation" || body["reply_to_message_id"] != "incoming" {
			t.Fatalf("wrong form body: %v", body)
		}
		if spec["fields"].([]any)[0].(map[string]any)["type"] != "number" {
			t.Fatalf("fields lost: %v", spec)
		}
		return testJSONResponse(req, http.StatusOK, map[string]any{"message_id": "form-1"}), nil
	})}}
	route, _ := EncodeRoute(Route{Source: "agenrena", ChatID: "conversation", ConversationID: "conversation"})
	params := SendParams{Route: route, InputRequest: testInputRequest(), ClientMessageID: "stable-form", ReplyTo: "incoming"}
	for i := 0; i < 2; i++ {
		result, err := client.SendMessage(context.Background(), params)
		if err != nil || result.MessageID != "form-1" || result.ClientMessageID != "stable-form" {
			t.Fatalf("result=%+v error=%v", result, err)
		}
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestInputRequestRejectsUnsupportedRoutesAndMixedContent(t *testing.T) {
	route, _ := EncodeRoute(Route{Source: "agenrena", ChatID: "conversation", ConversationID: "conversation"})
	for _, kind := range []string{"line", "discovery", "media", "markdown", "text", "empty", "too-many", "extra"} {
		t.Run(kind, func(t *testing.T) {
			params := SendParams{Route: route, InputRequest: testInputRequest()}
			switch kind {
			case "line":
				params.Route, _ = EncodeRoute(Route{Source: "line", ChatID: "conversation", ConversationID: "conversation"})
			case "discovery":
				params.Route, _ = EncodeRoute(Route{Source: "agenrena", ChatID: "discovery_inquiry:inquiry", ConversationID: "conversation"})
			case "media":
				params.Media = []SendMedia{{Path: "must-not-be-read"}}
			case "markdown":
				params.Format = "markdown"
			case "text":
				params.Text = "different question"
			case "empty":
				params.InputRequest["fields"] = []any{}
			case "too-many":
				params.InputRequest["fields"] = []any{1, 2, 3, 4, 5}
			case "extra":
				params.InputRequest["route"] = "someone-else"
			}
			if _, err := (&APIClient{}).SendMessage(context.Background(), params); err == nil {
				t.Fatal("invalid form reached sending")
			}
		})
	}
}

func TestFormSubmissionImageIDsSurviveMaterialization(t *testing.T) {
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	media := NewMediaStore(t.TempDir())
	media.AllowPrivateHosts = true
	media.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return testHTTPResponseBytes(req, http.StatusOK, png, "image/png"), nil
	})}
	service := &Service{media: media}
	message, err := service.normalizeIncoming(context.Background(), map[string]any{
		"id": "response", "source": "agenrena", "chat_id": "conversation", "conversation_id": "conversation", "sender": map[string]any{"id": "customer"}, "message_type": "text", "text": "Photos: [1 image(s)]",
		"images":   []any{map[string]any{"id": "photo-1", "url": "https://127.0.0.1/photo.png"}},
		"metadata": map[string]any{"input_response": map[string]any{"version": 1, "request_message_id": "form-1", "answers": map[string]any{"photos": []any{"photo-1"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Media) != 1 || message.Media[0].ID != "photo-1" || message.Metadata["input_response"] == nil {
		t.Fatalf("lost answer or image id: %+v", message)
	}
}

func TestFormErrorsRemainActionable(t *testing.T) {
	for _, code := range []string{"INPUT_REQUEST_INVALID", "INPUT_REQUEST_CONFLICT", "INPUT_REQUEST_UNAVAILABLE", "CHANNEL_MESSAGE_TYPE_UNSUPPORTED"} {
		err := apiRPCError(&APIError{Status: 400, Code: code, Message: code}, true)
		rpc, ok := err.(*RPCError)
		if !ok || rpc.Code != code || !strings.Contains(rpc.Message, code) {
			t.Fatalf("lost form error: %v", err)
		}
	}
}

func TestInputRequestValidationDetailsSurviveRPC(t *testing.T) {
	client := &APIClient{BaseURL: "https://api.example", HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return testJSONResponse(req, 400, map[string]any{"error": map[string]any{"code": "INPUT_REQUEST_INVALID", "message": "Invalid form", "fields": map[string]any{"fields": "duplicate_keys"}}}), nil
	})}}
	route, _ := EncodeRoute(Route{Source: "agenrena", ChatID: "conversation", ConversationID: "conversation"})
	_, err := client.SendMessage(context.Background(), SendParams{Route: route, InputRequest: testInputRequest()})
	rpc, ok := err.(*RPCError)
	if !ok || rpc.Code != "INPUT_REQUEST_INVALID" || rpc.Fields.(map[string]any)["fields"] != "duplicate_keys" {
		t.Fatalf("validation details lost: %v", err)
	}
}
