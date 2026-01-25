package tree_sitter

import (
	"os"
	"testing"

	"sidekick/utils"

	"github.com/stretchr/testify/assert"
)

func TestGetFileHeadersStringJavascript(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name      string
		code      string
		extension string
		expected  string
	}{
		{
			name:      "empty",
			code:      "",
			extension: ".js",
			expected:  "",
		},
		{
			name:      "no imports",
			code:      "const foo = 'bar';",
			extension: ".js",
			expected:  "",
		},
		{
			name:      "single import",
			code:      "import { foo } from 'bar';",
			extension: ".js",
			expected:  "import { foo } from 'bar';\n",
		},
		{
			name:      "single import with whitespace",
			code:      " import { foo } from 'bar';",
			extension: ".js",
			expected:  " import { foo } from 'bar';\n",
		},
		{
			name:      "multiple imports",
			code:      "import { foo, foo2 } from 'bar';\nimport { baz } from 'qux';",
			extension: ".js",
			expected:  "import { foo, foo2 } from 'bar';\nimport { baz } from 'qux';\n",
		},
		{
			name:      "import with alias",
			code:      "import { foo as f } from 'bar';",
			extension: ".js",
			expected:  "import { foo as f } from 'bar';\n",
		},
		{
			name:      "import with default",
			code:      "import foo from 'bar';",
			extension: ".js",
			expected:  "import foo from 'bar';\n",
		},
		{
			name:      "import with namespace",
			code:      "import * as foo from 'bar';",
			extension: ".js",
			expected:  "import * as foo from 'bar';\n",
		},
		{
			name:      "import with side effects",
			code:      "import 'bar';",
			extension: ".js",
			expected:  "import 'bar';\n",
		},
		{
			name:      "nested imports not captured",
			code:      "function x() {\n    import('bar');\n}",
			extension: ".js",
			expected:  "",
		},
		{
			name:      "jsx file with imports",
			code:      "import React from 'react';\nimport { useState } from 'react';",
			extension: ".jsx",
			expected:  "import React from 'react';\nimport { useState } from 'react';\n",
		},
		{
			name:      "jsx file with jsx content",
			code:      "import React from 'react';\n\nfunction App() {\n  return <div>Hello</div>;\n}",
			extension: ".jsx",
			expected:  "import React from 'react';\n",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmpfile, err := os.CreateTemp("", "test*"+tc.extension)
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
			if result != tc.expected {
				t.Errorf("GetFileHeadersString returned incorrect result. Expected:\n%s\nGot:\n%s", utils.PanicJSON(tc.expected), utils.PanicJSON(result))
			}
		})
	}
}
