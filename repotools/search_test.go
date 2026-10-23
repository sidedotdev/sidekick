package repotools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHasLiteralPathSegment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern string
		want    bool
	}{
		{"", false},
		{"*", false},
		{"**", false},
		{"**/*", false},
		{"**/*.go", false},
		{"*.py", false},
		{"?", false},
		{"[abc]", false},
		{"{a,b}", false},
		{"mocks/client.go", true},
		{"node_modules/**/*", true},
		{"**/something_specific/*.ext", true},
		{".hidden/**", true},
		{"src/components/**/*.vue", true},
		{"mocks/*.go", true},
		{"vendor/lib.go", true},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			t.Parallel()
			got := hasLiteralPathSegment(tt.pattern)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTargetUnignorePatterns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		glob string
		want []string
	}{
		{"client.go", []string{"!client.go"}},
		{"mocks/client.go", []string{"!mocks/", "!mocks/client.go"}},
		{"mocks/*.go", []string{"!mocks/", "!mocks/*.go"}},
		{"lib/sub/**/*.go", []string{"!lib/", "!lib/sub/", "!lib/sub/**/", "!lib/sub/**/*.go"}},
		{"lib*/generated.go", []string{"!lib*/", "!lib*/generated.go"}},
		{".side/tmp/*.go", []string{"!.side/", "!.side/tmp/", "!.side/tmp/*.go"}},
		// leading pure wildcards are not negated on their own
		{"**/mocks/*.go", []string{"!**/mocks/", "!**/mocks/*.go"}},
		{"*/mocks/*.go", []string{"!*/mocks/", "!*/mocks/*.go"}},
		{"**/something_specific/*.ext", []string{"!**/something_specific/", "!**/something_specific/*.ext"}},
	}
	for _, tc := range tests {
		t.Run(tc.glob, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, targetUnignorePatterns(tc.glob))
		})
	}
}

func TestHiddenIgnoredDirs(t *testing.T) {
	t.Parallel()

	entries := []string{".git/", ".side/", ".side/tmp/", "build/", "build/sub/", "libext/", "lib/cache/", "t.ignored", "lib/mocks/gen.go"}
	tests := []struct {
		glob string
		want []string
	}{
		{"*/test_file.txt", []string{"build", "libext"}},
		{"*/mocks/*.go", []string{"build", "libext"}},
		{"**/mocks/*.go", []string{"build", "libext", "lib/cache"}},
		{"lib/*/gen.go", []string{"lib/cache"}},
		{"other/*.go", nil},
		{"*.go", nil},
	}
	for _, tc := range tests {
		t.Run(tc.glob, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, hiddenIgnoredDirs(entries, tc.glob))
		})
	}
}

func TestSplitIgnoredEntriesListing(t *testing.T) {
	t.Parallel()

	entries, rest := splitIgnoredEntriesListing("build/\nt.ignored\n" + ignoredEntriesSeparator + "\nlib/a.go\nlib/b.go\n")
	assert.Equal(t, []string{"build/", "t.ignored"}, entries)
	assert.Equal(t, "lib/a.go\nlib/b.go\n", rest)

	entries, rest = splitIgnoredEntriesListing(ignoredEntriesSeparator + "\n")
	assert.Nil(t, entries)
	assert.Equal(t, "", rest)

	entries, rest = splitIgnoredEntriesListing("lib/a.go\n")
	assert.Nil(t, entries)
	assert.Equal(t, "lib/a.go\n", rest)
}

func TestStartsWithWildcardDir(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"*/test_file.txt": true,
		"**/mocks/*.go":   true,
		"*.go":            false,
		"client.go":       false,
		"lib/*.go":        false,
		"lib*/gen.go":     false,
		"lib/**/*.go":     false,
	}
	for glob, want := range tests {
		t.Run(glob, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, want, startsWithWildcardDir(glob))
		})
	}
}
