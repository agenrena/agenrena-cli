package codexbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenrena/agenrena-cli/internal/agentbridge"
)

func runtimeInputRequest() map[string]any {
	return map[string]any{"question": "How many guests?", "fields": []any{map[string]any{"key": "guests", "type": "number", "label": "Guests"}}}
}

func publicFormMessage(t *testing.T) InboundMessage {
	t.Helper()
	route, err := agentbridge.EncodeRoute(agentbridge.Route{Source: "agenrena", ChatID: "conversation", ConversationID: "conversation"})
	if err != nil {
		t.Fatal(err)
	}
	return InboundMessage{ID: "incoming", Route: route, MessageType: "text", Text: "Book a table", Capabilities: map[string]any{"supported_reply_types": []any{"text", "image", "input_request"}}}
}

func TestInputRequestBindsCurrentCustomerDestination(t *testing.T) {
	message := publicFormMessage(t)
	params, err := inputRequestParams(message, "call-1", runtimeInputRequest())
	if err != nil {
		t.Fatal(err)
	}
	if params.Route != message.Route || params.ReplyTo != message.ID {
		t.Fatalf("wrong destination: %+v", params)
	}
	retry, err := inputRequestParams(message, "call-1", runtimeInputRequest())
	if err != nil || retry.ClientMessageID != params.ClientMessageID {
		t.Fatal("retry id changed")
	}
	for _, kind := range []string{"line", "missing-capability", "model-route"} {
		t.Run(kind, func(t *testing.T) {
			input := message
			args := runtimeInputRequest()
			switch kind {
			case "line":
				input.Route, _ = agentbridge.EncodeRoute(agentbridge.Route{Source: "line", ChatID: "conversation", ConversationID: "conversation"})
			case "missing-capability":
				input.Capabilities = nil
			case "model-route":
				args["route"] = "other-customer"
			}
			if _, err := inputRequestParams(input, "call-1", args); err == nil {
				t.Fatal("unsupported form accepted")
			}
		})
	}
}

func TestStructuredFormAnswersReachSingleAndBatchTurns(t *testing.T) {
	message := InboundMessage{ID: "answer", Route: "route", Sender: Sender{ID: "customer"}, MessageType: "text", Text: "Guests: 2", Media: []Media{{ID: "photo-1", Kind: "image", Path: filepath.Join(t.TempDir(), "photo.png")}}, Metadata: map[string]any{"input_response": map[string]any{"version": 1, "request_message_id": "form-1", "answers": map[string]any{"guests": "2.00", "photos": []any{"photo-1"}}}}}
	for _, input := range []InboundMessage{message, {ID: "answer", Route: "route", MessageType: "batch", Messages: []InboundMessage{message}}} {
		values, err := turnInputs(input)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(values)
		for _, expected := range []string{"form-1", "2.00", "photo-1", "photo.png", "customer submitted a form"} {
			if !strings.Contains(string(raw), expected) {
				t.Fatalf("missing %q: %s", expected, raw)
			}
		}
	}
	message.Metadata = nil
	values, err := turnInputs(message)
	raw, _ := json.Marshal(values)
	if err != nil || strings.Contains(string(raw), "customer submitted a form") {
		t.Fatal("ordinary text was treated as a submission")
	}
	message.Metadata = map[string]any{"input_response": map[string]any{"version": 2}}
	if _, err := turnInputs(message); err == nil {
		t.Fatal("unsupported answer format accepted")
	}
}

func TestFormOnlyTurnCompletesWithoutPublicFollowup(t *testing.T) {
	t.Setenv("GO_WANT_INPUT_REQUEST_HELPER", "1")
	runner := codexRunner{settings: Settings{Workspace: t.TempDir(), CodexCommand: []string{os.Args[0], "-test.run=TestInputRequestHelperProcess"}, SandboxMode: "read-only", ApprovalPolicy: "never", TurnTimeout: 5 * time.Second}}
	calls := 0
	result, err := runner.RunTurn(context.Background(), publicFormMessage(t), "", nil, nil, turnToolOptions{sendInputRequest: func(_ context.Context, callID string, args map[string]any) (agentbridge.SendResult, error) {
		calls++
		if callID != "form-call" || args["question"] != "How many guests?" {
			t.Errorf("wrong tool arguments: %s %v", callID, args)
		}
		return agentbridge.SendResult{MessageID: "form-1"}, nil
	}})
	if err != nil || calls != 1 || !result.InputRequestSent || result.InputRequestMessageID != "form-1" || result.Text != "" || len(result.Media) != 0 {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls, err)
	}
}

func TestInputRequestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_INPUT_REQUEST_HELPER") == "" {
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
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{"messageId": "sent-form"}})
		case "initialize":
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{}})
		case "thread/start":
			params := mapValue(request["params"])
			raw, _ := json.Marshal(params["dynamicTools"])
			if !strings.Contains(string(raw), inputRequestToolName) {
				os.Exit(3)
			}
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{"thread": map[string]any{"id": "form-thread"}}})
		case "turn/start":
			// The model must receive the capability before it can follow the
			// instruction to use the form tool only on supported channels.
			capabilityVisible := false
			for _, input := range mapValue(request["params"])["input"].([]any) {
				text := stringValue(mapValue(input)["text"])
				const prefix = "Agenrena channel capabilities: "
				if strings.HasPrefix(text, prefix) {
					var capabilities struct {
						SupportedReplyTypes []string `json:"supported_reply_types"`
					}
					if json.Unmarshal([]byte(strings.TrimPrefix(text, prefix)), &capabilities) == nil {
						capabilityVisible = contains(capabilities.SupportedReplyTypes, "input_request")
					}
				}
			}
			if !capabilityVisible {
				os.Exit(6)
			}
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{"turn": map[string]any{"id": "form-turn"}}})
			_ = encoder.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"turnId": "form-turn", "item": map[string]any{"id": "comment", "type": "agentMessage", "phase": "commentary", "text": "Preparing a form"}}})
			_ = encoder.Encode(map[string]any{"id": 99, "method": "item/tool/call", "params": map[string]any{"turnId": "form-turn", "callId": "form-call", "tool": inputRequestToolName, "arguments": runtimeInputRequest()}})
		default:
			if id == float64(99) {
				if mapValue(request["result"])["success"] != true {
					os.Exit(4)
				}
				_ = encoder.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]any{"id": "form-turn", "status": "completed"}}})
			}
		}
	}
}

func TestInputRequestSenderDeduplicatesCallsAndPreventsReplacement(t *testing.T) {
	t.Setenv("GO_WANT_INPUT_REQUEST_HELPER", "1")
	process, err := startJSONLineProcess(os.Args[0], []string{"-test.run=TestInputRequestHelperProcess"}, t.TempDir(), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close(time.Second)
	message := publicFormMessage(t)
	message.Capabilities["supported_reply_types"] = []any{"input_request"}
	service := &bridgeService{bridge: &agentBridgeClient{process: process}}
	send := service.inputRequestSender(message)
	first, err := send(context.Background(), "same-call", runtimeInputRequest())
	if err != nil {
		t.Fatal(err)
	}
	again, err := send(context.Background(), "same-call", runtimeInputRequest())
	if err != nil || first.MessageID != "sent-form" || again.MessageID != first.MessageID {
		t.Fatalf("duplicate call: %+v %v", again, err)
	}
	changed := runtimeInputRequest()
	changed["question"] = "Changed"
	if _, err := send(context.Background(), "same-call", changed); err == nil {
		t.Fatal("changed retry accepted")
	}
	if _, err := send(context.Background(), "new-call", runtimeInputRequest()); err == nil {
		t.Fatal("second form replaced first in same turn")
	}
}
