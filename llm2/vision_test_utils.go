package llm2

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"strings"
	"unicode"

	"github.com/adrg/strutil"
	"github.com/adrg/strutil/metrics"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

var visionLevenshtein = metrics.NewLevenshtein()

// unambiguousChars excludes easily confused characters (0/O/Q, 1/l/I/T, 8/B, M/W, F/E, 5/S, L/1, 6/G, 2/Z, 7/Y, U/V).
const unambiguousChars = "ACDHJKNPRUX349"

// VisionTestCharSetHint returns a prompt fragment listing the exact characters
// used in generated vision test images.
func VisionTestCharSetHint() string {
	parts := make([]string, len(unambiguousChars))
	for i, c := range unambiguousChars {
		parts[i] = string(c)
	}
	return "Each character is one of: " + strings.Join(parts, ", ") + "."
}

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
	// Add extra spacing between characters to reduce positional confusion.
	letterSpacing := fontSize / 3
	d := &font.Drawer{Face: face}
	advance := d.MeasureString(text)
	textW := advance.Ceil() + letterSpacing*(len(text)-1)
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
	for i, ch := range text {
		d.DrawString(string(ch))
		if i < len(text)-1 {
			d.Dot.X += fixed.I(letterSpacing)
		}
	}

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
	// Allow at most 1 error per 6 expected characters.
	allowedErrors := max(1, len(exp)/6)
	minSimilarity := 1.0 - float64(allowedErrors)/float64(len(exp))
	// Slide windows of varying length across resp and check similarity.
	for winLen := max(1, len(exp)-allowedErrors); winLen <= len(exp)+allowedErrors; winLen++ {
		for i := 0; i <= len(resp)-winLen; i++ {
			if strutil.Similarity(exp, resp[i:i+winLen], visionLevenshtein) >= minSimilarity {
				return true
			}
		}
	}
	// Also check the full response in case it's shorter than exp.
	if len(resp) < len(exp) {
		if strutil.Similarity(exp, resp, visionLevenshtein) >= minSimilarity {
			return true
		}
	}
	return false
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
