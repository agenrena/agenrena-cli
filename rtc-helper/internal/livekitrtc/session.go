package livekitrtc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	media "github.com/livekit/media-sdk"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
	lkmedia "github.com/livekit/server-sdk-go/v2/pkg/media"
	"github.com/pion/webrtc/v4"
)

const (
	DefaultSampleRateHz = 24_000
	Channels            = 1
)

type AudioSink interface {
	TrySendIncomingAudio([]byte) error
}

type Config struct {
	ServerURL        string
	ParticipantToken string
	SampleRateHz     int
	Sink             AudioSink
}

type Session struct {
	config Config

	mu            sync.Mutex
	room          *lksdk.Room
	localTrack    *lkmedia.PCMLocalTrack
	remoteTrack   *lkmedia.PCMRemoteTrack
	remoteTrackID string
	closed        bool

	done     chan error
	doneOnce sync.Once
}

func New(config Config) (*Session, error) {
	if config.ServerURL == "" || config.ParticipantToken == "" || config.Sink == nil {
		return nil, errors.New("LiveKit server URL, participant token, and audio sink are required")
	}
	if config.SampleRateHz == 0 {
		config.SampleRateHz = DefaultSampleRateHz
	}
	if !SupportsSampleRate(config.SampleRateHz) {
		return nil, errors.New("LiveKit sample rate must be one of 16000, 24000, or 48000")
	}
	return &Session{config: config, done: make(chan error, 1)}, nil
}

func SupportsSampleRate(sampleRateHz int) bool {
	return sampleRateHz == 16_000 || sampleRateHz == 24_000 || sampleRateHz == 48_000
}

func (session *Session) Connect() error {
	callback := &lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackSubscribed: session.onTrackSubscribed,
			OnTrackUnsubscribed: func(track *webrtc.TrackRemote, _ *lksdk.RemoteTrackPublication, _ *lksdk.RemoteParticipant) {
				session.closeRemoteTrack(track.ID())
			},
		},
		OnDisconnectedWithReason: func(reason lksdk.DisconnectionReason) {
			session.finish(fmt.Errorf("LiveKit room disconnected: %s", reason))
		},
	}
	room, err := lksdk.ConnectToRoomWithToken(session.config.ServerURL, session.config.ParticipantToken, callback)
	if err != nil {
		return fmt.Errorf("join LiveKit room: %w", err)
	}

	localTrack, err := lkmedia.NewPCMLocalTrack(session.config.SampleRateHz, Channels, logger.GetLogger())
	if err != nil {
		room.Disconnect()
		return fmt.Errorf("create LiveKit PCM output track: %w", err)
	}
	if _, err := room.LocalParticipant.PublishTrack(localTrack, &lksdk.TrackPublicationOptions{
		Name:   "agenrena-agent-audio",
		Source: livekit.TrackSource_MICROPHONE,
	}); err != nil {
		_ = localTrack.Close()
		room.Disconnect()
		return fmt.Errorf("publish LiveKit PCM output track: %w", err)
	}
	session.mu.Lock()
	if session.closed {
		session.mu.Unlock()
		localTrack.ClearQueue()
		_ = localTrack.Close()
		room.Disconnect()
		return errors.New("LiveKit session closed while connecting")
	}
	session.room = room
	session.localTrack = localTrack
	session.mu.Unlock()
	return nil
}

func (session *Session) WriteOutgoingAudio(payload []byte) error {
	if len(payload) == 0 || len(payload)%2 != 0 {
		return errors.New("outgoing PCM16 audio must contain a non-empty even number of bytes")
	}
	session.mu.Lock()
	track := session.localTrack
	closed := session.closed
	session.mu.Unlock()
	if closed || track == nil {
		return errors.New("LiveKit output track is not available")
	}
	sample := make(media.PCM16Sample, len(payload)/2)
	for index := range sample {
		sample[index] = int16(binary.LittleEndian.Uint16(payload[index*2 : index*2+2]))
	}
	if err := track.WriteSample(sample); err != nil {
		return fmt.Errorf("write LiveKit PCM output: %w", err)
	}
	return nil
}

func (session *Session) ClearOutgoingAudio() {
	session.mu.Lock()
	track := session.localTrack
	session.mu.Unlock()
	if track != nil {
		track.ClearQueue()
	}
}

func (session *Session) Done() <-chan error { return session.done }

func (session *Session) Close() error {
	session.mu.Lock()
	if session.closed {
		session.mu.Unlock()
		return nil
	}
	session.closed = true
	room, localTrack, remoteTrack := session.room, session.localTrack, session.remoteTrack
	session.room, session.localTrack, session.remoteTrack = nil, nil, nil
	session.remoteTrackID = ""
	session.mu.Unlock()

	if remoteTrack != nil {
		remoteTrack.Close()
	}
	if localTrack != nil {
		localTrack.ClearQueue()
		_ = localTrack.Close()
	}
	if room != nil {
		room.Disconnect()
	}
	session.finish(nil)
	return nil
}

func (session *Session) onTrackSubscribed(track *webrtc.TrackRemote, _ *lksdk.RemoteTrackPublication, _ *lksdk.RemoteParticipant) {
	if track.Codec().MimeType != webrtc.MimeTypeOpus {
		return
	}
	session.mu.Lock()
	if session.closed || session.remoteTrack != nil {
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
		session.finish(fmt.Errorf("decode LiveKit Opus input track: %w", err))
		return
	}
	session.mu.Lock()
	if session.closed || session.remoteTrack != nil {
		session.mu.Unlock()
		pcmTrack.Close()
		return
	}
	session.remoteTrack = pcmTrack
	session.remoteTrackID = track.ID()
	session.mu.Unlock()
}

func (session *Session) closeRemoteTrack(trackID string) {
	session.mu.Lock()
	if session.remoteTrackID != trackID {
		session.mu.Unlock()
		return
	}
	track := session.remoteTrack
	session.remoteTrack, session.remoteTrackID = nil, ""
	session.mu.Unlock()
	if track != nil {
		track.Close()
	}
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
		return errors.New("remote PCM writer is closed")
	}
	payload := make([]byte, len(sample)*2)
	for index, value := range sample {
		binary.LittleEndian.PutUint16(payload[index*2:index*2+2], uint16(value))
	}
	// The IPC queue is intentionally bounded. Dropping a stale real-time frame is
	// preferable to blocking the LiveKit decoder and accumulating latency.
	_ = writer.sink.TrySendIncomingAudio(payload)
	return nil
}

func (writer *remotePCMWriter) Close() error {
	writer.mu.Lock()
	writer.closed = true
	writer.mu.Unlock()
	return nil
}
