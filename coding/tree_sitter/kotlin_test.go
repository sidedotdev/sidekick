package tree_sitter

import (
	"os"
	"sidekick/utils"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetFileHeadersStringKotlin(t *testing.T) {
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
			name:     "single import",
			code:     "import java.util.Scanner",
			expected: "import java.util.Scanner\n",
		},
		{
			name:     "multiple imports",
			code:     "import java.util.Scanner\nimport kotlin.collections.List",
			expected: "import java.util.Scanner\nimport kotlin.collections.List\n",
		},
		{
			name:     "wildcard import",
			code:     "import kotlin.collections.*",
			expected: "import kotlin.collections.*\n",
		},
		{
			name:     "package declaration",
			code:     "package com.example.app",
			expected: "package com.example.app\n",
		},
		{
			name:     "simple file annotation",
			code:     "@file:JvmMultifileClass",
			expected: "@file:JvmMultifileClass\n",
		},
		{
			name:     "file annotation with arguments",
			code:     "@file:JvmName(\"BuildersKt\")",
			expected: "@file:JvmName(\"BuildersKt\")\n",
		},
		{
			name:     "multiple file annotations",
			code:     "@file:JvmMultifileClass\n@file:JvmName(\"BuildersKt\")",
			expected: "@file:JvmMultifileClass\n@file:JvmName(\"BuildersKt\")\n",
		},
		{
			name:     "package + import",
			code:     "package com.example.app\nimport kotlin.collections.List",
			expected: "package com.example.app\nimport kotlin.collections.List\n",
		},
		{
			name:     "file annotation + package + import",
			code:     "@file:JvmMultifileClass\npackage com.example.app\nimport kotlin.collections.List",
			expected: "@file:JvmMultifileClass\npackage com.example.app\nimport kotlin.collections.List\n",
		},
		{
			name:     "package + empty line + import",
			code:     "package com.example.app\n\nimport kotlin.collections.List",
			expected: "package com.example.app\n\nimport kotlin.collections.List\n",
		},
		{
			name:     "package + multiple whitespace lines + import",
			code:     "package com.example.app\n\n\t\t\n  \n \t \t\nimport kotlin.collections.List",
			expected: "package com.example.app\n\n\t\t\n  \n \t \t\nimport kotlin.collections.List\n",
		},
		{
			name:     "package later in file",
			code:     "import kotlin.collections.List\npackage com.example.app",
			expected: "import kotlin.collections.List\n",
		},
		{
			name:     "import later in file",
			code:     "package com.example.app\nclass Main {}\nimport kotlin.collections.List",
			expected: "package com.example.app\n",
		},
		{
			name:     "file annotation later in file",
			code:     "package com.example.app\nclass Main {}\n@file:JvmName(\"BuildersKt\")",
			expected: "package com.example.app\n",
		},
		{
			name:     "package twice in file",
			code:     "package com.example.app\nclass Main {}\npackage com.other.app",
			expected: "package com.example.app\n",
		},
		{
			name:     "import twice in file",
			code:     "import kotlin.collections.List\nclass Main {}\nimport kotlin.collections.Set",
			expected: "import kotlin.collections.List\n",
		},
		{
			name:     "complex_combination",
			code:     "@file:JvmMultifileClass\n@file:JvmName(\"BuildersKt\")\npackage com.example.app\n\nimport kotlin.collections.List\nimport kotlin.collections.*\nclass Main {}\n@file:Suppress(\"unused\")\npackage com.other.app\nimport kotlin.collections.Set",
			expected: "@file:JvmMultifileClass\n@file:JvmName(\"BuildersKt\")\npackage com.example.app\n\nimport kotlin.collections.List\nimport kotlin.collections.*\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Create a temporary file with the test case code
			tmpfile, err := os.CreateTemp("", "test*.kt")
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
