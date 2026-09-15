//go:build windows

package agentbridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const windowsSharingViolation syscall.Errno = 32

type CredentialLock struct {
	file *os.File
}

func AcquireCredentialLock(stateDir, apiKey string) (*CredentialLock, error) {
	if stateDir == "" || apiKey == "" {
		return nil, bridgeError("BRIDGE_IN_USE", "bridge lock identity is unavailable", false)
	}
	lockDir := filepath.Join(stateDir, "locks")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		return nil, wrapBridgeError("BRIDGE_IN_USE", "could not prepare the bridge lock directory", false, err)
	}
	digest := sha256.Sum256([]byte(apiKey))
	path := filepath.Join(lockDir, hex.EncodeToString(digest[:16])+".lock")
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, wrapBridgeError("BRIDGE_IN_USE", "could not resolve the bridge credential lock path", false, err)
	}

	// Opening with a zero share mode keeps the lock exclusive until the handle
	// is closed. Unlike a create-and-delete lock file, this is released by the
	// operating system if the bridge exits unexpectedly.
	handle, err := syscall.CreateFile(
		pathPtr,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0,
		nil,
		syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil && !errors.Is(err, syscall.ERROR_ALREADY_EXISTS) {
		if errors.Is(err, windowsSharingViolation) {
			return nil, bridgeError("BRIDGE_IN_USE", "another bridge process is already using this Agenrena credential", false)
		}
		return nil, wrapBridgeError("BRIDGE_IN_USE", "could not acquire the bridge credential lock", false, err)
	}
	if handle == syscall.InvalidHandle {
		return nil, wrapBridgeError("BRIDGE_IN_USE", "could not acquire the bridge credential lock", false, err)
	}

	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = syscall.CloseHandle(handle)
		return nil, bridgeError("BRIDGE_IN_USE", "could not open the bridge credential lock", false)
	}
	metadata, _ := json.Marshal(map[string]any{
		"pid": os.Getpid(), "started_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	_ = file.Truncate(0)
	_, _ = file.Seek(0, 0)
	_, _ = file.Write(append(metadata, '\n'))
	_ = file.Sync()
	return &CredentialLock{file: file}, nil
}

func (lock *CredentialLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	err := lock.file.Close()
	lock.file = nil
	return err
}
