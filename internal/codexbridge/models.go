package codexbridge

import (
	"encoding/json"
	"fmt"
	"strings"
)

const Version = "0.4.0"

type IncomingMedia struct {
	Kind     string
	URL      string
	MIMEType string
}

type IncomingMessage struct {
	MessageID      string
	ConversationID string
	SenderID       string
	MessageType    string
	Text           string
	Media          []IncomingMedia
	CreatedAt      string
}

func IncomingMessageFromPayload(payload map[string]any) (*IncomingMessage, bool) {
	messageType := stringValue(payload["message_type"])
	if messageType != "text" && messageType != "image" && messageType != "sticker" {
		return nil, false
	}
	messageID := stringValue(payload["id"])
	conversationID := stringValue(payload["conversation_id"])
	text := stringValue(payload["text"])
	if messageID == "" || conversationID == "" {
		return nil, false
	}

	var media []IncomingMedia
	if values, ok := payload["images"].([]any); ok {
		for _, value := range values {
			image, ok := value.(map[string]any)
			if !ok {
				continue
			}
			url := stringValue(image["url"])
			if url != "" {
				media = append(media, IncomingMedia{
					Kind: "image", URL: url, MIMEType: stringValue(image["mime_type"]),
				})
			}
		}
	}
	if messageType == "sticker" {
		if sticker, ok := payload["sticker"].(map[string]any); ok {
			if url := stringValue(sticker["image_url"]); url != "" {
				media = append(media, IncomingMedia{
					Kind: "sticker", URL: url, MIMEType: "image/png",
				})
			}
		}
	}
	if text == "" && len(media) == 0 {
		return nil, false
	}

	var senderID string
	if sender, ok := payload["sender"].(map[string]any); ok {
		senderID = stringValue(sender["id"])
	}
	return &IncomingMessage{
		MessageID:      messageID,
		ConversationID: conversationID,
		SenderID:       senderID,
		MessageType:    messageType,
		Text:           text,
		Media:          media,
		CreatedAt:      stringValue(payload["created_at"]),
	}, true
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

type CodexTurnResult struct {
	ThreadID  string
	TurnID    string
	ReplyText string
}

type PendingReply struct {
	InboundMessageID string `json:"inbound_message_id"`
	ConversationID   string `json:"conversation_id"`
	ThreadID         string `json:"thread_id"`
	TurnID           string `json:"turn_id"`
	Text             string `json:"text"`
}

func (reply PendingReply) OutboundMessageID() string {
	value := "codex-" + reply.InboundMessageID
	if len(value) > 100 {
		return value[:100]
	}
	return value
}

func senderMetadata(message IncomingMessage) map[string]any {
	if message.SenderID == "" {
		return nil
	}
	raw, _ := json.Marshal(map[string]string{"id": message.SenderID})
	return map[string]any{
		"type": "text", "text": "Agenrena sender: " + string(raw), "text_elements": []any{},
	}
}
