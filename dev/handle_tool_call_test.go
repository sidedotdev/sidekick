package dev

import "testing"

func TestRemoveWorkingDirFromPaths(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		workingDir string
		expected   string
	}{
		{
			name:       "simple path replacement",
			input:      "/home/user/project/src/main.go",
			workingDir: "/home/user/project",
			expected:   "src/main.go",
		},
		{
			name:       "multiple paths in string",
			input:      "Error in /home/user/project/src/main.go and /home/user/project/src/util.go",
			workingDir: "/home/user/project",
			expected:   "Error in src/main.go and src/util.go",
		},
		{
			name:       "working dir alone - no replacement",
			input:      "The directory is /home/user/project and it exists",
			workingDir: "/home/user/project",
			expected:   "The directory is /home/user/project and it exists",
		},
		{
			name:       "working dir with trailing slash and space - no replacement",
			input:      "The directory is /home/user/project/ and it exists",
			workingDir: "/home/user/project",
			expected:   "The directory is /home/user/project/ and it exists",
		},
		{
			name:       "working dir followed by double quote - no replacement",
			input:      `path is "/home/user/project/"`,
			workingDir: "/home/user/project",
			expected:   `path is "/home/user/project/"`,
		},
		{
			name:       "working dir followed by single quote - no replacement",
			input:      `path is '/home/user/project/'`,
			workingDir: "/home/user/project",
			expected:   `path is '/home/user/project/'`,
		},
		{
			name:       "path inside quotes should be replaced",
			input:      `Error: file "/home/user/project/src/main.go" not found`,
			workingDir: "/home/user/project",
			expected:   `Error: file "src/main.go" not found`,
		},
		{
			name:       "empty working dir",
			input:      "/home/user/project/src/main.go",
			workingDir: "",
			expected:   "/home/user/project/src/main.go",
		},
		{
			name:       "working dir with trailing slash in input",
			input:      "/home/user/project/src/main.go",
			workingDir: "/home/user/project/",
			expected:   "src/main.go",
		},
		{
			name:       "no match",
			input:      "/other/path/file.go",
			workingDir: "/home/user/project",
			expected:   "/other/path/file.go",
		},
		{
			name:       "path at end of string",
			input:      "File: /home/user/project/main.go",
			workingDir: "/home/user/project",
			expected:   "File: main.go",
		},
		{
			name:       "nested path",
			input:      "/home/user/project/src/pkg/deep/file.go",
			workingDir: "/home/user/project",
			expected:   "src/pkg/deep/file.go",
		},
		{
			name:       "path with special regex chars in working dir",
			input:      "/home/user/project.name/src/main.go",
			workingDir: "/home/user/project.name",
			expected:   "src/main.go",
		},
		{
			name:       "multiline with paths",
			input:      "Error at /home/user/project/a.go:10\nWarning at /home/user/project/b.go:20",
			workingDir: "/home/user/project",
			expected:   "Error at a.go:10\nWarning at b.go:20",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := removeWorkingDirFromPaths(tt.input, tt.workingDir)
			if result != tt.expected {
				t.Errorf("removeWorkingDirFromPaths(%q, %q) = %q, want %q",
					tt.input, tt.workingDir, result, tt.expected)
			}
		})
	}
}
