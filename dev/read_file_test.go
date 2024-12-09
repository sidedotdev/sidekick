package dev

import (
	"fmt"
	"os"
	"path/filepath"
	"sidekick/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadFileActivity(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name             string
		fileContent      string
		relativeFilePath string
		params           ReadFileActivityInput
		want             string
		wantErr          bool
	}{
		{
			name:        "LineNumber less than 1",
			fileContent: "line1\nline2\nline3",
			params:      ReadFileActivityInput{LineNumber: 0, WindowSize: 1},
			wantErr:     true,
		},
		{
			name:        "WindowSize less than 0",
			fileContent: "line1\nline2\nline3",
			params:      ReadFileActivityInput{LineNumber: 1, WindowSize: -1},
			wantErr:     true,
		},
		{
			name:        "Line number exceeds total number of lines",
			fileContent: "line1\nline2\nline3",
			params:      ReadFileActivityInput{LineNumber: 4, WindowSize: 0},
			wantErr:     true,
		},
		{
			name:        "lines found in window but not on the line",
			fileContent: "line1\nline2\nline3",
			params:      ReadFileActivityInput{LineNumber: 4, WindowSize: 2},
			want:        "File: placeholder\nLines: 2-3\n```\nline2\nline3\n```",
		},
		{
			name:        "Successful read",
			fileContent: "line1\nline2\nline3\nline4\nline5",
			params:      ReadFileActivityInput{LineNumber: 3, WindowSize: 1},
			want:        "File: placeholder\nLines: 2-4\n```\nline2\nline3\nline4\n```",
		},
		{
			name:        "Successful read - first line",
			fileContent: "line1\nline2\nline3\nline4\nline5",
			params:      ReadFileActivityInput{LineNumber: 1, WindowSize: 1},
			want:        "File: placeholder\nLines: 1-2\n```\nline1\nline2\n```",
		},
		{
			name:        "Successful read - last line",
			fileContent: "line1\nline2\nline3\nline4\nline5",
			params:      ReadFileActivityInput{LineNumber: 5, WindowSize: 1},
			want:        "File: placeholder\nLines: 4-5\n```\nline4\nline5\n```",
		},
		{
			name:        "Successful read - no window size",
			fileContent: "line1\nline2\nline3\nline4\nline5",
			params:      ReadFileActivityInput{LineNumber: 3, WindowSize: 0},
			want:        "File: placeholder\nLines: 3-3\n```\nline3\n```",
		},
		{
			name:        "Successful read - larger window size",
			fileContent: "line1\nline2\nline3\nline4\nline5\nline6",
			params:      ReadFileActivityInput{LineNumber: 3, WindowSize: 2},
			want:        "File: placeholder\nLines: 1-5\n```\nline1\nline2\nline3\nline4\nline5\n```",
		},
		{
			name:        "Successful read - oversized window size",
			fileContent: "line1\nline2\nline3\nline4\nline5",
			params:      ReadFileActivityInput{LineNumber: 3, WindowSize: 100},
			want:        "File: placeholder\nLines: 1-5\n```\nline1\nline2\nline3\nline4\nline5\n```",
		},
		{
			name:             "Read file from subdirectory",
			fileContent:      "line1\nline2\nline3",
			relativeFilePath: "subdir/nested.txt",
			params:           ReadFileActivityInput{LineNumber: 2, WindowSize: 1},
			want:             "File: subdir/nested.txt\nLines: 1-3\n```\nline1\nline2\nline3\n```",
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			relativeFilePath := tt.relativeFilePath
			if relativeFilePath == "" {
				relativeFilePath = fmt.Sprintf("test%d.txt", i)
			}
			tt.params.FilePath = relativeFilePath
			absoluteFilePath := filepath.Join(tempDir, relativeFilePath)
			err := os.MkdirAll(filepath.Dir(absoluteFilePath), 0755)
			require.NoError(t, err)

			err = os.WriteFile(absoluteFilePath, []byte(tt.fileContent), 0644)
			require.NoError(t, err)

			got, err := ReadFileActivity(tempDir, tt.params)
			if (err != nil) != tt.wantErr {
				t.Errorf("ReadFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			tt.want = strings.ReplaceAll(tt.want, "placeholder", tt.params.FilePath)
			if got != tt.want {
				t.Errorf("ReadFile() = %v, want %v", got, tt.want)
				t.Errorf("\n Got:\n%s\nWant: \n%s", utils.PanicJSON(got), utils.PanicJSON(tt.want))
			}
		})
	}
}

func TestReadFileActivity_NonExistentFile(t *testing.T) {
	tempDir := t.TempDir()

	params := ReadFileActivityInput{
		FilePath:   "nonexistent.txt",
		LineNumber: 1,
		WindowSize: 1,
	}

	_, err := ReadFileActivity(tempDir, params)
	if err == nil || !strings.Contains(err.Error(), "no file exists at the given file path: nonexistent.txt") {
		t.Errorf("Expected error for non-existent file, got %v", err)
	}
}
