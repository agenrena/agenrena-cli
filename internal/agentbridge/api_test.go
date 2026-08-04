package agentbridge

import (
	"context"
	"net/http"
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
