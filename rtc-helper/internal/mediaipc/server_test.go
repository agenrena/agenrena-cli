package mediaipc

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type recordingHandler struct {
	audio   chan []byte
	cleared chan struct{}
	answer  chan string
}

func (handler *recordingHandler) WriteOutgoingAudio(payload []byte) error {
	handler.audio <- append([]byte(nil), payload...)
	return nil
}

func (handler *recordingHandler) ClearOutgoingAudio() {
	handler.cleared <- struct{}{}
}

func (handler *recordingHandler) SetRealtimeAnswer(sdp string) error {
	handler.answer <- sdp
	return nil
}

func TestServerHandshakeAndBidirectionalAudio(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "agenrena-rtc-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "media.sock")
	server, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	handler := &recordingHandler{audio: make(chan []byte, 1), cleared: make(chan struct{}, 1), answer: make(chan string, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx, "call-1", handler) }()

	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	hello, _ := json.Marshal(Hello{ProtocolVersion: ProtocolVersion, CallID: "call-1"})
	if err := WriteFrame(conn, FrameHello, hello); err != nil {
		t.Fatal(err)
	}
	ready, err := ReadFrame(conn)
	if err != nil || ready.Type != FrameReady {
		t.Fatalf("ready=%+v err=%v", ready, err)
	}

	outgoing := []byte{1, 0, 2, 0}
	if err := WriteFrame(conn, FrameOutgoingAudio, outgoing); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-handler.audio:
		if string(got) != string(outgoing) {
			t.Fatalf("outgoing=%v want=%v", got, outgoing)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for outgoing audio")
	}

	incoming := []byte{3, 0, 4, 0}
	if err := server.TrySendIncomingAudio(incoming); err != nil {
		t.Fatal(err)
	}
	frame, err := ReadFrame(conn)
	if err != nil || frame.Type != FrameIncomingAudio || string(frame.Payload) != string(incoming) {
		t.Fatalf("frame=%+v err=%v", frame, err)
	}

	if err := WriteFrame(conn, FrameClearOutgoing, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handler.cleared:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for clear")
	}

	answer, _ := json.Marshal(RealtimeAnswer{Type: "answer", SDP: "v=0\r\n"})
	if err := WriteFrame(conn, FrameRealtimeAnswer, answer); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-handler.answer:
		if got != "v=0\r\n" {
			t.Fatalf("answer=%q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for realtime answer")
	}

	_ = conn.Close()
	select {
	case <-served:
	case <-time.After(time.Second):
		t.Fatal("media server did not stop after client disconnected")
	}
}
