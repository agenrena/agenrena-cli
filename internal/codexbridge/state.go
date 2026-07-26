package codexbridge

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

const maxCompletedMessageIDs = 5000

type stateData struct {
	Version             int                     `json:"version"`
	Sessions            map[string]string       `json:"sessions"`
	PendingReplies      map[string]PendingReply `json:"pending_replies"`
	CompletedMessageIDs []string                `json:"completed_message_ids"`
}

func defaultState() stateData {
	return stateData{Version: 1, Sessions: map[string]string{}, PendingReplies: map[string]PendingReply{}, CompletedMessageIDs: []string{}}
}

type StateStore struct {
	path string
	mu   sync.Mutex
	data stateData
}

func NewStateStore(path string) *StateStore {
	return &StateStore{path: path, data: defaultState()}
}

func (store *StateStore) Load() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	raw, err := os.ReadFile(store.path)
	if os.IsNotExist(err) {
		store.data = defaultState()
		return nil
	}
	if err != nil {
		return err
	}
	next := defaultState()
	if err := json.Unmarshal(raw, &next); err != nil {
		return err
	}
	if next.Sessions == nil || next.PendingReplies == nil {
		return fmt.Errorf("bridge state at %s must be a JSON object", store.path)
	}
	if next.CompletedMessageIDs == nil {
		next.CompletedMessageIDs = []string{}
	}
	store.data = next
	return nil
}

func (store *StateStore) ThreadIDFor(conversationID string) string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.data.Sessions[conversationID]
}

func (store *StateStore) IsCompleted(messageID string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, value := range store.data.CompletedMessageIDs {
		if value == messageID {
			return true
		}
	}
	return false
}

func (store *StateStore) PendingReplyFor(messageID string) (PendingReply, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.data.PendingReplies[messageID]
	return value, ok
}

func (store *StateStore) ListPendingReplies() []PendingReply {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := make([]PendingReply, 0, len(store.data.PendingReplies))
	for _, value := range store.data.PendingReplies {
		result = append(result, value)
	}
	return result
}

func (store *StateStore) RecordCodexResult(reply PendingReply) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.data.Sessions[reply.ConversationID] = reply.ThreadID
	store.data.PendingReplies[reply.InboundMessageID] = reply
	return atomicWriteJSON(store.path, store.data, 0o600)
}

func (store *StateStore) MarkReplySent(messageID string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.data.PendingReplies, messageID)
	next := make([]string, 0, len(store.data.CompletedMessageIDs)+1)
	for _, value := range store.data.CompletedMessageIDs {
		if value != messageID {
			next = append(next, value)
		}
	}
	next = append(next, messageID)
	if len(next) > maxCompletedMessageIDs {
		next = next[len(next)-maxCompletedMessageIDs:]
	}
	store.data.CompletedMessageIDs = next
	return atomicWriteJSON(store.path, store.data, 0o600)
}
