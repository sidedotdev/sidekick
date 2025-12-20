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
		// Privilege/user context wrappers
		{
			name:     "sudo simple command",
			script:   "sudo cmd",
			expected: []string{"sudo cmd", "cmd"},
		},
		{
			name:     "sudo with -u flag",
			script:   "sudo -u user cmd",
			expected: []string{"sudo -u user cmd", "cmd"},
		},
		{
			name:     "su -c command",
			script:   `su -c "cmd"`,
			expected: []string{`su -c "cmd"`, "cmd"},
		},
		{
			name:     "doas command",
			script:   "doas cmd",
			expected: []string{"doas cmd", "cmd"},
		},
		{
			name:     "runuser with -u flag",
			script:   "runuser -u user cmd",
			expected: []string{"runuser -u user cmd", "cmd"},
		},
		// Process/environment wrappers
		{
			name:     "env simple command",
			script:   "env cmd",
			expected: []string{"env cmd", "cmd"},
		},
		{
			name:     "env with VAR=value",
			script:   "env VAR=value cmd",
			expected: []string{"env VAR=value cmd", "cmd"},
		},
		{
			name:     "nohup command",
			script:   "nohup cmd",
			expected: []string{"nohup cmd", "cmd"},
		},
		{
			name:     "nice simple command",
			script:   "nice cmd",
			expected: []string{"nice cmd", "cmd"},
		},
		{
			name:     "nice with -n flag",
			script:   "nice -n 10 cmd",
			expected: []string{"nice -n 10 cmd", "cmd"},
		},
		{
			name:     "ionice with -c flag",
			script:   "ionice -c 2 cmd",
			expected: []string{"ionice -c 2 cmd", "cmd"},
		},
		{
			name:     "timeout with duration",
			script:   "timeout 5s cmd",
			expected: []string{"timeout 5s cmd", "cmd"},
		},
		{
			name:     "stdbuf with -oL flag",
			script:   "stdbuf -oL cmd",
			expected: []string{"stdbuf -oL cmd", "cmd"},
		},
		// Remote/parallel execution
		{
			name:     "ssh with host and command",
			script:   `ssh host "cmd"`,
			expected: []string{`ssh host "cmd"`, "cmd"},
		},
		{
			name:     "ssh with -p flag",
			script:   `ssh -p 22 host "cmd"`,
			expected: []string{`ssh -p 22 host "cmd"`, "cmd"},
		},
		{
			name:     "find with -exec",
			script:   `find . -exec cmd {} \;`,
			expected: []string{`find . -exec cmd {} \;`, "cmd {}"},
		},
		{
			name:     "find with -execdir and +",
			script:   "find . -execdir cmd {} +",
			expected: []string{"find . -execdir cmd {} +", "cmd {}"},
		},
		{
			name:     "fd with -x flag",
			script:   "fd . -x cmd",
			expected: []string{"fd . -x cmd", "cmd"},
		},
		{
			name:     "parallel command",
			script:   "parallel cmd",
			expected: []string{"parallel cmd", "cmd"},
		},
		// Shell builtins
		{
			name:     "command builtin",
			script:   "command cmd",
			expected: []string{"command cmd", "cmd"},
		},
		{
			name:     "builtin builtin",
			script:   "builtin cmd",
			expected: []string{"builtin cmd", "cmd"},
		},
		// Debugging/tracing
		{
			name:     "time command",
			script:   "time cmd",
			expected: []string{"time cmd", "cmd"},
		},
		{
			name:     "strace simple",
			script:   "strace cmd",
			expected: []string{"strace cmd", "cmd"},
		},
		{
			name:     "strace with -f flag",
			script:   "strace -f cmd",
			expected: []string{"strace -f cmd", "cmd"},
		},
		{
			name:     "ltrace command",
			script:   "ltrace cmd",
			expected: []string{"ltrace cmd", "cmd"},
		},
		// Locking/synchronization
		{
			name:     "flock with lockfile",
			script:   "flock /lockfile cmd",
			expected: []string{"flock /lockfile cmd", "cmd"},
		},
		{
			name:     "flock with -n flag",
			script:   "flock -n /lockfile cmd",
			expected: []string{"flock -n /lockfile cmd", "cmd"},
		},
		// Watching/repeating
		{
			name:     "watch simple",
			script:   "watch cmd",
			expected: []string{"watch cmd", "cmd"},
		},
		{
			name:     "watch with -n flag",
			script:   "watch -n 5 cmd",
			expected: []string{"watch -n 5 cmd", "cmd"},
		},
		{
			name:     "entr command",
			script:   "entr cmd",
			expected: []string{"entr cmd", "cmd"},
		},
		// Privilege/capability manipulation
		{
			name:     "setpriv with flag",
			script:   "setpriv --reuid=1000 cmd",
			expected: []string{"setpriv --reuid=1000 cmd", "cmd"},
		},
		{
			name:     "capsh with -- -c",
			script:   `capsh -- -c "cmd"`,
			expected: []string{`capsh -- -c "cmd"`, "cmd"},
		},
		{
			name:     "cgexec with -g flag",
			script:   "cgexec -g cpu:mygroup cmd",
			expected: []string{"cgexec -g cpu:mygroup cmd", "cmd"},
		},
		// Misc wrappers
		{
			name:     "systemd-run command",
			script:   "systemd-run cmd",
			expected: []string{"systemd-run cmd", "cmd"},
		},
		{
			name:     "dbus-run-session command",
			script:   "dbus-run-session cmd",
			expected: []string{"dbus-run-session cmd", "cmd"},
		},
		// Script file execution
		{
			name:     "bash script execution",
			script:   "bash script.sh",
			expected: []string{"bash script.sh", "./script.sh"},
		},
		{
			name:     "sh script execution",
			script:   "sh script.sh",
			expected: []string{"sh script.sh", "./script.sh"},
		},
		{
			name:     "source script",
			script:   "source script.sh",
			expected: []string{"source script.sh", "./script.sh"},
		},
		{
			name:     "dot source script",
			script:   ". script.sh",
			expected: []string{". script.sh", "./script.sh"},
		},
		// Command grouping
		{
			name:     "brace group",
			script:   "{ cmd1; cmd2; }",
			expected: []string{"cmd1", "cmd2"},
		},
		// Nested wrappers
		{
			name:     "nested sudo env",
			script:   "sudo env VAR=1 cmd",
			expected: []string{"sudo env VAR=1 cmd", "env VAR=1 cmd", "cmd"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractCommands(tt.script)
			assert.Equal(t, tt.expected, result)
		})
	}
}
