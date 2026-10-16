package codexbridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runtimeBatch() InboundMessage {
	return InboundMessage{
		ID: "m2", Route: "route", MessageType: "batch",
		DelegationBootstrap: map[string]any{"prompt": "Help customers"},
		Capabilities:        map[string]any{"supported_reply_types": []any{"text", "image"}},
		Messages: []InboundMessage{
			{ID: "m1", Route: "route", MessageType: "text", Sender: Sender{ID: "alice"}, Text: "first"},
			{ID: "m2", Route: "route", MessageType: "image", Sender: Sender{ID: "bob"}, Text: "second", ReplyTo: "m1"},
		},
	}
}

func TestBatchTurnInputsPreservesOrderAndImageAssociation(t *testing.T) {
	message := runtimeBatch()
	imagePath := filepath.Join(t.TempDir(), "image.png")
	message.Messages[0].Media = []Media{{Kind: "image", Path: imagePath}}
	inputs, err := turnInputs(message)
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 8 {
		t.Fatalf("expected batch preamble, bootstrap, capabilities, and ordered items, got %#v", inputs)
	}
	if !strings.Contains(stringValue(mapValue(inputs[1])["text"]), "Help customers") || !strings.Contains(stringValue(mapValue(inputs[2])["text"]), "supported_reply_types") {
		t.Fatalf("missing envelope context: %#v", inputs)
	}
	if stringValue(mapValue(inputs[4])["text"]) != "first" || mapValue(inputs[5])["path"] != imagePath || stringValue(mapValue(inputs[7])["text"]) != "second" {
		t.Fatalf("item content and attachments lost their order: %#v", inputs)
	}
}

func TestBatchTransportMetadataKeepsPerMessageAuthority(t *testing.T) {
	for _, sameSender := range []bool{false, true} {
		message := runtimeBatch()
		message.Sender.ID = "must-not-trust-envelope-sender"
		if sameSender {
			message.Messages[1].Sender.ID = "alice"
		}
		context := transportMetadataContext(message)
		value := context["agenrena_transport_metadata"].(map[string]any)["value"].(string)
		var metadata map[string]any
		if err := json.Unmarshal([]byte(value), &metadata); err != nil {
			t.Fatal(err)
		}
		if sameSender && metadata["auth_sender_id"] != "alice" || !sameSender && metadata["auth_sender_id"] != nil {
			t.Fatalf("wrong batch sender scope: %s", value)
		}
		items := metadata["messages"].([]any)
		if metadata["inbound_message_id"] != "m2" || len(items) != 2 || items[0].(map[string]any)["auth_sender_id"] != "alice" || items[1].(map[string]any)["inbound_message_id"] != "m2" {
			t.Fatalf("lost per-message authority or reply anchor: %s", value)
		}
	}
}

func TestBatchRejectsMalformedRuntimeInput(t *testing.T) {
	for _, malformed := range []string{"empty", "anchor", "nested", "route", "sender"} {
		t.Run(malformed, func(t *testing.T) {
			message := runtimeBatch()
			switch malformed {
			case "empty":
				message.Messages = nil
			case "anchor":
				message.ID = "wrong"
			case "nested":
				message.Messages[0].MessageType = "batch"
			case "route":
				message.Messages[0].Route = "elsewhere"
			case "sender":
				message.Messages[0].Sender.ID = ""
			}
			if _, err := turnInputs(message); err == nil {
				t.Fatal("expected malformed batch to be rejected")
			}
		})
	}
}

func TestBatchAllowsAttachingMediaFromEveryItem(t *testing.T) {
	message := runtimeBatch()
	imagePath := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(imagePath, []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}, 0o600); err != nil {
		t.Fatal(err)
	}
	message.Messages[0].Media = []Media{{Kind: "image", Path: imagePath}}
	runner := codexRunner{settings: Settings{Workspace: t.TempDir()}}
	if _, err := runner.attachmentResolver(message)(imagePath); err != nil {
		t.Fatalf("batch image was not authorized as inbound media: %v", err)
	}
}
