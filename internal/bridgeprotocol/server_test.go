package bridgeprotocol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/agenrena/agenrena-cli/internal/agentbridge"
)

type fakeBackend struct {
	fatal      chan *agentbridge.RPCError
	initialize agentbridge.InitializeParams
	sent       agentbridge.SendParams
	closed     bool
	initErr    error
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{fatal: make(chan *agentbridge.RPCError, 1)}
}

func (backend *fakeBackend) Initialize(_ context.Context, params agentbridge.InitializeParams, notify func(agentbridge.Event)) (agentbridge.InitializeResult, error) {
	backend.initialize = params
	notify(agentbridge.Event{Method: "bridge/status", Params: agentbridge.Status{State: "connected"}})
	if backend.initErr != nil {
		return agentbridge.InitializeResult{}, backend.initErr
	}
	return agentbridge.InitializeResult{
		ProtocolVersion: 1, ServerInfo: agentbridge.ServerInfo{Name: "test", Version: "1"}, State: "connected",
	}, nil
}

func (backend *fakeBackend) Send(_ context.Context, params agentbridge.SendParams) (agentbridge.SendResult, error) {
	backend.sent = params
	return agentbridge.SendResult{MessageID: "out", ClientMessageID: params.ClientMessageID}, nil
}

func (backend *fakeBackend) Fatal() <-chan *agentbridge.RPCError { return backend.fatal }
func (backend *fakeBackend) Close() error {
	backend.closed = true
	return nil
}

func TestServerRunsInitializeSendAndShutdown(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientInfo":{"name":"hermes","version":"1"},"agent":{"type":"hermes"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"messages/send","params":{"route":"v1.route","text":"reply","clientMessageId":"client-1"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"shutdown","params":{}}`,
	}, "\n") + "\n"
	backend := newFakeBackend()
	var output bytes.Buffer
	server := NewServer(strings.NewReader(input), &output, backend)
	if err := server.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if backend.initialize.Agent.Type != "hermes" || backend.sent.Text != "reply" || !backend.closed {
		t.Fatalf("backend init=%+v sent=%+v closed=%v", backend.initialize, backend.sent, backend.closed)
	}
	lines := decodeOutputLines(t, output.String())
	if len(lines) != 4 {
		t.Fatalf("lines=%d output=%s", len(lines), output.String())
	}
	if lines[0]["method"] != "bridge/status" {
		t.Fatalf("first output=%v", lines[0])
	}
	if numericID(lines[1]["id"]) != 1 || numericID(lines[2]["id"]) != 2 || numericID(lines[3]["id"]) != 3 {
		t.Fatalf("unexpected response ids: %v", lines)
	}
}

func TestServerRejectsSendBeforeInitializeAndUnknownMethod(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"a","method":"messages/send","params":{"text":"x"}}`,
		`{"jsonrpc":"2.0","id":"b","method":"unknown","params":{}}`,
		`{"jsonrpc":"2.0","id":"c","method":"shutdown","params":{}}`,
	}, "\n") + "\n"
	backend := newFakeBackend()
	var output bytes.Buffer
	if err := NewServer(strings.NewReader(input), &output, backend).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	lines := decodeOutputLines(t, output.String())
	if nestedCode(lines[0]) != "NOT_INITIALIZED" || nestedCode(lines[1]) != "METHOD_NOT_FOUND" {
		t.Fatalf("output=%s", output.String())
	}
}

func TestServerReturnsInitializationErrorAndTerminalExit(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientInfo":{"name":"hermes","version":"1"},"agent":{"type":"hermes"}}}` + "\n"
	backend := newFakeBackend()
	backend.initErr = &agentbridge.RPCError{Code: "AUTH_REQUIRED", Message: "login required"}
	var output bytes.Buffer
	err := NewServer(strings.NewReader(input), &output, backend).Run(context.Background())
	var terminal *TerminalError
	if !errors.As(err, &terminal) {
		t.Fatalf("err=%v", err)
	}
	lines := decodeOutputLines(t, output.String())
	if len(lines) != 2 || nestedCode(lines[1]) != "AUTH_REQUIRED" {
		t.Fatalf("output=%s", output.String())
	}
}

func TestServerRejectsNonIntegerRequestID(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1.5,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":{},"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"shutdown","params":{}}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := NewServer(strings.NewReader(input), &output, newFakeBackend()).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	lines := decodeOutputLines(t, output.String())
	if len(lines) != 3 || nestedCode(lines[0]) != "PROTOCOL_INVALID" || nestedCode(lines[1]) != "PROTOCOL_INVALID" {
		t.Fatalf("output=%s", output.String())
	}
}

func decodeOutputLines(t *testing.T, output string) []map[string]any {
	t.Helper()
	result := []map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		var value map[string]any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			t.Fatalf("invalid JSON line %q: %v", line, err)
		}
		result = append(result, value)
	}
	return result
}

func nestedCode(value map[string]any) string {
	errorValue, _ := value["error"].(map[string]any)
	data, _ := errorValue["data"].(map[string]any)
	code, _ := data["code"].(string)
	return code
}

func numericID(value any) int {
	if number, ok := value.(float64); ok {
		return int(number)
	}
	return 0
}
