package codexbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRealtimeSessionNegotiatesWebRTCAndForwardsCallerIdentity(t *testing.T) {
	root := testEnvironment(t)
	capture := filepath.Join(root, "realtime-requests.jsonl")
	t.Setenv("GO_WANT_CODEX_REALTIME_HELPER", "1")
	t.Setenv("CODEX_HELPER_CAPTURE", capture)
	settings := Settings{
		Workspace: root, CodexCommand: []string{os.Args[0], "-test.run=TestCodexRealtimeHelperProcess", "--"},
		ApprovalPolicy: "never", CallsEnabled: true, RealtimeVersion: "v3", RealtimeModel: "gpt-live-1-codex",
	}
	cleared := make(chan struct{}, 1)
	session := newCodexRealtimeSession(settings, func() { cleared <- struct{}{} })
	call := IncomingCall{CallID: "call-1", ConversationID: "conversation-1", Route: "opaque", Caller: &Sender{ID: "identity-123"}}
	threadID, answer, err := session.Start(context.Background(), call, "v=0\r\noffer")
	if err != nil {
		t.Fatal(err)
	}
	if threadID != "voice-thread" || answer != "v=0\r\nanswer" {
		t.Fatalf("thread=%q answer=%q", threadID, answer)
	}
	select {
	case <-cleared:
	case <-time.After(2 * time.Second):
		t.Fatal("realtime barge-in did not clear outgoing audio")
	}
	session.Stop()

	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var threadParams, realtimeParams map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var request map[string]any
		if json.Unmarshal([]byte(line), &request) != nil {
			continue
		}
		switch request["method"] {
		case "thread/start":
			threadParams = mapValue(request["params"])
		case "thread/realtime/start":
			realtimeParams = mapValue(request["params"])
		}
	}
	if stringValue(threadParams["permissions"]) != realtimePermissionProfile ||
		!strings.Contains(stringValue(threadParams["developerInstructions"]), `"auth_sender_id":"identity-123"`) {
		t.Fatalf("thread params=%#v", threadParams)
	}
	transport := mapValue(realtimeParams["transport"])
	if stringValue(transport["type"]) != "webrtc" || stringValue(transport["sdp"]) != "v=0\r\noffer" ||
		stringValue(realtimeParams["version"]) != "v3" || stringValue(realtimeParams["model"]) != "gpt-live-1-codex" {
		t.Fatalf("realtime params=%#v", realtimeParams)
	}
}

func TestCodexRealtimeHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CODEX_REALTIME_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request map[string]any
		_ = json.Unmarshal(scanner.Bytes(), &request)
		if capture := os.Getenv("CODEX_HELPER_CAPTURE"); capture != "" {
			file, _ := os.OpenFile(capture, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			_, _ = file.Write(append(append([]byte{}, scanner.Bytes()...), '\n'))
			_ = file.Close()
		}
		id := request["id"]
		switch request["method"] {
		case "initialize":
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{}})
		case "thread/start":
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{"thread": map[string]any{"id": "voice-thread"}}})
		case "thread/realtime/start":
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{}})
			_ = encoder.Encode(map[string]any{"method": "thread/realtime/sdp", "params": map[string]any{"threadId": "voice-thread", "sdp": "v=0\r\nanswer"}})
			_ = encoder.Encode(map[string]any{"method": "thread/realtime/itemAdded", "params": map[string]any{"threadId": "voice-thread", "item": map[string]any{"type": "input_audio_buffer.speech_started"}}})
		case "thread/realtime/stop":
			_ = encoder.Encode(map[string]any{"id": id, "result": map[string]any{}})
		}
	}
}

func TestMediaSocketHandshakeAndRealtimeControl(t *testing.T) {
	dir, err := os.MkdirTemp("/private/tmp", "acb-media-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "media.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan byte, 2)
	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer conn.Close()
		frameType, _, readErr := readMediaFrame(conn)
		if readErr != nil || frameType != mediaFrameHello {
			serverDone <- readErr
			return
		}
		ready, _ := json.Marshal(map[string]any{"protocolVersion": mediaProtocolVersion})
		header := []byte{mediaFrameReady, 0, 0, 0, byte(len(ready))}
		_, _ = conn.Write(append(header, ready...))
		for index := 0; index < 2; index++ {
			value, _, frameErr := readMediaFrame(conn)
			if frameErr != nil {
				serverDone <- frameErr
				return
			}
			received <- value
		}
		serverDone <- nil
	}()
	client, err := newMediaSocketClient(path, "call-1", callSampleRateHz, callChannels, callFrameDurationMS)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := client.SetRealtimeAnswer("v=0\r\nanswer"); err != nil {
		t.Fatal(err)
	}
	if err := client.ClearOutgoingAudio(); err != nil {
		t.Fatal(err)
	}
	if first, second := <-received, <-received; first != mediaFrameRealtimeAnswer || second != mediaFrameClearOutgoing {
		t.Fatalf("frames=0x%02x,0x%02x", first, second)
	}
	client.Close()
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestLoadSettingsEnablesStageCalls(t *testing.T) {
	root := testEnvironment(t)
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Configure(workspace); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENRENA_CODEX_BRIDGE_CALLS", "true")
	t.Setenv("CODEX_REALTIME_VERSION", "v3")
	t.Setenv("CODEX_REALTIME_MODEL", "gpt-live-1-codex")
	settings, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.CallsEnabled || settings.RealtimeVersion != "v3" || settings.RealtimeModel != "gpt-live-1-codex" {
		t.Fatalf("settings=%#v", settings)
	}
}
