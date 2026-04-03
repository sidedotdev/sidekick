package dev

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
