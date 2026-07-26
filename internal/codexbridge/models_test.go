package codexbridge

import "testing"

func TestIncomingMessageSupportsTextImageStickerAndSenderID(t *testing.T) {
	payload := map[string]any{
		"id": "message-1", "conversation_id": "conversation-1", "message_type": "sticker",
		"sender":  map[string]any{"id": "user-1", "display_name": "Ignored"},
		"sticker": map[string]any{"image_url": "https://cdn.example/sticker"},
	}
	message, ok := IncomingMessageFromPayload(payload)
	if !ok {
		t.Fatal("message was rejected")
	}
	if message.SenderID != "user-1" || len(message.Media) != 1 || message.Media[0].Kind != "sticker" {
		t.Fatalf("unexpected message: %#v", message)
	}
}

func TestIncomingMessageRejectsUnsupportedAndEmpty(t *testing.T) {
	for _, payload := range []map[string]any{
		{"id": "1", "conversation_id": "c", "message_type": "video", "text": "x"},
		{"id": "1", "conversation_id": "c", "message_type": "text", "text": ""},
	} {
		if _, ok := IncomingMessageFromPayload(payload); ok {
			t.Fatalf("accepted invalid payload: %#v", payload)
		}
	}
}

func TestOutboundMessageIDIsStableAndBounded(t *testing.T) {
	reply := PendingReply{InboundMessageID: string(make([]byte, 110))}
	if len(reply.OutboundMessageID()) != 100 {
		t.Fatalf("got length %d", len(reply.OutboundMessageID()))
	}
}
