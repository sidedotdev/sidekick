package utils

import (
	"math"
	"strings"
	"testing"
)

func TestStringSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		s1       string
		s2       string
		expected float64
	}{
		{"Exact match", "hello world", "hello world", 1.0},
		{"Trimmed match", "  hello world  ", "hello world", 0.95},
		{"Lots of spaces in middle", "hello:           world", "hello: world", 0.9},
		{"Some spaces in middle", "hello:   world", "hello: world", 0.94},
		{"A little spaces in middle", "hello:  world", "hello: world", 0.97},
		{"Multiple long spaces differences", "hello     world     test", "hello world test", 0.9},
		{"Empty strings", "", "", 1.0},
		{"One empty string", "hello", "", 0.0},
		{"Strings with only spaces", "   ", "  ", 0.95},
		{"Empty vs String with only spaces", "   ", "", 0.95},
		{"Long strings with small difference", strings.Repeat("a", 1000) + "b", strings.Repeat("a", 1000) + "c", 0.999},
		{"Strings with numbers and punctuation", "test 123!", "test  123 !", 0.93},
		{"Case difference", "Hello World", "hello world", 0.82},
		{"Completely different strings", "apples", "oranges", 0.29},
		{"Similar strings with different lengths", "apple", "apples", 0.83},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StringSimilarity(tt.s1, tt.s2)
			if !almostEqual(result, tt.expected, 0.01) {
				t.Errorf("StringSimilarity(%q, %q) = %v, want %v", tt.s1, tt.s2, result, tt.expected)
			}

			tabResult := StringSimilarity(strings.ReplaceAll(tt.s1, " ", "\t"), strings.ReplaceAll(tt.s2, " ", "\t"))
			if tabResult != result {
				t.Errorf("mismatched score when replacing spaces with tabs: %v != %v", result, tabResult)
			}

			reverseResult := StringSimilarity(tt.s2, tt.s1)
			if reverseResult != result {
				t.Errorf("mismatched score when reversing arguments: %v != %v", result, reverseResult)
			}
		})
	}
}

func almostEqual(a, b, tolerance float64) bool {
	return math.Abs(a-b) <= tolerance
}

func TestSliceLevenshtein(t *testing.T) {
	tests := []struct {
		a        []string
		b        []string
		expected int
		ratio    float64
	}{
		{[]string{"testing", "123"}, []string{"testing", "xyz"}, 2, 1.0},
		{[]string{"123"}, []string{"testing", "123"}, 1, 0.5},
		{[]string{"123"}, []string{"testing", "123"}, 1, 0.5},
		{[]string{"a", "b", "c"}, []string{"a", "b", "c"}, 0, 0.0},
		{[]string{"a", "b", "c"}, []string{"a", "c", "b"}, 2, 0.6666666666666666},
		{[]string{"a", "b", "c"}, []string{"a", "b", "c", "d"}, 1, 0.25},
		{[]string{"a", "b", "c"}, []string{"a", "b", "c", "d"}, 1, 0.25},
		{[]string{"a", "b", "x"}, []string{"a", "c", "x"}, 2, 0.6666666666666666},
		{[]string{"a", "b", "x"}, []string{"a", "c", "z"}, 4, 1.3333333333333333},
		{[]string{"frontend", "src", "router.ts"}, []string{"frontend", "src", "router", "index.ts"}, 3, 0.75},
	}

	for _, test := range tests {
		dist, ratio := SliceLevenshtein(test.a, test.b)
		if dist != test.expected || ratio != test.ratio {
			t.Errorf("SliceLevenshtein(%v, %v) = (%d, %f); want (%d, %f)", test.a, test.b, dist, ratio, test.expected, test.ratio)
		}
	}
}
