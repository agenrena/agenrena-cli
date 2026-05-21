package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareStickerPNGResizesSquareImage(t *testing.T) {
	path := writeTestPNG(t, 256, 256)

	content, info, err := prepareStickerPNG(path)
	if err != nil {
		t.Fatalf("prepareStickerPNG returned error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected encoded PNG content")
	}
	if !info.Resized {
		t.Fatal("expected image to be resized")
	}
	if info.ProcessedWidth != stickerSize || info.ProcessedHeight != stickerSize {
		t.Fatalf("expected %dx%d output, got %dx%d", stickerSize, stickerSize, info.ProcessedWidth, info.ProcessedHeight)
	}
}

func TestPrepareStickerPNGRejectsNonSquareImage(t *testing.T) {
	path := writeTestPNG(t, 256, 128)

	_, _, err := prepareStickerPNG(path)
	if err == nil {
		t.Fatal("expected non-square image to fail")
	}
	ce, ok := err.(*cliError)
	if !ok {
		t.Fatalf("expected cliError, got %T", err)
	}
	if ce.Code != "STICKER_NOT_SQUARE" {
		t.Fatalf("expected STICKER_NOT_SQUARE, got %s", ce.Code)
	}
}

func TestPrepareStickerPNGReportsAlpha(t *testing.T) {
	opaquePath := writeTestPNG(t, 512, 512)
	_, opaqueInfo, err := prepareStickerPNG(opaquePath)
	if err != nil {
		t.Fatalf("prepare opaque sticker: %v", err)
	}
	if opaqueInfo.HasAlpha {
		t.Fatal("expected opaque test image to report no alpha")
	}

	transparentPath := writeTransparentTestPNG(t, 512, 512)
	_, transparentInfo, err := prepareStickerPNG(transparentPath)
	if err != nil {
		t.Fatalf("prepare transparent sticker: %v", err)
	}
	if !transparentInfo.HasAlpha {
		t.Fatal("expected transparent test image to report alpha")
	}
}

func writeTestPNG(t *testing.T, width, height int) string {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 120, A: 255})
		}
	}

	path := filepath.Join(t.TempDir(), "sticker.png")
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

func writeTransparentTestPNG(t *testing.T, width, height int) string {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			alpha := uint8(255)
			if x < width/2 {
				alpha = 0
			}
			img.SetNRGBA(x, y, color.NRGBA{R: 200, G: 80, B: 120, A: alpha})
		}
	}

	path := filepath.Join(t.TempDir(), "transparent-sticker.png")
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
