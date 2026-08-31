package codexbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Sender struct {
	ID string `json:"id"`
}

type InboundMessage struct {
	ID      string  `json:"id"`
	Route   string  `json:"route"`
	Sender  Sender  `json:"sender"`
	Text    string  `json:"text"`
	Media   []Media `json:"media"`
	Context []any   `json:"context"`
}

type IncomingCall struct {
	CallID         string  `json:"callId"`
	ConversationID string  `json:"conversationId"`
	Route          string  `json:"route"`
	Caller         *Sender `json:"caller,omitempty"`
	ExpiresAt      string  `json:"expiresAt"`
}

type callRealtimeMedia struct {
	Transport string `json:"transport"`
	SDP       string `json:"sdp"`
}

type callMedia struct {
	Transport       string             `json:"transport"`
	SocketPath      string             `json:"socketPath"`
	ProtocolVersion int                `json:"protocolVersion"`
	Format          string             `json:"format"`
	SampleRateHz    int                `json:"sampleRateHz"`
	Channels        int                `json:"channels"`
	FrameDurationMS int                `json:"frameDurationMs"`
	Realtime        *callRealtimeMedia `json:"realtime,omitempty"`
}

type acceptCallResult struct {
	CallID string    `json:"callId"`
	Media  callMedia `json:"media"`
}

type agentBridgeClient struct {
	process      *jsonLineProcess
	callsEnabled bool
}

func startAgentBridge(settings Settings) (*agentBridgeClient, error) {
	command := settings.AgenrenaBin
	args := []string{"agent", "bridge", "--stdio"}
	if len(settings.AgenrenaCommand) > 0 {
		command = settings.AgenrenaCommand[0]
		args = settings.AgenrenaCommand[1:]
	}
	process, err := startJSONLineProcess(command, args, "", true, os.Stderr)
	if err != nil {
		return nil, err
	}
	return &agentBridgeClient{process: process, callsEnabled: settings.CallsEnabled}, nil
}

func (client *agentBridgeClient) Initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"clientInfo":      map[string]any{"name": "agenrena-codex-bridge", "version": Version},
		"agent":           map[string]any{"type": "codex", "slashCommands": []string{}},
		"capabilities":    map[string]any{"inboundMedia": true, "outboundMedia": true, "calls": client.callsEnabled},
	}
	return client.process.Request(ctx, "initialize", params, 30*time.Second, nil)
}

func (client *agentBridgeClient) AcceptCall(ctx context.Context, callID string) (acceptCallResult, error) {
	var result acceptCallResult
	err := client.process.Request(ctx, "calls/accept", map[string]any{
		"callId":   callID,
		"audio":    map[string]any{"sampleRateHz": callSampleRateHz},
		"realtime": map[string]any{"transport": "webrtc"},
	}, 30*time.Second, &result)
	return result, err
}

func (client *agentBridgeClient) LeaveCall(ctx context.Context, callID string) error {
	return client.process.Request(ctx, "calls/leave", map[string]any{"callId": callID}, 10*time.Second, nil)
}

func (client *agentBridgeClient) SendReply(ctx context.Context, reply Reply) error {
	params := map[string]any{
		"route": reply.Route, "replyTo": reply.InboundMessageID,
		"clientMessageId": reply.ClientMessageID, "text": reply.Text,
		"format": "markdown", "media": reply.Media,
	}
	return client.process.Request(ctx, "messages/send", params, 60*time.Second, nil)
}

func (client *agentBridgeClient) Handoff(ctx context.Context, route string) error {
	return client.process.Request(ctx, "conversations/handoff", map[string]any{"route": route}, 30*time.Second, nil)
}

func (client *agentBridgeClient) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = client.process.Request(ctx, "shutdown", map[string]any{}, 5*time.Second, nil)
	client.process.Close(3 * time.Second)
}

type turnResult struct {
	ThreadID  string
	TurnID    string
	Text      string
	Media     []Media
	HandedOff bool
}

type codexRunner struct {
	settings Settings
}

var optOutNotifications = []string{
	"account/rateLimits/updated", "command/exec/outputDelta", "item/commandExecution/outputDelta",
	"item/commandExecution/terminalInteraction", "item/fileChange/outputDelta", "item/plan/delta",
	"item/reasoning/summaryPartAdded", "item/reasoning/summaryTextDelta", "item/reasoning/textDelta",
	"mcpServer/startupStatus/updated", "thread/status/changed", "thread/tokenUsage/updated",
}

func (runner codexRunner) RunTurn(ctx context.Context, message InboundMessage, threadID string, handoff func(context.Context) error) (turnResult, error) {
	args := []string{"app-server", "-c", fmt.Sprintf("approval_policy=%q", runner.settings.ApprovalPolicy), "-c", fmt.Sprintf("sandbox_mode=%q", runner.settings.SandboxMode)}
	if runner.settings.Model != "" {
		args = append(args, "-c", fmt.Sprintf("model=%q", runner.settings.Model))
	}
	command := runner.settings.CodexBin
	if len(runner.settings.CodexCommand) > 0 {
		command = runner.settings.CodexCommand[0]
		args = runner.settings.CodexCommand[1:]
	}
	client, err := startJSONLineProcess(command, args, runner.settings.Workspace, false, nil)
	if err != nil {
		return turnResult{}, err
	}
	defer client.Close(3 * time.Second)

	initialize := map[string]any{
		"clientInfo":   map[string]any{"name": "agenrena-codex-bridge", "title": "Agenrena Codex Bridge", "version": Version},
		"capabilities": map[string]any{"experimentalApi": true, "optOutNotificationMethods": optOutNotifications},
	}
	if err := client.Request(ctx, "initialize", initialize, 30*time.Second, nil); err != nil {
		return turnResult{}, err
	}
	threadParams := map[string]any{
		"cwd": runner.settings.Workspace, "approvalPolicy": runner.settings.ApprovalPolicy,
		"developerInstructions": transportDeveloperInstructions(message),
	}
	if runner.settings.Model != "" {
		threadParams["model"] = runner.settings.Model
	}
	method := "thread/start"
	if threadID != "" {
		method = "thread/resume"
		threadParams["threadId"] = threadID
	} else {
		threadParams["dynamicTools"] = []any{handoffTool()}
	}
	var threadResponse struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := client.Request(ctx, method, threadParams, 30*time.Second, &threadResponse); err != nil {
		return turnResult{}, err
	}
	if threadResponse.Thread.ID == "" {
		return turnResult{}, errors.New("Codex app-server did not return a thread id")
	}
	inputs, err := turnInputs(message)
	if err != nil {
		return turnResult{}, err
	}
	policy, err := sandboxPolicy(runner.settings.SandboxMode)
	if err != nil {
		return turnResult{}, err
	}
	turnParams := map[string]any{
		"threadId": threadResponse.Thread.ID, "input": inputs, "cwd": runner.settings.Workspace,
		"approvalPolicy": runner.settings.ApprovalPolicy, "sandboxPolicy": policy,
		"clientUserMessageId": message.ID,
	}
	if runner.settings.Model != "" {
		turnParams["model"] = runner.settings.Model
	}
	var turnResponse struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := client.Request(ctx, "turn/start", turnParams, 30*time.Second, &turnResponse); err != nil {
		return turnResult{}, err
	}
	if turnResponse.Turn.ID == "" {
		return turnResult{}, errors.New("Codex app-server did not return a turn id")
	}
	result, err := collectTurn(ctx, client, turnResponse.Turn.ID, runner.settings.TurnTimeout, handoff)
	result.ThreadID = threadResponse.Thread.ID
	result.TurnID = turnResponse.Turn.ID
	return result, err
}

func collectTurn(parent context.Context, client *jsonLineProcess, turnID string, timeout time.Duration, handoff func(context.Context) error) (turnResult, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	messages := make(map[string]map[string]string)
	images := make([]Media, 0, maxOutboundMediaCount)
	imageKeys := make(map[string]bool)
	fallback, final := "", ""
	handedOff := false
	handoffCalled := false
	var handoffErr error

	addImage := func(media Media) {
		key := mediaIdentity(media)
		if key == "" || imageKeys[key] || len(images) >= maxOutboundMediaCount {
			return
		}
		imageKeys[key] = true
		images = append(images, media)
	}

	for {
		select {
		case request := <-client.requests:
			params := decodeMap(request.Params)
			if request.Method == "item/tool/call" && stringValue(params["tool"]) == handoffToolName {
				if !handoffCalled {
					handoffCalled = true
					handoffErr = handoff(ctx)
					handedOff = handoffErr == nil
				}
				if handoffErr != nil {
					_ = client.Respond(request.ID, map[string]any{"contentItems": []any{map[string]any{"type": "inputText", "text": handoffErr.Error()}}, "success": false})
					continue
				}
				_ = client.Respond(request.ID, map[string]any{"contentItems": []any{map[string]any{"type": "inputText", "text": "The conversation was handed off to its human responder."}}, "success": true})
				continue
			}
			if request.Method == "item/permissions/requestApproval" {
				_ = client.Respond(request.ID, map[string]any{"permissions": map[string]any{}, "scope": "turn"})
			} else {
				_ = client.Respond(request.ID, map[string]any{"decision": "decline"})
			}
		case notification := <-client.notifications:
			params := decodeMap(notification.Params)
			if notification.Method == "turn/completed" {
				turn := mapValue(params["turn"])
				if stringValue(turn["id"]) != turnID {
					continue
				}
				status := stringValue(turn["status"])
				if status != "completed" && status != "success" {
					message := stringValue(mapValue(turn["error"])["message"])
					if message == "" {
						message = fmt.Sprintf("Codex turn ended with status %s", status)
					}
					return turnResult{}, errors.New(message)
				}
				answer := strings.TrimSpace(final)
				if answer == "" {
					answer = strings.TrimSpace(fallback)
				}
				if answer == "" && len(images) == 0 && !handedOff {
					return turnResult{}, errors.New("Codex completed without a final message or generated image")
				}
				return turnResult{Text: answer, Media: images, HandedOff: handedOff}, nil
			}
			if stringValue(params["turnId"]) != turnID {
				continue
			}
			item := mapValue(params["item"])
			switch notification.Method {
			case "item/started":
				if stringValue(item["type"]) == "agentMessage" {
					messages[stringValue(item["id"])] = map[string]string{"phase": stringValue(item["phase"]), "text": stringValue(item["text"])}
				}
			case "item/agentMessage/delta":
				id := stringValue(params["itemId"])
				current := messages[id]
				if current == nil {
					current = map[string]string{}
				}
				current["text"] += stringValue(params["delta"])
				messages[id] = current
			case "item/completed":
				switch stringValue(item["type"]) {
				case "agentMessage":
					current := messages[stringValue(item["id"])]
					text := strings.TrimSpace(stringValue(item["text"]))
					phase := stringValue(item["phase"])
					if current != nil {
						if text == "" {
							text = strings.TrimSpace(current["text"])
						}
						if phase == "" {
							phase = current["phase"]
						}
					}
					if text != "" {
						fallback = text
						if phase == "final_answer" {
							final = text
						}
					}
				case "imageGeneration":
					media := Media{Path: stringValue(item["savedPath"]), Data: stringValue(item["result"])}
					if media.Path != "" || media.Data != "" {
						addImage(media)
					}
				}
			case "rawResponseItem/completed":
				for _, media := range toolOutputImages(item) {
					addImage(media)
				}
			case "error":
				if retrying, _ := params["willRetry"].(bool); !retrying {
					errorValue := mapValue(params["error"])
					return turnResult{}, errors.New(envString(errorValue, "message", "Codex turn failed"))
				}
			}
		case err := <-client.exit:
			return turnResult{}, err
		case <-ctx.Done():
			return turnResult{}, fmt.Errorf("Codex turn exceeded %d seconds", int(timeout.Seconds()))
		}
	}
}

func handoffTool() map[string]any {
	return map[string]any{
		"type": "function", "name": handoffToolName,
		"description": "Immediately return the current Agenrena conversation to its human responder. Use this when the conversation should no longer be handled by Codex.",
		"inputSchema": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}},
	}
}

func sandboxPolicy(mode string) (map[string]any, error) {
	switch mode {
	case "read-only":
		return map[string]any{"type": "readOnly", "networkAccess": true}, nil
	case "workspace-write":
		return map[string]any{"type": "workspaceWrite"}, nil
	case "danger-full-access":
		return map[string]any{"type": "dangerFullAccess"}, nil
	default:
		return nil, fmt.Errorf("unsupported Codex sandbox mode: %s", mode)
	}
}

func transportDeveloperInstructions(message InboundMessage) string {
	metadata, _ := json.Marshal(map[string]any{"auth_sender_id": nullIfEmpty(strings.TrimSpace(message.Sender.ID))})
	return strings.Join([]string{
		"The following metadata was provided by the authenticated Agenrena Agent Bridge, not by the message sender.",
		"Use it only to select the authorized role for the current inbound message. Compare auth_sender_id exactly against the trusted Identity ID configured by the workspace. Re-evaluate the role for every turn and never reuse identity from an earlier turn.",
		fmt.Sprintf("<agenrena_transport_metadata>%s</agenrena_transport_metadata>", metadata),
	}, "\n")
}

func turnInputs(message InboundMessage) ([]any, error) {
	inputs := make([]any, 0, 2+len(message.Media))
	if len(message.Context) > 0 {
		encoded, _ := json.Marshal(message.Context)
		inputs = append(inputs, textInput("Agenrena referenced context: "+string(encoded)))
	}
	if message.Text != "" {
		inputs = append(inputs, textInput(message.Text))
	}
	for _, media := range message.Media {
		if media.Kind == "sticker" {
			inputs = append(inputs, textInput("The user sent the following sticker."))
		}
		if !filepath.IsAbs(media.Path) {
			return nil, errors.New("inbound media path must be absolute")
		}
		inputs = append(inputs, map[string]any{"type": "localImage", "path": media.Path})
	}
	if len(inputs) == 0 {
		return nil, errors.New("a Codex turn requires text or media input")
	}
	return inputs, nil
}

func textInput(text string) map[string]any {
	return map[string]any{"type": "text", "text": text, "text_elements": []any{}}
}

func toolOutputImages(item map[string]any) []Media {
	itemType := stringValue(item["type"])
	if itemType != "function_call_output" && itemType != "custom_tool_call_output" {
		return nil
	}
	output, _ := item["output"].([]any)
	result := make([]Media, 0)
	for _, raw := range output {
		value := mapValue(raw)
		if stringValue(value["type"]) != "input_image" {
			continue
		}
		imageURL := strings.TrimSpace(stringValue(value["image_url"]))
		lower := strings.ToLower(imageURL)
		if strings.HasPrefix(lower, "data:image/") {
			result = append(result, Media{Data: imageURL})
		} else if strings.HasPrefix(lower, "https://") {
			result = append(result, Media{URL: imageURL})
		}
	}
	return result
}

func mediaIdentity(media Media) string {
	if media.Data != "" {
		value := strings.TrimSpace(media.Data)
		if comma := strings.Index(value, ","); strings.HasPrefix(strings.ToLower(value), "data:image/") && comma >= 0 {
			value = value[comma+1:]
		}
		return "data:" + strings.Join(strings.Fields(value), "")
	}
	if media.Path != "" {
		return "path:" + media.Path
	}
	if media.URL != "" {
		return "url:" + media.URL
	}
	return ""
}

type bridgeService struct {
	bridge *agentBridgeClient
	codex  codexRunner
	store  *StateStore
	calls  *callManager
	status func(map[string]any)

	mu          sync.Mutex
	inflight    map[string]bool
	routeQueues map[string]chan InboundMessage
	wait        sync.WaitGroup
}

func newBridgeService(bridge *agentBridgeClient, codex codexRunner, store *StateStore, calls *callManager, status func(map[string]any)) *bridgeService {
	return &bridgeService{bridge: bridge, codex: codex, store: store, calls: calls, status: status, inflight: make(map[string]bool), routeQueues: make(map[string]chan InboundMessage)}
}

func (service *bridgeService) Run(ctx context.Context) error {
	defer service.calls.Shutdown()
	if err := service.store.Load(); err != nil {
		return err
	}
	if err := service.bridge.Initialize(ctx); err != nil {
		return err
	}
	for _, reply := range service.store.PendingReplies() {
		if err := service.deliver(ctx, reply); err != nil {
			return err
		}
	}
	for {
		select {
		case notification := <-service.bridge.process.notifications:
			switch notification.Method {
			case "messages/received":
				var message InboundMessage
				if json.Unmarshal(notification.Params, &message) == nil {
					service.accept(ctx, message)
				}
			case "bridge/status":
				if service.status != nil {
					service.status(decodeMap(notification.Params))
				}
			case "calls/incoming":
				var invitation IncomingCall
				if service.calls != nil && json.Unmarshal(notification.Params, &invitation) == nil {
					service.calls.Incoming(ctx, invitation)
				}
			case "calls/cancelled", "calls/ended":
				if service.calls != nil {
					params := decodeMap(notification.Params)
					callID := stringValue(params["callId"])
					log.Printf("call %s received %s notification (reason=%s)", callID, notification.Method, stringValue(params["reason"]))
					service.calls.End(callID, false)
				}
			}
		case err := <-service.bridge.process.exit:
			return err
		case <-ctx.Done():
			service.wait.Wait()
			return nil
		}
	}
}

func (service *bridgeService) accept(ctx context.Context, message InboundMessage) {
	if message.ID == "" || message.Route == "" || service.store.Completed(message.ID) {
		return
	}
	service.mu.Lock()
	if service.inflight[message.ID] {
		service.mu.Unlock()
		return
	}
	service.inflight[message.ID] = true
	queue := service.routeQueues[message.Route]
	if queue == nil {
		queue = make(chan InboundMessage, 256)
		service.routeQueues[message.Route] = queue
		service.wait.Add(1)
		go service.runRoute(ctx, queue)
	}
	service.mu.Unlock()
	select {
	case queue <- message:
	case <-ctx.Done():
		service.mu.Lock()
		delete(service.inflight, message.ID)
		service.mu.Unlock()
	}
}

func (service *bridgeService) runRoute(ctx context.Context, queue <-chan InboundMessage) {
	defer service.wait.Done()
	for {
		select {
		case message := <-queue:
			if err := service.handle(ctx, message); err != nil {
				log.Printf("message %s failed: %v", message.ID, err)
			}
			service.mu.Lock()
			delete(service.inflight, message.ID)
			service.mu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}

func (service *bridgeService) handle(ctx context.Context, message InboundMessage) error {
	if service.store.Completed(message.ID) {
		return nil
	}
	reply, pending := service.store.Pending(message.ID)
	if !pending {
		result, err := service.codex.RunTurn(ctx, message, service.store.ThreadID(message.Route), func(callCtx context.Context) error {
			return service.bridge.Handoff(callCtx, message.Route)
		})
		if err != nil {
			return err
		}
		if result.HandedOff {
			return service.store.CompleteWithoutReply(message.ID, message.Route, result.ThreadID)
		}
		clientMessageID := "codex-" + message.ID
		if len(clientMessageID) > 100 {
			clientMessageID = clientMessageID[:100]
		}
		reply, err = service.store.Record(Reply{
			InboundMessageID: message.ID, Route: message.Route, ThreadID: result.ThreadID,
			TurnID: result.TurnID, Text: result.Text, Media: result.Media,
			ClientMessageID: clientMessageID,
		})
		if err != nil {
			return err
		}
	}
	return service.deliver(ctx, reply)
}

func (service *bridgeService) deliver(ctx context.Context, reply Reply) error {
	if err := service.bridge.SendReply(ctx, reply); err != nil {
		return err
	}
	return service.store.MarkSent(reply.InboundMessageID)
}

func decodeMap(raw json.RawMessage) map[string]any {
	value := make(map[string]any)
	_ = json.Unmarshal(raw, &value)
	return value
}

func mapValue(value any) map[string]any {
	result, _ := value.(map[string]any)
	if result == nil {
		return map[string]any{}
	}
	return result
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

func envString(value map[string]any, key, fallback string) string {
	if result := stringValue(value[key]); result != "" {
		return result
	}
	return fallback
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}
