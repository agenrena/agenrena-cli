package codexrtc

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

type discardSink struct{}

func (discardSink) TrySendIncomingAudio([]byte) error { return nil }

func TestSessionCreatesOfferAndAcceptsAnswer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := New(ctx, Config{SampleRateHz: 24_000, Sink: discardSink{}})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if !strings.Contains(session.Offer(), "m=audio") || !strings.Contains(session.Offer(), "m=application") {
		t.Fatalf("offer does not negotiate audio and data: %s", session.Offer())
	}

	remote, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	if err := remote.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: session.Offer()}); err != nil {
		t.Fatal(err)
	}
	answer, err := remote.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gatheringComplete := webrtc.GatheringCompletePromise(remote)
	if err := remote.SetLocalDescription(answer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-gatheringComplete:
	}
	if err := session.SetRemoteAnswer(remote.LocalDescription().SDP); err != nil {
		t.Fatal(err)
	}
	if err := session.SetRemoteAnswer(remote.LocalDescription().SDP); err == nil {
		t.Fatal("expected a duplicate answer to be rejected")
	}
}

func TestSessionRejectsInvalidPCM(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := New(ctx, Config{SampleRateHz: 24_000, Sink: discardSink{}})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.WriteInputAudio([]byte{1}); err == nil {
		t.Fatal("expected odd PCM payload to be rejected")
	}
}
