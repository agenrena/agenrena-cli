package codexrtc

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	media "github.com/livekit/media-sdk"
	"github.com/livekit/protocol/logger"
	lkmedia "github.com/livekit/server-sdk-go/v2/pkg/media"
	"github.com/pion/webrtc/v4"
)

const (
	Channels          = 1
	eventsChannelName = "oai-events"
)

type AudioSink interface {
	TrySendIncomingAudio([]byte) error
}

type Config struct {
	SampleRateHz int
	Sink         AudioSink
}

type Session struct {
	config Config
	offer  string

	mu          sync.Mutex
	peer        *webrtc.PeerConnection
	inputTrack  *lkmedia.PCMLocalTrack
	outputTrack *lkmedia.PCMRemoteTrack
	answerSet   bool
	closed      bool

	done     chan error
	doneOnce sync.Once
}

func New(ctx context.Context, config Config) (*Session, error) {
	if config.SampleRateHz <= 0 || config.Sink == nil {
		return nil, errors.New("Codex WebRTC sample rate and audio sink are required")
	}
	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, fmt.Errorf("create Codex WebRTC peer: %w", err)
	}
	inputTrack, err := lkmedia.NewPCMLocalTrack(config.SampleRateHz, Channels, logger.GetLogger())
	if err != nil {
		_ = peer.Close()
		return nil, fmt.Errorf("create Codex WebRTC audio input track: %w", err)
	}
	sender, err := peer.AddTrack(inputTrack)
	if err != nil {
		_ = inputTrack.Close()
		_ = peer.Close()
		return nil, fmt.Errorf("add Codex WebRTC audio input track: %w", err)
	}
	go func() {
		buffer := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buffer); err != nil {
				return
			}
		}
	}()

	events, err := peer.CreateDataChannel(eventsChannelName, nil)
	if err != nil {
		_ = inputTrack.Close()
		_ = peer.Close()
		return nil, fmt.Errorf("create Codex realtime events data channel: %w", err)
	}
	// Codex app-server consumes realtime events through its sideband connection.
	// The peer still has to negotiate the standard data channel in its SDP.
	events.OnMessage(func(webrtc.DataChannelMessage) {})

	session := &Session{
		config: config, peer: peer, inputTrack: inputTrack, done: make(chan error, 1),
	}
	peer.OnTrack(session.onTrack)
	peer.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		switch state {
		case webrtc.PeerConnectionStateFailed:
			session.finish(errors.New("Codex WebRTC peer connection failed"))
		case webrtc.PeerConnectionStateClosed:
			session.finish(nil)
		}
	})

	offer, err := peer.CreateOffer(nil)
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("create Codex WebRTC offer: %w", err)
	}
	gatheringComplete := webrtc.GatheringCompletePromise(peer)
	if err := peer.SetLocalDescription(offer); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("set Codex WebRTC local description: %w", err)
	}
	select {
	case <-ctx.Done():
		_ = session.Close()
		return nil, fmt.Errorf("gather Codex WebRTC candidates: %w", ctx.Err())
	case <-gatheringComplete:
	}
	local := peer.LocalDescription()
	if local == nil || strings.TrimSpace(local.SDP) == "" {
		_ = session.Close()
		return nil, errors.New("Codex WebRTC peer did not produce an SDP offer")
	}
	session.offer = local.SDP
	return session, nil
}

func (session *Session) Offer() string { return session.offer }

func (session *Session) SetRemoteAnswer(sdp string) error {
	if strings.TrimSpace(sdp) == "" {
		return errors.New("Codex WebRTC answer SDP is required")
	}
	// Pion's SDP lexer requires the final SDP line to be newline-terminated,
	// while the Codex backend may return an otherwise valid answer without it.
	sdp = strings.TrimRight(sdp, "\r\n") + "\r\n"
	session.mu.Lock()
	if session.closed {
		session.mu.Unlock()
		return errors.New("Codex WebRTC session is closed")
	}
	if session.answerSet {
		session.mu.Unlock()
		return errors.New("Codex WebRTC answer was already set")
	}
	peer := session.peer
	session.mu.Unlock()

	if err := peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp}); err != nil {
		return fmt.Errorf("set Codex WebRTC remote description: %w", err)
	}
	session.mu.Lock()
	session.answerSet = true
	session.mu.Unlock()
	return nil
}

func (session *Session) WriteInputAudio(payload []byte) error {
	if len(payload) == 0 || len(payload)%2 != 0 {
		return errors.New("Codex WebRTC PCM16 input must contain a non-empty even number of bytes")
	}
	session.mu.Lock()
	track, closed := session.inputTrack, session.closed
	session.mu.Unlock()
	if closed || track == nil {
		return errors.New("Codex WebRTC audio input track is not available")
	}
	sample := make(media.PCM16Sample, len(payload)/2)
	for index := range sample {
		sample[index] = int16(binary.LittleEndian.Uint16(payload[index*2 : index*2+2]))
	}
	if err := track.WriteSample(sample); err != nil {
		return fmt.Errorf("write Codex WebRTC PCM input: %w", err)
	}
	return nil
}

func (session *Session) Done() <-chan error { return session.done }

func (session *Session) Close() error {
	session.mu.Lock()
	if session.closed {
		session.mu.Unlock()
		return nil
	}
	session.closed = true
	peer, inputTrack, outputTrack := session.peer, session.inputTrack, session.outputTrack
	session.peer, session.inputTrack, session.outputTrack = nil, nil, nil
	session.mu.Unlock()

	if outputTrack != nil {
		outputTrack.Close()
	}
	if inputTrack != nil {
		inputTrack.ClearQueue()
		_ = inputTrack.Close()
	}
	var err error
	if peer != nil {
		err = peer.Close()
	}
	session.finish(nil)
	return err
}

func (session *Session) onTrack(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
	if track.Codec().MimeType != webrtc.MimeTypeOpus {
		return
	}
	session.mu.Lock()
	if session.closed || session.outputTrack != nil {
		session.mu.Unlock()
		return
	}
	session.mu.Unlock()

	writer := &remotePCMWriter{sink: session.config.Sink}
	pcmTrack, err := lkmedia.NewPCMRemoteTrack(
		track,
		writer,
		lkmedia.WithTargetSampleRate(session.config.SampleRateHz),
		lkmedia.WithTargetChannels(Channels),
	)
	if err != nil {
		session.finish(fmt.Errorf("decode Codex WebRTC Opus output track: %w", err))
		return
	}
	session.mu.Lock()
	if session.closed || session.outputTrack != nil {
		session.mu.Unlock()
		pcmTrack.Close()
		return
	}
	session.outputTrack = pcmTrack
	session.mu.Unlock()
}

func (session *Session) finish(err error) {
	session.doneOnce.Do(func() {
		session.done <- err
		close(session.done)
	})
}

type remotePCMWriter struct {
	mu     sync.Mutex
	closed bool
	sink   AudioSink
}

func (writer *remotePCMWriter) WriteSample(sample media.PCM16Sample) error {
	writer.mu.Lock()
	closed := writer.closed
	writer.mu.Unlock()
	if closed {
		return io.ErrClosedPipe
	}
	payload := make([]byte, len(sample)*2)
	for index, value := range sample {
		binary.LittleEndian.PutUint16(payload[index*2:index*2+2], uint16(value))
	}
	// Delivery is best effort so a closed peer cannot stall the Opus decoder.
	_ = writer.sink.TrySendIncomingAudio(payload)
	return nil
}

func (writer *remotePCMWriter) Close() error {
	writer.mu.Lock()
	writer.closed = true
	writer.mu.Unlock()
	return nil
}
