//go:build windows

package codexbridge

import (
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
	handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)
	const waitTimeout = 258
	state, err := syscall.WaitForSingleObject(handle, 0)
	return err == nil && state == waitTimeout
}

func terminatePID(pid int) error { return exec.Command("taskkill", "/PID", fmt.Sprint(pid)).Run() }
func killPID(pid int) error      { return exec.Command("taskkill", "/F", "/PID", fmt.Sprint(pid)).Run() }

func daemonSignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt)
}
