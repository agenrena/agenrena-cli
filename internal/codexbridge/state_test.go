package codexbridge

import (
	"path/filepath"
	"testing"
)

func TestStatePersistsThreadPendingAndCompleted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := NewStateStore(path)
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	reply := PendingReply{
		InboundMessageID: "message-1", ConversationID: "conversation-1",
		ThreadID: "thread-1", TurnID: "turn-1", Text: "hello",
	}
	if err := store.RecordCodexResult(reply); err != nil {
		t.Fatal(err)
	}
	reloaded := NewStateStore(path)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if reloaded.ThreadIDFor("conversation-1") != "thread-1" {
		t.Fatal("thread was not persisted")
	}
	if _, ok := reloaded.PendingReplyFor("message-1"); !ok {
		t.Fatal("pending reply was not persisted")
	}
	if err := reloaded.MarkReplySent("message-1"); err != nil {
		t.Fatal(err)
	}
	if !reloaded.IsCompleted("message-1") {
		t.Fatal("message was not completed")
	}
}
