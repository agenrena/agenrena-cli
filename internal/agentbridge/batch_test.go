package agentbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
)

func batchPayload(t *testing.T) map[string]any {
	t.Helper()
	var payload map[string]any
	err := json.Unmarshal([]byte(`{
		"id":"m2", "conversation_id":"conversation-1", "source":"agenrena", "chat_id":"conversation-1",
		"message_type":"batch", "context_type":"delegation_bootstrap", "delegation_id":"delegation-1",
		"delegation_bootstrap":{"prompt":"Help customers", "conversation_members":"members", "recent_conversation":"history"},
		"capabilities":{"supported_reply_types":["text","image"]},
		"messages":[
			{"id":"m1", "conversation_id":"conversation-1", "message_type":"text", "sender":{"id":"alice","display_name":"Alice"}, "text":"Book a table", "created_at":"2026-10-15T01:00:00Z"},
			{"id":"m2", "conversation_id":"conversation-1", "message_type":"text", "sender":{"id":"bob"}, "text":"For two", "reply_to_message_id":"m1", "reply_to_preview":{"id":"m1"}, "metadata":{"input_response":{"answers":{"party_size":"2"}}}, "context":{"items":[{"content":"quoted context"}]}}
		]
	}`), &payload)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestNormalizeBatchPreservesEnvelopeAndOrderedMessages(t *testing.T) {
	service := &Service{media: &MediaStore{}}
	message, err := service.normalizeIncoming(context.Background(), batchPayload(t))
	if err != nil {
		t.Fatal(err)
	}
	if message.MessageType != "batch" || message.ID != "m2" || message.Sender != nil || len(message.Messages) != 2 {
		t.Fatalf("unexpected envelope: %+v", message)
	}
	first, last := message.Messages[0], message.Messages[1]
	if first.ID != "m1" || first.Text != "Book a table" || first.Sender.ID != "alice" || first.Sender.Name != "Alice" || last.Sender.ID != "bob" {
		t.Fatalf("lost item order, sender, or content: %+v", message.Messages)
	}
	if first.Route != message.Route || last.Route != message.Route || last.ReplyTo != "m1" || last.ReplyToPreview == nil || last.Metadata == nil || len(last.Context) != 1 {
		t.Fatalf("lost route or references: %+v", last)
	}
	if message.DelegationBootstrap["prompt"] != "Help customers" || message.Capabilities == nil || message.DelegationID != "delegation-1" {
		t.Fatalf("lost envelope details: %+v", message)
	}
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if _, exists := wire["sender"]; exists {
		t.Fatal("batch envelope must not invent a sender")
	}
}

func TestNormalizeBatchMaterializesEachItemsMedia(t *testing.T) {
	payload := batchPayload(t)
	items := payload["messages"].([]any)
	items[0].(map[string]any)["message_type"] = "image"
	items[0].(map[string]any)["images"] = []any{map[string]any{"url": "https://1.1.1.1/image.png"}}
	items[1].(map[string]any)["message_type"] = "sticker"
	items[1].(map[string]any)["sticker"] = map[string]any{"image_url": "https://1.1.1.1/sticker.png"}
	png := testPNG(t)
	service := &Service{media: &MediaStore{Root: t.TempDir(), HTTPClient: &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return testHTTPResponseBytes(request, http.StatusOK, png, "image/png"), nil
		}),
	}}}
	message, err := service.normalizeIncoming(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	for index, kind := range []string{"image", "sticker"} {
		media := message.Messages[index].Media
		if len(media) != 1 || media[0].Kind != kind {
			t.Fatalf("item %d media = %+v", index, media)
		}
		if _, err := os.Stat(media[0].Path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNormalizeBatchRejectsPartialOrConflictingDeliveries(t *testing.T) {
	cases := map[string]func(map[string]any, map[string]any){
		"empty":     func(p, item map[string]any) { p["messages"] = []any{} },
		"anchor":    func(p, item map[string]any) { p["id"] = "wrong" },
		"duplicate": func(p, item map[string]any) { item["id"] = "m1" },
		"nested":    func(p, item map[string]any) { item["message_type"] = "batch" },
		"sender":    func(p, item map[string]any) { delete(item, "sender") },
		"route":     func(p, item map[string]any) { item["conversation_id"] = "different" },
		"source":    func(p, item map[string]any) { item["source"] = "different" },
		"content":   func(p, item map[string]any) { item["text"] = ""; delete(item, "context") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			payload := batchPayload(t)
			mutate(payload, payload["messages"].([]any)[1].(map[string]any))
			service := &Service{media: &MediaStore{}}
			message, err := service.normalizeIncoming(context.Background(), payload)
			if err == nil || message != nil {
				t.Fatalf("got message=%+v error=%v; want whole batch rejected", message, err)
			}
		})
	}
}
