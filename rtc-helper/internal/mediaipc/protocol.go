package mediaipc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	ProtocolVersion = 1
	headerSize      = 5
	MaxPayloadBytes = 1024 * 1024
)

type FrameType byte

const (
	FrameHello         FrameType = 0x01
	FrameReady         FrameType = 0x02
	FrameIncomingAudio FrameType = 0x10
	FrameOutgoingAudio FrameType = 0x11
	FrameClearOutgoing FrameType = 0x12
)

type Frame struct {
	Type    FrameType
	Payload []byte
}

func ReadFrame(reader io.Reader) (Frame, error) {
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return Frame{}, err
	}
	length := binary.BigEndian.Uint32(header[1:])
	if length > MaxPayloadBytes {
		return Frame{}, fmt.Errorf("media frame payload exceeds %d bytes", MaxPayloadBytes)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return Frame{}, err
	}
	return Frame{Type: FrameType(header[0]), Payload: payload}, nil
}

func WriteFrame(writer io.Writer, frameType FrameType, payload []byte) error {
	if len(payload) > MaxPayloadBytes {
		return fmt.Errorf("media frame payload exceeds %d bytes", MaxPayloadBytes)
	}
	header := make([]byte, headerSize)
	header[0] = byte(frameType)
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if err := writeAll(writer, header); err != nil {
		return err
	}
	return writeAll(writer, payload)
}

func writeAll(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrUnexpectedEOF
		}
		payload = payload[written:]
	}
	return nil
}

var (
	ErrNotConnected = errors.New("media client is not connected")
	ErrBackpressure = errors.New("media client is not reading quickly enough")
)
