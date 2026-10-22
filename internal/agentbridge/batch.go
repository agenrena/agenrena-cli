package agentbridge

import (
	"context"
	"fmt"
)

// A batch is one delivery and one runtime turn. Its envelope has a route and
// reply anchor, but no sender; each item retains its own sender and content.
func (service *Service) normalizeBatch(ctx context.Context, payload map[string]any) (*IncomingMessage, error) {
	id := valueString(payload["id"])
	conversationID := valueString(payload["conversation_id"])
	if id == "" || conversationID == "" {
		return nil, fmt.Errorf("batch id and conversation_id are required")
	}
	route := Route{
		Source: valueString(payload["source"]), ChatID: valueString(payload["chat_id"]),
		ConversationID: conversationID,
	}
	routeValue, err := EncodeRoute(route)
	if err != nil {
		return nil, err
	}
	values, ok := payload["messages"].([]any)
	if !ok || len(values) == 0 {
		return nil, fmt.Errorf("batch messages must be a nonempty array")
	}
	// Validate the entire envelope before downloading any of its media. Never
	// silently discard a malformed item and answer only part of a request.
	items := make([]map[string]any, 0, len(values))
	seen := make(map[string]bool, len(values))
	for index, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("batch message %d must be an object", index+1)
		}
		itemID := valueString(item["id"])
		if itemID == "" || seen[itemID] {
			return nil, fmt.Errorf("batch message %d has a missing or duplicate id", index+1)
		}
		seen[itemID] = true
		switch valueString(item["message_type"]) {
		case "text", "image", "sticker", "showcase":
		default:
			return nil, fmt.Errorf("batch message %d has an unsupported message_type", index+1)
		}
		sender, _ := item["sender"].(map[string]any)
		if valueString(sender["id"]) == "" {
			return nil, fmt.Errorf("batch message %d is missing its sender id", index+1)
		}
		copy := make(map[string]any, len(item)+3)
		for key, value := range item {
			copy[key] = value
		}
		for key, expected := range map[string]string{
			"source": route.Source, "chat_id": route.ChatID, "conversation_id": conversationID,
		} {
			if supplied := valueString(item[key]); supplied != "" && supplied != expected {
				return nil, fmt.Errorf("batch message %d has a conflicting %s", index+1, key)
			}
			copy[key] = expected
		}
		items = append(items, copy)
	}
	if valueString(items[len(items)-1]["id"]) != id {
		return nil, fmt.Errorf("batch id must match its last message id")
	}
	message := &IncomingMessage{
		ID: id, Route: routeValue, MessageType: "batch",
		Messages: make([]IncomingMessage, 0, len(items)),
	}
	copyInboundDetails(message, payload)
	for index, item := range items {
		normalized, err := service.normalizeIncoming(ctx, item)
		if err != nil {
			return nil, fmt.Errorf("batch message %d: %w", index+1, err)
		}
		if normalized == nil {
			return nil, fmt.Errorf("batch message %d has no supported content", index+1)
		}
		message.Messages = append(message.Messages, *normalized)
	}
	return message, nil
}

// Keep backend context separate from user text and route identity. Reply
// tokens are transport data, never model input or part of a persistent route.
func copyInboundDetails(message *IncomingMessage, payload map[string]any) {
	message.ReplyToPreview = payload["reply_to_preview"]
	message.Metadata, _ = payload["metadata"].(map[string]any)
	message.ContextType = valueString(payload["context_type"])
	message.DelegationID = valueString(payload["delegation_id"])
	message.DelegationBootstrap, _ = payload["delegation_bootstrap"].(map[string]any)
	message.Capabilities, _ = payload["capabilities"].(map[string]any)
}
