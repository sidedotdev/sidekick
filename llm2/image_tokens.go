package llm2

import (
	"math"
)

// AnthropicImageTokens estimates token cost for an image sent to Anthropic Claude.
// Images are scaled so the longest edge ≤ 1568 px and total pixels ≤ 1568×1568.
func AnthropicImageTokens(w, h int) int {
	const maxDim = 1568
	const maxPixels = maxDim * maxDim

	fw, fh := float64(w), float64(h)

	if max(w, h) > maxDim {
		s := float64(maxDim) / float64(max(w, h))
		fw = math.Floor(fw * s)
		fh = math.Floor(fh * s)
	}

	if fw*fh > float64(maxPixels) {
		s := math.Sqrt(float64(maxPixels) / (fw * fh))
		fw = math.Floor(fw * s)
		fh = math.Floor(fh * s)
	}

	return int(math.Ceil(fw * fh / 750.0))
}

// OpenAITileImageTokens estimates token cost for GPT-4o / GPT-4.1 (high detail).
// The image is fit within 2048×2048, then the shortest side is scaled to 768,
// and 512px tiles are counted.
func OpenAITileImageTokens(w, h int) int {
	fw, fh := float64(w), float64(h)

	// Fit within 2048×2048.
	if math.Max(fw, fh) > 2048 {
		s := 2048.0 / math.Max(fw, fh)
		fw = math.Floor(fw * s)
		fh = math.Floor(fh * s)
	}

	// Scale shortest side to 768.
	if math.Min(fw, fh) > 768 {
		s := 768.0 / math.Min(fw, fh)
		fw = math.Floor(fw * s)
		fh = math.Floor(fh * s)
	}

	tilesX := int(math.Ceil(fw / 512.0))
	tilesY := int(math.Ceil(fh / 512.0))
	return 85 + tilesX*tilesY*170
}

// OpenAIPatchImageTokens estimates token cost for GPT-4.1/5 mini/nano models.
// These use 32×32 patches capped at 1536, with a model-specific multiplier.
// Use multiplier 1.0 for full-size models, 1.62 for 4.1-mini, 2.46 for 4.1-nano,
// 1.2 for 5-mini, 1.5 for 5-nano.
func OpenAIPatchImageTokens(w, h int, multiplier float64) int {
	const maxPatches = 1536
	const patchSize = 32

	patches := int(math.Ceil(float64(w)/float64(patchSize))) * int(math.Ceil(float64(h)/float64(patchSize)))

	if patches > maxPatches {
		r := math.Sqrt(float64(patchSize*patchSize*maxPatches) / float64(w*h))
		newW := math.Floor(float64(w) * r)
		newH := math.Floor(float64(h) * r)
		fitW := int(math.Ceil(newW / float64(patchSize)))
		fitH := int(math.Ceil(newH / float64(patchSize)))
		patches = fitW * fitH
		if patches > maxPatches {
			patches = maxPatches
		}
	}

	if multiplier <= 0 {
		multiplier = 1.0
	}
	return int(math.Ceil(float64(patches) * multiplier))
}

// GeminiImageTokens estimates token cost for Google Gemini.
// Images ≤ 384×384 cost 258 tokens; larger images are tiled at 768×768.
func GeminiImageTokens(w, h int) int {
	const tokensPerTile = 258
	if w <= 384 && h <= 384 {
		return tokensPerTile
	}
	tilesW := int(math.Ceil(float64(w) / 768.0))
	tilesH := int(math.Ceil(float64(h) / 768.0))
	return tilesW * tilesH * tokensPerTile
}

// ImageTokensForProvider returns the estimated token cost for an image
// based on the provider name. Unknown or empty providers default to
// Anthropic-style calculation.
func ImageTokensForProvider(provider string, w, h int) int {
	switch provider {
	case "openai":
		return OpenAIPatchImageTokens(w, h, 1.0)
	case "google":
		return GeminiImageTokens(w, h)
	default:
		return AnthropicImageTokens(w, h)
	}
}
