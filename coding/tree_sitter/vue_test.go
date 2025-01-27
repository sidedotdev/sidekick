package tree_sitter

import (
	"os"
	"path/filepath"
	"sidekick/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetFileSymbolsStringVue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		code     string
		expected string
	}{
		{
			name: "filename is included",
			code: `
				<template>
					<button @click="sayHello">Click me</button>
				</template>

				<script>
				export default {
					methods: {
						sayHello() {
							console.log('Hello, world!');
						}
					}
				}
				</script>
			`,
			expected: "<template>, <script>, sayHello, placeholder_filename",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			tmpfile, err := os.CreateTemp("", "*.vue")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpfile.Name())
			sfcName := strings.ReplaceAll(filepath.Base(tmpfile.Name()), filepath.Ext(tmpfile.Name()), "")
			test.expected = strings.ReplaceAll(test.expected, "placeholder_filename", sfcName)

			if _, err := tmpfile.Write([]byte(test.code)); err != nil {
				t.Fatalf("Failed to write to temp file: %v", err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatalf("Failed to close temp file: %v", err)
			}

			symbolsString, err := GetFileSymbolsString(tmpfile.Name())
			if err != nil {
				t.Fatalf("Failed to get symbols: %v", err)
			}

			if symbolsString != test.expected {
				t.Errorf("Got %s, expected %s", symbolsString, test.expected)
			}
		})
	}
}

func TestGetAllAlternativeFileSymbolsVue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		code                string
		expectedSymbolNames []string
	}{
		{
			name: "filename is included",
			code: `
				<template>
					<button @click="sayHello">Click me</button>
				</template>

				<script>
				export default {
					methods: {
						sayHello() {
							console.log('Hello, world!');
						}
					}
				}
				</script>
			`,
			expectedSymbolNames: []string{"<template>", "<script>", "sayHello", "placeholder_filename"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Write the input to a temp file with a '.go' extension
			filePath, err := utils.WriteTestTempFile(t, "vue", tc.code)
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(filePath)

			for i, symbol := range tc.expectedSymbolNames {
				if symbol == "placeholder_filename" {
					tc.expectedSymbolNames[i] = strings.ReplaceAll(filepath.Base(filePath), filepath.Ext(filePath), "")
				}
			}

			// Call the function and check the output
			output, err := GetAllAlternativeFileSymbols(filePath)
			if err != nil {
				t.Fatalf("failed to get all alternative file symbols: %v", err)
			}
			outputStr := symbolToStringSlice(output)
			if !assert.ElementsMatch(t, outputStr, tc.expectedSymbolNames) {
				t.Errorf("Expected %s, but got %s", utils.PanicJSON(tc.expectedSymbolNames), utils.PanicJSON(outputStr))
			}
		})
	}
}

func TestGetSymbolDefinitionVue(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name               string
		symbolName         string
		code               string
		expectedDefinition string
		expectedError      string
	}{
		{
			name:          "empty code",
			symbolName:    "<template>",
			code:          "",
			expectedError: `symbol not found: <template>`,
		},
		{
			name:       "template definition",
			symbolName: "<template>",
			code: `<template>
  <div id="app">
    <h1>{{ message }}</h1>
  </div>
</template>

<script>
export default {
  data() {
    return {
      message: 'Hello Vue!'
    }
  }
}
</script>

<style scoped>
h1 {
  color: red;
}
</style>`,
			expectedDefinition: `<template>
  <div id="app">
    <h1>{{ message }}</h1>
  </div>
</template>`,
		},
		{
			name:       "script definition",
			symbolName: "<script>",
			code: `<template>
  <div id="app">
    <h1>{{ message }}</h1>
  </div>
</template>

<script>
export default {
  data() {
    return {
      message: 'Hello Vue!'
    }
  }
}
</script>

<style scoped>
h1 {
  color: red;
}
</style>`,
			expectedDefinition: `<script>
export default {
  data() {
    return {
      message: 'Hello Vue!'
    }
  }
}
</script>`,
		},
		{
			name:       "style definition",
			symbolName: "<style>",
			code: `<template>
  <div id="app">
    <h1>{{ message }}</h1>
  </div>
</template>

<script>
export default {
  data() {
    return {
      message: 'Hello Vue!'
    }
  }
}
</script>

<style scoped>
h1 {
  color: red;
}
</style>`,
			expectedDefinition: `<style scoped>
h1 {
  color: red;
}
</style>`,
		},
		{
			name:       "TypeScript function definition",
			symbolName: "myFunction",
			code: `<template>
  <div id="app">
    <h1>{{ message }}</h1>
  </div>
</template>

<script lang="ts">
function myFunction() {
  return 'Hello TypeScript!';
}
</script>

<style scoped>
h1 {
  color: red;
}
</style>`,
			expectedDefinition: `function myFunction() {
  return 'Hello TypeScript!';
}`,
		},
		{
			name:       "TypeScript variable declaration",
			symbolName: "myVariable",
			code: `<template>
  <div id="app">
    <h1>{{ message }}</h1>
  </div>
</template>

<script lang="ts">
let myVariable = 'Hello TypeScript!';
</script>

<style scoped>
h1 {
  color: red;
}
</style>`,
			expectedDefinition: `let myVariable = 'Hello TypeScript!';`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			filePath, err := utils.WriteTestTempFile(t, "vue", tc.code)
			if err != nil {
				t.Fatalf("Failed to write temp file: %v", err)
			}
			defer os.Remove(filePath)

			definition, err := GetSymbolDefinitionsString(filePath, tc.symbolName, 0)
			if err != nil {
				if tc.expectedError == "" {
					t.Fatalf("Unexpected error: %v", err)
				} else if !strings.Contains(err.Error(), tc.expectedError) {
					t.Fatalf("Expected error: %s, got: %v", tc.expectedError, err)
				}
			}

			if strings.TrimSuffix(definition, "\n") != strings.TrimSuffix(tc.expectedDefinition, "\n") {
				t.Errorf("Expected definition:\n%s\nGot:\n%s", utils.PanicJSON(tc.expectedDefinition), utils.PanicJSON(definition))
			}
		})
	}
}

func TestGetFileHeadersStringVue(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		code     string
		expected string
	}{
		{
			name:     "empty",
			code:     "",
			expected: "",
		},
		{
			name:     "no imports",
			code:     "<script lang=\"ts\">\nconst foo = 'bar';\n</script>",
			expected: "",
		},
		{
			name:     "single import",
			code:     "<script lang=\"ts\">\nimport { foo } from 'bar';\n</script>",
			expected: "import { foo } from 'bar';\n",
		},
		{
			name:     "single import with whitespace",
			code:     "<script lang=\"ts\">\n import { foo } from 'bar';\n</script>",
			expected: " import { foo } from 'bar';\n",
		},
		{
			name:     "multiple imports",
			code:     "<script lang=\"ts\">\nimport { foo, foo2 } from 'bar';\nimport { baz } from 'qux';\n</script>",
			expected: "import { foo, foo2 } from 'bar';\nimport { baz } from 'qux';\n",
		},
		{
			name:     "import with alias",
			code:     "<script lang=\"ts\">\nimport { foo as f } from 'bar';\n</script>",
			expected: "import { foo as f } from 'bar';\n",
		},
		{
			name:     "import with default",
			code:     "<script lang=\"ts\">\nimport foo from 'bar';\n</script>",
			expected: "import foo from 'bar';\n",
		},
		{
			name:     "import with namespace",
			code:     "<script lang=\"ts\">\nimport * as foo from 'bar';\n</script>",
			expected: "import * as foo from 'bar';\n",
		},
		{
			name:     "import with side effects",
			code:     "<script lang=\"ts\">\nimport 'bar';\n</script>",
			expected: "import 'bar';\n",
		},
		{
			name:     "import with type only",
			code:     "<script lang=\"ts\">\nimport type { foo } from 'bar';\n</script>",
			expected: "import type { foo } from 'bar';\n",
		},
		{
			name:     "import with type and side effects",
			code:     "<script lang=\"ts\">\nimport type 'bar';\n</script>",
			expected: "import type 'bar';\n",
		},
		{
			name:     "import with type and default",
			code:     "<script lang=\"ts\">\nimport type foo from 'bar';\n</script>",
			expected: "import type foo from 'bar';\n",
		},
		{
			name:     "import with type and namespace",
			code:     "<script lang=\"ts\">\nimport type * as foo from 'bar';\n</script>",
			expected: "import type * as foo from 'bar';\n",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Create a temporary file with the test case code
			tmpfile, err := os.CreateTemp("", "test*.vue")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpfile.Name())
			if _, err := tmpfile.Write([]byte(tc.code)); err != nil {
				t.Fatalf("Failed to write to temp file: %v", err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatalf("Failed to close temp file: %v", err)
			}
			result, err := GetFileHeadersString(tmpfile.Name(), 0)
			assert.Nil(t, err)
			// Check the result
			if result != tc.expected {
				t.Errorf("GetFileHeadersString returned incorrect result. Expected:\n%s\nGot:\n%s", utils.PanicJSON(tc.expected), utils.PanicJSON(result))
			}
		})
	}
}
