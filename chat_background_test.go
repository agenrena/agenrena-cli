package main

import (
	"bytes"
	"image/jpeg"
	"testing"
)

func TestPrepareChatBackgroundOutputsPortraitJPEG(t *testing.T) {
	path := writeDraftTestJPEG(t, 2400, 1600)

	prepared, err := prepareChatBackground(path)
	if err != nil {
		t.Fatalf("prepareChatBackground returned error: %v", err)
	}
	if prepared.Width != chatBackgroundWidth || prepared.Height != chatBackgroundHeight {
		t.Fatalf("expected %dx%d, got %dx%d", chatBackgroundWidth, chatBackgroundHeight, prepared.Width, prepared.Height)
	}
	if prepared.MimeType != "image/jpeg" {
		t.Fatalf("expected image/jpeg, got %s", prepared.MimeType)
	}
	if prepared.SizeBytes <= 0 {
		t.Fatal("expected non-empty background bytes")
	}
	if _, err := jpeg.Decode(bytes.NewReader(prepared.Bytes)); err != nil {
		t.Fatalf("background is not a valid JPEG: %v", err)
	}
}
