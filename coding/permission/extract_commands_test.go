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
			name:     "lima shell command",
			script:   "lima python script.py",
			expected: []string{"lima python script.py", "python script.py"},
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
			name:     "command substitution with pipe",
			script:   "cat $(echo /etc | head -1)/passwd",
			expected: []string{"cat $(echo /etc | head -1)/passwd", "echo /etc", "head -1"},
		},
		{
			name:     "command substitution with path suffix",
			script:   "cat $(go env GOPATH)/pkg/mod/file.go",
			expected: []string{"cat $(go env GOPATH)/pkg/mod/file.go", "go env GOPATH"},
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
			name:     "sudo with -g flag",
			script:   "sudo -g group cmd",
			expected: []string{"sudo -g group cmd", "cmd"},
		},
		{
			name:     "sudo with -C flag",
			script:   "sudo -C 3 cmd",
			expected: []string{"sudo -C 3 cmd", "cmd"},
		},
		{
			name:     "sudo with -p flag",
			script:   "sudo -p 'Password:' cmd",
			expected: []string{"sudo -p 'Password:' cmd", "cmd"},
		},
		{
			name:     "sudo with multiple flags",
			script:   "sudo -u root -g wheel cmd",
			expected: []string{"sudo -u root -g wheel cmd", "cmd"},
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
			name:     "nice with --adjustment flag",
			script:   "nice --adjustment=10 cmd",
			expected: []string{"nice --adjustment=10 cmd", "cmd"},
		},
		{
			name:     "ionice with -c flag",
			script:   "ionice -c 2 cmd",
			expected: []string{"ionice -c 2 cmd", "cmd"},
		},
		{
			name:     "ionice with -c and -n flags",
			script:   "ionice -c 2 -n 7 cmd",
			expected: []string{"ionice -c 2 -n 7 cmd", "cmd"},
		},
		{
			name:     "ionice with -t flag",
			script:   "ionice -t cmd",
			expected: []string{"ionice -t cmd", "cmd"},
		},
		{
			name:     "ionice with -c -n and -t flags",
			script:   "ionice -c 2 -n 7 -t cmd",
			expected: []string{"ionice -c 2 -n 7 -t cmd", "cmd"},
		},
		{
			name:     "timeout with duration",
			script:   "timeout 5s cmd",
			expected: []string{"timeout 5s cmd", "cmd"},
		},
		{
			name:     "timeout with -k flag",
			script:   "timeout -k 5s 10s cmd",
			expected: []string{"timeout -k 5s 10s cmd", "cmd"},
		},
		{
			name:     "timeout with -s flag",
			script:   "timeout -s KILL 5s cmd",
			expected: []string{"timeout -s KILL 5s cmd", "cmd"},
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
			name:     "ssh with -i flag",
			script:   `ssh -i ~/.ssh/key host "cmd"`,
			expected: []string{`ssh -i ~/.ssh/key host "cmd"`, "cmd"},
		},
		{
			name:     "ssh with multiple flags",
			script:   `ssh -p 22 -i ~/.ssh/key host "cmd"`,
			expected: []string{`ssh -p 22 -i ~/.ssh/key host "cmd"`, "cmd"},
		},
		{
			name:     "ssh with nested sudo command",
			script:   `ssh host 'sudo dangerous_cmd'`,
			expected: []string{`ssh host 'sudo dangerous_cmd'`, "sudo dangerous_cmd", "dangerous_cmd"},
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
			name:     "find with -ok",
			script:   `find . -ok echo {} \;`,
			expected: []string{`find . -ok echo {} \;`, "echo {}"},
		},
		{
			name:     "find with -okdir",
			script:   `find . -okdir printf "%s\n" {} \;`,
			expected: []string{`find . -okdir printf "%s\n" {} \;`, `printf "%s\n" {}`},
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
			name:     "strace with -p flag",
			script:   "strace -p 1234 cmd",
			expected: []string{"strace -p 1234 cmd", "cmd"},
		},
		{
			name:     "strace with -o flag",
			script:   "strace -o output.log cmd",
			expected: []string{"strace -o output.log cmd", "cmd"},
		},
		{
			name:     "strace with -e flag",
			script:   "strace -e trace=open cmd",
			expected: []string{"strace -e trace=open cmd", "cmd"},
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
		{
			name:     "flock with -w flag",
			script:   "flock -w 10 /lockfile cmd",
			expected: []string{"flock -w 10 /lockfile cmd", "cmd"},
		},
		{
			name:     "flock with -c flag",
			script:   `flock /lockfile -c "cmd1; cmd2"`,
			expected: []string{`flock /lockfile -c "cmd1; cmd2"`, "cmd1", "cmd2"},
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
			name:     "watch with -d flag",
			script:   "watch -d cmd",
			expected: []string{"watch -d cmd", "cmd"},
		},
		{
			name:     "watch with -n and -d flags",
			script:   "watch -n 5 -d cmd",
			expected: []string{"watch -n 5 -d cmd", "cmd"},
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
			name:     "setpriv with separate flag value",
			script:   "setpriv --reuid 1000 cmd",
			expected: []string{"setpriv --reuid 1000 cmd", "cmd"},
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
		{
			name:     "cgexec with --sticky flag",
			script:   "cgexec --sticky -g cpu:mygroup cmd",
			expected: []string{"cgexec --sticky -g cpu:mygroup cmd", "cmd"},
		},
		{
			name:     "cgexec with multiple -g flags",
			script:   "cgexec -g cpu:mygroup -g memory:mygroup cmd",
			expected: []string{"cgexec -g cpu:mygroup -g memory:mygroup cmd", "cmd"},
		},
		// Misc wrappers
		{
			name:     "systemd-run command",
			script:   "systemd-run cmd",
			expected: []string{"systemd-run cmd", "cmd"},
		},
		{
			name:     "systemd-run with -u flag",
			script:   "systemd-run -u myunit cmd",
			expected: []string{"systemd-run -u myunit cmd", "cmd"},
		},
		{
			name:     "systemd-run with -p flag",
			script:   "systemd-run -p CPUQuota=50% cmd",
			expected: []string{"systemd-run -p CPUQuota=50% cmd", "cmd"},
		},
		{
			name:     "systemd-run with -t flag",
			script:   "systemd-run -t cmd",
			expected: []string{"systemd-run -t cmd", "cmd"},
		},
		{
			name:     "systemd-run with --pty flag",
			script:   "systemd-run --pty cmd",
			expected: []string{"systemd-run --pty cmd", "cmd"},
		},
		{
			name:     "systemd-run with -t and -u flags",
			script:   "systemd-run -t -u myunit cmd",
			expected: []string{"systemd-run -t -u myunit cmd", "cmd"},
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
		// Chained commands with go run - redirection on compound list is stripped
		// since we extract individual commands for permission checking
		{
			name:     "echo and go run chained",
			script:   `echo "=== Original workflow ===" && go run ./worker/replay -id flow_123 2>&1; echo "Exit code: $?"`,
			expected: []string{`echo "=== Original workflow ==="`, `go run ./worker/replay -id flow_123`, `echo "Exit code: $?"`},
		},
		// Chained commands with redirection on compound list
		{
			name:     "simple chained with redirect",
			script:   `echo hello && ls 2>&1`,
			expected: []string{`echo hello`, `ls`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractCommands(tt.script)
			assert.Equal(t, tt.expected, result)
		})
	}
}
