package agentbridge

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSendMessageUsesConversationRouteAndStableClientID(t *testing.T) {
	var sent map[string]any
	client := &APIClient{
		BaseURL: "https://api.example", APIKey: "key", MaxAttempts: 1,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			sent = decodeTestJSONBody(t, request.Body)
			return testJSONResponse(request, http.StatusOK, map[string]any{"message_id": "server-1"}), nil
		})},
	}
	route, err := EncodeRoute(Route{ConversationID: "conversation-1"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.SendMessage(context.Background(), SendParams{
		Route: route, Text: "hello", Format: "plain", ClientMessageID: "client-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.MessageID != "server-1" || result.ClientMessageID != "client-1" {
		t.Fatalf("result=%+v", result)
	}
	if sent["source"] != "agenrena" || sent["conversation_id"] != "conversation-1" || sent["message_id"] != "client-1" {
		t.Fatalf("sent=%v", sent)
	}
	if _, exists := sent["chat_id"]; exists {
		t.Fatalf("conversation route unexpectedly included chat_id: %v", sent)
	}
}

func TestSendMessageSplitsTextAndImagesIntoExclusivePlatformMessages(t *testing.T) {
	imagePath := writeOutboundTestImage(t)
	var sent []map[string]any
	client := &APIClient{
		BaseURL: "https://api.example", APIKey: "key", MaxAttempts: 1,
		HTTPClient: outboundImageTestClient(t, func(request *http.Request) *http.Response {
			sent = append(sent, decodeTestJSONBody(t, request.Body))
			return testJSONResponse(request, http.StatusOK, map[string]any{
				"message_id": []string{"server-1", "server-2"}[len(sent)-1],
			})
		}),
	}
	route, err := EncodeRoute(Route{Source: "telegram", ChatID: "chat-1"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.SendMessage(context.Background(), SendParams{
		Route: route, ReplyTo: "inbound-1", ClientMessageID: "client-1",
		Text: "hello", Format: "markdown", Media: []SendMedia{{Path: imagePath}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.MessageID != "server-2" || !reflect.DeepEqual(result.MessageIDs, []string{"server-1", "server-2"}) ||
		result.ClientMessageID != "client-1" {
		t.Fatalf("result=%+v", result)
	}
	if len(sent) != 2 {
		t.Fatalf("sent %d platform messages: %v", len(sent), sent)
	}
	textBody, imageBody := sent[0], sent[1]
	if textBody["message_type"] != "text" || textBody["text"] != "hello" || textBody["text_format"] != "markdown" ||
		textBody["message_id"] != "client-1" || textBody["reply_to_message_id"] != "inbound-1" {
		t.Fatalf("text body=%v", textBody)
	}
	if _, exists := textBody["images"]; exists {
		t.Fatalf("text body unexpectedly contains images: %v", textBody)
	}
	if imageBody["message_type"] != "image" || imageBody["message_id"] != derivedClientMessageID("client-1", "image") {
		t.Fatalf("image body=%v", imageBody)
	}
	for _, field := range []string{"text", "text_format", "reply_to_message_id"} {
		if _, exists := imageBody[field]; exists {
			t.Fatalf("image body unexpectedly contains %s: %v", field, imageBody)
		}
	}
	if images, ok := imageBody["images"].([]any); !ok || len(images) != 1 {
		t.Fatalf("image body images=%v", imageBody["images"])
	}
}

func TestSendMessageKeepsImageOnlyAsOnePlatformMessage(t *testing.T) {
	imagePath := writeOutboundTestImage(t)
	var sent []map[string]any
	client := &APIClient{
		BaseURL: "https://api.example", APIKey: "key", MaxAttempts: 1,
		HTTPClient: outboundImageTestClient(t, func(request *http.Request) *http.Response {
			sent = append(sent, decodeTestJSONBody(t, request.Body))
			return testJSONResponse(request, http.StatusOK, map[string]any{"message_id": "server-image"})
		}),
	}
	route, err := EncodeRoute(Route{ConversationID: "conversation-1"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.SendMessage(context.Background(), SendParams{
		Route: route, ReplyTo: "inbound-1", ClientMessageID: "client-1", Media: []SendMedia{{Path: imagePath}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.MessageID != "server-image" || result.MessageIDs != nil || result.ClientMessageID != "client-1" {
		t.Fatalf("result=%+v", result)
	}
	if len(sent) != 1 || sent[0]["message_type"] != "image" || sent[0]["message_id"] != "client-1" ||
		sent[0]["reply_to_message_id"] != "inbound-1" {
		t.Fatalf("sent=%v", sent)
	}
	for _, field := range []string{"text", "text_format"} {
		if _, exists := sent[0][field]; exists {
			t.Fatalf("image body unexpectedly contains %s: %v", field, sent[0])
		}
	}
}

func TestSendMessageReportsPartialDeliveryWhenImageMessageFails(t *testing.T) {
	imagePath := writeOutboundTestImage(t)
	sends := 0
	client := &APIClient{
		BaseURL: "https://api.example", APIKey: "key", MaxAttempts: 1,
		HTTPClient: outboundImageTestClient(t, func(request *http.Request) *http.Response {
			sends++
			if sends == 1 {
				return testJSONResponse(request, http.StatusOK, map[string]any{"message_id": "server-text"})
			}
			return testJSONResponse(request, http.StatusBadRequest, map[string]any{"detail": "image rejected"})
		}),
	}
	route, err := EncodeRoute(Route{ConversationID: "conversation-1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.SendMessage(context.Background(), SendParams{
		Route: route, ClientMessageID: "client-1", Text: "hello", Media: []SendMedia{{Path: imagePath}},
	})
	rpcErr, ok := err.(*RPCError)
	if !ok || rpcErr.Code != "API_ERROR" || rpcErr.Recoverable {
		t.Fatalf("err=%+v", err)
	}
	fields, ok := rpcErr.Fields.(map[string]any)
	if !ok || fields["failedPart"] != "image" ||
		!reflect.DeepEqual(fields["deliveredMessageIds"], []string{"server-text"}) {
		t.Fatalf("partial delivery fields=%v", rpcErr.Fields)
	}
}

func TestDerivedClientMessageIDStaysWithinBackendLimit(t *testing.T) {
	parent := strings.Repeat("x", 100)
	first := derivedClientMessageID(parent, "image")
	second := derivedClientMessageID(parent, "image")
	if first != second || len(first) > 100 || first == parent {
		t.Fatalf("derived id=%q len=%d", first, len(first))
	}
}

func TestHandoffPostsToTheConversationOfTheRoute(t *testing.T) {
	var requested string
	client := &APIClient{
		BaseURL: "https://api.example", APIKey: "key", MaxAttempts: 1,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requested = request.Method + " " + request.URL.Path
			return testJSONResponse(request, http.StatusOK, map[string]any{
				"responder": "human", "switched_at": "2026-08-04T10:30:00Z",
			}), nil
		})},
	}
	route, err := EncodeRoute(Route{Source: "agenrena", ChatID: "chat-1", ConversationID: "conversation-1"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Handoff(context.Background(), HandoffParams{Route: route})
	if err != nil {
		t.Fatal(err)
	}
	if requested != "POST /channels/conversations/conversation-1/handoff/" {
		t.Fatalf("requested=%q", requested)
	}
	if result.Responder != "human" || result.SwitchedAt != "2026-08-04T10:30:00Z" {
		t.Fatalf("result=%+v", result)
	}
}

func TestHandoffRejectsARouteWithoutAnAgenrenaConversation(t *testing.T) {
	client := &APIClient{
		BaseURL: "https://api.example", APIKey: "key", MaxAttempts: 1,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			t.Errorf("external route unexpectedly reached the API: %s", request.URL.Path)
			return testJSONResponse(request, http.StatusOK, map[string]any{}), nil
		})},
	}
	route, err := EncodeRoute(Route{Source: "telegram", ChatID: "chat-1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Handoff(context.Background(), HandoffParams{Route: route})
	rpcErr, ok := err.(*RPCError)
	if !ok || rpcErr.Code != "HANDOFF_UNSUPPORTED" || rpcErr.Recoverable {
		t.Fatalf("err=%+v", err)
	}
}

func TestHandoffReportsAConversationWithoutADelegationAsUnsupported(t *testing.T) {
	attempts := 0
	client := &APIClient{
		BaseURL: "https://api.example", APIKey: "key",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts++
			return testJSONResponse(request, http.StatusNotFound, map[string]any{
				"error": map[string]any{"code": "CONVERSATION_NOT_FOUND"},
			}), nil
		})},
	}
	route, err := EncodeRoute(Route{ConversationID: "assistant-conversation"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Handoff(context.Background(), HandoffParams{Route: route})
	rpcErr, ok := err.(*RPCError)
	if !ok || rpcErr.Code != "HANDOFF_UNSUPPORTED" || rpcErr.Recoverable {
		t.Fatalf("err=%+v", err)
	}
	if attempts != 1 {
		t.Fatalf("a missing delegation was retried %d times", attempts)
	}
}

func TestNetworkErrorIsRecoverableOnlyWithIdempotency(t *testing.T) {
	errorValue := &APIError{Message: "connection ended", Ambiguous: true}
	withID, _ := apiRPCError(errorValue, true).(*RPCError)
	withoutID, _ := apiRPCError(errorValue, false).(*RPCError)
	if withID == nil || withID.Code != "NETWORK_ERROR" || !withID.Recoverable {
		t.Fatalf("idempotent error=%+v", withID)
	}
	if withoutID == nil || withoutID.Code != "DELIVERY_UNKNOWN" || withoutID.Recoverable {
		t.Fatalf("unkeyed error=%+v", withoutID)
	}
}

func writeOutboundTestImage(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(path, testPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func outboundImageTestClient(t *testing.T, send func(*http.Request) *http.Response) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Host + request.URL.Path {
		case "api.example/hub/media/presign/":
			return testJSONResponse(request, http.StatusOK, map[string]any{"media": []any{map[string]any{
				"id":                   "media-1",
				"image_upload_url":     "https://uploads.example/image",
				"thumbnail_upload_url": "https://uploads.example/thumbnail",
			}}}), nil
		case "uploads.example/image", "uploads.example/thumbnail":
			return testHTTPResponse(request, http.StatusNoContent, "", ""), nil
		case "api.example/channels/messages/send/":
			return send(request), nil
		default:
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL)
			return nil, nil
		}
	})}
}
