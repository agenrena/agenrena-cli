package codexbridge

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
)

func TestWebSocketHandlesPingAndFragmentedText(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	socket := &WebSocketConnection{
		conn: clientConn, reader: bufio.NewReader(clientConn), maxSize: 1024,
	}
	defer socket.Close()
	defer serverConn.Close()
	go func() {
		writeServerFrame(serverConn, true, 0x9, []byte("probe"))
		readClientFrame(serverConn)
		writeServerFrame(serverConn, false, 0x1, []byte(`{"hello":`))
		writeServerFrame(serverConn, true, 0x0, []byte(`"world"}`))
	}()
	value, err := socket.ReceiveEvent(context.Background())
	if err != nil || value != nil {
		t.Fatalf("ping result=%q err=%v", value, err)
	}
	value, err = socket.ReceiveEvent(context.Background())
	if err != nil || value != nil {
		t.Fatalf("first fragment result=%q err=%v", value, err)
	}
	value, err = socket.ReceiveEvent(context.Background())
	if err != nil || string(value) != `{"hello":"world"}` {
		t.Fatalf("message=%q err=%v", value, err)
	}
}

func writeServerFrame(writer io.Writer, final bool, opcode byte, payload []byte) {
	first := opcode
	if final {
		first |= 0x80
	}
	header := []byte{first, byte(len(payload))}
	_, _ = writer.Write(append(header, payload...))
}

func readClientFrame(reader io.Reader) {
	header := make([]byte, 2)
	_, _ = io.ReadFull(reader, header)
	length := int(header[1] & 0x7f)
	if length == 126 {
		var extended uint16
		_ = binary.Read(reader, binary.BigEndian, &extended)
		length = int(extended)
	}
	mask := make([]byte, 4)
	_, _ = io.ReadFull(reader, mask)
	payload := make([]byte, length)
	_, _ = io.ReadFull(reader, payload)
}
