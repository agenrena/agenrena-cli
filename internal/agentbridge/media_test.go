package agentbridge

import (
	"context"
	"testing"
)

func TestMediaStoreRejectsPrivateAndCredentialedURLs(t *testing.T) {
	store := NewMediaStore(t.TempDir())
	for _, value := range []string{
		"https://127.0.0.1/image.png",
		"https://user:password@example.com/image.png",
		"http://example.com/image.png",
	} {
		if err := store.validateURL(context.Background(), value); err == nil {
			t.Fatalf("expected media URL to be rejected: %s", value)
		}
	}
}

func TestSafeURLForLogRemovesSecrets(t *testing.T) {
	value := SafeURLForLog("https://user:password@example.com/image.png?token=secret#fragment")
	if value != "https://example.com/image.png" {
		t.Fatalf("safe URL=%q", value)
	}
}
