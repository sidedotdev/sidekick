package llm2

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makePNGDataURL(t *testing.T, width, height int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return BuildDataURL("image/png", buf.Bytes())
}

func TestParseDataURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		input       string
		wantMime    string
		wantErr     bool
		errContains string
	}{
		{
			name:     "valid jpeg",
			input:    "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("fake")),
			wantMime: "image/jpeg",
		},
		{
			name:     "valid png",
			input:    "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("fake")),
			wantMime: "image/png",
		},
		{
			name:        "missing data prefix",
			input:       "http://example.com/image.png",
			wantErr:     true,
			errContains: "not a data URL",
		},
		{
			name:        "missing comma",
			input:       "data:image/png;base64",
			wantErr:     true,
			errContains: "missing comma",
		},
		{
			name:        "missing base64 marker",
			input:       "data:image/png,aGVsbG8=",
			wantErr:     true,
			errContains: "missing ';base64'",
		},
		{
			name:        "empty mime type",
			input:       "data:;base64," + base64.StdEncoding.EncodeToString([]byte("x")),
			wantErr:     true,
			errContains: "empty mime type",
		},
		{
			name:        "invalid base64 payload",
			input:       "data:image/png;base64,!!!not-base64!!!",
			wantErr:     true,
			errContains: "base64 decode error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mime, raw, err := ParseDataURL(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantMime, mime)
			assert.NotEmpty(t, raw)
		})
	}
}

func TestBuildDataURL(t *testing.T) {
	t.Parallel()
	raw := []byte("hello world")
	url := BuildDataURL("image/png", raw)
	assert.Equal(t, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(raw), url)

	// Round-trip
	mime, decoded, err := ParseDataURL(url)
	require.NoError(t, err)
	assert.Equal(t, "image/png", mime)
	assert.Equal(t, raw, decoded)
}

func TestResizeImage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		width, height    int
		maxLongEdge      int
		expectW, expectH int
	}{
		{
			name:  "landscape image clamped",
			width: 2000, height: 1000,
			maxLongEdge: 1000,
			expectW:     1000, expectH: 500,
		},
		{
			name:  "portrait image clamped",
			width: 800, height: 1600,
			maxLongEdge: 800,
			expectW:     400, expectH: 800,
		},
		{
			name:  "square image clamped",
			width: 1500, height: 1500,
			maxLongEdge: 500,
			expectW:     500, expectH: 500,
		},
		{
			name:  "already within limits",
			width: 400, height: 300,
			maxLongEdge: 1000,
			expectW:     400, expectH: 300,
		},
		{
			name:  "exact limit",
			width: 1000, height: 500,
			maxLongEdge: 1000,
			expectW:     1000, expectH: 500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			img := image.NewRGBA(image.Rect(0, 0, tt.width, tt.height))
			result := resizeImage(img, tt.maxLongEdge)
			bounds := result.Bounds()
			assert.Equal(t, tt.expectW, bounds.Dx())
			assert.Equal(t, tt.expectH, bounds.Dy())
		})
	}
}

func TestPrepareImageDataURLForLimits(t *testing.T) {
	t.Parallel()

	t.Run("already within limits returns original", func(t *testing.T) {
		t.Parallel()
		dataURL := makePNGDataURL(t, 100, 100)
		_, origRaw, err := ParseDataURL(dataURL)
		require.NoError(t, err)

		newURL, mime, data, err := PrepareImageDataURLForLimits(dataURL, 1024*1024, 0)
		require.NoError(t, err)
		assert.Equal(t, dataURL, newURL)
		assert.Equal(t, "image/png", mime)
		assert.Equal(t, origRaw, data)
	})

	t.Run("resize clamps long edge", func(t *testing.T) {
		t.Parallel()
		dataURL := makePNGDataURL(t, 2000, 1000)

		newURL, mime, _, err := PrepareImageDataURLForLimits(dataURL, 10*1024*1024, 500)
		require.NoError(t, err)
		assert.Equal(t, "image/jpeg", mime)

		_, raw, err := ParseDataURL(newURL)
		require.NoError(t, err)

		img, _, err := image.Decode(bytes.NewReader(raw))
		require.NoError(t, err)
		bounds := img.Bounds()
		longEdge := bounds.Dx()
		if bounds.Dy() > longEdge {
			longEdge = bounds.Dy()
		}
		assert.LessOrEqual(t, longEdge, 500)
	})

	t.Run("resize reduces output size", func(t *testing.T) {
		t.Parallel()
		dataURL := makePNGDataURL(t, 2000, 1000)
		_, origRaw, _ := ParseDataURL(dataURL)

		newURL, mime, data, err := PrepareImageDataURLForLimits(dataURL, 10*1024*1024, 200)
		require.NoError(t, err)
		assert.Equal(t, "image/jpeg", mime)
		assert.Less(t, len(data), len(origRaw))
		assert.NotEmpty(t, newURL)
	})

	t.Run("impossible size constraint returns error", func(t *testing.T) {
		t.Parallel()
		dataURL := makePNGDataURL(t, 200, 200)

		_, _, _, err := PrepareImageDataURLForLimits(dataURL, 10, 0)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be reduced")
	})

	t.Run("invalid data URL returns error", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := PrepareImageDataURLForLimits("not-a-data-url", 1024, 0)
		assert.Error(t, err)
	})

	t.Run("non-decodable image within size limit passes through", func(t *testing.T) {
		t.Parallel()
		raw := []byte("not-a-real-image-but-small")
		dataURL := BuildDataURL("image/webp", raw)

		newURL, mime, data, err := PrepareImageDataURLForLimits(dataURL, 1024*1024, 0)
		require.NoError(t, err)
		assert.Equal(t, dataURL, newURL)
		assert.Equal(t, "image/webp", mime)
		assert.Equal(t, raw, data)
	})

	t.Run("non-decodable image exceeding size limit returns error", func(t *testing.T) {
		t.Parallel()
		raw := make([]byte, 1000)
		dataURL := BuildDataURL("image/webp", raw)

		_, _, _, err := PrepareImageDataURLForLimits(dataURL, 500, 0)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be decoded for resizing")
	})
}
