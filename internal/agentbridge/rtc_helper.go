package agentbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	rtcHelperProtocolVersion = 1
	rtcHelperReadyTimeout    = 20 * time.Second
	defaultCallSampleRateHz  = 24_000
)

type RTCHelperManagerConfig struct {
	HelperPath string
	TempDir    string
	Notify     func(Event)
}

type rtcHelperProcess struct {
	callID string
	dir    string
	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   chan struct{}
}

type RTCHelperManager struct {
	config RTCHelperManagerConfig

	mu          sync.Mutex
	invitations map[string]IncomingCall
	accepting   map[string]struct{}
	starting    map[string]*rtcHelperProcess
	active      map[string]*rtcHelperProcess
	cancelled   map[string]struct{}
	closed      bool
}

type rtcHelperConfig struct {
	ProtocolVersion  int    `json:"protocolVersion"`
	CallID           string `json:"callId"`
	ServerURL        string `json:"serverUrl"`
	ParticipantToken string `json:"participantToken"`
	SocketPath       string `json:"socketPath"`
	SampleRateHz     int    `json:"sampleRateHz"`
}

type rtcHelperReady struct {
	Type            string `json:"type"`
	ProtocolVersion int    `json:"protocolVersion"`
	SocketPath      string `json:"socketPath"`
	Format          string `json:"format"`
	SampleRateHz    int    `json:"sampleRateHz"`
	Channels        int    `json:"channels"`
	FrameDurationMS int    `json:"frameDurationMs"`
}

func NewRTCHelperManager(config RTCHelperManagerConfig) *RTCHelperManager {
	return &RTCHelperManager{
		config:      config,
		invitations: make(map[string]IncomingCall),
		accepting:   make(map[string]struct{}),
		starting:    make(map[string]*rtcHelperProcess),
		active:      make(map[string]*rtcHelperProcess),
		cancelled:   make(map[string]struct{}),
	}
}

func (manager *RTCHelperManager) Remember(call IncomingCall) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed || call.CallID == "" {
		return
	}
	if _, accepting := manager.accepting[call.CallID]; accepting || manager.starting[call.CallID] != nil || manager.active[call.CallID] != nil {
		return
	}
	delete(manager.cancelled, call.CallID)
	manager.invitations[call.CallID] = call
}

func (manager *RTCHelperManager) Cancel(callID string) {
	manager.mu.Lock()
	delete(manager.invitations, callID)
	if _, accepting := manager.accepting[callID]; accepting {
		manager.cancelled[callID] = struct{}{}
	}
	starting := manager.starting[callID]
	process := manager.active[callID]
	delete(manager.active, callID)
	manager.mu.Unlock()
	if starting != nil {
		starting.cancel()
	}
	if process != nil {
		process.cancel()
		<-process.done
		_ = os.RemoveAll(process.dir)
	}
}

func (manager *RTCHelperManager) Accept(ctx context.Context, params AcceptCallParams) (AcceptCallResult, error) {
	callID := strings.TrimSpace(params.CallID)
	if callID == "" {
		return AcceptCallResult{}, bridgeError("MESSAGE_INVALID", "callId is required", false)
	}
	sampleRateHz := defaultCallSampleRateHz
	if params.Audio != nil && params.Audio.SampleRateHz != 0 {
		sampleRateHz = params.Audio.SampleRateHz
	}
	if !supportedCallSampleRate(sampleRateHz) {
		return AcceptCallResult{}, bridgeError(
			"MEDIA_FORMAT_UNSUPPORTED",
			"audio.sampleRateHz must be one of 16000, 24000, or 48000",
			false,
		)
	}
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return AcceptCallResult{}, bridgeError("INTERNAL_ERROR", "RTC helper manager is closed", false)
	}
	if _, exists := manager.active[callID]; exists {
		manager.mu.Unlock()
		return AcceptCallResult{}, bridgeError("CALL_ALREADY_ACTIVE", "call is already active", false)
	}
	if _, cancelled := manager.cancelled[callID]; cancelled {
		manager.mu.Unlock()
		return AcceptCallResult{}, bridgeError("CALL_NOT_FOUND", "call invitation is not available", false)
	}
	invitation, exists := manager.invitations[callID]
	if exists {
		delete(manager.invitations, callID)
		manager.accepting[callID] = struct{}{}
	}
	manager.mu.Unlock()
	if !exists {
		return AcceptCallResult{}, bridgeError("CALL_NOT_FOUND", "call invitation is not available", false)
	}
	defer manager.finishAccept(callID)
	if expiresAt, err := time.Parse(time.RFC3339, invitation.ExpiresAt); err != nil || !time.Now().Before(expiresAt) {
		return AcceptCallResult{}, bridgeError("CALL_EXPIRED", "call invitation has expired", false)
	}
	if err := validateRTCServerURL(invitation.RTC.ServerURL); err != nil {
		return AcceptCallResult{}, bridgeError("CALL_INVALID", err.Error(), false)
	}

	helperPath, err := manager.resolveHelperPath()
	if err != nil {
		return AcceptCallResult{}, err
	}
	tempRoot := manager.config.TempDir
	if tempRoot == "" {
		tempRoot = os.TempDir()
	}
	dir, err := os.MkdirTemp(tempRoot, "agenrena-rtc-")
	if err != nil {
		return AcceptCallResult{}, wrapBridgeError("RTC_HELPER_FAILED", "could not create RTC runtime directory", true, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return AcceptCallResult{}, wrapBridgeError("RTC_HELPER_FAILED", "could not secure RTC runtime directory", false, err)
	}
	socketPath := filepath.Join(dir, "media.sock")
	processCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(processCtx, helperPath)
	cmd.WaitDelay = 2 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		_ = os.RemoveAll(dir)
		return AcceptCallResult{}, wrapBridgeError("RTC_HELPER_FAILED", "could not open RTC helper input", true, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = os.RemoveAll(dir)
		return AcceptCallResult{}, wrapBridgeError("RTC_HELPER_FAILED", "could not open RTC helper output", true, err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		_ = os.RemoveAll(dir)
		return AcceptCallResult{}, wrapBridgeError("RTC_HELPER_UNAVAILABLE", "could not start agenrena-rtc-helper", false, err)
	}
	process := &rtcHelperProcess{callID: callID, dir: dir, cmd: cmd, cancel: cancel, done: make(chan struct{})}
	manager.mu.Lock()
	_, wasCancelled := manager.cancelled[callID]
	if manager.closed || wasCancelled {
		manager.mu.Unlock()
		manager.stopProcess(process)
		return AcceptCallResult{}, bridgeError("CALL_CANCELLED", "call was cancelled while starting RTC helper", false)
	}
	manager.starting[callID] = process
	manager.mu.Unlock()
	encoded := rtcHelperConfig{
		ProtocolVersion: rtcHelperProtocolVersion, CallID: callID,
		ServerURL: invitation.RTC.ServerURL, ParticipantToken: invitation.RTC.ParticipantToken,
		SocketPath: socketPath, SampleRateHz: sampleRateHz,
	}
	if err := json.NewEncoder(stdin).Encode(encoded); err != nil {
		_ = stdin.Close()
		manager.removeStarting(callID, process)
		manager.stopProcess(process)
		return AcceptCallResult{}, wrapBridgeError("RTC_HELPER_FAILED", "could not configure RTC helper", true, err)
	}
	_ = stdin.Close()

	readyResult := make(chan struct {
		ready rtcHelperReady
		err   error
	}, 1)
	go func() {
		line, readErr := bufio.NewReader(io.LimitReader(stdout, 64*1024)).ReadBytes('\n')
		var ready rtcHelperReady
		if readErr == nil {
			readErr = json.Unmarshal(line, &ready)
		}
		readyResult <- struct {
			ready rtcHelperReady
			err   error
		}{ready: ready, err: readErr}
	}()
	readyCtx, readyCancel := context.WithTimeout(ctx, rtcHelperReadyTimeout)
	defer readyCancel()
	var ready rtcHelperReady
	select {
	case <-processCtx.Done():
		manager.removeStarting(callID, process)
		manager.stopProcess(process)
		return AcceptCallResult{}, bridgeError("CALL_CANCELLED", "call was cancelled while starting RTC helper", false)
	case <-readyCtx.Done():
		manager.removeStarting(callID, process)
		manager.stopProcess(process)
		return AcceptCallResult{}, wrapBridgeError("RTC_HELPER_FAILED", "RTC helper did not become ready", true, readyCtx.Err())
	case result := <-readyResult:
		if result.err != nil {
			manager.removeStarting(callID, process)
			manager.stopProcess(process)
			return AcceptCallResult{}, wrapBridgeError("RTC_HELPER_FAILED", "RTC helper exited before becoming ready", true, result.err)
		}
		ready = result.ready
	}
	if ready.Type != "ready" || ready.ProtocolVersion != rtcHelperProtocolVersion || ready.SocketPath != socketPath ||
		ready.Format != "pcm_s16le" || ready.SampleRateHz != sampleRateHz || ready.Channels != 1 || ready.FrameDurationMS != 20 {
		manager.removeStarting(callID, process)
		manager.stopProcess(process)
		return AcceptCallResult{}, bridgeError("RTC_HELPER_FAILED", "RTC helper returned an unsupported media contract", false)
	}

	manager.mu.Lock()
	delete(manager.starting, callID)
	_, wasCancelled = manager.cancelled[callID]
	if manager.closed || wasCancelled {
		manager.mu.Unlock()
		manager.stopProcess(process)
		return AcceptCallResult{}, bridgeError("CALL_CANCELLED", "call was cancelled while starting RTC helper", false)
	}
	manager.active[callID] = process
	manager.mu.Unlock()
	go manager.monitor(process)

	return AcceptCallResult{CallID: callID, Media: CallMedia{
		Transport: "unix-socket", SocketPath: ready.SocketPath,
		ProtocolVersion: ready.ProtocolVersion, Format: ready.Format,
		SampleRateHz: ready.SampleRateHz, Channels: ready.Channels,
		FrameDurationMS: ready.FrameDurationMS,
	}}, nil
}

func supportedCallSampleRate(sampleRateHz int) bool {
	return sampleRateHz == 16_000 || sampleRateHz == 24_000 || sampleRateHz == 48_000
}

func (manager *RTCHelperManager) Leave(callID, _ string) (LeaveCallResult, error) {
	callID = strings.TrimSpace(callID)
	manager.mu.Lock()
	delete(manager.invitations, callID)
	if _, accepting := manager.accepting[callID]; accepting {
		manager.cancelled[callID] = struct{}{}
	}
	starting := manager.starting[callID]
	process := manager.active[callID]
	delete(manager.active, callID)
	manager.mu.Unlock()
	if starting != nil {
		starting.cancel()
	}
	if process != nil {
		process.cancel()
		<-process.done
		_ = os.RemoveAll(process.dir)
	}
	return LeaveCallResult{CallID: callID, State: "ended"}, nil
}

func (manager *RTCHelperManager) Close() error {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return nil
	}
	manager.closed = true
	starting := make([]*rtcHelperProcess, 0, len(manager.starting))
	for _, process := range manager.starting {
		starting = append(starting, process)
	}
	processes := make([]*rtcHelperProcess, 0, len(manager.active))
	for _, process := range manager.active {
		processes = append(processes, process)
	}
	manager.active = make(map[string]*rtcHelperProcess)
	manager.invitations = make(map[string]IncomingCall)
	manager.accepting = make(map[string]struct{})
	manager.cancelled = make(map[string]struct{})
	manager.mu.Unlock()
	for _, process := range starting {
		process.cancel()
	}
	for _, process := range processes {
		process.cancel()
		<-process.done
		_ = os.RemoveAll(process.dir)
	}
	return nil
}

func (manager *RTCHelperManager) removeStarting(callID string, process *rtcHelperProcess) {
	manager.mu.Lock()
	if manager.starting[callID] == process {
		delete(manager.starting, callID)
	}
	manager.mu.Unlock()
}

func (manager *RTCHelperManager) finishAccept(callID string) {
	manager.mu.Lock()
	delete(manager.accepting, callID)
	delete(manager.cancelled, callID)
	manager.mu.Unlock()
}

func (manager *RTCHelperManager) monitor(process *rtcHelperProcess) {
	err := process.cmd.Wait()
	close(process.done)
	_ = os.RemoveAll(process.dir)
	manager.mu.Lock()
	active := manager.active[process.callID] == process
	if active {
		delete(manager.active, process.callID)
	}
	manager.mu.Unlock()
	if active && manager.config.Notify != nil {
		reason := "rtc_helper_closed"
		if err != nil && !errors.Is(err, context.Canceled) {
			reason = "rtc_helper_failed"
		}
		manager.config.Notify(Event{Method: "calls/ended", Params: CallEnded{CallID: process.callID, Reason: reason}})
	}
}

func (manager *RTCHelperManager) stopProcess(process *rtcHelperProcess) {
	process.cancel()
	_ = process.cmd.Wait()
	close(process.done)
	_ = os.RemoveAll(process.dir)
}

func (manager *RTCHelperManager) resolveHelperPath() (string, error) {
	if value := strings.TrimSpace(manager.config.HelperPath); value != "" {
		return value, nil
	}
	executable, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(executable), "agenrena-rtc-helper")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	if value, err := exec.LookPath("agenrena-rtc-helper"); err == nil {
		return value, nil
	}
	return "", bridgeError("RTC_HELPER_UNAVAILABLE", "agenrena-rtc-helper is not installed; set AGENRENA_RTC_HELPER_PATH", false)
}

func validateRTCServerURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "wss" && parsed.Scheme != "ws") {
		return fmt.Errorf("RTC server URL is invalid")
	}
	if parsed.Scheme == "ws" && !isLoopbackHost(parsed.Hostname()) {
		return fmt.Errorf("insecure RTC server URL is only allowed for loopback hosts")
	}
	return nil
}
