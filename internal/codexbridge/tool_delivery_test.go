package codexbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/agenrena/agenrena-cli/internal/agentbridge"
)

type deliveryTestWriter func([]byte) (int, error)

func (write deliveryTestWriter) Write(data []byte) (int, error) { return write(data) }
func (write deliveryTestWriter) Close() error                   { return nil }

// Exercise the actual JSON-RPC request path with a backend that deduplicates
// clientMessageId, and permit failures at the boundary after backend delivery.
func deliveryTestBridge(t *testing.T, send func(agentbridge.SendParams) (agentbridge.SendResult, error)) *agentBridgeClient {
	t.Helper()
	process := &jsonLineProcess{pending: make(map[int]chan rpcReply)}
	process.stdin = deliveryTestWriter(func(data []byte) (int, error) {
		var request struct {
			ID     int                    `json:"id"`
			Method string                 `json:"method"`
			Params agentbridge.SendParams `json:"params"`
		}
		if err := json.Unmarshal(data, &request); err != nil {
			return 0, err
		}
		var result agentbridge.SendResult
		var err error
		if request.Method == "messages/send" {
			result, err = send(request.Params)
		}
		raw, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return 0, marshalErr
		}
		process.pendingMu.Lock()
		wait := process.pending[request.ID]
		delete(process.pending, request.ID)
		process.pendingMu.Unlock()
		wait <- rpcReply{result: raw, err: err}
		return len(data), nil
	})
	return &agentBridgeClient{process: process}
}

func deliveryTestMessage(t *testing.T) InboundMessage {
	message := publicFormMessage(t)
	message.Capabilities["supported_reply_types"] = []any{"input_request", "showcase"}
	return message
}

func deliveryTestTool(options turnToolOptions, name string) (toolSender, map[string]any) {
	if name == inputRequestToolName {
		return options.sendInputRequest, runtimeInputRequest()
	}
	return options.sendShowcase, runtimeShowcase()
}

func TestToolDeliveryRecoveryDoesNotRerunModel(t *testing.T) {
	for _, name := range []string{inputRequestToolName, showcaseToolName} {
		for _, failure := range []string{"before-send", "lost-response", "result-write", "model-failed"} {
			t.Run(name+"/"+failure, func(t *testing.T) {
				ctx := context.Background()
				message := deliveryTestMessage(t)
				path := filepath.Join(t.TempDir(), "state.json")
				store := NewStateStore(path)
				service := &bridgeService{store: store}
				requests := 0
				backend := map[string]string{}
				service.bridge = deliveryTestBridge(t, func(params agentbridge.SendParams) (agentbridge.SendResult, error) {
					requests++
					// The on-disk intent must exist before even the first send.
					disk := NewStateStore(path)
					if err := disk.Load(); err != nil {
						t.Fatal(err)
					}
					if len(disk.toolDeliveries(message.ID)) != 1 {
						t.Fatal("send preceded durable intent")
					}
					raw, _ := json.Marshal(params)
					if previous, exists := backend[params.ClientMessageID]; exists && previous != string(raw) {
						t.Fatal("retry changed payload")
					}
					backend[params.ClientMessageID] = string(raw)
					result := agentbridge.SendResult{MessageID: "sent", ClientMessageID: params.ClientMessageID}
					if requests == 1 && failure == "lost-response" {
						return result, errors.New("connection lost after backend commit")
					}
					if requests == 1 && failure == "result-write" {
						store.path = t.TempDir()
					}
					return result, nil
				})
				options := service.turnTools(message)
				options.setThreadID("original-thread")
				send, args := deliveryTestTool(options, name)
				if failure == "before-send" {
					build := inputRequestParams
					if name == showcaseToolName {
						build = showcaseParams
					}
					params, err := build(message, "old-call", args)
					if err != nil {
						t.Fatal(err)
					}
					raw, _ := json.Marshal(params)
					body, _ := json.Marshal(args)
					if err := store.saveToolDelivery(message.ID, toolDelivery{Tool: name, CallID: "old-call", ThreadID: "original-thread", Arguments: string(body), Params: raw}); err != nil {
						t.Fatal(err)
					}
				} else {
					_, err := send(ctx, "old-call", args)
					if (failure == "model-failed") != (err == nil) {
						t.Fatalf("unexpected send error: %v", err)
					}
				}
				// Recreate state as on process restart. handle must finish the
				// saved operation without starting Codex with a new call ID.
				service.store = NewStateStore(path)
				if err := service.store.Load(); err != nil {
					t.Fatal(err)
				}
				if err := service.handle(ctx, message); err != nil {
					t.Fatal(err)
				}
				if err := service.handle(ctx, message); err != nil {
					t.Fatal(err)
				}
				wantRequests := 2
				if failure == "before-send" || failure == "model-failed" {
					wantRequests = 1
				}
				if requests != wantRequests || len(backend) != 1 {
					t.Fatalf("requests=%d distinct messages=%d", requests, len(backend))
				}
				disk := NewStateStore(path)
				if err := disk.Load(); err != nil {
					t.Fatal(err)
				}
				if !disk.Completed(message.ID) || len(disk.toolDeliveries(message.ID)) != 0 || disk.ThreadID(message.Route) != "original-thread" {
					t.Fatal("recovery was not committed or thread was lost")
				}
			})
		}
	}
}

func TestToolDeliveryFailsClosedAndRetainsOriginalPayload(t *testing.T) {
	for _, name := range []string{inputRequestToolName, showcaseToolName} {
		t.Run(name, func(t *testing.T) {
			message := deliveryTestMessage(t)
			path := filepath.Join(t.TempDir(), "state.json")
			store := NewStateStore(t.TempDir()) // Cannot replace a directory with a file.
			requests := 0
			service := &bridgeService{store: store, bridge: deliveryTestBridge(t, func(params agentbridge.SendParams) (agentbridge.SendResult, error) {
				requests++
				return agentbridge.SendResult{}, errors.New("lost response")
			})}
			send, args := deliveryTestTool(service.turnTools(message), name)
			if _, err := send(context.Background(), "call", args); err == nil {
				t.Fatal("write failure was ignored")
			}
			if requests != 0 || len(store.toolDeliveries(message.ID)) != 0 {
				t.Fatal("failed intent write still allowed a send or changed state")
			}
			store.path = path
			if _, err := send(context.Background(), "call", args); err == nil {
				t.Fatal("transport failure was ignored")
			}
			original := string(store.toolDeliveries(message.ID)[0].Params)
			if name == inputRequestToolName {
				args["question"] = "Changed"
			} else {
				args["showcase"].(map[string]any)["items"].([]any)[0].(map[string]any)["title"] = "Changed"
			}
			if string(store.toolDeliveries(message.ID)[0].Params) != original {
				t.Fatal("caller mutated journal")
			}
			if _, err := send(context.Background(), "call", args); err == nil {
				t.Fatal("changed retry accepted")
			}
			other, otherArgs := deliveryTestTool(service.turnTools(message), inputRequestToolName)
			if _, err := other(context.Background(), "new-call", otherArgs); err == nil {
				t.Fatal("new operation bypassed uncertain delivery")
			}
			if requests != 1 {
				t.Fatalf("unexpected requests: %d", requests)
			}
		})
	}
}

func TestToolDeliveryRejectionsAndConflicts(t *testing.T) {
	for _, code := range []string{"INPUT_REQUEST_INVALID", "SHOWCASE_INVALID", "SHOWCASE_CONFLICT", "INPUT_REQUEST_CONFLICT", "DELIVERY_UNKNOWN", "CONVERSATION_AGENT_REPLY_DISABLED"} {
		t.Run(code, func(t *testing.T) {
			message := deliveryTestMessage(t)
			store := NewStateStore(filepath.Join(t.TempDir(), "state.json"))
			requests := 0
			service := &bridgeService{store: store, bridge: deliveryTestBridge(t, func(params agentbridge.SendParams) (agentbridge.SendResult, error) {
				requests++
				if requests == 1 {
					return agentbridge.SendResult{}, &agentbridge.RPCError{Code: code, Message: code}
				}
				return agentbridge.SendResult{MessageID: "sent"}, nil
			})}
			send := service.turnTools(message).sendInputRequest
			if _, err := send(context.Background(), "first", runtimeInputRequest()); err == nil {
				t.Fatal("missing backend error")
			}
			if replyAuthorityLost(&agentbridge.RPCError{Code: code}) {
				if err := service.handle(context.Background(), message); err != nil {
					t.Fatal(err)
				}
				if requests != 1 || !store.Completed(message.ID) {
					t.Fatal("revoked operation replayed")
				}
				return
			}
			_, err := send(context.Background(), "second", runtimeInputRequest())
			allowed := code == "INPUT_REQUEST_INVALID" || code == "SHOWCASE_INVALID"
			if allowed != (err == nil) {
				t.Fatalf("replacement error=%v", err)
			}
			if !allowed && requests != 1 {
				t.Fatal("conflict/unknown outcome was bypassed")
			}
		})
	}
}

func TestToolDeliveryCompletionWriteFailureRetainsRecoveryState(t *testing.T) {
	for _, followup := range []bool{false, true} {
		t.Run(fmt.Sprint(followup), func(t *testing.T) {
			message := deliveryTestMessage(t)
			path := filepath.Join(t.TempDir(), "state.json")
			store := NewStateStore(path)
			service := &bridgeService{store: store, bridge: deliveryTestBridge(t, func(params agentbridge.SendParams) (agentbridge.SendResult, error) {
				return agentbridge.SendResult{MessageID: "sent"}, nil
			})}
			if _, err := service.turnTools(message).sendShowcase(context.Background(), "call", runtimeShowcase()); err != nil {
				t.Fatal(err)
			}
			if followup {
				if _, err := store.Record(Reply{InboundMessageID: message.ID, Route: message.Route, Text: "follow up", ClientMessageID: "followup"}); err != nil {
					t.Fatal(err)
				}
			}
			store.path = t.TempDir()
			if _, err := service.recoverToolDeliveries(context.Background(), message.ID); err == nil {
				t.Fatal("completion write failure was ignored")
			}
			if store.Completed(message.ID) || len(store.toolDeliveries(message.ID)) != 1 {
				t.Fatal("failed completion erased recovery state")
			}
			store.path = path
			if _, err := service.recoverToolDeliveries(context.Background(), message.ID); err != nil {
				t.Fatal(err)
			}
			if !store.Completed(message.ID) || len(store.toolDeliveries(message.ID)) != 0 {
				t.Fatal("completion did not clean up journal")
			}
		})
	}
}

func TestToolDeliverySettlesOldFormBeforeNextMessage(t *testing.T) {
	message := deliveryTestMessage(t)
	store := NewStateStore(filepath.Join(t.TempDir(), "state.json"))
	requests := []agentbridge.SendParams{}
	service := &bridgeService{store: store, bridge: deliveryTestBridge(t, func(params agentbridge.SendParams) (agentbridge.SendResult, error) {
		requests = append(requests, params)
		if len(requests) == 1 {
			return agentbridge.SendResult{}, errors.New("lost response")
		}
		return agentbridge.SendResult{MessageID: "sent", ClientMessageID: params.ClientMessageID}, nil
	})}
	if _, err := service.turnTools(message).sendInputRequest(context.Background(), "call", runtimeInputRequest()); err == nil {
		t.Fatal("missing transport error")
	}
	next := message
	next.ID, next.Text = "next-message", "/status"
	if err := service.handle(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 || requests[1].ClientMessageID != requests[0].ClientMessageID || requests[2].InputRequest != nil || requests[2].ReplyTo != next.ID {
		t.Fatalf("wrong recovery order: %+v", requests)
	}
	if !store.Completed(message.ID) || !store.Completed(next.ID) {
		t.Fatal("conversation advanced without completing recovery")
	}
}

func TestToolDeliveryMigrationRetainsSendsWithoutObsoleteThreads(t *testing.T) {
	message := deliveryTestMessage(t)
	path := filepath.Join(t.TempDir(), "state.json")
	store := NewStateStore(path)
	requests := 0
	service := &bridgeService{store: store, bridge: deliveryTestBridge(t, func(params agentbridge.SendParams) (agentbridge.SendResult, error) {
		requests++
		return agentbridge.SendResult{MessageID: "sent"}, nil
	})}
	options := service.turnTools(message)
	options.setThreadID("obsolete-thread")
	if _, err := options.sendShowcase(context.Background(), "call", runtimeShowcase()); err != nil {
		t.Fatal(err)
	}
	store.data.ThreadToolsVersion = threadToolsSchemaVersion - 1
	if err := atomicWriteJSON(path, store.data); err != nil {
		t.Fatal(err)
	}
	service.store = NewStateStore(path)
	if err := service.store.Load(); err != nil {
		t.Fatal(err)
	}
	if err := service.handle(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || service.store.ThreadID(message.Route) != "" || !service.store.Completed(message.ID) {
		t.Fatal("migration lost delivery or resurrected obsolete thread")
	}
}

func TestToolDeliveryAllowsOrderedShowcasesButOnlyOneForm(t *testing.T) {
	message := deliveryTestMessage(t)
	path := filepath.Join(t.TempDir(), "state.json")
	store := NewStateStore(path)
	requests := 0
	service := &bridgeService{store: store, bridge: deliveryTestBridge(t, func(params agentbridge.SendParams) (agentbridge.SendResult, error) {
		requests++
		return agentbridge.SendResult{MessageID: fmt.Sprintf("sent-%d", requests)}, nil
	})}
	options := service.turnTools(message)
	for _, callID := range []string{"display-1", "display-2"} {
		if _, err := options.sendShowcase(context.Background(), callID, runtimeShowcase()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := options.sendInputRequest(context.Background(), "form-1", runtimeInputRequest()); err != nil {
		t.Fatal(err)
	}
	service.store = NewStateStore(path)
	if err := service.store.Load(); err != nil {
		t.Fatal(err)
	}
	options = service.turnTools(message)
	if _, err := options.sendInputRequest(context.Background(), "form-2", runtimeInputRequest()); err == nil {
		t.Fatal("new sender replaced the existing form")
	}
	if _, err := options.sendInputRequest(context.Background(), "form-1", runtimeInputRequest()); err != nil {
		t.Fatal(err)
	}
	deliveries := service.store.toolDeliveries(message.ID)
	if requests != 3 || len(deliveries) != 3 || deliveries[0].CallID != "display-1" || deliveries[1].CallID != "display-2" {
		t.Fatal("order or deduplication lost across reload")
	}
	if err := service.handle(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if requests != 3 {
		t.Fatal("known successes replayed")
	}
}

func TestReplyRecordFailureDoesNotExposeUnpersistedFollowup(t *testing.T) {
	store := NewStateStore(t.TempDir())
	if _, err := store.Record(Reply{InboundMessageID: "message", Route: "route", ThreadID: "thread", Text: "followup"}); err == nil {
		t.Fatal("write failure ignored")
	}
	if _, pending := store.Pending("message"); pending || store.ThreadID("route") != "" {
		t.Fatal("failed write exposed an unpersisted reply")
	}
}
