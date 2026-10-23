package repotools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilterFilesByGlob(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		files   []string
		pattern string
		want    []string
		wantErr bool
	}{
		{
			name:    "repository relative path",
			files:   []string{"dev/main.go", "other/main.go", "main.go"},
			pattern: "dev/*.go",
			want:    []string{"dev/main.go"},
		},
		{
			name:    "basename fallback preserves order",
			files:   []string{"other_test.go", "dev/main.go", "frontend/main.ts"},
			pattern: "*.go",
			want:    []string{"other_test.go", "dev/main.go"},
		},
		{
			name:    "recursive pattern includes root",
			files:   []string{"main.go", "dev/nested/other.go", "main.ts"},
			pattern: "**/*.go",
			want:    []string{"main.go", "dev/nested/other.go"},
		},
		{
			name:    "spaces and quotes",
			files:   []string{"dir with spaces/file's.go", "other.go"},
			pattern: "file's.go",
			want:    []string{"dir with spaces/file's.go"},
		},
		{
			name:    "empty paths are skipped",
			files:   []string{"", "main.go", ""},
			pattern: "*",
			want:    []string{"main.go"},
		},
		{
			name:    "duplicates are preserved",
			files:   []string{"main.go", "main.go"},
			pattern: "*.go",
			want:    []string{"main.go", "main.go"},
		},
		{
			name:    "no matches",
			files:   []string{"main.go"},
			pattern: "*.ts",
		},
		{
			name:    "empty input",
			pattern: "*.go",
		},
		{
			name:    "malformed pattern",
			files:   []string{"main.go"},
			pattern: "[",
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := FilterFilesByGlob(test.files, test.pattern)
			if test.wantErr {
				require.ErrorContains(t, err, "invalid glob pattern")
				require.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}
