//go:build windows

package codexbridge

import (
	"os"
	"testing"
)

func TestPIDIsAliveUsesWindowsProcessHandle(t *testing.T) {
	if !pidIsAlive(os.Getpid()) {
		t.Fatal("current process was not detected as alive")
	}
	if pidIsAlive(0) {
		t.Fatal("PID zero was detected as alive")
	}
}
