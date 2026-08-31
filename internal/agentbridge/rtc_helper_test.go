package agentbridge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRTCHelperManagerStartsSidecarAndReturnsMediaContract(t *testing.T) {
	dir := t.TempDir()
	helperPath := filepath.Join(dir, "fake-rtc-helper")
	script := `#!/bin/sh
IFS= read -r config
socket_path=$(printf '%s' "$config" | sed -n 's/.*"socketPath":"\([^"]*\)".*/\1/p')
sample_rate=$(printf '%s' "$config" | sed -n 's/.*"sampleRateHz":\([0-9]*\).*/\1/p')
test -n "$socket_path" || exit 2
test -n "$sample_rate" || exit 2
printf '{"type":"ready","protocolVersion":1,"socketPath":"%s","format":"pcm_s16le","sampleRateHz":%s,"channels":1,"frameDurationMs":20}\n' "$socket_path" "$sample_rate"
while :; do sleep 1; done
`
	if err := os.WriteFile(helperPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := NewRTCHelperManager(RTCHelperManagerConfig{
		HelperPath: helperPath,
		TempDir:    dir,
	})
	defer manager.Close()
	manager.Remember(IncomingCall{
		CallID: "call-1", ConversationID: "conversation-1",
		ExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339),
		RTC:       CallRTC{ServerURL: "wss://rtc.example", ParticipantToken: "secret-token"},
	})

	result, err := manager.Accept(context.Background(), AcceptCallParams{
		CallID: "call-1", Audio: &CallAudioPreferences{SampleRateHz: 16_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.CallID != "call-1" || result.Media.Transport != "unix-socket" ||
		result.Media.Format != "pcm_s16le" || result.Media.SampleRateHz != 16_000 ||
		result.Media.Channels != 1 || result.Media.ProtocolVersion != 1 {
		t.Fatalf("result=%+v", result)
	}
	if filepath.Dir(result.Media.SocketPath) == dir {
		t.Fatalf("socket must live in a private per-call directory: %s", result.Media.SocketPath)
	}
	if _, err := manager.Leave("call-1", "test_complete"); err != nil {
		t.Fatal(err)
	}
}

func TestRTCHelperManagerReturnsOptionalWebRTCOffer(t *testing.T) {
	dir := t.TempDir()
	helperPath := filepath.Join(dir, "fake-rtc-helper")
	script := `#!/bin/sh
IFS= read -r config
socket_path=$(printf '%s' "$config" | sed -n 's/.*"socketPath":"\([^"]*\)".*/\1/p')
sample_rate=$(printf '%s' "$config" | sed -n 's/.*"sampleRateHz":\([0-9]*\).*/\1/p')
printf '{"type":"ready","protocolVersion":1,"socketPath":"%s","format":"pcm_s16le","sampleRateHz":%s,"channels":1,"frameDurationMs":20,"realtime":{"transport":"webrtc","sdp":"v=0\\r\\n"}}\n' "$socket_path" "$sample_rate"
while :; do sleep 1; done
`
	if err := os.WriteFile(helperPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := NewRTCHelperManager(RTCHelperManagerConfig{HelperPath: helperPath, TempDir: dir})
	defer manager.Close()
	manager.Remember(IncomingCall{
		CallID: "call-webrtc", ConversationID: "conversation-1",
		ExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339),
		RTC:       CallRTC{ServerURL: "wss://rtc.example", ParticipantToken: "secret-token"},
	})

	result, err := manager.Accept(context.Background(), AcceptCallParams{
		CallID: "call-webrtc", Realtime: &CallRealtimePreferences{Transport: "webrtc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Media.Realtime == nil || result.Media.Realtime.Transport != "webrtc" || result.Media.Realtime.SDP != "v=0\r\n" {
		t.Fatalf("realtime=%+v", result.Media.Realtime)
	}
}

func TestRTCHelperManagerCancelsCallWhileSidecarIsStarting(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	helperPath := filepath.Join(dir, "slow-rtc-helper")
	script := fmt.Sprintf(`#!/bin/sh
IFS= read -r config
touch %q
exec sleep 30
`, marker)
	if err := os.WriteFile(helperPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := NewRTCHelperManager(RTCHelperManagerConfig{HelperPath: helperPath, TempDir: dir})
	defer manager.Close()
	manager.Remember(IncomingCall{
		CallID: "call-cancel", ConversationID: "conversation-1",
		ExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339),
		RTC:       CallRTC{ServerURL: "wss://rtc.example", ParticipantToken: "secret-token"},
	})

	accepted := make(chan error, 1)
	go func() {
		_, err := manager.Accept(context.Background(), AcceptCallParams{CallID: "call-cancel"})
		accepted <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	manager.Cancel("call-cancel")
	select {
	case err := <-accepted:
		if err == nil {
			t.Fatal("accept unexpectedly succeeded after cancellation")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("accept did not stop after cancellation")
	}
}

func TestRTCHelperManagerRejectsExpiredAndInsecureInvitations(t *testing.T) {
	manager := NewRTCHelperManager(RTCHelperManagerConfig{})
	manager.Remember(IncomingCall{
		CallID: "expired", ConversationID: "conversation-1",
		ExpiresAt: time.Now().Add(-time.Second).Format(time.RFC3339),
		RTC:       CallRTC{ServerURL: "wss://rtc.example", ParticipantToken: "token"},
	})
	if _, err := manager.Accept(context.Background(), AcceptCallParams{CallID: "expired"}); err == nil {
		t.Fatal("expected expired invitation to be rejected")
	}
	manager.Remember(IncomingCall{
		CallID: "insecure", ConversationID: "conversation-1",
		ExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339),
		RTC:       CallRTC{ServerURL: "ws://rtc.example", ParticipantToken: "token"},
	})
	if _, err := manager.Accept(context.Background(), AcceptCallParams{CallID: "insecure"}); err == nil {
		t.Fatal("expected insecure non-loopback RTC URL to be rejected")
	}
}

func TestRTCHelperManagerRejectsUnsupportedSampleRateWithoutConsumingInvitation(t *testing.T) {
	manager := NewRTCHelperManager(RTCHelperManagerConfig{})
	manager.Remember(IncomingCall{
		CallID: "call-rate", ConversationID: "conversation-1",
		ExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339),
		RTC:       CallRTC{ServerURL: "wss://rtc.example", ParticipantToken: "token"},
	})

	_, err := manager.Accept(context.Background(), AcceptCallParams{
		CallID: "call-rate", Audio: &CallAudioPreferences{SampleRateHz: 44_100},
	})
	rpcErr, ok := err.(*RPCError)
	if !ok || rpcErr.Code != "MEDIA_FORMAT_UNSUPPORTED" {
		t.Fatalf("err=%v", err)
	}
	if _, exists := manager.invitations["call-rate"]; !exists {
		t.Fatal("unsupported audio preference consumed the invitation")
	}
}

func TestRTCHelperManagerRejectsUnsupportedRealtimeTransportWithoutConsumingInvitation(t *testing.T) {
	manager := NewRTCHelperManager(RTCHelperManagerConfig{})
	manager.Remember(IncomingCall{
		CallID: "call-transport", ConversationID: "conversation-1",
		ExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339),
		RTC:       CallRTC{ServerURL: "wss://rtc.example", ParticipantToken: "token"},
	})

	_, err := manager.Accept(context.Background(), AcceptCallParams{
		CallID: "call-transport", Realtime: &CallRealtimePreferences{Transport: "websocket"},
	})
	rpcErr, ok := err.(*RPCError)
	if !ok || rpcErr.Code != "MEDIA_TRANSPORT_UNSUPPORTED" {
		t.Fatalf("err=%v", err)
	}
	if _, exists := manager.invitations["call-transport"]; !exists {
		t.Fatal("unsupported realtime transport consumed the invitation")
	}
}
