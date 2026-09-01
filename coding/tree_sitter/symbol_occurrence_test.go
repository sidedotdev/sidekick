package tree_sitter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindSymbolOccurrences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		languageName  string
		source        string
		symbolName    string
		expectedTexts []string
		expectedRows  []uint
	}{
		{
			name:         "plain identifier ignores substrings, comments and strings",
			languageName: "golang",
			source: `package main

import "runtime"

// run the activity
const runActivityTaskQueue = "run-activity-script"

func run() {
	runtime.Gosched()
}
`,
			symbolName:    "run",
			expectedTexts: []string{"run"},
			expectedRows:  []uint{7},
		},
		{
			name:         "qualified name matches the final segment",
			languageName: "golang",
			source: `package main

import "runtime"

func caller() {
	runtime.Gosched()
}
`,
			symbolName:    "runtime.Gosched",
			expectedTexts: []string{"Gosched"},
			expectedRows:  []uint{5},
		},
		{
			name:         "qualified name does not match a longer qualifier",
			languageName: "golang",
			source: `package main

func caller() {
	myruntime.Gosched()
}
`,
			symbolName:    "runtime.Gosched",
			expectedTexts: nil,
			expectedRows:  nil,
		},
		{
			name:         "python identifiers are matched syntactically",
			languageName: "python",
			source: `# run this
runner = "run"

def run():
    pass
`,
			symbolName:    "run",
			expectedTexts: []string{"run"},
			expectedRows:  []uint{3},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			source := []byte(tc.source)
			occurrences, err := FindSymbolOccurrences(tc.languageName, source, tc.symbolName)
			require.NoError(t, err)

			var texts []string
			var rows []uint
			for _, occurrence := range occurrences {
				texts = append(texts, string(source[occurrence.StartByte:occurrence.EndByte]))
				rows = append(rows, occurrence.StartPoint.Row)
			}
			assert.Equal(t, tc.expectedTexts, texts)
			assert.Equal(t, tc.expectedRows, rows)
		})
	}
}

func TestFindSymbolOccurrencesUnsupportedLanguage(t *testing.T) {
	t.Parallel()

	_, err := FindSymbolOccurrences("unknown", []byte("run\n"), "run")
	assert.Error(t, err)
}
