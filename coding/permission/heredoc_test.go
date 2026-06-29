package permission

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectHeredocFileWrites(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		script    string
		wantCount int
		wantDelim string
	}{
		{
			name:      "no heredoc",
			script:    "echo hello",
			wantCount: 0,
		},
		{
			name:      "heredoc without file redirect is fine",
			script:    "mysql << EOF\nSELECT 1;\nEOF",
			wantCount: 0,
		},
		{
			name:      "cat with file redirect before heredoc",
			script:    "cat > some/path << 'SOME_EOF'\nhello\nworld\nSOME_EOF",
			wantCount: 1,
			wantDelim: "SOME_EOF",
		},
		{
			name:      "cat with file redirect after heredoc",
			script:    "cat << EOF > out.txt\nhi\nEOF",
			wantCount: 1,
			wantDelim: "EOF",
		},
		{
			name:      "append redirect with heredoc",
			script:    "cat >> out.txt << EOF\nhi\nEOF",
			wantCount: 1,
			wantDelim: "EOF",
		},
		{
			name:      "double-quoted delimiter",
			script:    "cat > out.txt << \"EOF\"\nhi\nEOF",
			wantCount: 1,
			wantDelim: "EOF",
		},
		{
			name:      "escape hatch delimiter",
			script:    "cat > out.txt << 'ESCAPE_HATCH_EOF'\nhi\nESCAPE_HATCH_EOF",
			wantCount: 1,
			wantDelim: "ESCAPE_HATCH_EOF",
		},
		{
			name:      "heredoc with dash variant",
			script:    "cat > out.txt <<- EOF\n\thi\n\tEOF",
			wantCount: 1,
			wantDelim: "EOF",
		},
		{
			name:      "conflict markers in sed are not heredocs",
			script:    "sed -i.bak -E -e 's/^<<<<<<< CREATE_FILE$/MARK_CREATE/' -e 's/^<<<<<<< APPEND_TO_FILE$/MARK_APPEND/' -e 's/^>>>>>>> NEW_LINES$/MARK_NEWLINES/' edit_block_test.go && rm edit_block_test.go.bak",
			wantCount: 0,
		},
		{
			name:      "here-string is not a heredoc file write",
			script:    "cat > out.txt <<< word",
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			results := DetectHeredocFileWrites(tt.script)
			assert.Len(t, results, tt.wantCount)
			if tt.wantCount > 0 && len(results) > 0 {
				assert.Equal(t, tt.wantDelim, results[0].Delimiter)
			}
		})
	}
}

func TestHeredocFileWrite_UsesEscapeHatch(t *testing.T) {
	t.Parallel()
	assert.True(t, HeredocFileWrite{Delimiter: HeredocEscapeHatchDelimiter}.UsesEscapeHatch())
	assert.False(t, HeredocFileWrite{Delimiter: "EOF"}.UsesEscapeHatch())
	assert.False(t, HeredocFileWrite{}.UsesEscapeHatch())
}
