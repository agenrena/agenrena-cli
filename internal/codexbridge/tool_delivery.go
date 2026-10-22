package codexbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sort"
	"time"

	"github.com/agenrena/agenrena-cli/internal/agentbridge"
)

type toolSender func(context.Context, string, map[string]any) (agentbridge.SendResult, error)
type toolParams func(InboundMessage, string, map[string]any) (agentbridge.SendParams, error)

type turnToolOptions struct {
	sendShowcase     toolSender
	sendInputRequest toolSender
	setThreadID      func(string)
}

// Params and Arguments are immutable JSON snapshots, not references to model
// arguments. An absent result/rejection means delivery has an unknown outcome.
type toolDelivery struct {
	Tool      string                  `json:"tool"`
	CallID    string                  `json:"callId"`
	ThreadID  string                  `json:"threadId,omitempty"`
	Arguments string                  `json:"arguments"`
	Params    json.RawMessage         `json:"params"`
	Result    *agentbridge.SendResult `json:"result,omitempty"`
	Rejection *agentbridge.RPCError   `json:"rejection,omitempty"`
}

func (store *StateStore) toolDeliveries(id string) []toolDelivery {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]toolDelivery(nil), store.data.ToolDeliveries[id]...)
}

func (store *StateStore) toolDeliveryIDs() []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	ids := make([]string, 0, len(store.data.ToolDeliveries))
	for id := range store.data.ToolDeliveries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (store *StateStore) saveToolDelivery(id string, delivery toolDelivery) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	next := store.data
	next.ToolDeliveries = maps.Clone(store.data.ToolDeliveries)
	items := append([]toolDelivery(nil), next.ToolDeliveries[id]...)
	index := len(items)
	for i, previous := range items {
		if previous.Tool == delivery.Tool && previous.CallID == delivery.CallID {
			index = i
			break
		}
	}
	if index == len(items) {
		items = append(items, delivery)
	} else {
		items[index] = delivery
	}
	next.ToolDeliveries[id] = items
	if err := atomicWriteJSON(store.path, next); err != nil {
		return err
	}
	store.data = next
	return nil
}

func (service *bridgeService) turnTools(message InboundMessage) turnToolOptions {
	threadID := ""
	return turnToolOptions{
		setThreadID:      func(id string) { threadID = id },
		sendInputRequest: service.toolSender(message, inputRequestToolName, inputRequestParams, &threadID),
		sendShowcase:     service.toolSender(message, showcaseToolName, showcaseParams, &threadID),
	}
}

func (service *bridgeService) toolSender(message InboundMessage, name string, build toolParams, threadID *string) toolSender {
	return func(ctx context.Context, callID string, args map[string]any) (agentbridge.SendResult, error) {
		if service.store == nil {
			return agentbridge.SendResult{}, errors.New("tool delivery requires a persistent state store")
		}
		body, err := json.Marshal(args)
		if err != nil {
			return agentbridge.SendResult{}, err
		}
		deliveries := service.store.toolDeliveries(message.ID)
		for _, previous := range deliveries {
			if previous.Tool == name && previous.CallID == callID {
				if previous.Arguments != string(body) {
					return agentbridge.SendResult{}, errors.New("tool retry changed its content")
				}
				return service.deliverTool(ctx, message.ID, previous)
			}
		}
		for _, previous := range deliveries {
			if previous.Rejection != nil {
				continue
			}
			if previous.Result == nil {
				return agentbridge.SendResult{}, errors.New("a tool delivery is uncertain; retry its original call before sending another")
			}
			if name == inputRequestToolName && previous.Tool == name {
				return agentbridge.SendResult{}, errors.New("a form was already sent; do not replace it in this turn")
			}
		}
		params, err := build(message, callID, args)
		if err != nil {
			return agentbridge.SendResult{}, err
		}
		raw, err := json.Marshal(params)
		if err != nil {
			return agentbridge.SendResult{}, err
		}
		delivery := toolDelivery{Tool: name, CallID: callID, Arguments: string(body), Params: raw}
		if threadID != nil {
			delivery.ThreadID = *threadID
		}
		// No network side effect is allowed until this intent is durable.
		if err := service.store.saveToolDelivery(message.ID, delivery); err != nil {
			return agentbridge.SendResult{}, err
		}
		return service.deliverTool(ctx, message.ID, delivery)
	}
}

func (service *bridgeService) deliverTool(ctx context.Context, id string, delivery toolDelivery) (agentbridge.SendResult, error) {
	if delivery.Result != nil {
		return *delivery.Result, nil
	}
	if delivery.Rejection != nil {
		return agentbridge.SendResult{}, delivery.Rejection
	}
	var result agentbridge.SendResult
	err := service.bridge.process.Request(ctx, "messages/send", delivery.Params, 60*time.Second, &result)
	if err == nil && result.MessageID == "" {
		err = &agentbridge.RPCError{Code: "DELIVERY_UNKNOWN", Message: "tool send returned no message id"}
	}
	if err != nil {
		var rpc *agentbridge.RPCError
		// A conflict can mean the operation already exists. Never allow a new
		// operation to bypass that conflict, or an uncertain transport failure.
		if errors.As(err, &rpc) && !rpc.Recoverable && rpc.Code != "DELIVERY_UNKNOWN" && rpc.Code != "SHOWCASE_CONFLICT" && rpc.Code != "INPUT_REQUEST_CONFLICT" {
			delivery.Rejection = rpc
			if saveErr := service.store.saveToolDelivery(id, delivery); saveErr != nil {
				return result, fmt.Errorf("persist tool rejection: %w", saveErr)
			}
		}
		return result, err
	}
	delivery.Result = &result
	if err := service.store.saveToolDelivery(id, delivery); err != nil {
		return result, fmt.Errorf("persist tool delivery: %w", err)
	}
	return result, nil
}

// Recovery uses the saved payload before running any new model turn. Thus a
// regenerated call ID or changed model output cannot create a second send.
func (service *bridgeService) recoverToolDeliveries(ctx context.Context, id string) (bool, error) {
	deliveries := service.store.toolDeliveries(id)
	var last agentbridge.SendResult
	route, threadID := "", ""
	for _, delivery := range deliveries {
		var params agentbridge.SendParams
		if err := json.Unmarshal(delivery.Params, &params); err != nil {
			return true, err
		}
		route, threadID = params.Route, delivery.ThreadID
		if delivery.Rejection != nil && !replyAuthorityLost(delivery.Rejection) {
			continue
		}
		result, err := service.deliverTool(ctx, id, delivery)
		if err != nil {
			if replyAuthorityLost(err) {
				return true, service.store.CompleteWithoutReply(id, route, threadID)
			}
			return true, err
		}
		last = result
	}
	if last.MessageID == "" {
		return false, nil
	}
	if reply, pending := service.store.Pending(id); pending {
		return true, service.deliver(ctx, reply, newTurnProgressEmitter(service.bridge, reply.Route, id))
	}
	if err := service.store.CompleteWithoutReply(id, route, threadID); err != nil {
		return true, err
	}
	newTurnProgressEmitter(service.bridge, route, id).Emit(ctx, turnProgress{Status: "completed", Stage: "delivery"}, last.MessageID)
	return true, nil
}

func (service *bridgeService) unsettledToolDelivery(id string) bool {
	for _, delivery := range service.store.toolDeliveries(id) {
		if delivery.Result == nil && delivery.Rejection == nil {
			return true
		}
	}
	return false
}

func (service *bridgeService) recoverEarlierToolDeliveries(ctx context.Context, message InboundMessage) error {
	for _, id := range service.store.toolDeliveryIDs() {
		if id == message.ID {
			continue
		}
		for _, delivery := range service.store.toolDeliveries(id) {
			var params agentbridge.SendParams
			if err := json.Unmarshal(delivery.Params, &params); err != nil {
				return err
			}
			if params.Route == message.Route {
				if _, err := service.recoverToolDeliveries(ctx, id); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}
