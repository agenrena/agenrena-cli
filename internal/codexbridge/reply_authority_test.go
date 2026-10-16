package codexbridge

import (
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
