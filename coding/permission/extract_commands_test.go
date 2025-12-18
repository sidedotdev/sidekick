package permission

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractCommands(t *testing.T) {
	tests := []struct {
		name     string
		script   string
		expected []string
	}{
		{
			name:     "simple single command",
			script:   "ls -la",
			expected: []string{"ls -la"},
		},
		{
			name:     "piped commands",
			script:   "cat file | grep foo",
			expected: []string{"cat file", "grep foo"},
		},
		{
			name:     "AND-chained commands",
			script:   "cmd1 && cmd2",
			expected: []string{"cmd1", "cmd2"},
		},
		{
			name:     "OR-chained commands",
			script:   "cmd1 || cmd2",
			expected: []string{"cmd1", "cmd2"},
		},
		{
			name:     "semicolon-separated commands",
			script:   "cmd1; cmd2",
			expected: []string{"cmd1", "cmd2"},
		},
		{
			name:     "subshell commands",
			script:   "(cd dir && make)",
			expected: []string{"cd dir", "make"},
		},
		{
			name:     "command substitution",
			script:   "echo $(ls -la)",
			expected: []string{"echo $(ls -la)", "ls -la"},
		},
		{
			name:     "backgrounded command",
			script:   "sleep 10 &",
			expected: []string{"sleep 10 &"},
		},
		{
			name:     "redirections",
			script:   "cat file > output.txt",
			expected: []string{"cat file > output.txt"},
		},
		{
			name:     "combined example",
			script:   "./x.sh && go build && go test -short | jq -x",
			expected: []string{"./x.sh", "go build", "go test -short", "jq -x"},
		},
		{
			name:     "shell -c command",
			script:   `sh -c "echo hello && ls"`,
			expected: []string{`sh -c "echo hello && ls"`, "echo hello", "ls"},
		},
		{
			name:     "bash -c command",
			script:   `bash -c "pwd"`,
			expected: []string{`bash -c "pwd"`, "pwd"},
		},
		{
			name:     "zsh -c command",
			script:   `zsh -c "whoami"`,
			expected: []string{`zsh -c "whoami"`, "whoami"},
		},
		{
			name:     "eval command",
			script:   `eval "rm -rf /"`,
			expected: []string{`eval "rm -rf /"`, "rm -rf /"},
		},
		{
			name:     "exec command",
			script:   "exec python script.py",
			expected: []string{"exec python script.py", "python script.py"},
		},
		{
			name:     "xargs simple",
			script:   "find . | xargs rm",
			expected: []string{"find .", "xargs rm", "rm"},
		},
		{
			name:     "xargs with -I flag",
			script:   "xargs -I {} cp {} /tmp",
			expected: []string{"xargs -I {} cp {} /tmp", "cp {} /tmp"},
		},
		{
			name:     "xargs with multiple flags",
			script:   "xargs -n 1 -P 4 echo",
			expected: []string{"xargs -n 1 -P 4 echo", "echo"},
		},
		{
			name:     "xargs with -0 flag",
			script:   "ls | xargs -0 wc -l",
			expected: []string{"ls", "xargs -0 wc -l", "wc -l"},
		},
		{
			name:     "empty script",
			script:   "",
			expected: nil,
		},
		{
			name:     "whitespace only",
			script:   "   ",
			expected: nil,
		},
		{
			name:     "nested command substitution",
			script:   "echo $(cat $(ls))",
			expected: []string{"echo $(cat $(ls))", "cat $(ls)", "ls"},
		},
		{
			name:     "backtick command substitution",
			script:   "echo `ls -la`",
			expected: []string{"echo `ls -la`", "ls -la"},
		},
		{
			name:     "input redirection",
			script:   "sort < input.txt",
			expected: []string{"sort < input.txt"},
		},
		{
			name:     "append redirection",
			script:   "echo hello >> log.txt",
			expected: []string{"echo hello >> log.txt"},
		},
		{
			name:     "stderr redirection",
			script:   "cmd 2>&1",
			expected: []string{"cmd 2>&1"},
		},
		{
			name:     "heredoc with redirection",
			script:   "cat <<EOF > output.txt\nhello world\nEOF",
			expected: []string{"cat <<EOF > output.txt\nhello world\nEOF"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractCommands(tt.script)
			assert.Equal(t, tt.expected, result)
		})
	}
}
