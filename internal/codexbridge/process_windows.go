//go:build windows

package codexbridge

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func detachCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
}

func pidIsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	output, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
	return err == nil && bytes.Contains(output, []byte(fmt.Sprintf("\"%d\"", pid)))
}

func terminatePID(pid int) error { return exec.Command("taskkill", "/PID", fmt.Sprint(pid)).Run() }
func killPID(pid int) error      { return exec.Command("taskkill", "/F", "/PID", fmt.Sprint(pid)).Run() }

func daemonSignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt)
}
