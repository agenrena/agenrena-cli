package codexbridge

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

const (
	callSampleRateHz    = 24_000
	callChannels        = 1
	callFrameDurationMS = 20
)

type callManager struct {
	bridge   *agentBridgeClient
	settings Settings
	mu       sync.Mutex
	sessions map[string]*callSession
}

func newCallManager(bridge *agentBridgeClient, settings Settings) *callManager {
	if !settings.CallsEnabled {
		return nil
	}
	return &callManager{bridge: bridge, settings: settings, sessions: make(map[string]*callSession)}
}

func (manager *callManager) Incoming(ctx context.Context, invitation IncomingCall) {
	if manager == nil || invitation.CallID == "" {
		return
	}
	manager.mu.Lock()
	if manager.sessions[invitation.CallID] != nil {
		manager.mu.Unlock()
		return
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	session := &callSession{
		manager: manager, bridge: manager.bridge, settings: manager.settings,
		invitation: invitation, ctx: sessionCtx, cancel: cancel, stopped: make(chan struct{}),
	}
	manager.sessions[invitation.CallID] = session
	manager.mu.Unlock()
	go func() {
		if err := session.Start(); err != nil {
			log.Printf("call %s failed: %v", invitation.CallID, err)
			session.Stop(true)
		}
	}()
}

func (manager *callManager) End(callID string, leave bool) {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	session := manager.sessions[callID]
	manager.mu.Unlock()
	if session != nil {
		session.Stop(leave)
	}
}

func (manager *callManager) Shutdown() {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	sessions := make([]*callSession, 0, len(manager.sessions))
	for _, session := range manager.sessions {
		sessions = append(sessions, session)
	}
	manager.mu.Unlock()
	for _, session := range sessions {
		session.Stop(true)
	}
	for _, session := range sessions {
		<-session.stopped
	}
}

func (manager *callManager) remove(session *callSession) {
	manager.mu.Lock()
	if manager.sessions[session.invitation.CallID] == session {
		delete(manager.sessions, session.invitation.CallID)
	}
	manager.mu.Unlock()
}

type callSession struct {
	manager    *callManager
	bridge     *agentBridgeClient
	settings   Settings
	invitation IncomingCall
	ctx        context.Context
	cancel     context.CancelFunc
	media      *mediaSocketClient
	realtime   *codexRealtimeSession
	stopOnce   sync.Once
	stopped    chan struct{}
}

func (session *callSession) Start() error {
	expiresAt, err := time.Parse(time.RFC3339, session.invitation.ExpiresAt)
	if err != nil || !time.Now().Before(expiresAt) || strings.TrimSpace(session.invitation.Route) == "" {
		return errors.New("incoming call invitation is missing required fields or has expired")
	}
	accepted, err := session.bridge.AcceptCall(session.ctx, session.invitation.CallID)
	if err != nil {
		return err
	}
	if err := validateCallMedia(accepted, session.invitation.CallID); err != nil {
		return err
	}
	media, err := newMediaSocketClient(
		accepted.Media.SocketPath, session.invitation.CallID, accepted.Media.SampleRateHz,
		accepted.Media.Channels, accepted.Media.FrameDurationMS,
	)
	if err != nil {
		return err
	}
	session.media = media
	if err := media.Connect(session.ctx); err != nil {
		return err
	}
	session.realtime = newCodexRealtimeSession(session.settings, func() {
		if session.media != nil {
			if clearErr := session.media.ClearOutgoingAudio(); clearErr != nil {
				go session.fail(clearErr)
			}
		}
	})
	_, answer, err := session.realtime.Start(session.ctx, session.invitation, accepted.Media.Realtime.SDP)
	if err != nil {
		return err
	}
	if err := media.SetRealtimeAnswer(answer); err != nil {
		return err
	}
	go session.monitor()
	return nil
}

func (session *callSession) monitor() {
	select {
	case err := <-session.media.Done():
		session.fail(fmt.Errorf("Agenrena media socket closed: %w", err))
	case err := <-session.realtime.Done():
		session.fail(err)
	case <-session.ctx.Done():
	}
}

func (session *callSession) fail(err error) {
	select {
	case <-session.ctx.Done():
		return
	default:
	}
	log.Printf("call %s ended with error: %v", session.invitation.CallID, err)
	session.Stop(true)
}

func (session *callSession) Stop(leave bool) {
	session.stopOnce.Do(func() {
		go func() {
			session.cancel()
			if session.media != nil {
				session.media.Close()
			}
			if session.realtime != nil {
				session.realtime.Stop()
			}
			if leave {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_ = session.bridge.LeaveCall(ctx, session.invitation.CallID)
				cancel()
			}
			session.manager.remove(session)
			close(session.stopped)
		}()
	})
}

func validateCallMedia(result acceptCallResult, callID string) error {
	media := result.Media
	if result.CallID != callID || media.Transport != "unix-socket" || strings.TrimSpace(media.SocketPath) == "" ||
		media.ProtocolVersion != mediaProtocolVersion || media.Format != "pcm_s16le" || media.SampleRateHz != callSampleRateHz ||
		media.Channels != callChannels || media.FrameDurationMS != callFrameDurationMS || media.Realtime == nil ||
		media.Realtime.Transport != "webrtc" || strings.TrimSpace(media.Realtime.SDP) == "" {
		return errors.New("Agenrena CLI returned an unsupported call media contract")
	}
	return nil
}
