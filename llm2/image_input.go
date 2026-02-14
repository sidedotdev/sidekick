package llm2

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"

	"golang.org/x/image/draw"
)

// ParseDataURL splits a data URL into its mime type and decoded raw bytes.
// It expects the format: data:<mime>;base64,<payload>
func ParseDataURL(dataURL string) (mimeType string, raw []byte, err error) {
	if !strings.HasPrefix(dataURL, "data:") {
		return "", nil, fmt.Errorf("not a data URL: missing 'data:' prefix")
	}

	rest := dataURL[len("data:"):]
	commaIdx := strings.Index(rest, ",")
	if commaIdx < 0 {
		return "", nil, fmt.Errorf("invalid data URL: missing comma separator")
	}

	meta := rest[:commaIdx]
	payload := rest[commaIdx+1:]

	if !strings.HasSuffix(meta, ";base64") {
		return "", nil, fmt.Errorf("invalid data URL: missing ';base64' encoding marker")
	}

	mimeType = meta[:len(meta)-len(";base64")]
	if mimeType == "" {
		return "", nil, fmt.Errorf("invalid data URL: empty mime type")
	}

	raw, err = base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", nil, fmt.Errorf("invalid data URL: base64 decode error: %w", err)
	}

	return mimeType, raw, nil
}

// BuildDataURL constructs a data URL from a mime type and raw bytes.
func BuildDataURL(mimeType string, raw []byte) string {
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(raw)
}

// decodeImage decodes raw bytes into an image.Image.
// Supports PNG, JPEG, and GIF.
func decodeImage(raw []byte) (image.Image, string, error) {
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("failed to decode image: %w", err)
	}
	return img, format, nil
}

// resizeImage scales img so the longest edge is at most maxLongEdgePx,
// preserving aspect ratio. Returns the original image if already within limits.
func resizeImage(img image.Image, maxLongEdgePx int) image.Image {
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	longEdge := w
	if h > longEdge {
		longEdge = h
	}

	if longEdge <= maxLongEdgePx {
		return img
	}

	scale := float64(maxLongEdgePx) / float64(longEdge)
	newW := int(float64(w) * scale)
	newH := int(float64(h) * scale)
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.BiLinear.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
	return dst
}

// encodeAsJPEG encodes img as JPEG with the given quality (1-100).
func encodeAsJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	if err != nil {
		return nil, fmt.Errorf("failed to encode image as JPEG: %w", err)
	}
	return buf.Bytes(), nil
}

// PrepareImageDataURLForLimits takes a data URL containing an image and returns
// a (possibly resized/recompressed) data URL that satisfies the given constraints:
//   - maxLongEdgePx: the longest dimension (width or height) will be clamped to this value.
//   - maxBytes: the decoded image payload must not exceed this many bytes.
//
// If the image is already within limits, it is returned as-is. Otherwise the image
// is decoded, resized, and re-encoded as JPEG. Returns an error if the constraints
// cannot be met even after resizing and compression.
func PrepareImageDataURLForLimits(dataURL string, maxBytes int, maxLongEdgePx int) (newDataURL string, mime string, data []byte, err error) {
	mime, raw, err := ParseDataURL(dataURL)
	if err != nil {
		return "", "", nil, err
	}

	// Already within limits: return as-is.
	if len(raw) <= maxBytes && maxLongEdgePx <= 0 {
		return dataURL, mime, raw, nil
	}

	// Try to decode the image to check dimensions and potentially resize.
	img, _, decodeErr := decodeImage(raw)
	if decodeErr != nil {
		// Can't decode — check if the raw bytes are at least within the size limit.
		if len(raw) <= maxBytes {
			return dataURL, mime, raw, nil
		}
		return "", "", nil, fmt.Errorf("image exceeds %d bytes and cannot be decoded for resizing: %w", maxBytes, decodeErr)
	}

	bounds := img.Bounds()
	longEdge := bounds.Dx()
	if bounds.Dy() > longEdge {
		longEdge = bounds.Dy()
	}

	needsResize := maxLongEdgePx > 0 && longEdge > maxLongEdgePx
	needsRecompress := len(raw) > maxBytes

	if !needsResize && !needsRecompress {
		return dataURL, mime, raw, nil
	}

	if needsResize {
		img = resizeImage(img, maxLongEdgePx)
	}

	// Re-encode as JPEG, trying decreasing quality levels until within maxBytes.
	qualities := []int{95, 85, 75, 60, 40, 20, 10}
	for _, q := range qualities {
		encoded, encErr := encodeAsJPEG(img, q)
		if encErr != nil {
			return "", "", nil, encErr
		}
		if len(encoded) <= maxBytes {
			outMime := "image/jpeg"
			return BuildDataURL(outMime, encoded), outMime, encoded, nil
		}
	}

	return "", "", nil, fmt.Errorf("image cannot be reduced below %d bytes even at minimum quality", maxBytes)
}

func init() {
	// Ensure standard image formats are registered for image.Decode.
	_ = png.Decode
	_ = gif.Decode
	_ = jpeg.Decode
}
