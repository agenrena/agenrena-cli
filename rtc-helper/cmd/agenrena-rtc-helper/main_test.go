package main

import (
	"strings"
	"testing"
)

func TestReadConfigDefaultsAndValidatesSampleRate(t *testing.T) {
	base := `{"protocolVersion":1,"callId":"call-1","serverUrl":"wss://rtc.example","participantToken":"token","socketPath":"/tmp/media.sock"}`
	config, err := readConfig(strings.NewReader(base))
	if err != nil {
		t.Fatal(err)
	}
	if config.SampleRateHz != 24_000 {
		t.Fatalf("default sample rate=%d", config.SampleRateHz)
	}

	unsupported := `{"protocolVersion":1,"callId":"call-1","serverUrl":"wss://rtc.example","participantToken":"token","socketPath":"/tmp/media.sock","sampleRateHz":44100}`
	if _, err := readConfig(strings.NewReader(unsupported)); err == nil {
		t.Fatal("expected unsupported sample rate to be rejected")
	}
}
