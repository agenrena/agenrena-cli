package codexbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodexRunnerStartsThreadAndSendsSenderTextImagesSticker(t *testing.T) {
	root := t.TempDir()
	image := filepath.Join(root, "photo.jpg")
	sticker := filepath.Join(root, "sticker.png")
	capture := filepath.Join(root, "turn.json")
	_ = os.WriteFile(image, []byte("\xff\xd8\xfffake"), 0o600)
	_ = os.WriteFile(sticker, []byte("\x89PNG\r\n\x1a\nfake"), 0o600)
	runner := &CodexRunner{
		Workspace: root, SandboxMode: "read-only", ApprovalPolicy: "never",
		Timeout:         5 * time.Second,
		CommandOverride: []string{os.Args[0], "-test.run=TestCodexHelperProcess", "--", capture},
	}
	result, err := runner.RunTurn(
		context.Background(),
		IncomingMessage{
			MessageID: "message-1", ConversationID: "conversation-1", SenderID: "user-\"1\n",
			Text: "Please explain.", MessageType: "image",
		},
		"",
		[]MaterializedMedia{{Kind: "image", Path: image}, {Kind: "sticker", Path: sticker}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.ThreadID != "thread-new" || result.TurnID != "turn-test" || result.ReplyText != "Fake Codex reply" {
		t.Fatalf("unexpected result: %#v", result)
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var turn map[string]any
	_ = json.Unmarshal(raw, &turn)
	input := turn["input"].([]any)
	if len(input) != 5 {
		t.Fatalf("input=%#v", input)
	}
	firstInput := input[0].(map[string]any)
	if firstInput["text"] != "Agenrena sender: {\"id\":\"user-\\\"1\\n\"}" {
		t.Fatalf("sender metadata=%q", firstInput["text"])
	}
	if input[2].(map[string]any)["type"] != "localImage" ||
		input[3].(map[string]any)["text"] != "The user sent the following sticker." ||
		input[4].(map[string]any)["type"] != "localImage" {
		t.Fatalf("media input=%#v", input)
	}
}

func TestCodexHelperProcess(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--" {
		return
	}
	capture := os.Args[len(os.Args)-1]
	send := func(value any) {
		raw, _ := json.Marshal(value)
		_, _ = os.Stdout.Write(append(raw, '\n'))
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var message map[string]any
		_ = json.Unmarshal(scanner.Bytes(), &message)
		id, method := message["id"], stringValue(message["method"])
		params, _ := message["params"].(map[string]any)
		switch method {
		case "initialize":
			send(map[string]any{"id": id, "result": map[string]any{}})
		case "thread/start":
			send(map[string]any{"id": id, "result": map[string]any{"thread": map[string]any{"id": "thread-new"}}})
		case "thread/resume":
			send(map[string]any{"id": id, "result": map[string]any{"thread": map[string]any{"id": params["threadId"]}}})
		case "turn/start":
			raw, _ := json.Marshal(params)
			_ = os.WriteFile(capture, raw, 0o600)
			send(map[string]any{"id": id, "result": map[string]any{"turn": map[string]any{"id": "turn-test"}}})
			send(map[string]any{"method": "item/started", "params": map[string]any{
				"turnId": "turn-test", "item": map[string]any{"id": "agent-1", "type": "agentMessage", "phase": "final_answer", "text": ""},
			}})
			send(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{
				"turnId": "turn-test", "itemId": "agent-1", "delta": "Fake Codex reply",
			}})
			send(map[string]any{"method": "item/completed", "params": map[string]any{
				"turnId": "turn-test", "item": map[string]any{"id": "agent-1", "type": "agentMessage", "phase": "final_answer", "text": "Fake Codex reply"},
			}})
			send(map[string]any{"method": "turn/completed", "params": map[string]any{
				"turn": map[string]any{"id": "turn-test", "status": "completed", "error": nil},
			}})
		}
	}
	os.Exit(0)
}
