//go:build unix

package agentbridge

import "testing"

func TestCredentialLockExcludesSameCredential(t *testing.T) {
	root := t.TempDir()
	first, err := AcquireCredentialLock(root, "agr_same")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := AcquireCredentialLock(root, "agr_same"); err == nil {
		t.Fatal("expected the second lock attempt to fail")
	}
	other, err := AcquireCredentialLock(root, "agr_other")
	if err != nil {
		t.Fatal(err)
	}
	_ = other.Close()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := AcquireCredentialLock(root, "agr_same")
	if err != nil {
		t.Fatal(err)
	}
	_ = again.Close()
}
