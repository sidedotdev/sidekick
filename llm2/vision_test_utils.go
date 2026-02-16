package llm2

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"strings"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// unambiguousChars excludes easily confused characters (0/O, 1/l/I, etc.).
const unambiguousChars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GenerateRandomText returns a random alphanumeric string of the given length
// using only characters that are visually unambiguous.
func GenerateRandomText(length int) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = unambiguousChars[rand.Intn(len(unambiguousChars))]
	}
	return string(b)
}

// RenderTextImage creates a high-contrast PNG image with the given text
// rendered using Go's built-in bold monospace font. fontSize controls the
// point size of the rendered text. Returns the raw PNG bytes.
func RenderTextImage(text string, fontSize int) []byte {
	if fontSize < 8 {
		fontSize = 8
	}

	tt, err := opentype.Parse(gomonobold.TTF)
	if err != nil {
		panic("failed to parse embedded font: " + err.Error())
	}
	face, err := opentype.NewFace(tt, &opentype.FaceOptions{
		Size:    float64(fontSize),
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		panic("failed to create font face: " + err.Error())
	}
	defer face.Close()

	// Measure the text to determine image dimensions.
	d := &font.Drawer{Face: face}
	advance := d.MeasureString(text)
	textW := advance.Ceil()
	metrics := face.Metrics()
	ascent := metrics.Ascent.Ceil()
	descent := metrics.Descent.Ceil()
	textH := ascent + descent

	padding := fontSize / 2
	imgW := textW + 2*padding
	imgH := textH + 2*padding

	img := image.NewRGBA(image.Rect(0, 0, imgW, imgH))

	// Fill white background.
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	for y := 0; y < imgH; y++ {
		for x := 0; x < imgW; x++ {
			img.Set(x, y, white)
		}
	}

	d.Dst = img
	d.Src = image.NewUniform(color.Black)
	d.Dot = fixed.P(padding, padding+ascent)
	d.DrawString(text)

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// VisionTestFuzzyMatch checks whether the model's response is close enough to
// the expected text by extracting only ASCII alphanumeric characters, uppercasing,
// and requiring that at least (len-1) characters match in the best alignment.
func VisionTestFuzzyMatch(expected, response string) bool {
	clean := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				b.WriteRune(unicode.ToUpper(r))
			}
		}
		return b.String()
	}
	exp := clean(expected)
	resp := clean(response)
	if len(exp) == 0 {
		return false
	}
	if strings.Contains(resp, exp) {
		return true
	}
	// Sliding window: find the best alignment of exp within resp.
	maxMatches := 0
	for i := 0; i <= len(resp)-len(exp); i++ {
		matches := 0
		for j := 0; j < len(exp); j++ {
			if resp[i+j] == exp[j] {
				matches++
			}
		}
		if matches > maxMatches {
			maxMatches = matches
		}
	}
	// Allow at most 1 mismatched character per 6 expected characters.
	allowedErrors := max(1, len(exp)/6)
	return maxMatches >= len(exp)-allowedErrors
}

// GenerateVisionTestImage produces a random text string and a data URL of a
// PNG image containing that text rendered in a large bold monospace font. The
// returned expectedText is the string embedded in the image.
func GenerateVisionTestImage(textLen int) (expectedText string, dataURL string) {
	expectedText = GenerateRandomText(textLen)
	raw := RenderTextImage(expectedText, 120)
	dataURL = BuildDataURL("image/png", raw)
	return expectedText, dataURL
}
