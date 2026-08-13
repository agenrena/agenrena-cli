package livekitrtc

import "testing"

func TestSupportsSampleRate(t *testing.T) {
	for _, sampleRateHz := range []int{16_000, 24_000, 48_000} {
		if !SupportsSampleRate(sampleRateHz) {
			t.Fatalf("expected %d Hz to be supported", sampleRateHz)
		}
	}
	if SupportsSampleRate(44_100) {
		t.Fatal("44100 Hz must not be supported")
	}
}
