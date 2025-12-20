package common

import (
	"regexp"
	"sidekick/coding/permission"
	"strconv"
	"strings"
)

// PermissionResult represents the result of evaluating command permissions
type PermissionResult int

const (
	PermissionAutoApprove PermissionResult = iota
	PermissionRequireApproval
	PermissionDeny
)

// CommandPattern represents a pattern for matching commands
type CommandPattern struct {
	Pattern string `toml:"pattern" json:"pattern" koanf:"pattern"`
	Message string `toml:"message,omitempty" json:"message,omitempty" koanf:"message,omitempty"`
}

// CommandPermissionConfig defines permission rules for shell commands
type CommandPermissionConfig struct {
	AutoApprove          []CommandPattern `toml:"auto_approve" json:"autoApprove" koanf:"auto_approve"`
	RequireApproval      []CommandPattern `toml:"require_approval" json:"requireApproval" koanf:"require_approval"`
	Deny                 []CommandPattern `toml:"deny" json:"deny" koanf:"deny"`
	ResetAutoApprove     bool             `toml:"reset_auto_approve" json:"resetAutoApprove" koanf:"reset_auto_approve"`
	ResetRequireApproval bool             `toml:"reset_require_approval" json:"resetRequireApproval" koanf:"reset_require_approval"`
}

// BaseCommandPermissions returns the hardcoded base permission configuration
// with sensible defaults for safe and dangerous commands
func BaseCommandPermissions() CommandPermissionConfig {
	return CommandPermissionConfig{
		AutoApprove: []CommandPattern{
			// Basic read-only commands
			{Pattern: "ls"},
			{Pattern: "cat"},
			{Pattern: "echo"},
			{Pattern: "pwd"},
			{Pattern: "head"},
			{Pattern: "tail"},
			{Pattern: "wc"},
			{Pattern: "grep"},
			{Pattern: "find"},
			{Pattern: "which"},
			{Pattern: "date"},
			{Pattern: "whoami"},
			{Pattern: "hostname"},
			{Pattern: "uname"},
			{Pattern: "file"},
			{Pattern: "stat"},
			{Pattern: "du"},
			{Pattern: "df"},
			{Pattern: "tree"},
			{Pattern: "less"},
			{Pattern: "more"},
			{Pattern: "diff"},
			{Pattern: "sort"},
			{Pattern: "uniq"},
			{Pattern: "cut"},
			{Pattern: "awk"},
			{Pattern: "sed"},
			{Pattern: "tr"},
			{Pattern: "basename"},
			{Pattern: "dirname"},
			{Pattern: "realpath"},
			{Pattern: "readlink"},
			// Git read operations
			{Pattern: "git status"},
			{Pattern: "git log"},
			{Pattern: "git diff"},
			{Pattern: "git branch"},
			{Pattern: "git show"},
			{Pattern: "git remote"},
			{Pattern: "git tag"},
			{Pattern: "git describe"},
			{Pattern: "git rev-parse"},
			{Pattern: "git ls-files"},
			{Pattern: "git ls-tree"},
			{Pattern: "git cat-file"},
			{Pattern: "git blame"},
			{Pattern: "git shortlog"},
			{Pattern: "git stash list"},
			// Go commands
			{Pattern: "go test"},
			{Pattern: "go build"},
			{Pattern: "go fmt"},
			{Pattern: "go vet"},
			{Pattern: "go mod tidy"},
			{Pattern: "go mod download"},
			{Pattern: "go list"},
			{Pattern: "go version"},
			{Pattern: "go env"},
			{Pattern: "go doc"},
			{Pattern: "gofmt"},
			{Pattern: "golint"},
			{Pattern: "staticcheck"},
			// Node.js/npm commands
			{Pattern: "npm test"},
			{Pattern: "npm run lint"},
			{Pattern: "npm run build"},
			{Pattern: "npm run check"},
			{Pattern: "npm run format"},
			{Pattern: "npm list"},
			{Pattern: "npm outdated"},
			{Pattern: "npm version"},
			{Pattern: "npm audit"},
			// Yarn commands
			{Pattern: "yarn test"},
			{Pattern: "yarn lint"},
			{Pattern: "yarn build"},
			{Pattern: "yarn check"},
			{Pattern: "yarn list"},
			{Pattern: "yarn outdated"},
			{Pattern: "yarn audit"},
			// Bun commands
			{Pattern: "bun test"},
			{Pattern: "bun run lint"},
			{Pattern: "bun run build"},
			{Pattern: "bun run check"},
			{Pattern: "bun run format"},
			{Pattern: "bun pm ls"},
			// Python commands
			{Pattern: "pytest"},
			{Pattern: "python -m pytest"},
			{Pattern: "python3 -m pytest"},
			{Pattern: "pip list"},
			{Pattern: "pip show"},
			{Pattern: "pip check"},
			{Pattern: "pip freeze"},
			{Pattern: "pylint"},
			{Pattern: "flake8"},
			{Pattern: "mypy"},
			{Pattern: "black --check"},
			{Pattern: "isort --check"},
			{Pattern: "ruff check"},
			// Make commands
			{Pattern: "make test"},
			{Pattern: "make check"},
			{Pattern: "make lint"},
			{Pattern: "make build"},
			{Pattern: "make fmt"},
			// Rust commands
			{Pattern: "cargo test"},
			{Pattern: "cargo build"},
			{Pattern: "cargo check"},
			{Pattern: "cargo fmt"},
			{Pattern: "cargo clippy"},
			{Pattern: "rustfmt"},
			// Ruby commands
			{Pattern: "bundle exec rspec"},
			{Pattern: "rspec"},
			{Pattern: "rubocop"},
			{Pattern: "bundle list"},
			{Pattern: "bundle check"},
			// Java/Maven/Gradle commands
			{Pattern: "mvn test"},
			{Pattern: "mvn compile"},
			{Pattern: "mvn verify"},
			{Pattern: "gradle test"},
			{Pattern: "gradle build"},
			{Pattern: "gradle check"},
			// Other common tools
			{Pattern: "jq"},
			{Pattern: "yq"},
			{Pattern: "true"},
			{Pattern: "false"},
			{Pattern: "test"},
			{Pattern: "["},
		},
		RequireApproval: []CommandPattern{
			// Commands that can expose secrets
			{Pattern: "env"},
			{Pattern: "printenv"},
			// Network requests
			{Pattern: "curl"},
			{Pattern: "wget"},
			{Pattern: "http"},
			{Pattern: "https"},
			// Commands referencing secret files (regex patterns)
			{Pattern: `.*\.env`},
			{Pattern: `.*\.envrc`},
			// Dangerous awk patterns that can execute commands or access network
			{Pattern: `awk.*system\(`},
			{Pattern: `awk.*\| *getline`},
			{Pattern: `awk.*\|&`},
			{Pattern: `awk.*/inet/`},
			{Pattern: `awk.*print.*\|`},
			{Pattern: `awk.*printf.*\|`},
			// Home directory access (potential secret exfiltration)
			{Pattern: `.*~`},
			{Pattern: `.*\$HOME`},
			{Pattern: `.*\$\{HOME\}`},
			// Parent directory traversal (escaping repo context)
			{Pattern: `.*\.\./`},
			// Additional network commands
			{Pattern: "nc"},
			{Pattern: "netcat"},
			{Pattern: "ncat"},
			{Pattern: "socat"},
			{Pattern: "telnet"},
			{Pattern: "ftp"},
			{Pattern: "sftp"},
			{Pattern: "scp"},
			{Pattern: "rsync"},
			{Pattern: "ssh"},
			{Pattern: "ping"},
			{Pattern: "nslookup"},
			{Pattern: "dig"},
			{Pattern: "host"},
		},
		Deny: []CommandPattern{
			// Destructive file operations
			{Pattern: "rm -rf /", Message: "Recursive force delete of root directory is extremely dangerous"},
			{Pattern: "rm -rf ~", Message: "Recursive force delete of home directory is extremely dangerous"},
			{Pattern: "rm -rf /*", Message: "Recursive force delete of root contents is extremely dangerous"},
			{Pattern: "rm -rf ~/*", Message: "Recursive force delete of home contents is extremely dangerous"},
			{Pattern: "rm -fr /", Message: "Recursive force delete of root directory is extremely dangerous"},
			{Pattern: "rm -fr ~", Message: "Recursive force delete of home directory is extremely dangerous"},
			// Privilege escalation
			{Pattern: "sudo", Message: "sudo commands require manual execution for security"},
			{Pattern: "su ", Message: "su commands require manual execution for security"},
			{Pattern: "doas", Message: "doas commands require manual execution for security"},
			// Dangerous permission changes
			{Pattern: "chmod 777", Message: "Setting world-writable permissions is a security risk"},
			{Pattern: "chmod -R 777", Message: "Recursively setting world-writable permissions is a security risk"},
			// Disk/filesystem operations
			{Pattern: "mkfs", Message: "Filesystem creation commands are extremely dangerous"},
			{Pattern: "dd if=", Message: "dd can overwrite disks and cause data loss"},
			{Pattern: "fdisk", Message: "Disk partitioning commands are extremely dangerous"},
			{Pattern: "parted", Message: "Disk partitioning commands are extremely dangerous"},
			// Fork bomb
			{Pattern: ":(){:|:&};:", Message: "Fork bomb detected - this will crash the system"},
			// Heredoc file creation (should use edit blocks instead)
			{Pattern: "cat << EOF >", Message: "Use edit blocks with APPEND_TO_FILE instead, or DELETE_FILE and CREATE_FILE to replace a file if it has been read in full already"},
			{Pattern: "cat <<EOF >", Message: "Use edit blocks with APPEND_TO_FILE instead, or DELETE_FILE and CREATE_FILE to replace a file if it has been read in full already"},
			{Pattern: "cat <<-EOF >", Message: "Use edit blocks with APPEND_TO_FILE instead, or DELETE_FILE and CREATE_FILE to replace a file if it has been read in full already"},
			{Pattern: "cat <<'EOF' >", Message: "Use edit blocks with APPEND_TO_FILE instead, or DELETE_FILE and CREATE_FILE to replace a file if it has been read in full already"},
			{Pattern: `cat <<\"EOF\" >`, Message: "Use edit blocks with APPEND_TO_FILE instead, or DELETE_FILE and CREATE_FILE to replace a file if it has been read in full already"},
			// Network attacks
			{Pattern: ":(){ :|:& };:", Message: "Fork bomb detected - this will crash the system"},
			// History manipulation
			{Pattern: "history -c", Message: "Clearing shell history is suspicious"},
			{Pattern: "> ~/.bash_history", Message: "Clearing bash history is suspicious"},
			// Shutdown/reboot
			{Pattern: "shutdown", Message: "System shutdown requires manual execution"},
			{Pattern: "reboot", Message: "System reboot requires manual execution"},
			{Pattern: "poweroff", Message: "System poweroff requires manual execution"},
			{Pattern: "halt", Message: "System halt requires manual execution"},
			{Pattern: "init 0", Message: "System shutdown requires manual execution"},
			{Pattern: "init 6", Message: "System reboot requires manual execution"},
		},
	}
}

// MergeCommandPermissions merges multiple permission configs in order.
// Later configs append to earlier ones by default.
// If ResetAutoApprove is true, that config's AutoApprove replaces all previous.
// If ResetRequireApproval is true, that config's RequireApproval replaces all previous.
// Deny lists always accumulate (no reset option for safety).
func MergeCommandPermissions(configs ...CommandPermissionConfig) CommandPermissionConfig {
	var result CommandPermissionConfig

	for _, cfg := range configs {
		// Handle auto-approve: reset or append
		if cfg.ResetAutoApprove {
			result.AutoApprove = make([]CommandPattern, len(cfg.AutoApprove))
			copy(result.AutoApprove, cfg.AutoApprove)
		} else {
			result.AutoApprove = append(result.AutoApprove, cfg.AutoApprove...)
		}

		// Handle require-approval: reset or append
		if cfg.ResetRequireApproval {
			result.RequireApproval = make([]CommandPattern, len(cfg.RequireApproval))
			copy(result.RequireApproval, cfg.RequireApproval)
		} else {
			result.RequireApproval = append(result.RequireApproval, cfg.RequireApproval...)
		}

		// Deny always accumulates (no reset for safety)
		result.Deny = append(result.Deny, cfg.Deny...)
	}

	return result
}

// regexMetaChars contains characters that indicate a pattern should be treated as regex
const regexMetaChars = `\.*+?[](){}|^$`

// matchPattern attempts to match a pattern against a command.
// It first tries an exact prefix match. If that fails and the pattern contains
// regex metacharacters, it compiles the pattern as a regex (anchored at start).
// Returns whether it matched and any capture groups for message interpolation.
func matchPattern(pattern string, command string) (bool, []string) {
	// Try exact prefix match first
	if strings.HasPrefix(command, pattern) {
		// For prefix match, return the matched portion as $0
		return true, []string{pattern}
	}

	// Check if pattern contains regex metacharacters
	if !strings.ContainsAny(pattern, regexMetaChars) {
		return false, nil
	}

	// Anchor pattern at start if not already anchored
	regexPattern := pattern
	if !strings.HasPrefix(regexPattern, "^") {
		regexPattern = "^" + regexPattern
	}

	re, err := regexp.Compile(regexPattern)
	if err != nil {
		return false, nil
	}

	matches := re.FindStringSubmatch(command)
	if matches == nil {
		return false, nil
	}

	return true, matches
}

// interpolateMessage replaces $0, $1, $2, etc. in the message with capture groups.
// $0 is the full match, $1 is the first capture group, etc.
func interpolateMessage(message string, matches []string) string {
	if len(matches) == 0 {
		return message
	}

	result := message
	for i := len(matches) - 1; i >= 0; i-- {
		placeholder := "$" + strconv.Itoa(i)
		result = strings.ReplaceAll(result, placeholder, matches[i])
	}
	return result
}

// EvaluateCommandPermission evaluates a single command against the permission config.
// It checks deny patterns first, then require-approval, then auto-approve.
// Returns the permission result and any associated message.
func EvaluateCommandPermission(config CommandPermissionConfig, command string) (PermissionResult, string) {
	// Check deny patterns first
	for _, p := range config.Deny {
		if matched, matches := matchPattern(p.Pattern, command); matched {
			msg := p.Message
			if msg != "" && len(matches) > 0 {
				msg = interpolateMessage(msg, matches)
			}
			return PermissionDeny, msg
		}
	}

	// Check require-approval patterns
	for _, p := range config.RequireApproval {
		if matched, _ := matchPattern(p.Pattern, command); matched {
			return PermissionRequireApproval, ""
		}
	}

	// Check auto-approve patterns
	for _, p := range config.AutoApprove {
		if matched, _ := matchPattern(p.Pattern, command); matched {
			return PermissionAutoApprove, ""
		}
	}

	// Default to require approval
	return PermissionRequireApproval, ""
}

// EvaluateScriptPermission evaluates a shell script by extracting all commands
// and checking each against the permission config.
// Returns PermissionDeny if ANY command is denied.
// Returns PermissionRequireApproval if ANY command requires approval (and none denied).
// Returns PermissionAutoApprove only if ALL commands are auto-approved.
func EvaluateScriptPermission(config CommandPermissionConfig, script string) (PermissionResult, string) {
	commands := permission.ExtractCommands(script)

	// If no commands extracted, default to require approval
	if len(commands) == 0 {
		return PermissionRequireApproval, ""
	}

	hasRequireApproval := false

	for _, cmd := range commands {
		result, msg := EvaluateCommandPermission(config, cmd)
		switch result {
		case PermissionDeny:
			return PermissionDeny, msg
		case PermissionRequireApproval:
			hasRequireApproval = true
		}
	}

	if hasRequireApproval {
		return PermissionRequireApproval, ""
	}

	return PermissionAutoApprove, ""
}
