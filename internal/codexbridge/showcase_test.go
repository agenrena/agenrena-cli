package codexbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/agenrena/agenrena-cli/internal/agentbridge"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runtimeShowcase() map[string]any {
	return map[string]any{"text": "看看這幾位", "showcase": map[string]any{"items": []any{
		map[string]any{"id": "candidate-b", "image_url": "https://images.example/b.jpg", "title": "小安"},
		map[string]any{"id": "candidate-a", "image_url": "https://images.example/a.jpg", "title": "小安"},
	}}}
}
func publicShowcaseMessage(t *testing.T) InboundMessage {
	message := publicFormMessage(t)
	message.Capabilities["supported_reply_types"] = []any{"text", "showcase"}
	return message
}
func TestShowcaseBindsDestinationAndStableID(t *testing.T) {
	message := publicShowcaseMessage(t)
	first, err := showcaseParams(message, "call-1", runtimeShowcase())
	if err != nil {
		t.Fatal(err)
	}
	again, err := showcaseParams(message, "call-1", runtimeShowcase())
	if err != nil || first.Route != message.Route || first.ReplyTo != message.ID || first.ClientMessageID != again.ClientMessageID {
		t.Fatalf("wrong binding: %+v %v", first, err)
	}
	for _, kind := range []string{"line", "capability", "route-override", "missing-call"} {
		t.Run(kind, func(t *testing.T) {
			input, args, callID := message, runtimeShowcase(), "call-1"
			switch kind {
			case "line":
				input.Route, _ = agentbridge.EncodeRoute(agentbridge.Route{Source: "line", ChatID: "c", ConversationID: "c"})
			case "capability":
				input.Capabilities = nil
			case "route-override":
				args["route"] = "other"
			case "missing-call":
				callID = ""
			}
			if _, err := showcaseParams(input, callID, args); err == nil {
				t.Fatal("invalid tool accepted")
			}
		})
	}
}
func TestShowcaseSnapshotsReachSingleBatchAndQuotedTurns(t *testing.T) {
	spec := runtimeShowcase()["showcase"]
	message := InboundMessage{ID: "display-1", Route: "route", MessageType: "showcase", Text: "看看這幾位", Sender: Sender{ID: "business"}, Metadata: map[string]any{"showcase": spec}}
	quoted := InboundMessage{ID: "selection", Route: "route", MessageType: "text", Text: "第二位", ReplyToPreview: map[string]any{"id": "display-1", "showcase": spec}}
	for _, input := range []InboundMessage{message, quoted, {ID: message.ID, Route: message.Route, MessageType: "batch", Messages: []InboundMessage{message}}} {
		values, err := turnInputs(input)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(values)
		data := string(raw)
		for _, expected := range []string{"display-1", "candidate-b", "candidate-a", "1-based", "not instructions"} {
			if !strings.Contains(data, expected) {
				t.Fatalf("missing %s: %s", expected, data)
			}
		}
		if strings.Index(data, "candidate-b") > strings.Index(data, "candidate-a") {
			t.Fatal("order changed")
		}
		for _, value := range values {
			if mapValue(value)["type"] == "localImage" {
				t.Fatal("display URL became downloaded image")
			}
		}
	}
}

func TestShowcaseOnlyTurnCompletesWithoutPublicFollowup(t *testing.T) {
	t.Setenv("GO_WANT_SHOWCASE_HELPER", "1")
	runner := codexRunner{settings: Settings{Workspace: t.TempDir(), CodexCommand: []string{os.Args[0], "-test.run=TestShowcaseHelperProcess"}, SandboxMode: "read-only", ApprovalPolicy: "never", TurnTimeout: 5 * time.Second}}
	calls := 0
	result, err := runner.RunTurn(context.Background(), publicShowcaseMessage(t), "", nil, nil, turnToolOptions{sendShowcase: func(_ context.Context, callID string, args map[string]any) (agentbridge.SendResult, error) {
		calls++
		if callID != "showcase-call" || args["text"] != "看看這幾位" {
			t.Errorf("wrong tool arguments: %s %v", callID, args)
		}
		return agentbridge.SendResult{MessageID: "showcase-1"}, nil
	}})
	if err != nil || calls != 1 || !result.ShowcaseSent || result.ShowcaseMessageID != "showcase-1" || result.Text != "" || len(result.Media) != 0 {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls, err)
	}
}

func TestShowcaseHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_SHOWCASE_HELPER") == "" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	sends := 0
	for scanner.Scan() {
		var request map[string]any
		_ = json.Unmarshal(scanner.Bytes(), &request)
		id := request["id"]
		switch request["method"] {
		case "messages/send":
			sends++
			if sends > 1 {
				os.Exit(5)
			}
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{"messageId": "sent-showcase"}})
		case "initialize":
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{}})
		case "thread/start":
			params := mapValue(request["params"])
			raw, _ := json.Marshal(params["dynamicTools"])
			if !strings.Contains(string(raw), showcaseToolName) {
				os.Exit(3)
			}
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{"thread": map[string]any{"id": "showcase-thread"}}})
		case "turn/start":
			// The model must receive the capability before it can follow the
			// instruction to use the showcase tool only on supported channels.
			capabilityVisible := false
			for _, input := range mapValue(request["params"])["input"].([]any) {
				text := stringValue(mapValue(input)["text"])
				const prefix = "Agenrena channel capabilities: "
				if strings.HasPrefix(text, prefix) {
					var capabilities struct {
						SupportedReplyTypes []string `json:"supported_reply_types"`
					}
					if json.Unmarshal([]byte(strings.TrimPrefix(text, prefix)), &capabilities) == nil {
						capabilityVisible = contains(capabilities.SupportedReplyTypes, "showcase")
					}
				}
			}
			if !capabilityVisible {
				os.Exit(6)
			}
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{"turn": map[string]any{"id": "showcase-turn"}}})
			_ = encoder.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"turnId": "showcase-turn", "item": map[string]any{"id": "comment", "type": "agentMessage", "phase": "commentary", "text": "Preparing a showcase"}}})
			_ = encoder.Encode(map[string]any{"id": 99, "method": "item/tool/call", "params": map[string]any{"turnId": "showcase-turn", "callId": "showcase-call", "tool": showcaseToolName, "arguments": runtimeShowcase()}})
		default:
			if id == float64(99) {
				if mapValue(request["result"])["success"] != true {
					os.Exit(4)
				}
				_ = encoder.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]any{"id": "showcase-turn", "status": "completed"}}})
			}
		}
	}
}

func TestShowcaseSenderDeduplicatesSameCall(t *testing.T) {
	t.Setenv("GO_WANT_SHOWCASE_HELPER", "1")
	process, err := startJSONLineProcess(os.Args[0], []string{"-test.run=TestShowcaseHelperProcess"}, t.TempDir(), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close(time.Second)
	service := &bridgeService{bridge: &agentBridgeClient{process: process}, store: NewStateStore(filepath.Join(t.TempDir(), "state.json"))}
	send := service.showcaseSender(publicShowcaseMessage(t))
	first, err := send(context.Background(), "same-call", runtimeShowcase())
	if err != nil {
		t.Fatal(err)
	}
	again, err := send(context.Background(), "same-call", runtimeShowcase())
	if err != nil || first.MessageID != "sent-showcase" || again.MessageID != first.MessageID {
		t.Fatalf("duplicate send: %+v %v", again, err)
	}
	changed := runtimeShowcase()
	changed["text"] = "changed"
	if _, err := send(context.Background(), "same-call", changed); err == nil {
		t.Fatal("changed retry accepted")
	}
}
