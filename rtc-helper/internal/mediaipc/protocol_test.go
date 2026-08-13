package mediaipc

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var encoded bytes.Buffer
	payload := []byte{0, 1, 2, 3}
	if err := WriteFrame(&encoded, FrameIncomingAudio, payload); err != nil {
		t.Fatal(err)
	}
	frame, err := ReadFrame(&encoded)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Type != FrameIncomingAudio || !bytes.Equal(frame.Payload, payload) {
		t.Fatalf("frame=%+v", frame)
	}
}

func TestReadFrameRejectsOversizedPayloadBeforeAllocation(t *testing.T) {
	header := make([]byte, headerSize)
	header[0] = byte(FrameIncomingAudio)
	binary.BigEndian.PutUint32(header[1:], MaxPayloadBytes+1)
	_, err := ReadFrame(bytes.NewReader(header))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err=%v", err)
	}
}
