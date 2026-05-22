package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareDraftImageResizesAndCreatesThumbnail(t *testing.T) {
	path := writeDraftTestJPEG(t, 3200, 2400)

	prepared, err := prepareDraftImage(path)
	if err != nil {
		t.Fatalf("prepareDraftImage returned error: %v", err)
	}
	if prepared.Width != 1600 || prepared.Height != 1200 {
		t.Fatalf("expected 1600x1200, got %dx%d", prepared.Width, prepared.Height)
	}
	if prepared.ThumbnailWidth != 400 || prepared.ThumbnailHeight != 300 {
		t.Fatalf("expected 400x300 thumbnail, got %dx%d", prepared.ThumbnailWidth, prepared.ThumbnailHeight)
	}
	if prepared.MimeType != "image/jpeg" {
		t.Fatalf("expected image/jpeg, got %s", prepared.MimeType)
	}
	if !prepared.Resized {
		t.Fatal("expected image to be resized")
	}
	if len(prepared.ImageBytes) == 0 || len(prepared.ThumbnailBytes) == 0 {
		t.Fatal("expected image and thumbnail bytes")
	}
	if _, err := jpeg.Decode(bytes.NewReader(prepared.ImageBytes)); err != nil {
		t.Fatalf("processed image is not a valid JPEG: %v", err)
	}
}

func TestPrepareDraftImageAcceptsTransparentPNG(t *testing.T) {
	path := writeDraftTransparentPNG(t, 900, 1200)

	prepared, err := prepareDraftImage(path)
	if err != nil {
		t.Fatalf("prepareDraftImage returned error: %v", err)
	}
	if prepared.Width != 900 || prepared.Height != 1200 {
		t.Fatalf("expected no display resize, got %dx%d", prepared.Width, prepared.Height)
	}
	if prepared.ThumbnailWidth != 300 || prepared.ThumbnailHeight != 400 {
		t.Fatalf("expected 300x400 thumbnail, got %dx%d", prepared.ThumbnailWidth, prepared.ThumbnailHeight)
	}
	if _, err := jpeg.Decode(bytes.NewReader(prepared.ImageBytes)); err != nil {
		t.Fatalf("processed PNG output is not a valid JPEG: %v", err)
	}
}

func writeDraftTestJPEG(t *testing.T, width, height int) string {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 160, A: 255})
		}
	}
	path := filepath.Join(t.TempDir(), "draft.jpg")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeDraftTransparentPNG(t *testing.T, width, height int) string {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			alpha := uint8(255)
			if x < width/2 {
				alpha = 80
			}
			img.SetNRGBA(x, y, color.NRGBA{R: 30, G: 120, B: 220, A: alpha})
		}
	}
	path := filepath.Join(t.TempDir(), "draft.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}
