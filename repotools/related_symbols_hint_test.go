package repotools

import (
	"path/filepath"
	"strings"
	"testing"

	"sidekick/coding/lsp/core"
	"sidekick/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newGoplsSessions runs a real gopls so the hint reflects actual references.
func newGoplsSessions(t *testing.T) *core.Sessions {
	sessions := &core.Sessions{
		LSPClientProvider:  core.NewLocalServerProvider(nil),
		InitializedClients: make(map[string]core.LSPClient),
	}
	t.Cleanup(func() { require.NoError(t, sessions.Close()) })
	return sessions
}

func TestE2EBulkGetSymbolDefinitionsWithRelatedSymbols(t *testing.T) {
	t.Parallel()

	testDir := t.TempDir()
	file1, err := utils.WriteTestFile(t, testDir, "file1.go", `package main

// G3 referenced thrice, G2 twice, G1 once, G0 zero times
func G4() {}
func G3() {
	G4()
}
func G2() {
	G4()
}
func G1() {
	G2()
	G3()
	G4()
}
func G0(s string, n int) {
	G2()
	G1()
	G4()
	G4() // call twice
}
var x = G3()
const Y = G3()

// Referenced by file2.go
func H1() {}
func H2() {}
// Referenced by file2.go and file3.go
func H3() {}

// X2 is feferenced here and by file2.go
func X2() {}
func X0() {
	X2()
}

// S2 referenced twice, S1 once, S0 zero times
type S0 struct {
	abc S1
}
type S2 struct {}
type S1 int
func (s S2) M_a() {}
func (s S2) M_b() {}

func FooBar(f Foo){}
func (f Foo) FooBaz(){}
// Foo is a struct and this comment is a distractor
type Foo struct {}
`)
	assert.Nil(t, err)

	_, err = utils.WriteTestFile(t, testDir, "file2.go", `package main

func File2H0() {
	H1()
	H2()
}
func File2H0_b() {
	H2()
	H3()
}

func File2X0() {
	X2()
}
`)
	assert.Nil(t, err)

	_, err = utils.WriteTestFile(t, testDir, "file3.go", `package main

func File3H0() {
	H3()
}
`)
	assert.Nil(t, err)

	tool := &SymbolDefinitions{
		Repository: testRepository(testDir),
		Sessions:   newGoplsSessions(t),
		Scope:      core.ClientScope{BaseDir: testDir},
		// Reduced thresholds so a small fixture exercises every branch.
		HintLimits: &RelatedSymbolsHintLimits{
			MaxSameFileRelatedSymbols:   3,
			MaxOtherFilesRelatedSymbols: 2,
			MaxOtherFiles:               1,
			MaxSameFileSignatureLines:   2,
			MaxOtherFileSignatureLines:  1,
		},
	}

	testCases := []struct {
		name           string
		filename       string
		symbol         string
		referenceLines []string
		expectedOutput string
	}{
		{
			name:           "Few same-file calls: show signatures",
			filename:       file1,
			symbol:         "G2",
			referenceLines: []string{"\tG2()", "= G2()"},
			expectedOutput: `
G2 is referenced in the same file by:
	func G1()
	func G0(s string, n int)`,
		},
		{
			name:           "More same-file calls: show symbols",
			filename:       file1,
			symbol:         "G3",
			referenceLines: []string{"\tG3()", "= G3()"},
			expectedOutput: `
G3 is referenced in the same file by: G1, x, Y`,
		},
		{
			name:           "Even more same-file calls: show counts",
			filename:       file1,
			symbol:         "G4",
			referenceLines: []string{"\tG4()", "= G4()"},
			expectedOutput: `
G4 is referenced in the same file by 4 other symbols 5 times`,
		},
		{
			name:           "Struct: show method signature",
			filename:       file1,
			symbol:         "S2",
			referenceLines: []string{"\tS2", "func (s S2)"},
			expectedOutput: `
S2 is referenced in the same file by:
	func (s S2) M_a()
	func (s S2) M_b()`,
		},
		{
			name:           "Few calls in other files: show signature",
			filename:       file1,
			symbol:         "H1",
			referenceLines: []string{"\tH1"},
			expectedOutput: `
H1 is referenced in other files:
	file2.go:
		func File2H0()`,
		},
		{
			name:           "More calls in other files: show symbols",
			filename:       file1,
			symbol:         "H2",
			referenceLines: []string{"\tH2"},
			expectedOutput: `
H2 is referenced in other files:
	file2.go: File2H0, File2H0_b`,
		},
		{
			name:           "Too many other files: show stats",
			filename:       file1,
			symbol:         "H3",
			referenceLines: []string{"\tH3"},
			expectedOutput: `
H3 is referenced in 2 other files. Total referencing symbols: 2. Total references: 2`,
		},
		{
			name:           "Both few lines: show signatures for both",
			filename:       file1,
			symbol:         "X2",
			referenceLines: []string{"\tX2"},
			expectedOutput: `
X2 is referenced in the same file by:
	func X0()
X2 is referenced in other files:
	file2.go:
		func File2X0()`,
		},
		{
			name:     "Name overlap",
			filename: file1,
			symbol:   "Foo",
			referenceLines: []string{
				"func FooBar(f Foo){}",
				"func (f Foo) FooBaz(){}",
			},
			expectedOutput: `
Foo is referenced in the same file by:
	func FooBar(f Foo)
	func (f Foo) FooBaz()`,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := []FileSymDefRequest{
				{FilePath: filepath.Base(tc.filename), Symbols: []RequestedSymbol{{Name: tc.symbol}}},
			}

			numContextLines := 0
			result, err := tool.BulkGetSymbolDefinitions(t.Context(), DirectorySymDefRequest{
				Requests:              input,
				NumContextLines:       &numContextLines,
				IncludeRelatedSymbols: true,
			})
			assert.Nil(t, err)
			if !strings.Contains(result.SymbolDefinitions, tc.expectedOutput) {
				t.Errorf("Expected to contain:\n%s\nInstead, got:\n%s", tc.expectedOutput, result.SymbolDefinitions)
			}
		})
	}
}
