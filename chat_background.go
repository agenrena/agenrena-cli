package main

import (
	"image"
	"image/draw"
	"math"
)

const (
	chatBackgroundWidth        = 1080
	chatBackgroundHeight       = 1920
	chatBackgroundMaxBytes     = 2 * 1024 * 1024
	chatBackgroundStartQuality = 85
	chatBackgroundMinQuality   = 65
)

type PreparedChatBackground struct {
	Bytes     []byte
	Width     int
	Height    int
	SizeBytes int
	MimeType  string
	Quality   int
}

func prepareChatBackground(filePath string) (*PreparedChatBackground, error) {
	img, err := decodeDraftImage(filePath)
	if err != nil {
		return nil, err
	}

	covered := resizeCover(img, chatBackgroundWidth, chatBackgroundHeight)
	cropped := centerCrop(covered, chatBackgroundWidth, chatBackgroundHeight)
	opaque := drawOnWhite(cropped)

	var encoded []byte
	quality := chatBackgroundStartQuality
	for q := chatBackgroundStartQuality; q >= chatBackgroundMinQuality; q -= 5 {
		candidate, err := encodeJPEG(opaque, q)
		if err != nil {
			return nil, err
		}
		encoded = candidate
		quality = q
		if len(candidate) <= chatBackgroundMaxBytes {
			break
		}
	}
	if len(encoded) > chatBackgroundMaxBytes {
		return nil, &cliError{
			Code:        "CHAT_BACKGROUND_TOO_LARGE",
			Message:     "processed chat background exceeds 2MB after JPEG compression",
			Recoverable: true,
		}
	}

	return &PreparedChatBackground{
		Bytes:     encoded,
		Width:     chatBackgroundWidth,
		Height:    chatBackgroundHeight,
		SizeBytes: len(encoded),
		MimeType:  "image/jpeg",
		Quality:   quality,
	}, nil
}

func resizeCover(src image.Image, targetW, targetH int) image.Image {
	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()
	if srcW == targetW && srcH == targetH {
		return src
	}
	scale := math.Max(float64(targetW)/float64(srcW), float64(targetH)/float64(srcH))
	dstW := int(math.Ceil(float64(srcW) * scale))
	dstH := int(math.Ceil(float64(srcH) * scale))
	if dstW < targetW {
		dstW = targetW
	}
	if dstH < targetH {
		dstH = targetH
	}
	return resizeBilinear(src, dstW, dstH)
}

func centerCrop(src image.Image, targetW, targetH int) image.Image {
	bounds := src.Bounds()
	startX := bounds.Min.X + (bounds.Dx()-targetW)/2
	startY := bounds.Min.Y + (bounds.Dy()-targetH)/2
	if startX < bounds.Min.X {
		startX = bounds.Min.X
	}
	if startY < bounds.Min.Y {
		startY = bounds.Min.Y
	}
	rect := image.Rect(0, 0, targetW, targetH)
	dst := image.NewRGBA(rect)
	draw.Draw(dst, rect, src, image.Point{X: startX, Y: startY}, draw.Src)
	return dst
}
