package tree_sitter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAllSymbolDefinitionsFromSource_Golang(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name            string
		code            string
		expectedSymbols []string
		expectedRanges  [][2]int // [startLine, endLine] pairs (0-indexed)
	}{
		{
			name: "single function",
			code: `package main

func myFunc() {
	println("hello")
}
`,
			expectedSymbols: []string{"myFunc"},
			expectedRanges:  [][2]int{{2, 4}},
		},
		{
			name: "multiple functions",
			code: `package main

func firstFunc() {
	println("first")
}

func secondFunc() {
	println("second")
}
`,
			expectedSymbols: []string{"firstFunc", "secondFunc"},
			expectedRanges:  [][2]int{{2, 4}, {6, 8}},
		},
		{
			name: "function with comment",
			code: `package main

// myFunc does something.
func myFunc() {
	println("hello")
}
`,
			expectedSymbols: []string{"myFunc"},
			expectedRanges:  [][2]int{{3, 5}}, // comment not included when not adjacent
		},
		{
			name: "struct definition",
			code: `package main

type MyStruct struct {
	Name string
	Age  int
}
`,
			expectedSymbols: []string{"MyStruct"},
			expectedRanges:  [][2]int{{2, 5}},
		},
		{
			name: "const and var",
			code: `package main

const MyConst = "test"
var MyVar = 42
`,
			expectedSymbols: []string{"MyConst", "MyVar"},
			expectedRanges:  [][2]int{{2, 2}, {3, 3}},
		},
		{
			name:            "empty code",
			code:            "",
			expectedSymbols: nil,
			expectedRanges:  nil,
		},
		{
			name: "interface definition",
			code: `package main

type MyInterface interface {
	DoSomething()
}
`,
			// Interface methods are also captured as separate symbols
			expectedSymbols: []string{"MyInterface", "DoSomething"},
			expectedRanges:  [][2]int{{2, 4}, {2, 4}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			defs, err := GetAllSymbolDefinitionsFromSource("golang", []byte(tc.code))
			require.NoError(t, err)

			if tc.expectedSymbols == nil {
				assert.Empty(t, defs)
				return
			}

			require.Len(t, defs, len(tc.expectedSymbols), "number of symbols mismatch")

			for i, def := range defs {
				assert.Equal(t, tc.expectedSymbols[i], def.SymbolName, "symbol name mismatch at index %d", i)
				assert.Equal(t, uint(tc.expectedRanges[i][0]), def.Range.StartPoint.Row, "start line mismatch for %s", tc.expectedSymbols[i])
				assert.Equal(t, uint(tc.expectedRanges[i][1]), def.Range.EndPoint.Row, "end line mismatch for %s", tc.expectedSymbols[i])
			}
		})
	}
}

func TestGetAllSymbolDefinitionsFromSource_Python(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name            string
		code            string
		expectedSymbols []string
		expectedRanges  [][2]int
	}{
		{
			name: "single function",
			code: `def my_func():
    print("hello")
`,
			expectedSymbols: []string{"my_func"},
			expectedRanges:  [][2]int{{0, 1}},
		},
		{
			name: "class definition",
			code: `class MyClass:
    def __init__(self):
        self.value = 0

    def get_value(self):
        return self.value
`,
			expectedSymbols: []string{"MyClass", "__init__", "get_value"},
			expectedRanges:  [][2]int{{0, 5}, {1, 2}, {4, 5}},
		},
		{
			name: "import aliases excluded from all symbols",
			code: `from x.agents.some_agent import AGENT_ID as DEFAULT_AGENT_ID

def my_func():
    print("hello")
`,
			expectedSymbols: []string{"my_func"},
			expectedRanges:  [][2]int{{2, 3}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			defs, err := GetAllSymbolDefinitionsFromSource("python", []byte(tc.code))
			require.NoError(t, err)

			require.Len(t, defs, len(tc.expectedSymbols), "number of symbols mismatch")

			for i, def := range defs {
				assert.Equal(t, tc.expectedSymbols[i], def.SymbolName, "symbol name mismatch at index %d", i)
				assert.Equal(t, uint(tc.expectedRanges[i][0]), def.Range.StartPoint.Row, "start line mismatch for %s", tc.expectedSymbols[i])
				assert.Equal(t, uint(tc.expectedRanges[i][1]), def.Range.EndPoint.Row, "end line mismatch for %s", tc.expectedSymbols[i])
			}
		})
	}
}

func TestGetAllSymbolDefinitionsFromSource_TypeScript(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name            string
		code            string
		expectedSymbols []string
		expectedRanges  [][2]int
	}{
		{
			name: "single function",
			code: `function myFunc() {
    console.log("hello");
}
`,
			expectedSymbols: []string{"myFunc"},
			expectedRanges:  [][2]int{{0, 2}},
		},
		{
			name: "arrow function const",
			code: `const myFunc = () => {
    console.log("hello");
};
`,
			expectedSymbols: []string{"myFunc"},
			expectedRanges:  [][2]int{{0, 2}},
		},
		{
			name: "class with methods",
			code: `class MyClass {
    constructor() {
        this.value = 0;
    }

    getValue() {
        return this.value;
    }
}
`,
			expectedSymbols: []string{"MyClass", "constructor", "getValue"},
			expectedRanges:  [][2]int{{0, 8}, {1, 3}, {5, 7}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			defs, err := GetAllSymbolDefinitionsFromSource("typescript", []byte(tc.code))
			require.NoError(t, err)

			require.Len(t, defs, len(tc.expectedSymbols), "number of symbols mismatch")

			for i, def := range defs {
				assert.Equal(t, tc.expectedSymbols[i], def.SymbolName, "symbol name mismatch at index %d", i)
				assert.Equal(t, uint(tc.expectedRanges[i][0]), def.Range.StartPoint.Row, "start line mismatch for %s", tc.expectedSymbols[i])
				assert.Equal(t, uint(tc.expectedRanges[i][1]), def.Range.EndPoint.Row, "end line mismatch for %s", tc.expectedSymbols[i])
			}
		})
	}
}
