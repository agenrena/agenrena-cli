//go:build unix

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
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, wrapBridgeError("BRIDGE_IN_USE", "could not open the bridge credential lock", false, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, bridgeError("BRIDGE_IN_USE", "another bridge process is already using this Agenrena credential", false)
		}
		return nil, wrapBridgeError("BRIDGE_IN_USE", "could not acquire the bridge credential lock", false, err)
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
	err := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	if err != nil {
		return err
	}
	return closeErr
}
