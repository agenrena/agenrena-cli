package mediaipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
)

const outboundQueueFrames = 64

type Hello struct {
	ProtocolVersion int    `json:"protocolVersion"`
	CallID          string `json:"callId"`
}

type Ready struct {
	ProtocolVersion int `json:"protocolVersion"`
}

type Handler interface {
	WriteOutgoingAudio([]byte) error
	ClearOutgoingAudio()
}

type RealtimeHandler interface {
	SetRealtimeAnswer(string) error
}

type RealtimeAnswer struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

type Server struct {
	path     string
	listener *net.UnixListener
	outbound chan []byte

	mu        sync.RWMutex
	connected bool
	closed    bool
}

func Listen(path string) (*Server, error) {
	if path == "" {
		return nil, errors.New("media socket path is required")
	}
	if len(path) >= 104 {
		return nil, fmt.Errorf("media socket path is too long: %d bytes", len(path))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); err == nil {
		return nil, fmt.Errorf("media socket path already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return &Server{
		path: path, listener: listener,
		outbound: make(chan []byte, outboundQueueFrames),
	}, nil
}

func (server *Server) Serve(ctx context.Context, callID string, handler Handler) error {
	if callID == "" || handler == nil {
		return errors.New("call ID and media handler are required")
	}
	conn, err := server.accept(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	frame, err := ReadFrame(conn)
	if err != nil {
		return fmt.Errorf("read media handshake: %w", err)
	}
	if frame.Type != FrameHello {
		return fmt.Errorf("expected media hello frame, got 0x%02x", byte(frame.Type))
	}
	var hello Hello
	if err := json.Unmarshal(frame.Payload, &hello); err != nil || hello.ProtocolVersion != ProtocolVersion || hello.CallID != callID {
		return errors.New("invalid media hello frame")
	}
	ready, _ := json.Marshal(Ready{ProtocolVersion: ProtocolVersion})
	if err := WriteFrame(conn, FrameReady, ready); err != nil {
		return fmt.Errorf("write media ready frame: %w", err)
	}

	server.mu.Lock()
	if server.closed {
		server.mu.Unlock()
		return net.ErrClosed
	}
	server.connected = true
	server.mu.Unlock()
	defer func() {
		server.mu.Lock()
		server.connected = false
		server.mu.Unlock()
	}()

	writeErr := make(chan error, 1)
	go func() { writeErr <- server.writeLoop(ctx, conn) }()
	for {
		frame, err := ReadFrame(conn)
		if err != nil {
			return err
		}
		switch frame.Type {
		case FrameOutgoingAudio:
			if len(frame.Payload) == 0 || len(frame.Payload)%2 != 0 {
				return errors.New("outgoing PCM16 frame must contain a non-empty even number of bytes")
			}
			if err := handler.WriteOutgoingAudio(frame.Payload); err != nil {
				return err
			}
		case FrameClearOutgoing:
			if len(frame.Payload) != 0 {
				return errors.New("clear outgoing frame must not contain a payload")
			}
			handler.ClearOutgoingAudio()
		case FrameRealtimeAnswer:
			realtimeHandler, ok := handler.(RealtimeHandler)
			if !ok {
				return errors.New("realtime answer is not supported by this media session")
			}
			var answer RealtimeAnswer
			if err := json.Unmarshal(frame.Payload, &answer); err != nil || answer.Type != "answer" || answer.SDP == "" {
				return errors.New("invalid realtime answer frame")
			}
			if err := realtimeHandler.SetRealtimeAnswer(answer.SDP); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unexpected media frame type 0x%02x", byte(frame.Type))
		}

		select {
		case err := <-writeErr:
			return err
		default:
		}
	}
}

func (server *Server) TrySendIncomingAudio(payload []byte) error {
	if len(payload) == 0 || len(payload)%2 != 0 {
		return errors.New("incoming PCM16 frame must contain a non-empty even number of bytes")
	}
	server.mu.RLock()
	connected, closed := server.connected, server.closed
	server.mu.RUnlock()
	if closed {
		return net.ErrClosed
	}
	if !connected {
		return ErrNotConnected
	}
	copyOfPayload := append([]byte(nil), payload...)
	select {
	case server.outbound <- copyOfPayload:
		return nil
	default:
		return ErrBackpressure
	}
}

func (server *Server) Close() error {
	server.mu.Lock()
	if server.closed {
		server.mu.Unlock()
		return nil
	}
	server.closed = true
	server.mu.Unlock()
	err := server.listener.Close()
	removeErr := os.Remove(server.path)
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	return nil
}

func (server *Server) accept(ctx context.Context) (*net.UnixConn, error) {
	type result struct {
		conn *net.UnixConn
		err  error
	}
	accepted := make(chan result, 1)
	go func() {
		conn, err := server.listener.AcceptUnix()
		accepted <- result{conn: conn, err: err}
	}()
	select {
	case <-ctx.Done():
		_ = server.listener.Close()
		return nil, ctx.Err()
	case result := <-accepted:
		return result.conn, result.err
	}
}

func (server *Server) writeLoop(ctx context.Context, conn net.Conn) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case payload := <-server.outbound:
			if err := WriteFrame(conn, FrameIncomingAudio, payload); err != nil {
				return err
			}
		}
	}
}
