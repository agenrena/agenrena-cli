package codexbridge

import (
	"encoding/json"
	"fmt"
	"strings"
)

func batchTurnInputs(message InboundMessage) ([]any, error) {
	if len(message.Messages) == 0 || message.ID == "" || message.Messages[len(message.Messages)-1].ID != message.ID {
		return nil, fmt.Errorf("batch requires messages anchored to its last message id")
	}
	inputs := []any{textInput("The following Agenrena messages arrived as one ordered batch. Read all messages and their attachments before responding once to the batch.")}
	if len(message.DelegationBootstrap) > 0 {
		encoded, err := json.Marshal(message.DelegationBootstrap)
		if err != nil {
			return nil, fmt.Errorf("invalid delegation bootstrap: %w", err)
		}
		inputs = append(inputs, textInput("Agenrena delegation bootstrap: "+string(encoded)))
	}
	if len(message.Capabilities) > 0 {
		encoded, err := json.Marshal(message.Capabilities)
		if err != nil {
			return nil, fmt.Errorf("invalid channel capabilities: %w", err)
		}
		inputs = append(inputs, textInput("Agenrena channel capabilities: "+string(encoded)))
	}
	seen := make(map[string]bool, len(message.Messages))
	for index, item := range message.Messages {
		if item.ID == "" || seen[item.ID] || item.Route != message.Route || strings.TrimSpace(item.Sender.ID) == "" {
			return nil, fmt.Errorf("batch message %d has an invalid id, route, or sender", index+1)
		}
		seen[item.ID] = true
		switch item.MessageType {
		case "text", "image", "sticker":
		default:
			return nil, fmt.Errorf("batch message %d has an unsupported message type", index+1)
		}
		// Include attribution, references and structured answers without
		// serializing the entire transport envelope (which can contain tokens).
		details, err := json.Marshal(map[string]any{
			"id": item.ID, "message_type": item.MessageType, "sender": item.Sender,
			"created_at": item.CreatedAt, "reply_to": item.ReplyTo,
			"reply_to_preview": item.ReplyToPreview, "metadata": item.Metadata,
		})
		if err != nil {
			return nil, fmt.Errorf("batch message %d has invalid details: %w", index+1, err)
		}
		inputs = append(inputs, textInput(fmt.Sprintf("Agenrena batch message %d/%d: %s", index+1, len(message.Messages), details)))
		content, err := turnInputs(item)
		if err != nil {
			return nil, fmt.Errorf("batch message %d: %w", index+1, err)
		}
		inputs = append(inputs, content...)
	}
	return inputs, nil
}

func addBatchTransportMetadata(metadata map[string]any, message InboundMessage) {
	items := make([]map[string]any, 0, len(message.Messages))
	commonSender := ""
	for index, item := range message.Messages {
		sender := strings.TrimSpace(item.Sender.ID)
		if index == 0 {
			commonSender = sender
		} else if sender != commonSender {
			commonSender = ""
		}
		items = append(items, map[string]any{
			"auth_sender_id": nullIfEmpty(sender), "inbound_message_id": item.ID,
		})
	}
	metadata["message_type"] = "batch"
	metadata["auth_sender_id"] = nullIfEmpty(commonSender)
	metadata["messages"] = items
}
