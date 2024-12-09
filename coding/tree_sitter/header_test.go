package tree_sitter

import (
	"os"
	"sidekick/utils"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetFileHeadersStringGolang(t *testing.T) {
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
			code:     "import \"fmt\"",
			expected: "import \"fmt\"\n",
		},
		{
			name:     "multiple imports",
			code:     "import (\n\t\"fmt\"\n\t\"os\"\n)",
			expected: "import (\n\t\"fmt\"\n\t\"os\"\n)\n",
		},
		{
			name:     "import with alias",
			code:     "import f \"fmt\"",
			expected: "import f \"fmt\"\n",
		},
		{
			name:     "import with dot",
			code:     "import . \"fmt\"",
			expected: "import . \"fmt\"\n",
		},
		{
			name:     "import with underscore",
			code:     "import _ \"fmt\"",
			expected: "import _ \"fmt\"\n",
		},
		{
			name:     "package declaration",
			code:     "package main",
			expected: "package main\n",
		},
		{
			name:     "package + import",
			code:     "package main\nimport \"fmt\"",
			expected: "package main\nimport \"fmt\"\n",
		},
		{
			name:     "package + empty line + import",
			code:     "package main\n\nimport \"fmt\"",
			expected: "package main\n\nimport \"fmt\"\n",
		},
		{
			name:     "package + multiple whitespace lines + import",
			code:     "package main\n\n\t\t\n  \n \t \t\nimport \"fmt\"",
			expected: "package main\n\n\t\t\n  \n \t \t\nimport \"fmt\"\n",
		},
		{
			name:     "package later in file",
			code:     "import \"fmt\"\npackage main",
			expected: "import \"fmt\"\npackage main\n",
		},
		{
			name:     "import later in file",
			code:     "package main\nfunc main() {}\nimport \"fmt\"",
			expected: "package main\n---\nimport \"fmt\"\n",
		},
		{
			name:     "package twice in file, top and later",
			code:     "package main\nfunc main() {}\npackage main",
			expected: "package main\n---\npackage main\n",
		},
		{
			name:     "import twice in file, top and later",
			code:     "import \"fmt\"\nfunc main() {}\nimport \"os\"",
			expected: "import \"fmt\"\n---\nimport \"os\"\n",
		},
		{
			name:     "package + import twice in file, top and later",
			code:     "package main\nimport \"fmt\"\nfunc main() {}\npackage main\nimport \"os\"",
			expected: "package main\nimport \"fmt\"\n---\npackage main\nimport \"os\"\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a temporary file with the test case code
			tmpfile, err := os.CreateTemp("", "test*.go")
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

func TestGetFileHeadersStringTypescript(t *testing.T) {
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
			code:     "const foo = 'bar';",
			expected: "",
		},
		{
			name:     "single import",
			code:     "import { foo } from 'bar';",
			expected: "import { foo } from 'bar';\n",
		},
		{
			name:     "single import with whitespace",
			code:     " import { foo } from 'bar';",
			expected: " import { foo } from 'bar';\n",
		},
		{
			name:     "multiple imports",
			code:     "import { foo, foo2 } from 'bar';\nimport { baz } from 'qux';",
			expected: "import { foo, foo2 } from 'bar';\nimport { baz } from 'qux';\n",
		},
		{
			name:     "import with alias",
			code:     "import { foo as f } from 'bar';",
			expected: "import { foo as f } from 'bar';\n",
		},
		{
			name:     "import with default",
			code:     "import foo from 'bar';",
			expected: "import foo from 'bar';\n",
		},
		{
			name:     "import with namespace",
			code:     "import * as foo from 'bar';",
			expected: "import * as foo from 'bar';\n",
		},
		{
			name:     "import with side effects",
			code:     "import 'bar';",
			expected: "import 'bar';\n",
		},
		{
			name:     "import with type only",
			code:     "import type { foo } from 'bar';",
			expected: "import type { foo } from 'bar';\n",
		},
		{
			name:     "import with type and side effects",
			code:     "import type 'bar';",
			expected: "import type 'bar';\n",
		},
		{
			name:     "import with type and default",
			code:     "import type foo from 'bar';",
			expected: "import type foo from 'bar';\n",
		},
		{
			name:     "import with type and namespace",
			code:     "import type * as foo from 'bar';",
			expected: "import type * as foo from 'bar';\n",
		},
		{
			name:     "nested imports",
			code:     "function x() {\n    import { foo } from 'bar';\n    import { baz } from 'qux';\n}",
			expected: "",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a temporary file with the test case code
			tmpfile, err := os.CreateTemp("", "test*.ts")
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

func TestGetFileHeadersStringVue(t *testing.T) {
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

func TestGetFileHeadersStringPython(t *testing.T) {
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
			code:     "print('Hello, world!')",
			expected: "",
		},
		{
			name:     "import with comments",
			code:     "import math  # Import the math module",
			expected: "import math  # Import the math module\n",
		},
		{
			name:     "import with multiple lines",
			code:     "import math\nimport os\nimport sys",
			expected: "import math\nimport os\nimport sys\n",
		},
		{
			name:     "import with leading and trailing whitespace",
			code:     "    import math  \n  import os  \n  import sys  ",
			expected: "    import math  \n  import os  \n  import sys  \n",
		},
		{
			name:     "import with from and comments",
			code:     "from math import sqrt  # Import the sqrt function",
			expected: "from math import sqrt  # Import the sqrt function\n",
		},
		{
			name:     "import with from and alias",
			code:     "from math import sqrt as s",
			expected: "from math import sqrt as s\n",
		},
		{
			name:     "import with multiple from and alias",
			code:     "from math import sqrt as s, pow as p",
			expected: "from math import sqrt as s, pow as p\n",
		},
		{
			name:     "nested imports",
			code:     "def x():\n    import math\n    import os\n    import sys",
			expected: "",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a temporary file with the test case code
			tmpfile, err := os.CreateTemp("", "test*.py")
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
