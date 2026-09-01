package lsp

import (
	"context"
	"path/filepath"
	"testing"

	"sidekick/env"
	"sidekick/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const goOccurrenceSource = `package main

import (
	"runtime"
)

// run the activity via runActivityTaskQueue
const runActivityTaskQueue = "run-activity-script"

func run() {
	runtime.Gosched()
}

func caller() {
	run()
}
`

func TestFindSymbolOccurrencePositions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		langName  string
		source    string
		symbol    string
		fileRange *Range
		expected  []Position
	}{
		{
			name:     "go identifier ignores substrings, comments and string literals",
			langName: "golang",
			source:   goOccurrenceSource,
			symbol:   "run",
			expected: []Position{
				{Line: 9, Character: 7},
				{Line: 14, Character: 3},
			},
		},
		{
			name:     "go qualified identifier",
			langName: "golang",
			source:   goOccurrenceSource,
			symbol:   "runtime.Gosched",
			expected: []Position{
				{Line: 10, Character: 15},
			},
		},
		{
			name:      "go occurrences restricted to reference line range",
			langName:  "golang",
			source:    goOccurrenceSource,
			symbol:    "run",
			fileRange: &Range{Start: Position{Line: 14}, End: Position{Line: 14, Character: 6}},
			expected: []Position{
				{Line: 14, Character: 3},
			},
		},
		{
			name:     "go symbol only present as substring yields no positions",
			langName: "golang",
			source:   goOccurrenceSource,
			symbol:   "runActivity",
			expected: nil,
		},
		{
			name:     "character offsets count utf-16 code units",
			langName: "golang",
			source:   "package main\n\nvar naïve = \"🚀\" // run\n\nfunc run() {\n\tprintln(naïve, run)\n}\n",
			symbol:   "naïve",
			expected: []Position{
				{Line: 2, Character: 8},
				{Line: 5, Character: 13},
			},
		},
		{
			name:     "python identifiers are matched syntactically",
			langName: "python",
			source:   "# run this\nrunner = \"run\"\n\ndef run():\n    pass\n",
			symbol:   "run",
			expected: []Position{
				{Line: 3, Character: 6},
			},
		},
		{
			name:     "typescript identifiers are matched syntactically",
			langName: "typescript",
			source:   "// run this\nconst runner = \"run\";\n\nfunction run() {}\n",
			symbol:   "run",
			expected: []Position{
				{Line: 3, Character: 11},
			},
		},
		{
			name:     "language without a grammar falls back to text search",
			langName: "unknown",
			source:   "run and rerun\n",
			symbol:   "run",
			expected: []Position{
				{Line: 0, Character: 2},
				{Line: 0, Character: 12},
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			positions := findSymbolOccurrencePositions([]byte(tc.source), tc.fileRange, tc.symbol, tc.langName)
			assert.Equal(t, tc.expected, positions)
		})
	}
}

func TestGetSingleFileDefinitionsOnlyResolvesWholeGoTokens(t *testing.T) {
	t.Parallel()

	testDir := t.TempDir()
	_, err := utils.WriteTestFile(t, testDir, "main.go", goOccurrenceSource)
	require.NoError(t, err)

	var requestedPositions []Position
	lspa := &LSPActivities{
		LSPClientProvider: func(language string) LSPClient {
			return MockLSPClient{
				TextDocumentDefinitionFunc: func(ctx context.Context, uri string, line, character int) ([]Location, error) {
					requestedPositions = append(requestedPositions, Position{Line: line, Character: character})
					return []Location{{
						URI:   "file://" + filepath.Join(testDir, "main.go"),
						Range: Range{Start: Position{Line: 9, Character: 5}, End: Position{Line: 9, Character: 8}},
					}}, nil
				},
			}
		},
		InitializedClients: map[string]LSPClient{},
	}

	envContainer := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: testDir}}
	definitions, err := lspa.GetSingleFileDefinitions(context.Background(), LSPDefinitionLocationsRequest{
		FilePath:     "main.go",
		EnvContainer: &envContainer,
		Symbols:      []string{"run"},
	})
	require.NoError(t, err)

	assert.Equal(t, []Position{
		{Line: 9, Character: 7},
		{Line: 14, Character: 3},
	}, requestedPositions)
	require.Len(t, definitions, 1)
	assert.Equal(t, "run", definitions[0].Symbol)
	assert.Empty(t, definitions[0].Error)
}
