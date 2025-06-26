package tree_sitter

import (
	"os"
	"path/filepath"
	"reflect" // Used for NewTestSqliteStorage
	"sidekick/srv/sqlite"
	"sidekick/utils" // Used by TestSplitOutlineIntoChunks
	"testing"
)

func TestSplitOutlineIntoChunks(t *testing.T) {
	testCases := []struct {
		name           string
		input          string
		goodChunkSize  int
		maxChunkSize   int
		expectedOutput []string
	}{
		{
			name:           "Empty input",
			input:          "",
			goodChunkSize:  5,
			maxChunkSize:   10,
			expectedOutput: []string{},
		},
		{
			name:          "Simple case - no splitting needed",
			input:         "a\nb\nc",
			goodChunkSize: 5,
			maxChunkSize:  10,
			expectedOutput: []string{
				"a\nb\nc",
			},
		},
		{
			name:          "Split due to indentation change",
			input:         "a\nb\n  c\nd\ne\nf",
			goodChunkSize: 3,
			maxChunkSize:  20,
			expectedOutput: []string{
				"a\nb\n  c",
				"d\ne\nf",
			},
		},
		{
			name:          "Merge too-small chunks",
			input:         "a\n  b\nc\n  d",
			goodChunkSize: 15,
			maxChunkSize:  30,
			expectedOutput: []string{
				"a\n  b\nc\n  d",
			},
		},
		{
			name:          "Split at empty line due to exceeding maxChunkSize",
			input:         "a\nb\nc\nd\n\ne\nf\ng\nh",
			goodChunkSize: 4,
			maxChunkSize:  8,
			expectedOutput: []string{
				"a\nb\nc\nd",
				"e\nf\ng\nh",
			},
		},
		{
			name:          "Split at blank line due to exceeding maxChunkSize",
			input:         "a\nb\nc\nd\n \t\ne\nf\ng\nh",
			goodChunkSize: 8,
			maxChunkSize:  15,
			expectedOutput: []string{
				"a\nb\nc\nd\n \t",
				"e\nf\ng\nh",
			},
		},
		{
			name:          "Split anywhere due to exceeding maxChunkSize",
			input:         "a\nb\nc\nd\ne\nf\ng\nh",
			goodChunkSize: 6,
			maxChunkSize:  10,
			expectedOutput: []string{
				"a\nb\nc\nd",
				"e\nf\ng\nh",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := splitOutlineIntoChunks(tc.input, tc.goodChunkSize, tc.maxChunkSize)
			if !reflect.DeepEqual(result, tc.expectedOutput) {
				t.Errorf("Expected %s, but got %s", utils.PrettyJSON(tc.expectedOutput), utils.PrettyJSON(result))
			}
		})
	}
}

func TestCreateDirSignatureOutlines(t *testing.T) {
	storage := sqlite.NewTestSqliteStorage(t, t.Name())
	activities := &TreeSitterActivities{
		DatabaseAccessor: storage,
	}
	tempDir := t.TempDir()

	filePath := filepath.Join(tempDir, "test.go")
	fileContent := `package main

import "fmt"

type MyStruct struct {
	ID   int
	Name string
}

func (ms *MyStruct) String() string {
	return fmt.Sprintf("ID: %d, Name: %s", ms.ID, ms.Name)
}

var GlobalVar int = 100

const MyPI = 3.14159

func HelperFunction(a, b int) int {
	// This is a simple helper function with some comments
	// to add to its length and complexity for the outline.
	sum := a + b
	return sum
}

func AnotherFunctionWithLogic(data []string) (map[string]int, error) {
	// Process data and return something.
	// More lines to make the outline longer.
	result := make(map[string]int)
	for i, s := range data {
		result[s] = i
	}
	return result, nil
}
`
	err := os.WriteFile(filePath, []byte(fileContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// DefaultPreferredChunkChars is 3000. The generated outline from test.go is expected to be < 3000 chars.
	testCases := []struct {
		name              string
		maxCharacterLimit int
		minExpectedChunks int
		maxExpectedChunks int
	}{
		{
			name:              "Large maxCharacterLimit, outline fits in DefaultPreferredChunkChars",
			maxCharacterLimit: 10000, // goodLimit = min(3000, 10000) = 3000. Outline expected < 3000.
			minExpectedChunks: 1,
			maxExpectedChunks: 1,
		},
		{
			name:              "maxCharacterLimit smaller than DefaultPreferredChunkChars, outline fits in this maxCharacterLimit",
			maxCharacterLimit: 2000, // goodLimit = min(3000, 2000) = 2000. Outline expected < 2000.
			minExpectedChunks: 1,
			maxExpectedChunks: 1,
		},
		{
			name:              "maxCharacterLimit very small, forces multiple chunks",
			maxCharacterLimit: 150, // goodLimit = min(3000, 150) = 150. Outline expected > 150.
			minExpectedChunks: 2,   // Expecting the outline (likely a few hundred chars) to be split.
			maxExpectedChunks: -1,  // Max chunks can vary, so not strictly checking upper bound.
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			chunks, err := activities.CreateDirSignatureOutlines("test_workspace", tempDir, tc.maxCharacterLimit)
			if err != nil {
				t.Fatalf("CreateDirSignatureOutlines failed: %v", err)
			}

			if len(chunks) < tc.minExpectedChunks {
				firstChunkLen := 0
				if len(chunks) > 0 {
					firstChunkLen = len(chunks[0])
				}
				t.Errorf("Expected at least %d chunks, got %d. Outline len for first chunk (if any): %d", tc.minExpectedChunks, len(chunks), firstChunkLen)
			}
			if tc.maxExpectedChunks != -1 && len(chunks) > tc.maxExpectedChunks {
				t.Errorf("Expected at most %d chunks, got %d", tc.maxExpectedChunks, len(chunks))
			}
		})
	}
}
