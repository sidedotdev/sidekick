package utils

import (
	"testing"
)

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
