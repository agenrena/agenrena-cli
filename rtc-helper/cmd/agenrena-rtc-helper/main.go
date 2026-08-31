package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/agenrena/agenrena-cli/rtc-helper/internal/codexrtc"
	"github.com/agenrena/agenrena-cli/rtc-helper/internal/livekitrtc"
	"github.com/agenrena/agenrena-cli/rtc-helper/internal/mediaipc"
	"github.com/livekit/protocol/logger"
)

const configLimitBytes = 64 * 1024

var helperLog = log.New(os.Stderr, "agenrena-rtc-helper: ", log.LstdFlags|log.Lmicroseconds)

type helperConfig struct {
	ProtocolVersion   int    `json:"protocolVersion"`
	CallID            string `json:"callId"`
	ServerURL         string `json:"serverUrl"`
	ParticipantToken  string `json:"participantToken"`
	SocketPath        string `json:"socketPath"`
	SampleRateHz      int    `json:"sampleRateHz"`
	RealtimeTransport string `json:"realtimeTransport,omitempty"`
}

type realtimeReady struct {
	Transport string `json:"transport"`
	SDP       string `json:"sdp"`
}

type readyEvent struct {
	Type            string         `json:"type"`
	ProtocolVersion int            `json:"protocolVersion"`
	SocketPath      string         `json:"socketPath"`
	Format          string         `json:"format"`
	SampleRateHz    int            `json:"sampleRateHz"`
	Channels        int            `json:"channels"`
	FrameDurationMS int            `json:"frameDurationMs"`
	Realtime        *realtimeReady `json:"realtime,omitempty"`
}

type audioSinkFunc func([]byte) error

func (sink audioSinkFunc) TrySendIncomingAudio(payload []byte) error { return sink(payload) }

type directBridge struct {
	mu      sync.RWMutex
	livekit *livekitrtc.Session
	codex   *codexrtc.Session
}

func (bridge *directBridge) setLiveKit(session *livekitrtc.Session) {
	bridge.mu.Lock()
	bridge.livekit = session
	bridge.mu.Unlock()
}

func (bridge *directBridge) setCodex(session *codexrtc.Session) {
	bridge.mu.Lock()
	bridge.codex = session
	bridge.mu.Unlock()
}

func (bridge *directBridge) sendToCodex(payload []byte) error {
	bridge.mu.RLock()
	session := bridge.codex
	bridge.mu.RUnlock()
	if session == nil {
		return mediaipc.ErrNotConnected
	}
	return session.WriteInputAudio(payload)
}

func (bridge *directBridge) sendToLiveKit(payload []byte) error {
	bridge.mu.RLock()
	session := bridge.livekit
	bridge.mu.RUnlock()
	if session == nil {
		return mediaipc.ErrNotConnected
	}
	return session.WriteOutgoingAudio(payload)
}

func (bridge *directBridge) WriteOutgoingAudio([]byte) error {
	return errors.New("PCM output frames are disabled for direct Codex WebRTC media")
}

func (bridge *directBridge) ClearOutgoingAudio() {
	bridge.mu.RLock()
	session := bridge.livekit
	bridge.mu.RUnlock()
	if session != nil {
		session.ClearOutgoingAudio()
	}
}

func (bridge *directBridge) SetRealtimeAnswer(sdp string) error {
	bridge.mu.RLock()
	session := bridge.codex
	bridge.mu.RUnlock()
	if session == nil {
		return errors.New("Codex WebRTC session is not available")
	}
	return session.SetRemoteAnswer(sdp)
}

func main() {
	logger.InitFromConfig(&logger.Config{Level: "warn"}, "agenrena-rtc-helper")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Stdin, os.Stdout); err != nil {
		helperLog.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, input io.Reader, output io.Writer) error {
	config, err := readConfig(input)
	if err != nil {
		return err
	}
	server, err := mediaipc.Listen(config.SocketPath)
	if err != nil {
		return fmt.Errorf("open local media socket: %w", err)
	}
	defer server.Close()

	var bridge *directBridge
	var codex *codexrtc.Session
	var realtime *realtimeReady
	livekitSink := livekitrtc.AudioSink(server)
	if config.RealtimeTransport == "webrtc" {
		bridge = &directBridge{}
		codex, err = codexrtc.New(ctx, codexrtc.Config{
			SampleRateHz: config.SampleRateHz,
			Sink:         audioSinkFunc(bridge.sendToLiveKit),
		})
		if err != nil {
			return err
		}
		defer codex.Close()
		bridge.setCodex(codex)
		livekitSink = audioSinkFunc(bridge.sendToCodex)
		realtime = &realtimeReady{Transport: "webrtc", SDP: codex.Offer()}
	}

	rtc, err := livekitrtc.New(livekitrtc.Config{
		ServerURL: config.ServerURL, ParticipantToken: config.ParticipantToken,
		SampleRateHz: config.SampleRateHz, Sink: livekitSink,
	})
	if err != nil {
		return err
	}
	defer rtc.Close()
	if err := rtc.Connect(); err != nil {
		return err
	}
	if bridge != nil {
		bridge.setLiveKit(rtc)
	}

	if err := json.NewEncoder(output).Encode(readyEvent{
		Type: "ready", ProtocolVersion: mediaipc.ProtocolVersion,
		SocketPath: config.SocketPath, Format: "pcm_s16le",
		SampleRateHz: config.SampleRateHz, Channels: livekitrtc.Channels,
		FrameDurationMS: 20, Realtime: realtime,
	}); err != nil {
		return fmt.Errorf("write ready event: %w", err)
	}

	mediaDone := make(chan error, 1)
	handler := mediaipc.Handler(rtc)
	if bridge != nil {
		handler = bridge
	}
	go func() { mediaDone <- server.Serve(ctx, config.CallID, handler) }()
	var codexDone <-chan error
	if codex != nil {
		codexDone = codex.Done()
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-rtc.Done():
		if err == nil {
			err = errors.New("LiveKit media session ended without an error")
		}
		return fmt.Errorf("LiveKit media session ended: %w", err)
	case err := <-codexDone:
		if err == nil {
			err = errors.New("Codex WebRTC session ended without an error")
		}
		return fmt.Errorf("Codex WebRTC session ended: %w", err)
	case err := <-mediaDone:
		if err == nil {
			err = errors.New("local media IPC ended without an error")
		}
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("local media connection ended: %w", err)
	}
}

func readConfig(input io.Reader) (helperConfig, error) {
	reader := bufio.NewReader(io.LimitReader(input, configLimitBytes+1))
	decoder := json.NewDecoder(reader)
	var config helperConfig
	if err := decoder.Decode(&config); err != nil {
		return helperConfig{}, fmt.Errorf("read helper configuration: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return helperConfig{}, errors.New("helper configuration must contain exactly one JSON object")
	}
	if config.ProtocolVersion != mediaipc.ProtocolVersion {
		return helperConfig{}, fmt.Errorf("unsupported helper protocol version %d", config.ProtocolVersion)
	}
	if config.CallID == "" || config.ServerURL == "" || config.ParticipantToken == "" || config.SocketPath == "" {
		return helperConfig{}, errors.New("helper configuration is missing required fields")
	}
	if config.SampleRateHz == 0 {
		config.SampleRateHz = livekitrtc.DefaultSampleRateHz
	}
	if !livekitrtc.SupportsSampleRate(config.SampleRateHz) {
		return helperConfig{}, errors.New("helper sampleRateHz must be one of 16000, 24000, or 48000")
	}
	if config.RealtimeTransport != "" && config.RealtimeTransport != "webrtc" {
		return helperConfig{}, errors.New("helper realtimeTransport must be webrtc when set")
	}
	return config, nil
}
