package codexbridge

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestMediaMaterializesImageAndStickerAndCleans(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var data []byte
		if strings.Contains(request.URL.Path, "photo") {
			data = append([]byte("\xff\xd8\xff"), []byte("jpeg")...)
		} else {
			data = append([]byte("\x89PNG\r\n\x1a\n"), []byte("png")...)
		}
		return &http.Response{
			StatusCode: 200, Header: http.Header{}, ContentLength: int64(len(data)),
			Body: io.NopCloser(bytes.NewReader(data)), Request: request,
		}, nil
	})
	store := NewMediaStore(t.TempDir())
	store.AllowPrivateHosts = true
	store.HTTPClient = &http.Client{Transport: transport}
	batch, err := store.Materialize(context.Background(), []IncomingMedia{
		{Kind: "image", URL: "https://localhost/photo?secret=one"},
		{Kind: "sticker", URL: "https://localhost/sticker?secret=two"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Items) != 2 || !strings.HasSuffix(batch.Items[0].Path, ".jpg") || !strings.HasSuffix(batch.Items[1].Path, ".png") {
		t.Fatalf("unexpected media: %#v", batch.Items)
	}
	directory := batch.Directory
	batch.Cleanup()
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("batch directory was not removed: %v", err)
	}
}

func TestMediaRejectsHTTPAndNonImage(t *testing.T) {
	store := NewMediaStore(t.TempDir())
	if _, err := store.Materialize(context.Background(), []IncomingMedia{{Kind: "image", URL: "http://example.com/x"}}); err == nil || !strings.Contains(err.Error(), "https://") {
		t.Fatalf("expected https error, got %v", err)
	}
	if SafeURLForLog("https://cdn.example/x?secret=1#fragment") != "https://cdn.example/x" {
		t.Fatal("safe URL retained signed data")
	}
}

func TestPublicIPClassification(t *testing.T) {
	tests := []struct {
		address string
		public  bool
	}{
		{address: "1.1.1.1", public: true},
		{address: "2606:4700:4700::1111", public: true},
		{address: "0.0.0.1", public: false},
		{address: "10.0.0.1", public: false},
		{address: "100.64.0.1", public: false},
		{address: "127.0.0.1", public: false},
		{address: "169.254.0.1", public: false},
		{address: "192.0.2.1", public: false},
		{address: "::1", public: false},
		{address: "fc00::1", public: false},
		{address: "fe80::1", public: false},
		{address: "2001:db8::1", public: false},
	}
	for _, test := range tests {
		t.Run(test.address, func(t *testing.T) {
			if actual := isPublicIP(net.ParseIP(test.address)); actual != test.public {
				t.Fatalf("isPublicIP(%q)=%t, want %t", test.address, actual, test.public)
			}
		})
	}
}
