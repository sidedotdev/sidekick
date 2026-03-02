package llm2

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAnthropicImageTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		w, h   int
		expect int
	}{
		{"small image", 100, 100, 14},
		{"1024x768", 1024, 768, 1049},
		{"large landscape needs scaling", 3000, 2000, 2184},
		{"exactly 1568 wide", 1568, 1000, 2091},
		{"square at limit", 1568, 1568, 3279},
		{"very large", 8000, 6000, 2459},
		{"tall portrait", 500, 3000, 546},
		{"tiny 1x1", 1, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := AnthropicImageTokens(tt.w, tt.h)
			assert.Equal(t, tt.expect, got)
		})
	}
}

func TestOpenAITileImageTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		w, h   int
		expect int
	}{
		// 512x512: no scaling needed. 1 tile × 1 tile = 1 tile → 85 + 170 = 255
		{"single tile", 512, 512, 255},
		// 1024x768: min(1024,768)=768 ≤ 768 so no short-side scaling.
		// tiles: ceil(1024/512)=2, ceil(768/512)=2 → 4 tiles → 85 + 680 = 765
		{"1024x768", 1024, 768, 765},
		// 2048x2048: fit in 2048 (ok), min=2048>768 → scale: 768/2048=0.375
		// → floor(2048*0.375)=768, floor(2048*0.375)=768
		// tiles: ceil(768/512)=2, ceil(768/512)=2 → 4 → 85+680=765
		{"large square", 2048, 2048, 765},
		// 4096x2048: fit in 2048 → scale 2048/4096=0.5 → 2048,1024
		// min(2048,1024)=1024>768 → scale 768/1024=0.75 → floor(2048*0.75)=1536, floor(1024*0.75)=768
		// tiles: ceil(1536/512)=3, ceil(768/512)=2 → 6 → 85+1020=1105
		{"wide panorama", 4096, 2048, 1105},
		// 100x100: no scaling. tiles: 1×1 → 85+170=255
		{"tiny", 100, 100, 255},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := OpenAITileImageTokens(tt.w, tt.h)
			assert.Equal(t, tt.expect, got)
		})
	}
}

func TestOpenAIPatchImageTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		w, h       int
		multiplier float64
		expect     int
	}{
		// 320x320: ceil(320/32)*ceil(320/32) = 10*10 = 100 patches, ×1.0 = 100
		{"small no multiplier", 320, 320, 1.0, 100},
		// 320x320 with 1.62 multiplier: 100*1.62 = 162
		{"small with 4.1-mini multiplier", 320, 320, 1.62, 162},
		// 1280x1280: ceil(1280/32)^2 = 40*40 = 1600 > 1536 → needs scaling
		{"exceeds cap", 1280, 1280, 1.0, 1521},
		// Very large: definitely capped
		{"very large", 4000, 3000, 1.0, 1485},
		// With nano multiplier 2.46
		{"small with nano multiplier", 320, 320, 2.46, 246},
		// Zero multiplier defaults to 1.0
		{"zero multiplier defaults", 320, 320, 0, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := OpenAIPatchImageTokens(tt.w, tt.h, tt.multiplier)
			assert.Equal(t, tt.expect, got)
		})
	}
}

func TestGeminiImageTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		w, h   int
		expect int
	}{
		{"tiny image", 100, 100, 258},
		{"at threshold", 384, 384, 258},
		{"just over threshold width", 385, 384, 258},  // ceil(385/768)=1, ceil(384/768)=1 → 1 tile
		{"just over threshold height", 384, 385, 258}, // same
		{"both over threshold", 385, 385, 258},        // ceil(385/768)=1 × ceil(385/768)=1 = 1 tile
		{"one full tile", 768, 768, 258},              // 1×1 = 1 tile
		{"two tiles wide", 1536, 768, 516},            // 2×1 = 2 tiles
		{"1920x1080", 1920, 1080, 1548},
		{"4k", 3840, 2160, 5 * 3 * 258}, // ceil(3840/768)=5, ceil(2160/768)=3 → 15 tiles
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := GeminiImageTokens(tt.w, tt.h)
			assert.Equal(t, tt.expect, got)
		})
	}
}

func TestEstimateImageTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		w, h int
	}{
		{"small", 100, 100},
		{"medium", 1024, 768},
		{"large", 1920, 1080},
		{"huge", 4000, 3000},
		{"tall", 500, 3000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			est := EstimateImageTokens(tt.w, tt.h)

			// Must be >= each provider estimate.
			assert.GreaterOrEqual(t, est, AnthropicImageTokens(tt.w, tt.h))
			assert.GreaterOrEqual(t, est, OpenAITileImageTokens(tt.w, tt.h))
			assert.GreaterOrEqual(t, est, OpenAIPatchImageTokens(tt.w, tt.h, 1.0))
			assert.GreaterOrEqual(t, est, GeminiImageTokens(tt.w, tt.h))
		})
	}
}

func TestImageDimensionsFromDataURL(t *testing.T) {
	t.Parallel()

	t.Run("valid png", func(t *testing.T) {
		t.Parallel()
		dataURL := makePNGDataURL(t, 200, 150)
		w, h := ImageDimensionsFromDataURL(dataURL)
		assert.Equal(t, 200, w)
		assert.Equal(t, 150, h)
	})

	t.Run("not a data URL", func(t *testing.T) {
		t.Parallel()
		w, h := ImageDimensionsFromDataURL("https://example.com/image.png")
		assert.Equal(t, 0, w)
		assert.Equal(t, 0, h)
	})

	t.Run("invalid base64", func(t *testing.T) {
		t.Parallel()
		w, h := ImageDimensionsFromDataURL("data:image/png;base64,!!!invalid!!!")
		assert.Equal(t, 0, w)
		assert.Equal(t, 0, h)
	})

	t.Run("valid base64 but not an image", func(t *testing.T) {
		t.Parallel()
		dataURL := BuildDataURL("image/png", []byte("not an image"))
		w, h := ImageDimensionsFromDataURL(dataURL)
		assert.Equal(t, 0, w)
		assert.Equal(t, 0, h)
	})
}
