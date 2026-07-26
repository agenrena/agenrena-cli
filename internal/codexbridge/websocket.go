package codexbridge

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type WebSocketConnection struct {
	conn           net.Conn
	reader         *bufio.Reader
	maxSize        int
	fragmentOpcode byte
	fragmentData   []byte
	closeSent      bool
	writeMu        sync.Mutex
}

func DialWebSocket(ctx context.Context, rawURL string, maxSize int) (*WebSocketConnection, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "ws" && parsed.Scheme != "wss") {
		return nil, fmt.Errorf("WebSocket URL must be an absolute ws:// or wss:// URL")
	}
	secure := parsed.Scheme == "wss"
	port := parsed.Port()
	if port == "" {
		if secure {
			port = "443"
		} else {
			port = "80"
		}
	}
	address := net.JoinHostPort(parsed.Hostname(), port)
	dialer := &net.Dialer{}
	var conn net.Conn
	if secure {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: parsed.Hostname(), MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", address)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = conn.Close()
		}
	}()
	var keyBytes [16]byte
	if _, err := rand.Read(keyBytes[:]); err != nil {
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes[:])
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	if parsed.RawQuery != "" {
		path += "?" + parsed.RawQuery
	}
	host := parsed.Hostname()
	defaultPort := "80"
	if secure {
		defaultPort = "443"
	}
	if port != defaultPort {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	request := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\nUser-Agent: agenrena-codex-bridge/%s\r\n\r\n", path, host, key, Version)
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := io.WriteString(conn, request); err != nil {
		return nil, err
	}
	reader := bufio.NewReaderSize(conn, 64*1024)
	response, err := readHTTPUpgrade(reader)
	if err != nil {
		return nil, err
	}
	if response.status != 101 {
		return nil, fmt.Errorf("WebSocket server rejected the upgrade with HTTP %d", response.status)
	}
	if strings.ToLower(response.headers["upgrade"]) != "websocket" || !headerToken(response.headers["connection"], "upgrade") {
		return nil, fmt.Errorf("WebSocket upgrade response is missing required headers")
	}
	sum := sha1.Sum([]byte(key + websocketGUID))
	if response.headers["sec-websocket-accept"] != base64.StdEncoding.EncodeToString(sum[:]) {
		return nil, fmt.Errorf("WebSocket server returned an invalid Sec-WebSocket-Accept")
	}
	_ = conn.SetDeadline(noDeadline)
	success = true
	return &WebSocketConnection{conn: conn, reader: reader, maxSize: maxSize}, nil
}

var noDeadline = func() (value time.Time) { return }()

type upgradeResponse struct {
	status  int
	headers map[string]string
}

func readHTTPUpgrade(reader *bufio.Reader) (upgradeResponse, error) {
	total := 0
	lines := []string{}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return upgradeResponse{}, fmt.Errorf("WebSocket server returned an incomplete HTTP upgrade response")
		}
		total += len(line)
		if total > 64*1024 {
			return upgradeResponse{}, fmt.Errorf("WebSocket HTTP upgrade response is too large")
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			break
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return upgradeResponse{}, fmt.Errorf("invalid WebSocket HTTP upgrade response")
	}
	parts := strings.SplitN(lines[0], " ", 3)
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "HTTP/1.") {
		return upgradeResponse{}, fmt.Errorf("invalid WebSocket HTTP upgrade response")
	}
	status, err := strconv.Atoi(parts[1])
	if err != nil {
		return upgradeResponse{}, fmt.Errorf("invalid WebSocket HTTP upgrade response")
	}
	headers := map[string]string{}
	for _, line := range lines[1:] {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return upgradeResponse{}, fmt.Errorf("invalid WebSocket HTTP header")
		}
		name, value := strings.ToLower(strings.TrimSpace(parts[0])), strings.TrimSpace(parts[1])
		if headers[name] != "" {
			headers[name] += "," + value
		} else {
			headers[name] = value
		}
	}
	return upgradeResponse{status: status, headers: headers}, nil
}

func headerToken(value, target string) bool {
	for _, item := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(item), target) {
			return true
		}
	}
	return false
}

func (socket *WebSocketConnection) ReceiveEvent(ctx context.Context) ([]byte, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = socket.conn.SetReadDeadline(deadline)
	} else {
		_ = socket.conn.SetReadDeadline(noDeadline)
	}
	final, opcode, payload, err := socket.readFrame()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	switch opcode {
	case 0x8:
		if len(payload) == 1 {
			return nil, fmt.Errorf("WebSocket close frame has an invalid payload")
		}
		if !socket.closeSent {
			_ = socket.sendFrame(0x8, payload)
			socket.closeSent = true
		}
		return nil, fmt.Errorf("WebSocket peer closed the connection")
	case 0x9:
		return nil, socket.sendFrame(0xA, payload)
	case 0xA:
		return nil, nil
	case 0:
		if socket.fragmentOpcode == 0 {
			return nil, fmt.Errorf("unexpected WebSocket continuation frame")
		}
		socket.fragmentData = append(socket.fragmentData, payload...)
		if len(socket.fragmentData) > socket.maxSize {
			return nil, fmt.Errorf("WebSocket message exceeds the %d-byte limit", socket.maxSize)
		}
		if !final {
			return nil, nil
		}
		result := append([]byte(nil), socket.fragmentData...)
		opcode = socket.fragmentOpcode
		socket.fragmentOpcode, socket.fragmentData = 0, nil
		if opcode == 1 && !utf8.Valid(result) {
			return nil, fmt.Errorf("WebSocket text message is not valid UTF-8")
		}
		return result, nil
	case 1, 2:
		if socket.fragmentOpcode != 0 {
			return nil, fmt.Errorf("received a new WebSocket message before fragments completed")
		}
		if !final {
			socket.fragmentOpcode = opcode
			socket.fragmentData = append(socket.fragmentData, payload...)
			return nil, nil
		}
		if opcode == 1 && !utf8.Valid(payload) {
			return nil, fmt.Errorf("WebSocket text message is not valid UTF-8")
		}
		return payload, nil
	default:
		return nil, fmt.Errorf("unsupported WebSocket opcode: %d", opcode)
	}
}

func (socket *WebSocketConnection) readFrame() (bool, byte, []byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(socket.reader, header); err != nil {
		return false, 0, nil, fmt.Errorf("WebSocket connection ended: %w", err)
	}
	final, opcode := header[0]&0x80 != 0, header[0]&0x0f
	if header[0]&0x70 != 0 {
		return false, 0, nil, fmt.Errorf("WebSocket frame uses an extension that was not negotiated")
	}
	if header[1]&0x80 != 0 {
		return false, 0, nil, fmt.Errorf("WebSocket server frames must not be masked")
	}
	length := uint64(header[1] & 0x7f)
	if opcode >= 8 && (!final || length > 125) {
		return false, 0, nil, fmt.Errorf("invalid fragmented WebSocket control frame")
	}
	if length == 126 {
		var value uint16
		if err := binary.Read(socket.reader, binary.BigEndian, &value); err != nil {
			return false, 0, nil, err
		}
		length = uint64(value)
	} else if length == 127 {
		if err := binary.Read(socket.reader, binary.BigEndian, &length); err != nil {
			return false, 0, nil, err
		}
		if length>>63 != 0 {
			return false, 0, nil, fmt.Errorf("invalid 64-bit WebSocket frame length")
		}
	}
	if length > uint64(socket.maxSize) {
		return false, 0, nil, fmt.Errorf("WebSocket message exceeds the %d-byte limit", socket.maxSize)
	}
	payload := make([]byte, int(length))
	_, err := io.ReadFull(socket.reader, payload)
	return final, opcode, payload, err
}

func (socket *WebSocketConnection) sendFrame(opcode byte, payload []byte) error {
	socket.writeMu.Lock()
	defer socket.writeMu.Unlock()
	if len(payload) > socket.maxSize {
		return fmt.Errorf("WebSocket outbound frame is too large")
	}
	header := []byte{0x80 | opcode}
	length := len(payload)
	switch {
	case length < 126:
		header = append(header, 0x80|byte(length))
	case length < 1<<16:
		header = append(header, 0x80|126, byte(length>>8), byte(length))
	default:
		header = append(header, 0x80|127)
		var value [8]byte
		binary.BigEndian.PutUint64(value[:], uint64(length))
		header = append(header, value[:]...)
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	masked := make([]byte, length)
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	_, err := socket.conn.Write(append(append(header, mask[:]...), masked...))
	return err
}

func (socket *WebSocketConnection) Ping(ctx context.Context) error {
	if deadline, ok := ctx.Deadline(); ok {
		_ = socket.conn.SetWriteDeadline(deadline)
	}
	var probe [4]byte
	_, _ = rand.Read(probe[:])
	return socket.sendFrame(0x9, probe[:])
}

func (socket *WebSocketConnection) Close() error {
	if !socket.closeSent {
		_ = socket.sendFrame(0x8, []byte{0x03, 0xe8})
		socket.closeSent = true
	}
	return socket.conn.Close()
}
