package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"os"
)

const (
	draftImageMaxLongEdge  = 1600
	draftThumbMaxLongEdge  = 400
	draftImageMaxBytes     = 2 * 1024 * 1024
	draftImageStartQuality = 85
	draftImageMinQuality   = 65
	draftThumbQuality      = 80
)

type PreparedDraftImage struct {
	ImageBytes      []byte
	ThumbnailBytes  []byte
	Width           int
	Height          int
	ThumbnailWidth  int
	ThumbnailHeight int
	SizeBytes       int
	MimeType        string
	Resized         bool
	Quality         int
}

func prepareDraftImage(filePath string) (*PreparedDraftImage, error) {
	img, err := decodeDraftImage(filePath)
	if err != nil {
		return nil, err
	}
	sourceBounds := img.Bounds()
	sourceW := sourceBounds.Dx()
	sourceH := sourceBounds.Dy()
	if sourceW <= 0 || sourceH <= 0 {
		return nil, &cliError{Code: "IMAGE_INVALID_DIMENSIONS", Message: "image dimensions must be positive", Recoverable: true}
	}

	display := resizeToMaxLongEdge(img, draftImageMaxLongEdge)
	displayOpaque := drawOnWhite(display)
	displayBounds := displayOpaque.Bounds()

	var imageBytes []byte
	quality := draftImageStartQuality
	for q := draftImageStartQuality; q >= draftImageMinQuality; q -= 5 {
		encoded, err := encodeJPEG(displayOpaque, q)
		if err != nil {
			return nil, err
		}
		imageBytes = encoded
		quality = q
		if len(encoded) <= draftImageMaxBytes {
			break
		}
	}
	if len(imageBytes) > draftImageMaxBytes {
		return nil, &cliError{
			Code:        "DRAFT_IMAGE_TOO_LARGE",
			Message:     "processed image exceeds 2MB after JPEG compression",
			Recoverable: true,
		}
	}

	thumb := resizeToMaxLongEdge(displayOpaque, draftThumbMaxLongEdge)
	thumbOpaque := drawOnWhite(thumb)
	thumbBytes, err := encodeJPEG(thumbOpaque, draftThumbQuality)
	if err != nil {
		return nil, err
	}
	thumbBounds := thumbOpaque.Bounds()

	return &PreparedDraftImage{
		ImageBytes:      imageBytes,
		ThumbnailBytes:  thumbBytes,
		Width:           displayBounds.Dx(),
		Height:          displayBounds.Dy(),
		ThumbnailWidth:  thumbBounds.Dx(),
		ThumbnailHeight: thumbBounds.Dy(),
		SizeBytes:       len(imageBytes),
		MimeType:        "image/jpeg",
		Resized:         displayBounds.Dx() != sourceW || displayBounds.Dy() != sourceH,
		Quality:         quality,
	}, nil
}

func decodeDraftImage(filePath string) (image.Image, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, wrapError("FILE_OPEN_FAILED", "failed to open image file", err)
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err == nil {
		return img, nil
	}

	fallback, fallbackErr := os.Open(filePath)
	if fallbackErr != nil {
		return nil, wrapError("IMAGE_DECODE_FAILED", "failed to decode image", err)
	}
	defer fallback.Close()
	if img, decodeErr := png.Decode(fallback); decodeErr == nil {
		return img, nil
	}
	return nil, wrapError("IMAGE_DECODE_FAILED", "image must be JPEG or PNG", err)
}

func resizeToMaxLongEdge(src image.Image, maxLongEdge int) image.Image {
	bounds := src.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	longEdge := width
	if height > longEdge {
		longEdge = height
	}
	if longEdge <= maxLongEdge {
		return src
	}
	scale := float64(maxLongEdge) / float64(longEdge)
	dstW := int(math.Round(float64(width) * scale))
	dstH := int(math.Round(float64(height) * scale))
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}
	return resizeBilinear(src, dstW, dstH)
}

func drawOnWhite(src image.Image) *image.RGBA {
	bounds := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), src, bounds.Min, draw.Over)
	return dst
}

func encodeJPEG(img image.Image, quality int) ([]byte, error) {
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, wrapError("IMAGE_ENCODE_FAILED", "failed to encode JPEG", err)
	}
	return out.Bytes(), nil
}

func init() {
	image.RegisterFormat("jpeg", "\xff\xd8", jpeg.Decode, jpeg.DecodeConfig)
	image.RegisterFormat("png", "\x89PNG\r\n\x1a\n", png.Decode, png.DecodeConfig)
}
