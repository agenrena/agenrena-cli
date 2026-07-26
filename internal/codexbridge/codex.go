package codexbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var optOutNotifications = []string{
	"account/rateLimits/updated", "command/exec/outputDelta",
	"item/commandExecution/outputDelta", "item/commandExecution/terminalInteraction",
	"item/fileChange/outputDelta", "item/plan/delta", "item/reasoning/summaryPartAdded",
	"item/reasoning/summaryTextDelta", "item/reasoning/textDelta",
	"mcpServer/startupStatus/updated", "thread/status/changed", "thread/tokenUsage/updated",
}

type appResponse struct {
	Result any
	Err    error
}

type AppServerClient struct {
	command        []string
	cwd            string
	onNotification func(map[string]any)
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	nextID         atomic.Int64
	pendingMu      sync.Mutex
	pending        map[int64]chan appResponse
	writeMu        sync.Mutex
	stderrMu       sync.Mutex
	stderr         string
	exitMu         sync.Mutex
	exitErr        error
	closed         chan struct{}
	closing        atomic.Bool
}

func NewAppServerClient(command []string, cwd string, onNotification func(map[string]any)) *AppServerClient {
	return &AppServerClient{
		command: command, cwd: cwd, onNotification: onNotification,
		pending: map[int64]chan appResponse{}, closed: make(chan struct{}),
	}
}

func (client *AppServerClient) Start() error {
	if len(client.command) == 0 {
		return fmt.Errorf("empty Codex app-server command")
	}
	client.cmd = exec.Command(client.command[0], client.command[1:]...)
	client.cmd.Dir = client.cwd
	stdout, err := client.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := client.cmd.StderrPipe()
	if err != nil {
		return err
	}
	client.stdin, err = client.cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := client.cmd.Start(); err != nil {
		return err
	}
	go client.readStderr(stderr)
	go client.readStdout(stdout)
	return nil
}

func (client *AppServerClient) Request(ctx context.Context, method string, params map[string]any) (any, error) {
	id := client.nextID.Add(1)
	result := make(chan appResponse, 1)
	client.pendingMu.Lock()
	client.pending[id] = result
	client.pendingMu.Unlock()
	defer func() {
		client.pendingMu.Lock()
		delete(client.pending, id)
		client.pendingMu.Unlock()
	}()
	if err := client.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	select {
	case response := <-result:
		return response.Result, response.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (client *AppServerClient) write(value any) error {
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	if client.stdin == nil {
		return fmt.Errorf("Codex app-server stdin is closed")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	_, err = client.stdin.Write(raw)
	return err
}

func (client *AppServerClient) readStdout(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var message map[string]any
		if json.Unmarshal(scanner.Bytes(), &message) != nil {
			continue
		}
		client.dispatch(message)
	}
	waitErr := client.cmd.Wait()
	if scanner.Err() != nil {
		waitErr = scanner.Err()
	}
	if !client.closing.Load() {
		if text := client.Stderr(); text != "" {
			waitErr = fmt.Errorf("%s", text)
		} else if waitErr != nil {
			waitErr = fmt.Errorf("Codex app-server exited unexpectedly: %w", waitErr)
		} else {
			waitErr = fmt.Errorf("Codex app-server exited unexpectedly")
		}
		client.rejectPending(waitErr)
	}
	client.exitMu.Lock()
	client.exitErr = waitErr
	client.exitMu.Unlock()
	close(client.closed)
}

func (client *AppServerClient) readStderr(reader io.Reader) {
	buffer := make([]byte, 4096)
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			client.stderrMu.Lock()
			client.stderr += string(buffer[:count])
			if len(client.stderr) > 20000 {
				client.stderr = client.stderr[len(client.stderr)-20000:]
			}
			client.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (client *AppServerClient) Stderr() string {
	client.stderrMu.Lock()
	defer client.stderrMu.Unlock()
	return strings.TrimSpace(client.stderr)
}

func (client *AppServerClient) ExitError() error {
	client.exitMu.Lock()
	defer client.exitMu.Unlock()
	return client.exitErr
}

func (client *AppServerClient) dispatch(message map[string]any) {
	method, hasMethod := message["method"].(string)
	rawID, hasID := message["id"]
	if hasMethod && hasID {
		go func() {
			result := any(map[string]any{"decision": "decline"})
			if method == "item/permissions/requestApproval" {
				result = map[string]any{"permissions": map[string]any{}, "scope": "turn"}
			}
			_ = client.write(map[string]any{"id": rawID, "result": result})
		}()
		return
	}
	if hasID {
		id, ok := numberID(rawID)
		if !ok {
			return
		}
		client.pendingMu.Lock()
		pending := client.pending[id]
		client.pendingMu.Unlock()
		if pending == nil {
			return
		}
		if errorValue, ok := message["error"].(map[string]any); ok {
			pending <- appResponse{Err: fmt.Errorf("%s", first(stringValue(errorValue["message"]), "Codex request failed"))}
		} else {
			pending <- appResponse{Result: message["result"]}
		}
		return
	}
	if hasMethod && client.onNotification != nil {
		client.onNotification(message)
	}
}

func numberID(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), true
	case json.Number:
		value, err := typed.Int64()
		return value, err == nil
	case int64:
		return typed, true
	default:
		return 0, false
	}
}

func (client *AppServerClient) rejectPending(err error) {
	client.pendingMu.Lock()
	defer client.pendingMu.Unlock()
	for _, pending := range client.pending {
		select {
		case pending <- appResponse{Err: err}:
		default:
		}
	}
}

func (client *AppServerClient) Close() {
	if client.cmd == nil {
		return
	}
	client.closing.Store(true)
	if client.stdin != nil {
		_ = client.stdin.Close()
		client.stdin = nil
	}
	if client.cmd.Process == nil {
		return
	}
	_ = client.cmd.Process.Signal(os.Interrupt)
	select {
	case <-client.closed:
	case <-time.After(3 * time.Second):
		_ = client.cmd.Process.Kill()
		<-client.closed
	}
}

type CodexRunner struct {
	CodexBin        string
	Workspace       string
	Model           string
	SandboxMode     string
	ApprovalPolicy  string
	Timeout         time.Duration
	CommandOverride []string
}

func (runner *CodexRunner) command() []string {
	if len(runner.CommandOverride) > 0 {
		return append([]string(nil), runner.CommandOverride...)
	}
	command := []string{
		runner.CodexBin, "app-server", "-c", "approval_policy=" + jsonString(runner.ApprovalPolicy),
		"-c", "sandbox_mode=" + jsonString(runner.SandboxMode),
	}
	if runner.Model != "" {
		command = append(command, "-c", "model="+jsonString(runner.Model))
	}
	return command
}

func jsonString(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func (runner *CodexRunner) RunTurn(ctx context.Context, message IncomingMessage, threadID string, media []MaterializedMedia) (CodexTurnResult, error) {
	var mu sync.Mutex
	activeTurnID := ""
	finalReply, fallbackReply := "", ""
	agentMessages := map[string]map[string]string{}
	var early []map[string]any
	completion := make(chan appResponse, 1)

	onNotification := func(notification map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		if activeTurnID == "" {
			early = append(early, notification)
			return
		}
		handleCodexNotification(notification, activeTurnID, agentMessages, &finalReply, &fallbackReply, completion)
	}
	client := NewAppServerClient(runner.command(), runner.Workspace, onNotification)
	if err := client.Start(); err != nil {
		return CodexTurnResult{}, err
	}
	defer client.Close()
	requestContext := ctx
	timeout := runner.Timeout
	if timeout == 0 {
		timeout = 900 * time.Second
	}
	requestContext, cancel := context.WithTimeout(requestContext, timeout)
	defer cancel()

	_, err := client.Request(requestContext, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "agenrena-codex-bridge", "title": "Agenrena Codex Bridge", "version": Version},
		"capabilities": map[string]any{"experimentalApi": false, "optOutNotificationMethods": optOutNotifications},
	})
	if err != nil {
		return CodexTurnResult{}, err
	}
	threadParams := map[string]any{"cwd": runner.Workspace, "approvalPolicy": runner.ApprovalPolicy}
	if runner.Model != "" {
		threadParams["model"] = runner.Model
	}
	method := "thread/start"
	if threadID != "" {
		method = "thread/resume"
		threadParams["threadId"] = threadID
	}
	threadRaw, err := client.Request(requestContext, method, threadParams)
	if err != nil {
		return CodexTurnResult{}, err
	}
	resolvedThreadID := nestedString(threadRaw, "thread", "id")
	if resolvedThreadID == "" {
		return CodexTurnResult{}, fmt.Errorf("Codex app-server did not return a thread id")
	}
	input := []any{}
	if metadata := senderMetadata(message); metadata != nil {
		input = append(input, metadata)
	}
	if message.Text != "" {
		input = append(input, map[string]any{"type": "text", "text": message.Text, "text_elements": []any{}})
	}
	for _, item := range media {
		if item.Kind == "sticker" {
			input = append(input, map[string]any{
				"type": "text", "text": "The user sent the following sticker.", "text_elements": []any{},
			})
		}
		absolute, _ := filepath.Abs(item.Path)
		input = append(input, map[string]any{"type": "localImage", "path": absolute})
	}
	if len(input) == 0 {
		return CodexTurnResult{}, fmt.Errorf("a Codex turn requires text or media input")
	}
	policy, err := sandboxPolicy(runner.SandboxMode)
	if err != nil {
		return CodexTurnResult{}, err
	}
	turnParams := map[string]any{
		"threadId": resolvedThreadID, "input": input, "cwd": runner.Workspace,
		"approvalPolicy": runner.ApprovalPolicy, "sandboxPolicy": policy,
		"clientUserMessageId": message.MessageID,
	}
	if runner.Model != "" {
		turnParams["model"] = runner.Model
	}
	turnRaw, err := client.Request(requestContext, "turn/start", turnParams)
	if err != nil {
		return CodexTurnResult{}, err
	}
	mu.Lock()
	activeTurnID = nestedString(turnRaw, "turn", "id")
	buffered := early
	early = nil
	for _, notification := range buffered {
		handleCodexNotification(notification, activeTurnID, agentMessages, &finalReply, &fallbackReply, completion)
	}
	mu.Unlock()
	if activeTurnID == "" {
		return CodexTurnResult{}, fmt.Errorf("Codex app-server did not return a turn id")
	}

	var completed appResponse
	select {
	case completed = <-completion:
	case <-client.closed:
		err := client.ExitError()
		if err == nil {
			err = fmt.Errorf("Codex app-server exited unexpectedly")
		}
		return CodexTurnResult{}, err
	case <-requestContext.Done():
		if requestContext.Err() == context.DeadlineExceeded {
			return CodexTurnResult{}, fmt.Errorf("Codex turn exceeded %d seconds", int(timeout.Seconds()))
		}
		return CodexTurnResult{}, requestContext.Err()
	}
	if completed.Err != nil {
		return CodexTurnResult{}, completed.Err
	}
	turn, _ := completed.Result.(map[string]any)
	status := stringValue(turn["status"])
	if status != "completed" && status != "success" {
		return CodexTurnResult{}, fmt.Errorf("%v", first(stringValue(turn["error"]), "Codex turn ended with status "+first(status, "unknown")))
	}
	mu.Lock()
	replyText := strings.TrimSpace(first(finalReply, fallbackReply))
	mu.Unlock()
	if replyText == "" {
		return CodexTurnResult{}, fmt.Errorf("Codex completed without a final message")
	}
	return CodexTurnResult{ThreadID: resolvedThreadID, TurnID: activeTurnID, ReplyText: replyText}, nil
}

func sandboxPolicy(mode string) (map[string]string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "read-only":
		return map[string]string{"type": "readOnly"}, nil
	case "workspace-write":
		return map[string]string{"type": "workspaceWrite"}, nil
	case "danger-full-access":
		return map[string]string{"type": "dangerFullAccess"}, nil
	default:
		return nil, fmt.Errorf("unsupported Codex sandbox mode: %s", mode)
	}
}

func nestedString(value any, keys ...string) string {
	current := value
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	return stringValue(current)
}

func handleCodexNotification(notification map[string]any, turnID string, messages map[string]map[string]string, finalReply, fallbackReply *string, completion chan appResponse) {
	method := stringValue(notification["method"])
	params, ok := notification["params"].(map[string]any)
	if !ok {
		return
	}
	if method == "turn/completed" {
		turn, _ := params["turn"].(map[string]any)
		if stringValue(turn["id"]) == turnID {
			select {
			case completion <- appResponse{Result: turn}:
			default:
			}
		}
		return
	}
	if stringValue(params["turnId"]) != turnID {
		return
	}
	switch method {
	case "item/started":
		item, _ := params["item"].(map[string]any)
		if stringValue(item["type"]) == "agentMessage" {
			messages[stringValue(item["id"])] = map[string]string{
				"phase": stringValue(item["phase"]), "text": untrimmedString(item["text"]),
			}
		}
	case "item/agentMessage/delta":
		id := stringValue(params["itemId"])
		if messages[id] == nil {
			messages[id] = map[string]string{}
		}
		messages[id]["text"] += untrimmedString(params["delta"])
	case "item/completed":
		item, _ := params["item"].(map[string]any)
		if stringValue(item["type"]) != "agentMessage" {
			return
		}
		id := stringValue(item["id"])
		current := messages[id]
		phase := first(stringValue(item["phase"]), current["phase"])
		text := strings.TrimSpace(first(untrimmedString(item["text"]), current["text"]))
		messages[id] = map[string]string{"phase": phase, "text": text}
		if text != "" {
			*fallbackReply = text
			if phase == "final_answer" {
				*finalReply = text
			}
		}
	case "error":
		if params["willRetry"] == true {
			return
		}
		detail := "Codex turn failed"
		if value, ok := params["error"].(map[string]any); ok {
			detail = first(stringValue(value["message"]), detail)
		} else if params["error"] != nil {
			detail = fmt.Sprint(params["error"])
		}
		select {
		case completion <- appResponse{Err: fmt.Errorf("%s", detail)}:
		default:
		}
	}
}

func untrimmedString(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
