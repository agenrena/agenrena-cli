package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const stickerSize = 512

type StickerImageInfo struct {
	SourceWidth     int
	SourceHeight    int
	ProcessedWidth  int
	ProcessedHeight int
	Resized         bool
	HasAlpha        bool
}

func prepareStickerPNG(filePath string) ([]byte, StickerImageInfo, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, StickerImageInfo{}, wrapError("FILE_OPEN_FAILED", "failed to open sticker file", err)
	}
	defer f.Close()

	img, err := png.Decode(f)
	if err != nil {
		return nil, StickerImageInfo{}, wrapError("STICKER_INVALID_PNG", "sticker must be a valid PNG file", err)
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	info := StickerImageInfo{
		SourceWidth:     width,
		SourceHeight:    height,
		ProcessedWidth:  stickerSize,
		ProcessedHeight: stickerSize,
		HasAlpha:        hasAlpha(img),
	}

	if width != height {
		return nil, info, &cliError{
			Code:        "STICKER_NOT_SQUARE",
			Message:     "sticker PNG must be square",
			Recoverable: true,
		}
	}

	var processed image.Image
	if width == stickerSize && height == stickerSize {
		processed = img
	} else {
		processed = resizeBilinear(img, stickerSize, stickerSize)
		info.Resized = true
	}

	var out bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&out, processed); err != nil {
		return nil, info, wrapError("STICKER_ENCODE_FAILED", "failed to encode sticker PNG", err)
	}
	return out.Bytes(), info, nil
}

func hasAlpha(img image.Image) bool {
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a < 0xffff {
				return true
			}
		}
	}
	return false
}

func resizeBilinear(src image.Image, dstW, dstH int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()

	if srcW == 0 || srcH == 0 {
		return dst
	}

	xRatio := float64(srcW) / float64(dstW)
	yRatio := float64(srcH) / float64(dstH)

	for y := 0; y < dstH; y++ {
		srcY := (float64(y)+0.5)*yRatio - 0.5
		y0 := int(math.Floor(srcY))
		y1 := y0 + 1
		wy := srcY - float64(y0)
		if y0 < 0 {
			y0 = 0
			wy = 0
		}
		if y1 >= srcH {
			y1 = srcH - 1
		}

		for x := 0; x < dstW; x++ {
			srcX := (float64(x)+0.5)*xRatio - 0.5
			x0 := int(math.Floor(srcX))
			x1 := x0 + 1
			wx := srcX - float64(x0)
			if x0 < 0 {
				x0 = 0
				wx = 0
			}
			if x1 >= srcW {
				x1 = srcW - 1
			}

			c00 := rgba64(src.At(srcBounds.Min.X+x0, srcBounds.Min.Y+y0).RGBA())
			c10 := rgba64(src.At(srcBounds.Min.X+x1, srcBounds.Min.Y+y0).RGBA())
			c01 := rgba64(src.At(srcBounds.Min.X+x0, srcBounds.Min.Y+y1).RGBA())
			c11 := rgba64(src.At(srcBounds.Min.X+x1, srcBounds.Min.Y+y1).RGBA())

			dst.SetNRGBA(x, y, blendPremultiplied(c00, c10, c01, c11, wx, wy))
		}
	}
	return dst
}

type rgba64Pixel struct {
	r float64
	g float64
	b float64
	a float64
}

func rgba64(r, g, b, a uint32) rgba64Pixel {
	return rgba64Pixel{
		r: float64(r),
		g: float64(g),
		b: float64(b),
		a: float64(a),
	}
}

func blendPremultiplied(c00, c10, c01, c11 rgba64Pixel, wx, wy float64) colorNRGBA {
	r := bilinearFloat(c00.r, c10.r, c01.r, c11.r, wx, wy)
	g := bilinearFloat(c00.g, c10.g, c01.g, c11.g, wx, wy)
	b := bilinearFloat(c00.b, c10.b, c01.b, c11.b, wx, wy)
	a := bilinearFloat(c00.a, c10.a, c01.a, c11.a, wx, wy)
	return unpremultiplyNRGBA(r, g, b, a)
}

func bilinearFloat(v00, v10, v01, v11, wx, wy float64) float64 {
	top := v00*(1-wx) + v10*wx
	bottom := v01*(1-wx) + v11*wx
	return top*(1-wy) + bottom*wy
}

type colorNRGBA = color.NRGBA

func unpremultiplyNRGBA(r, g, b, a float64) colorNRGBA {
	if a <= 0 {
		return colorNRGBA{}
	}
	r = r * 65535 / a
	g = g * 65535 / a
	b = b * 65535 / a
	return colorNRGBA{
		R: clamp8(r / 257),
		G: clamp8(g / 257),
		B: clamp8(b / 257),
		A: clamp8(a / 257),
	}
}

func clamp8(value float64) uint8 {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return uint8(value + 0.5)
}
