package codexbridge

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/agenrena/agenrena-cli/internal/agentbridge"
)

func TestRevokedPublicReplyIsStillRemovedFromLocalPending(t *testing.T) {
	message := publicFormMessage(t)
	store := NewStateStore(filepath.Join(t.TempDir(), "state.json"))
	reply, err := store.Record(Reply{InboundMessageID: "incoming", Route: message.Route, ThreadID: "thread", Text: "obsolete"})
	if err != nil {
		t.Fatal(err)
	}
	service := &bridgeService{store: store}
	discarded, err := service.discardRevokedReply(reply, &agentbridge.RPCError{Code: "CONVERSATION_AGENT_REPLY_DISABLED"})
	if err != nil || !discarded {
		t.Fatalf("discard=%v error=%v", discarded, err)
	}
	if _, pending := store.Pending(reply.InboundMessageID); pending || !store.Completed(reply.InboundMessageID) {
		t.Fatal("revoked reply remains eligible for retry")
	}
}

func TestExpiredReplyClearsPendingAcrossRestartAndDuplicateDelivery(t *testing.T) {
	message := publicFormMessage(t)
	path := filepath.Join(t.TempDir(), "state.json")
	store := NewStateStore(path)
	reply, err := store.Record(Reply{InboundMessageID: message.ID, Route: message.Route,
		ThreadID: "thread", Text: "obsolete", ClientMessageID: "answer-1"})
	if err != nil {
		t.Fatal(err)
	}
	sends := 0
	bridge := deliveryTestBridge(t, func(params agentbridge.SendParams) (agentbridge.SendResult, error) {
		sends++
		return agentbridge.SendResult{}, &agentbridge.RPCError{Code: "BUSINESS_SERVICE_REPLY_WINDOW_EXPIRED", Recoverable: false}
	})
	service := &bridgeService{store: store, bridge: bridge}
	if err := service.deliver(context.Background(), reply, newTurnProgressEmitter(bridge, reply.Route, message.ID)); err != nil {
		t.Fatal(err)
	}
	reloaded := NewStateStore(path)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.PendingReplies()) != 0 || !reloaded.Completed(message.ID) || reloaded.ThreadID(message.Route) != "thread" {
		t.Fatal("expired reply was retained or conversation memory was lost")
	}
	service.store = reloaded
	if err := service.handle(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if sends != 1 {
		t.Fatalf("duplicate delivery resent expired reply: %d", sends)
	}
}

func TestNetworkFailureStillKeepsPublicReplyPending(t *testing.T) {
	message := publicFormMessage(t)
	store := NewStateStore(filepath.Join(t.TempDir(), "state.json"))
	reply, err := store.Record(Reply{InboundMessageID: message.ID, Route: message.Route, Text: "answer", ClientMessageID: "answer-1"})
	if err != nil {
		t.Fatal(err)
	}
	bridge := deliveryTestBridge(t, func(params agentbridge.SendParams) (agentbridge.SendResult, error) {
		return agentbridge.SendResult{}, &agentbridge.RPCError{Code: "NETWORK_ERROR", Recoverable: true}
	})
	service := &bridgeService{store: store, bridge: bridge}
	if err := service.deliver(context.Background(), reply, newTurnProgressEmitter(bridge, reply.Route, message.ID)); err == nil {
		t.Fatal("network error was swallowed")
	}
	if len(store.PendingReplies()) != 1 || store.Completed(message.ID) {
		t.Fatal("retryable reply was removed")
	}
}
