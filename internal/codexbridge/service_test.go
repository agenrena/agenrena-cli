package codexbridge

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

type fakeSource struct{ payloads []map[string]any }

func (source *fakeSource) Stream(ctx context.Context, output chan<- map[string]any) error {
	for _, payload := range source.payloads {
		select {
		case output <- payload:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

type fakeReplyClient struct {
	mu      sync.Mutex
	replies []PendingReply
}

func (client *fakeReplyClient) SendReply(_ context.Context, reply PendingReply) (map[string]any, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.replies = append(client.replies, reply)
	return map[string]any{"message_id": reply.OutboundMessageID()}, nil
}

type fakeRunner struct {
	mu        sync.Mutex
	threadIDs []string
	senders   []string
}

func (runner *fakeRunner) RunTurn(_ context.Context, message IncomingMessage, threadID string, _ []MaterializedMedia) (CodexTurnResult, error) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.threadIDs = append(runner.threadIDs, threadID)
	runner.senders = append(runner.senders, message.SenderID)
	if threadID == "" {
		threadID = "thread-created"
	}
	return CodexTurnResult{ThreadID: threadID, TurnID: "turn-" + message.MessageID, ReplyText: "reply"}, nil
}

func TestBridgeCreatesResumesAndDeduplicates(t *testing.T) {
	makePayload := func(id, sender string) map[string]any {
		return map[string]any{
			"id": id, "conversation_id": "conversation-1", "message_type": "text",
			"text": id, "sender": map[string]any{"id": sender, "display_name": "ignored"},
		}
	}
	source := &fakeSource{payloads: []map[string]any{
		makePayload("message-1", "alice"),
		makePayload("message-2", "bob"),
		makePayload("message-1", "alice"),
	}}
	replies, runner := &fakeReplyClient{}, &fakeRunner{}
	service := &BridgeService{
		MessageSource: source, ReplyClient: replies, CodexRunner: runner,
		StateStore: NewStateStore(filepath.Join(t.TempDir(), "state.json")),
	}
	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(replies.replies) != 2 || len(runner.threadIDs) != 2 ||
		runner.threadIDs[0] != "" || runner.threadIDs[1] != "thread-created" {
		t.Fatalf("replies=%#v threads=%#v", replies.replies, runner.threadIDs)
	}
	if runner.senders[0] != "alice" || runner.senders[1] != "bob" {
		t.Fatalf("senders=%#v", runner.senders)
	}
}
