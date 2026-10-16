package agentbridge

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ValidateInputRequest checks the envelope before sending. The backend remains
// authoritative for field-specific rules, permissions, and form lifecycle.
func ValidateInputRequest(spec map[string]any) error {
	invalid := func(message string) error { return bridgeError("INPUT_REQUEST_INVALID", message, false) }
	for key := range spec {
		if key != "question" && key != "fields" {
			return invalid("input request accepts only question and fields")
		}
	}
	question, ok := spec["question"].(string)
	if !ok || strings.TrimSpace(question) == "" || utf8.RuneCountInString(question) > 200 {
		return invalid("input request question must contain 1–200 characters")
	}
	fields, ok := spec["fields"].([]any)
	if !ok || len(fields) < 1 || len(fields) > 4 {
		return invalid("input request requires 1–4 fields")
	}
	return nil
}

func (client *APIClient) sendInputRequest(ctx context.Context, route Route, params SendParams) (SendResult, error) {
	if IsDiscoveryInquiryRoute(route) || route.Source == "line" || (route.Source != "" && route.Source != "agenrena") {
		return SendResult{}, bridgeError("CHANNEL_MESSAGE_TYPE_UNSUPPORTED", "input requests require an Agenrena customer conversation", false)
	}
	if err := ValidateInputRequest(params.InputRequest); err != nil {
		return SendResult{}, err
	}
	question := params.InputRequest["question"].(string)
	if len(params.Media) > 0 || (params.Format != "" && params.Format != "plain") || (params.Text != "" && params.Text != question) {
		return SendResult{}, bridgeError("INPUT_REQUEST_INVALID", "input requests require plain text matching the question, without attached media", false)
	}
	if params.ClientMessageID == "" {
		id, err := NewOperationID()
		if err != nil {
			return SendResult{}, err
		}
		params.ClientMessageID = id
	}
	if len(params.ClientMessageID) > 100 {
		return SendResult{}, bridgeError("MESSAGE_INVALID", "clientMessageId exceeds 100 characters", false)
	}
	body := outboundMessageBody(route, params.ClientMessageID, params.ReplyTo)
	body["message_type"], body["text"], body["text_format"] = "input_request", question, "plain"
	body["input_request"] = params.InputRequest
	id, err := client.sendOutboundMessage(ctx, body)
	if err != nil {
		return SendResult{}, err
	}
	if id == "" {
		return SendResult{}, bridgeError("DELIVERY_UNKNOWN", "form send returned no message id; delivery outcome is unknown", false)
	}
	return SendResult{MessageID: id, ClientMessageID: params.ClientMessageID}, nil
}

// NewOperationID creates a UUID retained across retries of a form send.
func NewOperationID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("could not create an operation id: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[:4], value[4:6], value[6:8], value[8:10], value[10:]), nil
}
