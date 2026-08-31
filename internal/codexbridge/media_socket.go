package codexbridge

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	mediaProtocolVersion     = 1
	mediaFrameHello          = byte(0x01)
	mediaFrameReady          = byte(0x02)
	mediaFrameIncoming       = byte(0x10)
	mediaFrameClearOutgoing  = byte(0x12)
	mediaFrameRealtimeAnswer = byte(0x13)
	maxMediaPayloadBytes     = 1024 * 1024
)

type mediaSocketClient struct {
	path       string
	callID     string
	frameBytes int
	conn       net.Conn
	writeMu    sync.Mutex
	closeOnce  sync.Once
	closing    chan struct{}
	done       chan error
}

func newMediaSocketClient(path, callID string, sampleRateHz, channels, frameDurationMS int) (*mediaSocketClient, error) {
	frameBytes := sampleRateHz * channels * 2 * frameDurationMS / 1000
	if path == "" || callID == "" || frameBytes <= 0 || sampleRateHz*channels*2*frameDurationMS%1000 != 0 {
		return nil, errors.New("invalid call media socket contract")
	}
	return &mediaSocketClient{
		path: path, callID: callID, frameBytes: frameBytes,
		closing: make(chan struct{}), done: make(chan error, 1),
	}, nil
}

func (client *mediaSocketClient) Connect(ctx context.Context) error {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", client.path)
	if err != nil {
		return err
	}
	client.conn = conn
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	hello, _ := json.Marshal(map[string]any{"protocolVersion": mediaProtocolVersion, "callId": client.callID})
	if err := client.writeFrame(mediaFrameHello, hello); err != nil {
		client.Close()
		return err
	}
	frameType, payload, err := readMediaFrame(conn)
	if err != nil {
		client.Close()
		return fmt.Errorf("media socket handshake failed: %w", err)
	}
	var ready struct {
		ProtocolVersion int `json:"protocolVersion"`
	}
	if frameType != mediaFrameReady || json.Unmarshal(payload, &ready) != nil || ready.ProtocolVersion != mediaProtocolVersion {
		client.Close()
		return errors.New("media socket returned an unsupported ready frame")
	}
	_ = conn.SetDeadline(time.Time{})
	go client.readLoop()
	return nil
}

func (client *mediaSocketClient) SetRealtimeAnswer(sdp string) error {
	if sdp == "" {
		return errors.New("realtime SDP answer is required")
	}
	payload, _ := json.Marshal(map[string]any{"type": "answer", "sdp": sdp})
	return client.writeFrame(mediaFrameRealtimeAnswer, payload)
}

func (client *mediaSocketClient) ClearOutgoingAudio() error {
	return client.writeFrame(mediaFrameClearOutgoing, nil)
}

func (client *mediaSocketClient) Done() <-chan error { return client.done }

func (client *mediaSocketClient) Close() {
	client.closeOnce.Do(func() {
		close(client.closing)
		if client.conn != nil {
			_ = client.conn.Close()
		}
	})
}

func (client *mediaSocketClient) readLoop() {
	for {
		frameType, payload, err := readMediaFrame(client.conn)
		if err != nil {
			select {
			case <-client.closing:
				return
			default:
				client.done <- err
				return
			}
		}
		if frameType != mediaFrameIncoming || len(payload) != client.frameBytes {
			client.done <- fmt.Errorf("unexpected media frame type 0x%02x", frameType)
			return
		}
		// Direct Codex WebRTC owns call audio; legacy PCM frames are discarded.
	}
}

func (client *mediaSocketClient) writeFrame(frameType byte, payload []byte) error {
	if len(payload) > maxMediaPayloadBytes {
		return fmt.Errorf("media frame exceeds %d bytes", maxMediaPayloadBytes)
	}
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	if client.conn == nil {
		return errors.New("media socket is closed")
	}
	header := make([]byte, 5)
	header[0] = frameType
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if err := writeMediaBytes(client.conn, header); err != nil {
		return err
	}
	if len(payload) > 0 {
		return writeMediaBytes(client.conn, payload)
	}
	return nil
}

func writeMediaBytes(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		payload = payload[written:]
	}
	return nil
}

func readMediaFrame(reader io.Reader) (byte, []byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(reader, header); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint32(header[1:])
	if length > maxMediaPayloadBytes {
		return 0, nil, fmt.Errorf("media frame exceeds %d bytes", maxMediaPayloadBytes)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return 0, nil, err
	}
	return header[0], payload, nil
}
