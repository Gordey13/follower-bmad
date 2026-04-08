package worker

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

var pngSignature = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

func normalizeScreenshotPayload(payload []byte) ([]byte, error) {
	if bytes.HasPrefix(payload, pngSignature) {
		cloned := make([]byte, len(payload))
		copy(cloned, payload)
		return cloned, nil
	}

	// Fallback placeholder so follow.png is always a valid image.
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	placeholder := color.RGBA{R: 242, G: 242, B: 242, A: 255}
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img.Set(x, y, placeholder)
		}
	}

	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}

	return out.Bytes(), nil
}
