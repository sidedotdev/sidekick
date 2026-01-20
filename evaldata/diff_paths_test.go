package evaldata

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseDiffPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		diff     string
		expected []string
	}{
		{
			name:     "empty diff",
			diff:     "",
			expected: nil,
		},
		{
			name: "single file edit",
			diff: `diff --git a/foo/bar.go b/foo/bar.go
index 1234567..abcdefg 100644
--- a/foo/bar.go
+++ b/foo/bar.go
@@ -1,3 +1,4 @@
 package foo
+// new comment
 func Bar() {}`,
			expected: []string{"foo/bar.go"},
		},
		{
			name: "multiple file edits",
			diff: `diff --git a/pkg/a.go b/pkg/a.go
index 1234567..abcdefg 100644
--- a/pkg/a.go
+++ b/pkg/a.go
@@ -1 +1 @@
-old
+new
diff --git a/pkg/b.go b/pkg/b.go
index 1234567..abcdefg 100644
--- a/pkg/b.go
+++ b/pkg/b.go
@@ -1 +1 @@
-old
+new`,
			expected: []string{"pkg/a.go", "pkg/b.go"},
		},
		{
			name: "new file",
			diff: `diff --git a/new_file.go b/new_file.go
new file mode 100644
index 0000000..1234567
--- /dev/null
+++ b/new_file.go
@@ -0,0 +1,3 @@
+package main
+
+func main() {}`,
			expected: []string{"new_file.go"},
		},
		{
			name: "deleted file",
			diff: `diff --git a/old_file.go b/old_file.go
deleted file mode 100644
index 1234567..0000000
--- a/old_file.go
+++ /dev/null
@@ -1,3 +0,0 @@
-package main
-
-func main() {}`,
			expected: []string{"old_file.go"},
		},
		{
			name: "renamed file",
			diff: `diff --git a/old_name.go b/new_name.go
similarity index 100%
rename from old_name.go
rename to new_name.go`,
			expected: []string{"old_name.go", "new_name.go"},
		},
		{
			name: "renamed file with changes",
			diff: `diff --git a/pkg/old.go b/pkg/new.go
similarity index 80%
rename from pkg/old.go
rename to pkg/new.go
index 1234567..abcdefg 100644
--- a/pkg/old.go
+++ b/pkg/new.go
@@ -1 +1 @@
-old content
+new content`,
			expected: []string{"pkg/old.go", "pkg/new.go"},
		},
		{
			name: "mixed operations",
			diff: `diff --git a/existing.go b/existing.go
index 1234567..abcdefg 100644
--- a/existing.go
+++ b/existing.go
@@ -1 +1 @@
-old
+new
diff --git a/brand_new.go b/brand_new.go
new file mode 100644
index 0000000..1234567
--- /dev/null
+++ b/brand_new.go
@@ -0,0 +1 @@
+new file
diff --git a/to_delete.go b/to_delete.go
deleted file mode 100644
index 1234567..0000000
--- a/to_delete.go
+++ /dev/null
@@ -1 +0,0 @@
-deleted`,
			expected: []string{"existing.go", "brand_new.go", "to_delete.go"},
		},
		{
			name: "deduplicates paths",
			diff: `diff --git a/same.go b/same.go
index 1234567..abcdefg 100644
--- a/same.go
+++ b/same.go
@@ -1 +1 @@
-old
+new
diff --git a/same.go b/same.go
index abcdefg..1234567 100644
--- a/same.go
+++ b/same.go
@@ -1 +1 @@
-new
+newer`,
			expected: []string{"same.go"},
		},
		{
			name: "handles paths with spaces",
			diff: `diff --git a/path with spaces/file.go b/path with spaces/file.go
index 1234567..abcdefg 100644
--- a/path with spaces/file.go
+++ b/path with spaces/file.go
@@ -1 +1 @@
-old
+new`,
			expected: []string{"path with spaces/file.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ParseDiffPaths(tt.diff)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseBulkSearchResultPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		result   string
		expected []string
	}{
		{
			name:     "empty result",
			result:   "",
			expected: nil,
		},
		{
			name: "single file match",
			result: `Searched for "foo" in "**/*.go"
pkg/bar.go:10:func foo() {
pkg/bar.go:11:    return
pkg/bar.go:12:}`,
			expected: []string{"pkg/bar.go"},
		},
		{
			name: "multiple file matches",
			result: `Searched for "test" in "**/*.go"
pkg/a.go:5:func test() {}
pkg/b.go:10:// test comment
pkg/c.go:1:package test`,
			expected: []string{"pkg/a.go", "pkg/b.go", "pkg/c.go"},
		},
		{
			name: "deduplicates files",
			result: `pkg/foo.go:1:line1
pkg/foo.go:2:line2
pkg/foo.go:3:line3`,
			expected: []string{"pkg/foo.go"},
		},
		{
			name: "handles context lines with dash separator",
			result: `pkg/foo.go:10:matched line
pkg/foo.go-11-context line
pkg/foo.go-12-another context`,
			expected: []string{"pkg/foo.go"},
		},
		{
			name:     "no matches message",
			result:   "No results found. Try a less restrictive search query.",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ParseBulkSearchResultPaths(tt.result)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestContainsDiff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "no diff",
			input:    "just some regular text",
			expected: false,
		},
		{
			name:     "contains diff header",
			input:    "some text\ndiff --git a/foo.go b/foo.go\nmore text",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ContainsDiff(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"foo/bar.go", "foo/bar.go"},
		{"./foo/bar.go", "foo/bar.go"},
		{"/dev/null", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			result := normalizePath(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseToolResultPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		result   string
		expected []string
	}{
		{
			name:     "empty result",
			result:   "",
			expected: nil,
		},
		{
			name:     "File header format",
			result:   "File: foo/bar.go\nLines: 1-10\n```go\ncode here\n```",
			expected: []string{"foo/bar.go"},
		},
		{
			name:     "multiple File headers",
			result:   "File: foo/bar.go\nLines: 1-10\n```go\ncode\n```\n\nFile: pkg/util.go\nLines: 5-15\n```go\nmore code\n```",
			expected: []string{"foo/bar.go", "pkg/util.go"},
		},
		{
			name:     "ripgrep format",
			result:   "Searched for \"test\" in \"*.go\"\nfoo/bar.go:10:func Test()\npkg/util.go:20:var x = 1",
			expected: []string{"foo/bar.go", "pkg/util.go"},
		},
		{
			name:     "mixed formats",
			result:   "File: foo/bar.go\nLines: 1-10\n```go\ncode\n```\n\npkg/util.go:20:var x = 1",
			expected: []string{"foo/bar.go", "pkg/util.go"},
		},
		{
			name:     "deduplicates paths",
			result:   "File: foo/bar.go\nLines: 1-10\n\nfoo/bar.go:20:more code",
			expected: []string{"foo/bar.go"},
		},
		{
			name:     "handles context lines with dash separator",
			result:   "foo/bar.go:10-context line\nfoo/bar.go:11:match line\nfoo/bar.go:12-context line",
			expected: []string{"foo/bar.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ParseToolResultPaths(tt.result)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("ParseToolResultPaths() = %v, want %v", result, tt.expected)
			}
		})
	}
}
