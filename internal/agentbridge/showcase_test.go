package agentbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func testShowcase() map[string]any {
	return map[string]any{"items": []any{map[string]any{"id": "opaque-b", "image_url": "https://images.example/b.jpg", "title": "小安", "note": "喜歡爬山"}, map[string]any{"id": "opaque-a", "image_url": "https://images.example/a.jpg", "title": "小安"}}}
}
func TestSendShowcaseIsOneMessageAndRetainsRetryID(t *testing.T) {
	calls := 0
	client := &APIClient{BaseURL: "https://api.example", HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Path != "/channels/messages/send/" || req.Method != http.MethodPost {
			t.Fatal("wrong endpoint")
		}
		body := decodeTestJSONBody(t, req.Body)
		if body["message_type"] != "showcase" || body["text"] != "看看這幾位" || body["message_id"] != "stable-display" || body["text_format"] != "plain" || body["reply_to_message_id"] != "incoming" || body["chat_id"] != "conversation" {
			t.Fatalf("wrong payload: %v", body)
		}
		items := body["showcase"].(map[string]any)["items"].([]any)
		if len(items) != 2 || items[0].(map[string]any)["id"] != "opaque-b" || items[1].(map[string]any)["id"] != "opaque-a" {
			t.Fatal("item order lost")
		}
		return testJSONResponse(req, 200, map[string]any{"message_id": "display-1"}), nil
	})}}
	route, _ := EncodeRoute(Route{Source: "agenrena", ChatID: "conversation", ConversationID: "conversation"})
	original := SendParams{Route: route, ClientMessageID: "stable-display", ReplyTo: "incoming", Text: "看看這幾位", Showcase: testShowcase()}
	raw, _ := json.Marshal(original)
	var params SendParams
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		result, err := client.SendMessage(context.Background(), params)
		if err != nil || result.MessageID != "display-1" || result.ClientMessageID != "stable-display" {
			t.Fatalf("%+v %v", result, err)
		}
	}
	if calls != 2 {
		t.Fatalf("expected one request per send, got %d", calls)
	}
}
func TestShowcaseRejectsInvalidContentBeforeSending(t *testing.T) {
	route, _ := EncodeRoute(Route{Source: "agenrena", ChatID: "conversation", ConversationID: "conversation"})
	for _, kind := range []string{"line", "discovery", "media", "form", "markdown", "text", "empty", "count", "duplicate", "http", "credentials", "unknown", "version", "title", "length", "non-string", "non-object"} {
		t.Run(kind, func(t *testing.T) {
			params := SendParams{Route: route, Text: "介紹", Showcase: testShowcase()}
			items := params.Showcase["items"].([]any)
			item := items[0].(map[string]any)
			switch kind {
			case "line":
				params.Route, _ = EncodeRoute(Route{Source: "line", ChatID: "conversation", ConversationID: "conversation"})
			case "discovery":
				params.Route, _ = EncodeRoute(Route{Source: "agenrena", ChatID: "discovery_inquiry:inquiry", ConversationID: "conversation"})
			case "media":
				params.Media = []SendMedia{{Path: "must-not-be-read"}}
			case "form":
				params.InputRequest = testInputRequest()
			case "markdown":
				params.Format = "markdown"
			case "text":
				params.Text = " "
			case "empty":
				params.Showcase["items"] = []any{}
			case "count":
				params.Showcase["items"] = make([]any, 11)
			case "duplicate":
				items[1].(map[string]any)["id"] = " opaque-b "
			case "http":
				item["image_url"] = "http://images.example/a.jpg"
			case "credentials":
				item["image_url"] = "https://user:pass@images.example/a.jpg"
			case "unknown":
				item["actions"] = []any{}
			case "version":
				params.Showcase["version"] = 1
			case "title":
				delete(item, "title")
			case "length":
				item["title"] = strings.Repeat("安", 101)
			case "non-string":
				item["id"] = 123
			case "non-object":
				items[0] = "invalid"
			}
			if _, err := (&APIClient{}).SendMessage(context.Background(), params); err == nil {
				t.Fatal("invalid showcase reached sending")
			}
		})
	}
}
func TestShowcaseErrorsPreserveBackendDetails(t *testing.T) {
	for _, code := range []string{"SHOWCASE_INVALID", "SHOWCASE_CONFLICT", "CHANNEL_MESSAGE_TYPE_UNSUPPORTED"} {
		client := &APIClient{BaseURL: "https://api.example", HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return testJSONResponse(req, 409, map[string]any{"error": map[string]any{"code": code, "message": "Invalid display", "fields": map[string]any{"items": "conflict"}}}), nil
		})}}
		route, _ := EncodeRoute(Route{Source: "agenrena", ChatID: "conversation", ConversationID: "conversation"})
		_, err := client.SendMessage(context.Background(), SendParams{Route: route, Text: "介紹", Showcase: testShowcase()})
		rpc, ok := err.(*RPCError)
		if !ok || rpc.Code != code || rpc.Fields.(map[string]any)["items"] != "conflict" {
			t.Fatalf("lost backend details: %v", err)
		}
	}
}
func TestInboundShowcaseAndQuotedBatchRetainSnapshots(t *testing.T) {
	payload := batchPayload(t)
	items := payload["messages"].([]any)
	items[0].(map[string]any)["message_type"] = "showcase"
	items[0].(map[string]any)["metadata"] = map[string]any{"showcase": testShowcase()}
	items[1].(map[string]any)["reply_to_preview"] = map[string]any{"id": "m1", "showcase": testShowcase()}
	service := &Service{media: &MediaStore{}}
	message, err := service.normalizeIncoming(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	first, last := message.Messages[0], message.Messages[1]
	if first.MessageType != "showcase" || first.Metadata["showcase"] == nil || len(first.Media) != 0 || last.ReplyToPreview.(map[string]any)["showcase"] == nil {
		t.Fatalf("snapshot lost: %+v", message)
	}
}
