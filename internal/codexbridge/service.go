package codexbridge

import (
	"context"
	"log"
	"path/filepath"
	"sync"
	"time"
)

type MessageSource interface {
	Stream(context.Context, chan<- map[string]any) error
}

type ReplyClient interface {
	SendReply(context.Context, PendingReply) (map[string]any, error)
}

type TurnRunner interface {
	RunTurn(context.Context, IncomingMessage, string, []MaterializedMedia) (CodexTurnResult, error)
}

type MediaMaterializer interface {
	Materialize(context.Context, []IncomingMedia) (*MaterializedBatch, error)
}

type BridgeService struct {
	MessageSource MessageSource
	ReplyClient   ReplyClient
	CodexRunner   TurnRunner
	StateStore    *StateStore
	MediaStore    MediaMaterializer

	mu                sync.Mutex
	conversationTails map[string]chan struct{}
	inflight          map[string]bool
	wg                sync.WaitGroup
}

func (service *BridgeService) Run(ctx context.Context) error {
	if service.conversationTails == nil {
		service.conversationTails = map[string]chan struct{}{}
	}
	if service.inflight == nil {
		service.inflight = map[string]bool{}
	}
	if err := service.StateStore.Load(); err != nil {
		return err
	}
	service.flushPending(ctx)
	messages := make(chan map[string]any)
	sourceDone := make(chan error, 1)
	go func() { sourceDone <- service.MessageSource.Stream(ctx, messages) }()
	for {
		select {
		case payload := <-messages:
			message, ok := IncomingMessageFromPayload(payload)
			if !ok {
				log.Printf("ignored unsupported or invalid Agenrena message type=%s", first(stringValue(payload["message_type"]), "unknown"))
				continue
			}
			service.mu.Lock()
			duplicate := service.inflight[message.MessageID]
			completed := service.StateStore.IsCompleted(message.MessageID)
			if !duplicate && !completed {
				service.inflight[message.MessageID] = true
				previous := service.conversationTails[message.ConversationID]
				done := make(chan struct{})
				service.conversationTails[message.ConversationID] = done
				service.wg.Add(1)
				go service.handleMessage(ctx, *message, previous, done)
			}
			service.mu.Unlock()
		case err := <-sourceDone:
			service.wg.Wait()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		case <-ctx.Done():
			service.wg.Wait()
			return ctx.Err()
		}
	}
}

func (service *BridgeService) handleMessage(ctx context.Context, message IncomingMessage, previous, done chan struct{}) {
	defer service.wg.Done()
	defer func() {
		close(done)
		service.mu.Lock()
		delete(service.inflight, message.MessageID)
		if service.conversationTails[message.ConversationID] == done {
			delete(service.conversationTails, message.ConversationID)
		}
		service.mu.Unlock()
	}()
	var batch *MaterializedBatch
	var err error
	if len(message.Media) > 0 {
		if service.MediaStore == nil {
			log.Printf("failed to process Agenrena message %s: a media store is required", message.MessageID)
			return
		}
		batch, err = service.MediaStore.Materialize(ctx, message.Media)
		if err != nil {
			log.Printf("failed to process Agenrena message %s: %v", message.MessageID, err)
			return
		}
		defer batch.Cleanup()
	}
	if previous != nil {
		select {
		case <-previous:
		case <-ctx.Done():
			return
		}
	}
	if service.StateStore.IsCompleted(message.MessageID) {
		return
	}
	pending, ok := service.StateStore.PendingReplyFor(message.MessageID)
	if !ok {
		var media []MaterializedMedia
		if batch != nil {
			media = batch.Items
		}
		result, err := service.CodexRunner.RunTurn(ctx, message, service.StateStore.ThreadIDFor(message.ConversationID), media)
		if err != nil {
			log.Printf("failed to process Agenrena message %s in conversation %s: %v", message.MessageID, message.ConversationID, err)
			return
		}
		pending = PendingReply{
			InboundMessageID: message.MessageID, ConversationID: message.ConversationID,
			ThreadID: result.ThreadID, TurnID: result.TurnID, Text: result.ReplyText,
		}
		if err := service.StateStore.RecordCodexResult(pending); err != nil {
			log.Printf("failed to persist Codex result for message %s: %v", message.MessageID, err)
			return
		}
	}
	if _, err := service.ReplyClient.SendReply(ctx, pending); err != nil {
		log.Printf("failed to send Agenrena reply for message %s: %v", message.MessageID, err)
		return
	}
	if err := service.StateStore.MarkReplySent(message.MessageID); err != nil {
		log.Printf("failed to mark Agenrena reply sent for message %s: %v", message.MessageID, err)
		return
	}
	log.Printf("replied to Agenrena conversation %s for message %s", message.ConversationID, message.MessageID)
}

func (service *BridgeService) flushPending(ctx context.Context) {
	for _, reply := range service.StateStore.ListPendingReplies() {
		if _, err := service.ReplyClient.SendReply(ctx, reply); err != nil {
			log.Printf("could not deliver pending reply for Agenrena message %s; it remains in bridge state: %v", reply.InboundMessageID, err)
			continue
		}
		if err := service.StateStore.MarkReplySent(reply.InboundMessageID); err != nil {
			log.Printf("could not update pending reply state for %s: %v", reply.InboundMessageID, err)
		}
	}
}

func RunDaemon(ctx context.Context, settings Settings) error {
	media := NewMediaStore(filepath.Join(settings.StateDir, "media"))
	if err := media.Prepare(); err != nil {
		return err
	}
	log.Printf("starting Agenrena Codex Bridge (workspace=%s, sandbox=%s)", settings.CodexWorkspace, settings.CodexSandboxMode)
	service := &BridgeService{
		MessageSource: &AgenrenaWebSocketClient{WSURL: settings.WSURL, APIKey: settings.APIKey},
		ReplyClient:   &AgenrenaAPIClient{APIBase: settings.APIBase, APIKey: settings.APIKey, UserAgent: settings.UserAgent},
		CodexRunner: &CodexRunner{
			CodexBin: settings.CodexBin, Workspace: settings.CodexWorkspace,
			Model: settings.CodexModel, SandboxMode: settings.CodexSandboxMode,
			ApprovalPolicy: settings.CodexApprovalPolicy,
			Timeout:        time.Duration(settings.CodexTurnTimeoutSeconds) * time.Second,
		},
		StateStore: NewStateStore(filepath.Join(settings.StateDir, "bridge-state.json")),
		MediaStore: media,
	}
	err := service.Run(ctx)
	if ctx.Err() != nil {
		log.Printf("stopping Agenrena Codex Bridge")
		return nil
	}
	return err
}
