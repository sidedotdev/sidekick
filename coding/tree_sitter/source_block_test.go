package tree_sitter

import (
	"reflect"
	"sidekick/utils"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"
)

type testCase struct {
	name            string
	sourceBlocks    []SourceBlock
	numContextLines int
	sourceCode      []byte
	expectedStrings []string
	expected        []SourceBlock
}

func TestExpandContextLines(t *testing.T) {
	t.Parallel()
	testCases := []testCase{
		{
			name:            "empty source blocks",
			sourceBlocks:    []SourceBlock{},
			numContextLines: 2,
			sourceCode:      []byte("line1\nline2\nline3"),
			expectedStrings: []string{},
			expected:        []SourceBlock{},
		},
		{
			name: "empty source code",
			sourceBlocks: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  0,
						EndByte:    0,
						StartPoint: sitter.Point{Row: 0, Column: 0},
						EndPoint:   sitter.Point{Row: 0, Column: 0},
					},
				},
			},
			numContextLines: 2,
			sourceCode:      []byte(""),
			expectedStrings: []string{""},
			expected: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  0,
						EndByte:    0,
						StartPoint: sitter.Point{Row: 0, Column: 0},
						EndPoint:   sitter.Point{Row: 0, Column: 0},
					},
				},
			},
		},
		{
			name: "zero context lines expands partial line",
			sourceBlocks: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  6,
						EndByte:    7,
						StartPoint: sitter.Point{Row: 1, Column: 0},
						EndPoint:   sitter.Point{Row: 1, Column: 1},
					},
				},
			},
			numContextLines: 0,
			sourceCode:      []byte("line1\nline2\nline3"),
			expectedStrings: []string{"line2\n"},
			expected: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  6,
						EndByte:    12,
						StartPoint: sitter.Point{Row: 1, Column: 0},
						EndPoint:   sitter.Point{Row: 1, Column: 6},
					},
				},
			},
		},
		{
			name: "context lines greater than available lines, no newline at end of source code",
			sourceBlocks: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  6,
						EndByte:    12,
						StartPoint: sitter.Point{Row: 1, Column: 0},
						EndPoint:   sitter.Point{Row: 1, Column: 6},
					},
				},
			},
			numContextLines: 3,
			sourceCode:      []byte("line1\nline2\nline3"),
			expectedStrings: []string{"line1\nline2\nline3"},
			expected: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  0,
						EndByte:    uint32(len([]byte("line1\nline2\nline3"))),
						StartPoint: sitter.Point{Row: 0, Column: 0},
						EndPoint:   sitter.Point{Row: 2, Column: 5},
					},
				},
			},
		},
		{
			name: "context lines not able to be expanded",
			sourceBlocks: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  0,
						EndByte:    uint32(len([]byte("line1\nline2\nline3"))),
						StartPoint: sitter.Point{Row: 0, Column: 0},
						EndPoint:   sitter.Point{Row: 2, Column: 5},
					},
				},
			},
			numContextLines: 3,
			sourceCode:      []byte("line1\nline2\nline3"),
			expectedStrings: []string{"line1\nline2\nline3"},
			expected: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  0,
						EndByte:    uint32(len([]byte("line1\nline2\nline3"))),
						StartPoint: sitter.Point{Row: 0, Column: 0},
						EndPoint:   sitter.Point{Row: 2, Column: 5},
					},
				},
			},
		},
		{
			name: "context lines greater than available lines, with newline at end of source code",
			sourceBlocks: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  6,
						EndByte:    12,
						StartPoint: sitter.Point{Row: 1, Column: 0},
						EndPoint:   sitter.Point{Row: 1, Column: 6},
					},
				},
			},
			numContextLines: 3,
			sourceCode:      []byte("line1\nline2\nline3\n"),
			expectedStrings: []string{"line1\nline2\nline3\n"},
			expected: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  0,
						EndByte:    uint32(len([]byte("line1\nline2\nline3\n"))),
						StartPoint: sitter.Point{Row: 0, Column: 0},
						EndPoint:   sitter.Point{Row: 3, Column: 0},
					},
				},
			},
		},
		{
			name: "context lines well within available lines",
			sourceBlocks: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  uint32(len([]byte("line1\nline2\n"))),
						EndByte:    uint32(len([]byte("line1\nline2\nline3\n"))),
						StartPoint: sitter.Point{Row: 2, Column: 0},
						EndPoint:   sitter.Point{Row: 2, Column: 6},
					},
				},
			},
			numContextLines: 1,
			sourceCode:      []byte("line1\nline2\nline3\nline4\nline5"),
			expectedStrings: []string{"line2\nline3\nline4\n"},
			expected: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  6,
						EndByte:    uint32(len([]byte("line1\nline2\nline3\nline4\n"))),
						StartPoint: sitter.Point{Row: 1, Column: 0},
						EndPoint:   sitter.Point{Row: 3, Column: 6},
					},
				},
			},
		},
		{
			name: "context lines exactly matching available lines",
			sourceBlocks: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  uint32(len([]byte("line1\nline2\n"))),
						EndByte:    uint32(len([]byte("line1\nline2\nline3\n"))),
						StartPoint: sitter.Point{Row: 2, Column: 0},
						EndPoint:   sitter.Point{Row: 2, Column: 6},
					},
				},
			},
			numContextLines: 2,
			sourceCode:      []byte("line1\nline2\nline3\nline4\nline5"),
			expectedStrings: []string{"line1\nline2\nline3\nline4\nline5"},
			expected: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  0,
						EndByte:    uint32(len([]byte("line1\nline2\nline3\nline4\nline5"))),
						StartPoint: sitter.Point{Row: 0, Column: 0},
						EndPoint:   sitter.Point{Row: 4, Column: 5},
					},
				},
			},
		},
		{
			name: "all empty lines, expand less than full available",
			sourceBlocks: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  1,
						EndByte:    2,
						StartPoint: sitter.Point{Row: 1, Column: 0},
						EndPoint:   sitter.Point{Row: 1, Column: 0},
					},
				},
			},
			numContextLines: 1,
			sourceCode:      []byte("\n\n\n"),
			expectedStrings: []string{"\n\n\n"},
			expected: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  0,
						EndByte:    uint32(len([]byte("\n\n\n"))),
						StartPoint: sitter.Point{Row: 0, Column: 0},
						EndPoint:   sitter.Point{Row: 2, Column: 0},
					},
				},
			},
		},
		{
			name: "all empty lines, expand more than full available",
			sourceBlocks: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  1,
						EndByte:    2,
						StartPoint: sitter.Point{Row: 1, Column: 0},
						EndPoint:   sitter.Point{Row: 1, Column: 0},
					},
				},
			},
			numContextLines: 2,
			sourceCode:      []byte("\n\n\n"),
			expectedStrings: []string{"\n\n\n"},
			expected: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  0,
						EndByte:    uint32(len([]byte("\n\n\n"))),
						StartPoint: sitter.Point{Row: 0, Column: 0},
						EndPoint:   sitter.Point{Row: 3, Column: 0},
					},
				},
			},
		},
		{
			name: "out of bounds byte range clamped",
			sourceBlocks: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  100,
						EndByte:    200,
						StartPoint: sitter.Point{Row: 50, Column: 0},
						EndPoint:   sitter.Point{Row: 60, Column: 0},
					},
				},
			},
			numContextLines: 2,
			sourceCode:      []byte("line1\nline2\nline3"),
			expectedStrings: []string{"line2\nline3"},
			expected: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  6,
						EndByte:    uint32(len([]byte("line1\nline2\nline3"))),
						StartPoint: sitter.Point{Row: 0, Column: 0},
						EndPoint:   sitter.Point{Row: 2, Column: 5},
					},
				},
			},
		},
		{
			name: "end byte exceeds source length",
			sourceBlocks: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  6,
						EndByte:    100,
						StartPoint: sitter.Point{Row: 1, Column: 0},
						EndPoint:   sitter.Point{Row: 10, Column: 0},
					},
				},
			},
			numContextLines: 1,
			sourceCode:      []byte("line1\nline2\nline3"),
			expectedStrings: []string{"line1\nline2\nline3"},
			expected: []SourceBlock{
				{
					Range: sitter.Range{
						StartByte:  0,
						EndByte:    uint32(len([]byte("line1\nline2\nline3"))),
						StartPoint: sitter.Point{Row: 0, Column: 0},
						EndPoint:   sitter.Point{Row: 2, Column: 5},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := ExpandContextLines(tc.sourceBlocks, tc.numContextLines, tc.sourceCode)
			if !reflect.DeepEqual(result, tc.expected) {
				t.Errorf("Expected %s, Got: %s", utils.PanicJSON(tc.expected), utils.PanicJSON(result))
			}
			for i := range result {
				result[i].Source = &tc.sourceCode
				tc.expected[i].Source = &tc.sourceCode
				if result[i].String() != tc.expectedStrings[i] {
					t.Errorf("Expected string %s, Got string: %s", utils.PanicJSON(tc.expectedStrings[i]), utils.PanicJSON(result[i].String()))
				}
				if result[i].String() != tc.expected[i].String() {
					t.Errorf("Expected %s, Got: %s", utils.PanicJSON(tc.expected[i].String()), utils.PanicJSON(result[i].String()))
				}
			}
		})
	}
}

func TestSourceBlockString_OutOfBounds(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name      string
		source    []byte
		startByte uint32
		endByte   uint32
		expected  string
	}{
		{
			name:      "nil source",
			source:    nil,
			startByte: 0,
			endByte:   10,
			expected:  "",
		},
		{
			name:      "empty source",
			source:    []byte{},
			startByte: 0,
			endByte:   10,
			expected:  "",
		},
		{
			name:      "start and end beyond length",
			source:    []byte("hello"),
			startByte: 100,
			endByte:   200,
			expected:  "",
		},
		{
			name:      "end beyond length",
			source:    []byte("hello"),
			startByte: 2,
			endByte:   100,
			expected:  "llo",
		},
		{
			name:      "start greater than end",
			source:    []byte("hello"),
			startByte: 10,
			endByte:   5,
			expected:  "",
		},
		{
			name:      "valid range",
			source:    []byte("hello"),
			startByte: 1,
			endByte:   4,
			expected:  "ell",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var sourcePtr *[]byte
			if tc.source != nil {
				sourcePtr = &tc.source
			}
			sb := SourceBlock{
				Source: sourcePtr,
				Range: sitter.Range{
					StartByte: tc.startByte,
					EndByte:   tc.endByte,
				},
			}
			result := sb.String()
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}
