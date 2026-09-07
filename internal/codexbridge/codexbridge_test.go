package codexbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/agenrena/agenrena-cli/internal/buildinfo"
)

const testPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func testEnvironment(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("AGENRENA_CODEX_BRIDGE_CONFIG_FILE", filepath.Join(root, "config.json"))
	t.Setenv("AGENRENA_CODEX_BRIDGE_STATE_DIR", filepath.Join(root, "state"))
	return root
}

func TestConfigureStoresWorkspaceOnly(t *testing.T) {
	root := testEnvironment(t)
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := Configure(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if result["workspace"] != workspace {
		t.Fatalf("workspace = %v, want %s", result["workspace"], workspace)
	}
	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config != (fileConfig{Version: configSchemaVersion, Workspace: workspace}) {
		t.Fatalf("config = %#v", config)
	}
}

func TestStatePersistsRepliesAndStagesImages(t *testing.T) {
	root := testEnvironment(t)
	path := filepath.Join(root, "state.json")
	first := NewStateStore(path)
	if err := first.Load(); err != nil {
		t.Fatal(err)
	}
	reply, err := first.Record(Reply{
		InboundMessageID: "m1", Route: "opaque.route", ThreadID: "thread-1", TurnID: "turn-1",
		Text: "answer", ClientMessageID: "codex-m1", Media: []Media{{Data: testPNGBase64}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Media) != 1 || reply.Media[0].Path == "" {
		t.Fatalf("staged media = %#v", reply.Media)
	}
	data, err := os.ReadFile(reply.Media[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if base64.StdEncoding.EncodeToString(data) != testPNGBase64 {
		t.Fatal("staged image changed")
	}
	second := NewStateStore(path)
	if err := second.Load(); err != nil {
		t.Fatal(err)
	}
	if second.ThreadID("opaque.route") != "thread-1" {
		t.Fatalf("thread = %q", second.ThreadID("opaque.route"))
	}
	if err := second.MarkSent("m1"); err != nil {
		t.Fatal(err)
	}
	if !second.Completed("m1") {
		t.Fatal("message was not marked complete")
	}
	if _, err := os.Stat(reply.Media[0].Path); !os.IsNotExist(err) {
		t.Fatalf("staged image still exists: %v", err)
	}
}

func TestStateResetsThreadsWhenToolSurfaceChanges(t *testing.T) {
	root := testEnvironment(t)
	path := filepath.Join(root, "state.json")
	value := map[string]any{
		"version": 2, "sessions": map[string]string{"opaque.route": "old-thread"},
		"pendingReplies":      map[string]any{"m1": map[string]any{"inboundMessageID": "m1", "text": "pending"}},
		"completedMessageIDs": []string{"m0"},
	}
	if err := atomicWriteJSON(path, value); err != nil {
		t.Fatal(err)
	}
	store := NewStateStore(path)
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if store.ThreadID("opaque.route") != "" {
		t.Fatal("old thread was retained")
	}
	if _, ok := store.Pending("m1"); !ok || !store.Completed("m0") {
		t.Fatal("delivery state was not retained")
	}
}

func TestStateClearThreadPersists(t *testing.T) {
	root := testEnvironment(t)
	path := filepath.Join(root, "state.json")
	store := NewStateStore(path)
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteWithoutReply("m1", "opaque.route", "thread-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.ClearThread("opaque.route"); err != nil {
		t.Fatal(err)
	}
	reloaded := NewStateStore(path)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if threadID := reloaded.ThreadID("opaque.route"); threadID != "" {
		t.Fatalf("thread = %q, want empty", threadID)
	}
}

func TestMCPListsToolsAndConfiguresWorkspace(t *testing.T) {
	root := testEnvironment(t)
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	requests := []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-06-18"}},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}},
		{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "agenrena_bridge_setup", "arguments": map[string]any{"workspace": workspace}}},
	}
	var input bytes.Buffer
	for _, request := range requests {
		if err := json.NewEncoder(&input).Encode(request); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	if err := RunMCP(context.Background(), &input, &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("responses = %d\n%s", len(lines), output.String())
	}
	var initialized struct {
		Result struct {
			ServerInfo struct {
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &initialized); err != nil {
		t.Fatal(err)
	}
	if initialized.Result.ServerInfo.Version != buildinfo.Version {
		t.Fatalf(
			"MCP version = %q, want CLI version %q",
			initialized.Result.ServerInfo.Version,
			buildinfo.Version,
		)
	}
	var listed struct {
		Result struct {
			Tools []mcpTool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Result.Tools) != 4 {
		t.Fatalf("tools = %d", len(listed.Result.Tools))
	}
	config, err := loadConfig()
	if err != nil || config.Workspace != workspace {
		t.Fatalf("config = %#v, err = %v", config, err)
	}
}

func TestDaemonStartAndStopLifecycle(t *testing.T) {
	root := testEnvironment(t)
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Configure(workspace); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENRENA_BIN", os.Args[0])
	t.Setenv("CODEX_BIN", os.Args[0])
	t.Setenv("GO_WANT_DAEMON_HELPER", "1")
	original := daemonCommand
	daemonCommand = func() (*exec.Cmd, error) {
		return exec.Command(os.Args[0], "-test.run=TestDaemonHelperProcess"), nil
	}
	t.Cleanup(func() { daemonCommand = original })
	started, err := StartDaemon()
	if err != nil {
		t.Fatal(err)
	}
	if !started.Running || started.State != "connected" {
		t.Fatalf("started = %#v", started)
	}
	stopped, err := StopDaemon()
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Running || stopped.State != "stopped" {
		t.Fatalf("stopped = %#v", stopped)
	}
}

func TestDaemonHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_DAEMON_HELPER") != "1" {
		return
	}
	startedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if err := writeDaemonStatus(processRecord{PID: os.Getpid(), State: "connected", StartedAt: startedAt}); err != nil {
		os.Exit(2)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	<-signals
	_ = writeDaemonStatus(processRecord{State: "stopped", StartedAt: startedAt})
}

func TestAgentBridgeHandoffUsesOpaqueRoute(t *testing.T) {
	testEnvironment(t)
	t.Setenv("GO_WANT_AGENT_BRIDGE_HELPER", "1")
	client, err := startAgentBridge(Settings{AgenrenaCommand: []string{os.Args[0], "-test.run=TestAgentBridgeHelperProcess"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Handoff(ctx, "opaque.handoff"); err != nil {
		t.Fatal(err)
	}
	client.Shutdown()
}

func TestAgentBridgeHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_AGENT_BRIDGE_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request map[string]any
		_ = json.Unmarshal(scanner.Bytes(), &request)
		id := request["id"]
		switch request["method"] {
		case "initialize":
			params := mapValue(request["params"])
			agent := mapValue(params["agent"])
			commands, _ := agent["slashCommands"].([]any)
			if agent["type"] != "codex" || len(commands) != 2 ||
				mapValue(commands[0])["name"] != "new" || mapValue(commands[1])["name"] != "status" {
				os.Exit(3)
			}
			for _, command := range commands {
				value := mapValue(command)
				if _, ok := value["aliases"]; ok {
					os.Exit(4)
				}
				if _, ok := value["subcommands"]; ok {
					os.Exit(5)
				}
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"state": "connected"}})
		case "conversations/handoff":
			params := mapValue(request["params"])
			if params["route"] != "opaque.handoff" {
				os.Exit(2)
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"responder": "human"}})
		case "shutdown":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"state": "stopped"}})
			return
		}
	}
}

func TestRuntimeCommands(t *testing.T) {
	root := testEnvironment(t)
	store := NewStateStore(filepath.Join(root, "state.json"))
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteWithoutReply("m1", "opaque.route", "thread-1"); err != nil {
		t.Fatal(err)
	}
	service := &bridgeService{store: store}

	status, handled, err := service.runRuntimeCommand(InboundMessage{Route: "opaque.route", Text: "  /STATUS  "})
	if err != nil || !handled || status.ThreadID != "thread-1" || !strings.Contains(status.Text, "active") {
		t.Fatalf("status = %#v, handled = %v, err = %v", status, handled, err)
	}
	started, handled, err := service.runRuntimeCommand(InboundMessage{Route: "opaque.route", Text: "/new"})
	if err != nil || !handled || started.ThreadID != "" || store.ThreadID("opaque.route") != "" {
		t.Fatalf("new = %#v, handled = %v, thread = %q, err = %v", started, handled, store.ThreadID("opaque.route"), err)
	}
	if _, handled, err := service.runRuntimeCommand(InboundMessage{Route: "opaque.route", Text: "/new now"}); err != nil || handled {
		t.Fatalf("command with arguments handled = %v, err = %v", handled, err)
	}
}

func TestCodexRunnerReturnsFinalAnswerAndRefreshesSender(t *testing.T) {
	root := testEnvironment(t)
	capture := filepath.Join(root, "requests.jsonl")
	t.Setenv("GO_WANT_CODEX_HELPER", "normal")
	t.Setenv("CODEX_HELPER_CAPTURE", capture)
	runner := codexRunner{settings: Settings{
		Workspace: root, CodexCommand: []string{os.Args[0], "-test.run=TestCodexHelperProcess"},
		SandboxMode: "read-only", ApprovalPolicy: "never", TurnTimeout: 2 * time.Second,
	}}
	var progress []turnProgress
	first, err := runner.RunTurn(context.Background(), InboundMessage{ID: "m1", Route: "same", Sender: Sender{ID: "owner-id"}, Text: "first"}, "", func(context.Context) error { return nil }, func(update turnProgress) {
		progress = append(progress, update)
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Text != "final reply" || first.ThreadID != "shared-thread" {
		t.Fatalf("first result = %#v", first)
	}
	if len(progress) < 2 || progress[0].Status != "thinking" || progress[len(progress)-1].Stage != "final_answer" {
		t.Fatalf("progress = %#v", progress)
	}
	if _, err := runner.RunTurn(context.Background(), InboundMessage{ID: "m2", Route: "same", Sender: Sender{ID: "guest-id"}, Text: "second"}, first.ThreadID, func(context.Context) error { return nil }, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var instructions []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var request map[string]any
		if json.Unmarshal([]byte(line), &request) != nil {
			continue
		}
		params := mapValue(request["params"])
		if value := stringValue(params["developerInstructions"]); value != "" {
			instructions = append(instructions, value)
		}
	}
	if len(instructions) != 2 || !strings.Contains(instructions[0], `<agenrena_transport_metadata>{"auth_sender_id":"owner-id"}</agenrena_transport_metadata>`) ||
		!strings.Contains(instructions[1], `<agenrena_transport_metadata>{"auth_sender_id":"guest-id"}</agenrena_transport_metadata>`) {
		t.Fatalf("sender metadata missing: %#v", instructions)
	}
}

func TestCodexRunnerHandoffAndImageOutput(t *testing.T) {
	root := testEnvironment(t)
	t.Setenv("GO_WANT_CODEX_HELPER", "handoff-image")
	runner := codexRunner{settings: Settings{
		Workspace: root, CodexCommand: []string{os.Args[0], "-test.run=TestCodexHelperProcess"},
		SandboxMode: "read-only", ApprovalPolicy: "never", TurnTimeout: 2 * time.Second,
	}}
	calls := 0
	result, err := runner.RunTurn(context.Background(), InboundMessage{ID: "m-image", Route: "opaque", Text: "human please"}, "", func(context.Context) error {
		calls++
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !result.HandedOff || len(result.Media) != 1 || result.Media[0].Data != testPNGBase64 {
		t.Fatalf("result = %#v, handoff calls = %d", result, calls)
	}
}

func TestCodexRunnerAttachesWorkspaceImage(t *testing.T) {
	root := testEnvironment(t)
	pngBytes, err := base64.StdEncoding.DecodeString(testPNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	attachable := filepath.Join(root, "diagram.png")
	if err := os.WriteFile(attachable, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO_WANT_CODEX_HELPER", "attach-image")
	t.Setenv("CODEX_HELPER_ATTACH_PATH", attachable)
	runner := codexRunner{settings: Settings{
		Workspace: root, CodexCommand: []string{os.Args[0], "-test.run=TestCodexHelperProcess"},
		SandboxMode: "read-only", ApprovalPolicy: "never", TurnTimeout: 2 * time.Second,
	}}
	result, err := runner.RunTurn(context.Background(), InboundMessage{ID: "m-attach", Route: "opaque", Text: "send the diagram"}, "", func(context.Context) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(attachable)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Media) != 1 || result.Media[0].Path != resolved {
		t.Fatalf("result media = %#v, want one attachment at %q", result.Media, resolved)
	}
	if result.Text != "here it is" {
		t.Fatalf("result text = %q", result.Text)
	}
}

func TestCodexRunnerRejectsAttachmentOutsideWorkspace(t *testing.T) {
	root := testEnvironment(t)
	pngBytes, err := base64.StdEncoding.DecodeString(testPNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(outside, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO_WANT_CODEX_HELPER", "attach-image")
	t.Setenv("CODEX_HELPER_ATTACH_PATH", outside)
	t.Setenv("CODEX_HELPER_ATTACH_EXPECT_FAILURE", "1")
	runner := codexRunner{settings: Settings{
		Workspace: filepath.Join(root, "workspace"), CodexCommand: []string{os.Args[0], "-test.run=TestCodexHelperProcess"},
		SandboxMode: "read-only", ApprovalPolicy: "never", TurnTimeout: 2 * time.Second,
	}}
	if err := os.MkdirAll(runner.settings.Workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := runner.RunTurn(context.Background(), InboundMessage{ID: "m-reject", Route: "opaque", Text: "send the secret"}, "", func(context.Context) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Media) != 0 {
		t.Fatalf("expected no attachment, got %#v", result.Media)
	}
}

func TestCodexHelperProcess(t *testing.T) {
	mode := os.Getenv("GO_WANT_CODEX_HELPER")
	if mode == "" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request map[string]any
		_ = json.Unmarshal(scanner.Bytes(), &request)
		if capture := os.Getenv("CODEX_HELPER_CAPTURE"); capture != "" {
			file, _ := os.OpenFile(capture, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			_, _ = fmt.Fprintln(file, string(scanner.Bytes()))
			_ = file.Close()
		}
		id := request["id"]
		switch request["method"] {
		case "initialize":
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{}})
		case "thread/start", "thread/resume":
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{"thread": map[string]any{"id": "shared-thread"}}})
		case "turn/start":
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{"turn": map[string]any{"id": "turn-1"}}})
			switch mode {
			case "handoff-image":
				_ = encoder.Encode(map[string]any{"id": 99, "method": "item/tool/call", "params": map[string]any{"turnId": "turn-1", "tool": handoffToolName}})
			case "attach-image":
				arguments, _ := json.Marshal(map[string]any{"path": os.Getenv("CODEX_HELPER_ATTACH_PATH")})
				_ = encoder.Encode(map[string]any{"id": 98, "method": "item/tool/call", "params": map[string]any{"turnId": "turn-1", "tool": attachImageToolName, "arguments": string(arguments)}})
			default:
				writeCompletedTurn(encoder, "final reply", false)
			}
		default:
			if mode == "handoff-image" && request["id"] == float64(99) {
				result := mapValue(request["result"])
				if result["success"] != true {
					os.Exit(3)
				}
				writeCompletedTurn(encoder, "handoff complete", true)
			}
			if mode == "attach-image" && request["id"] == float64(98) {
				result := mapValue(request["result"])
				wantFailure := os.Getenv("CODEX_HELPER_ATTACH_EXPECT_FAILURE") != ""
				if result["success"] != !wantFailure {
					os.Exit(4)
				}
				writeCompletedTurn(encoder, "here it is", false)
			}
		}
	}
}

func writeCompletedTurn(encoder *json.Encoder, text string, includeImage bool) {
	if includeImage {
		_ = encoder.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"turnId": "turn-1", "item": map[string]any{"id": "image-1", "type": "imageGeneration", "result": testPNGBase64}}})
	}
	_ = encoder.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"turnId": "turn-1", "item": map[string]any{"id": "message-1", "type": "agentMessage", "phase": "final_answer", "text": text}}})
	_ = encoder.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]any{"id": "turn-1", "status": "completed"}}})
}

func TestResolveAttachment(t *testing.T) {
	pngBytes, err := base64.StdEncoding.DecodeString(testPNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	workspace, err := filepath.EvalSymlinks(filepath.Join(root, "workspace"))
	if err != nil {
		if mkErr := os.MkdirAll(filepath.Join(root, "workspace"), 0o700); mkErr != nil {
			t.Fatal(mkErr)
		}
		if workspace, err = filepath.EvalSymlinks(filepath.Join(root, "workspace")); err != nil {
			t.Fatal(err)
		}
	}
	outside, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}

	insideImage := filepath.Join(workspace, "diagram.png")
	if err := os.WriteFile(insideImage, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	nestedImage := filepath.Join(workspace, "assets", "nested.png")
	if err := os.MkdirAll(filepath.Dir(nestedImage), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nestedImage, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	notAnImage := filepath.Join(workspace, "notes.txt")
	if err := os.WriteFile(notAnImage, []byte("plain text"), 0o600); err != nil {
		t.Fatal(err)
	}
	secretImage := filepath.Join(outside, "secret.png")
	if err := os.WriteFile(secretImage, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	escape := filepath.Join(workspace, "escape.png")
	if err := os.Symlink(secretImage, escape); err != nil {
		t.Fatal(err)
	}
	inboundImage := filepath.Join(outside, "inbound.png")
	if err := os.WriteFile(inboundImage, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{name: "workspace file", path: insideImage, want: insideImage},
		{name: "nested workspace file", path: nestedImage, want: nestedImage},
		{name: "inbound media outside workspace", path: inboundImage, want: inboundImage},
		{name: "outside workspace", path: secretImage, wantErr: true},
		{name: "symlink escaping workspace", path: escape, wantErr: true},
		{name: "traversal out of workspace", path: filepath.Join(workspace, "..", "secret.png"), wantErr: true},
		{name: "relative path", path: "diagram.png", wantErr: true},
		{name: "empty path", path: "", wantErr: true},
		{name: "directory", path: workspace, wantErr: true},
		{name: "not an image", path: notAnImage, wantErr: true},
		{name: "missing file", path: filepath.Join(workspace, "absent.png"), wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			media, err := resolveAttachment(testCase.path, workspace, []string{inboundImage})
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got media %#v", media)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if media.Path != testCase.want {
				t.Fatalf("media.Path = %q, want %q", media.Path, testCase.want)
			}
		})
	}
}

func TestResolveAttachmentRejectsOriginalOverFiveMiB(t *testing.T) {
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, "oversized.png")
	pngBytes, err := base64.StdEncoding.DecodeString(testPNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, int64(maxOutboundMediaBytes+1)); err != nil {
		t.Fatal(err)
	}
	_, err = resolveAttachment(path, workspace, nil)
	if err == nil || !strings.Contains(err.Error(), "5242880-byte limit") {
		t.Fatalf("error = %v, want 5 MiB limit", err)
	}
}

func TestToolArguments(t *testing.T) {
	object := toolArguments(map[string]any{"arguments": map[string]any{"path": "/tmp/a.png"}})
	if stringValue(object["path"]) != "/tmp/a.png" {
		t.Fatalf("object form = %#v", object)
	}
	encoded := toolArguments(map[string]any{"arguments": `{"path":"/tmp/b.png"}`})
	if stringValue(encoded["path"]) != "/tmp/b.png" {
		t.Fatalf("string form = %#v", encoded)
	}
	if len(toolArguments(map[string]any{})) != 0 {
		t.Fatal("missing arguments should decode to an empty map")
	}
	if len(toolArguments(map[string]any{"arguments": "not json"})) != 0 {
		t.Fatal("invalid arguments should decode to an empty map")
	}
}
