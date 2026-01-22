package common

import (
	"context"
	"regexp"
	"sidekick/coding/permission"
	"strconv"
	"strings"
)

// PermissionResult represents the result of evaluating command permissions
type PermissionResult string

const (
	PermissionAutoApprove     PermissionResult = "auto_approve"
	PermissionRequireApproval PermissionResult = "require_approval"
	PermissionDeny            PermissionResult = "deny"
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

// BaseCommandPermissionsActivity is a Temporal activity wrapper for BaseCommandPermissions
func BaseCommandPermissionsActivity(ctx context.Context) (CommandPermissionConfig, error) {
	return BaseCommandPermissions(), nil
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
			{Pattern: "cd"},
			{Pattern: "head"},
			{Pattern: "tail"},
			{Pattern: "tee"},
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
			{Pattern: "go clean"},
			{Pattern: "go fmt"},
			{Pattern: "go generate"},
			{Pattern: "go vet"},
			{Pattern: "go mod tidy"},
			{Pattern: "go mod download"},
			{Pattern: "go mod why"},
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
			{Pattern: "npm run test"},
			{Pattern: "npm run build"},
			{Pattern: "npm run check"},
			{Pattern: "npm run format"},
			{Pattern: "npm list"},
			{Pattern: "npm outdated"},
			{Pattern: "npm version"},
			{Pattern: "npm audit"},
			{Pattern: "npm update"},
			// Yarn commands
			{Pattern: "yarn test"},
			{Pattern: "yarn lint"},
			{Pattern: "yarn build"},
			{Pattern: "yarn check"},
			{Pattern: "yarn list"},
			{Pattern: "yarn outdated"},
			{Pattern: "yarn audit"},
			// Bun commands
			{Pattern: "bun info"},
			{Pattern: "bun update"},
			{Pattern: "bun audit"},
			{Pattern: "bun outdated"},
			{Pattern: "bun init"},
			{Pattern: "bun create"},
			{Pattern: "bun test"},
			{Pattern: "bun run test"},
			{Pattern: "bun lint"},
			{Pattern: "bun run lint"},
			{Pattern: "bun build"},
			{Pattern: "bun run build"},
			{Pattern: "bun check"},
			{Pattern: "bun run check"},
			{Pattern: "bun format"},
			{Pattern: "bun run format"},
			{Pattern: "bun list"},
			{Pattern: "bun pm list"},
			{Pattern: "bun pm ls"},
			{Pattern: "bun pm scan"},
			{Pattern: "bun pm pack"},
			{Pattern: "bun pm why"},
			{Pattern: "bun why"},
			{Pattern: "bun pm view"},
			{Pattern: "bun pm version"},
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
			{Pattern: "mkdir"},
			{Pattern: "lima"},
			{Pattern: "jq"},
			{Pattern: "yq"},
			{Pattern: "true"},
			{Pattern: "false"},
			{Pattern: "test"},
			{Pattern: "xxd"},
			{Pattern: "od"},
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
			// Match ~ when NOT preceded by alphanumeric (excludes git refs like HEAD~1)
			{Pattern: `.*(^|[^a-zA-Z0-9])~($|/| )`},
			{Pattern: `.*(^|[^a-zA-Z0-9])~[a-zA-Z]`},
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
			// Shell redirection to network endpoints
			{Pattern: `^.*/dev/(tcp|udp)/`, Message: "Shell redirection to TCP/UDP endpoints enables network exfiltration via otherwise safe commands and FD operations (e.g., echo, exec)."},
			// GNU sed command execution (s///e flag)
			{Pattern: `^sed\b.*s.*/e\b`, Message: "GNU sed substitution with execute flag runs shell commands, enabling exfiltration or side effects."},
			// GNU sed e command (standalone, with optional address like 1e or $e)
			{Pattern: `^sed\b.*'[0-9$]*e[[:space:]]`, Message: "GNU sed `e` command executes shell commands for addressed lines, enabling exfiltration or side effects."},
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
			// Unnecessary cd to home directory paths
			{Pattern: `cd /home/`, Message: "cd not needed, the command will already be run in the correct working directory"},
			{Pattern: `cd /Users/`, Message: "cd not needed, the command will already be run in the correct working directory"},
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

// envVarPrefixRegex matches environment variable assignments at the start of a command
// e.g., "VAR=value", "FOO_BAR=123", etc.
var envVarPrefixRegex = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*=[^\s]*\s+)+`)

// stripEnvVarPrefix removes leading environment variable assignments from a command.
// For example, "FOO=bar BAZ=qux go test" becomes "go test".
func stripEnvVarPrefix(command string) string {
	return envVarPrefixRegex.ReplaceAllString(command, "")
}

// envVarRefRegex matches actual environment variable references like $VAR, ${VAR},
// but not regex anchors like $ at end of line. Also matches escaped versions
// like \$HOME or \$\{HOME\} that appear in regex patterns.
var envVarRefRegex = regexp.MustCompile(`\\?\$[A-Za-z_]|\\?\$\\?\{`)

// envVarAssignRegex matches environment variable assignments like VAR=value at the
// start of a pattern (possibly after regex anchor ^)
var envVarAssignRegex = regexp.MustCompile(`^(\^|\\^)?[A-Za-z_][A-Za-z0-9_]*=`)

// patternContainsEnvVar returns true if the pattern contains env var references
// (like $HOME, ${HOME}) or env var assignments (like FOO=bar at the start).
func patternContainsEnvVar(pattern string) bool {
	return envVarRefRegex.MatchString(pattern) || envVarAssignRegex.MatchString(pattern)
}

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

// containsAbsolutePath checks if a command contains an absolute path argument.
// Returns true if any argument contains an absolute path (excluding common safe paths).
func containsAbsolutePath(command string) bool {
	parts := parseCommandForPaths(command)

	// Detect if this is a sed or perl regex command context
	regexArgIndices := detectRegexArguments(parts)

	for i, part := range parts {
		// Skip parts that are known regex arguments in sed/perl context
		if regexArgIndices[i] {
			continue
		}
		if containsAbsolutePathInPart(part) {
			return true
		}
	}
	return false
}

// detectRegexArguments identifies which command parts are regex arguments
// in known regex-based commands (sed, perl -pe/-pie). Returns a map of
// part indices that should be treated as regex patterns, not paths.
func detectRegexArguments(parts []string) map[int]bool {
	result := make(map[int]bool)
	if len(parts) == 0 {
		return result
	}

	cmd := parts[0]

	// Handle sed commands: sed [options] 'pattern' or sed [options] '/pattern/d'
	if cmd == "sed" {
		for i := 1; i < len(parts); i++ {
			part := parts[i]
			// Skip flags like -i, -n, -e, etc.
			if strings.HasPrefix(part, "-") {
				continue
			}
			// The first non-flag argument is the sed expression
			if isSedExpression(part) {
				result[i] = true
				break
			}
		}
	}

	// Handle perl -pe or -pi -e commands
	if cmd == "perl" {
		for i := 1; i < len(parts); i++ {
			part := parts[i]
			// Look for -e flag followed by expression, or -pe/-pie combined flags
			if part == "-e" && i+1 < len(parts) {
				if isPerlExpression(parts[i+1]) {
					result[i+1] = true
				}
			} else if (part == "-pe" || part == "-pie" || part == "-pi") && i+1 < len(parts) {
				// -pe and -pie: expression follows immediately
				// -pi: followed by -e, then expression
				if part == "-pe" || part == "-pie" {
					if isPerlExpression(parts[i+1]) {
						result[i+1] = true
					}
				} else if part == "-pi" && parts[i+1] == "-e" && i+2 < len(parts) {
					if isPerlExpression(parts[i+2]) {
						result[i+2] = true
					}
				}
			} else if strings.HasPrefix(part, "-") && strings.Contains(part, "e") {
				// Combined flags like -pie, -pe, -npe, etc.
				// Next non-flag argument after -e containing flag is the expression
				if i+1 < len(parts) && isPerlExpression(parts[i+1]) {
					result[i+1] = true
				}
			}
		}
	}

	return result
}

// isSedExpression checks if a string looks like a sed expression
// (substitution, deletion, print commands, etc.)
func isSedExpression(s string) bool {
	if len(s) == 0 {
		return false
	}
	// Common sed expression patterns:
	// s/pattern/replacement/flags - substitution
	// /pattern/d - delete
	// /pattern/p - print
	// /pattern/! - negation
	// Address ranges like 1,5d or /start/,/end/d
	if s[0] == 's' || s[0] == 'y' {
		return true
	}
	if s[0] == '/' {
		return true
	}
	// Numeric address like "1d", "1,5d"
	if len(s) > 0 && s[0] >= '0' && s[0] <= '9' {
		return true
	}
	return false
}

// isPerlExpression checks if a string looks like a perl one-liner expression
func isPerlExpression(s string) bool {
	if len(s) == 0 {
		return false
	}
	// Common perl expressions start with s/, tr/, y/, or contain typical perl code
	if strings.HasPrefix(s, "s/") || strings.HasPrefix(s, "tr/") || strings.HasPrefix(s, "y/") {
		return true
	}
	// Also match expressions that start with quotes containing these
	if len(s) > 1 && (s[0] == '\'' || s[0] == '"') {
		inner := s[1:]
		if strings.HasPrefix(inner, "s/") || strings.HasPrefix(inner, "tr/") || strings.HasPrefix(inner, "y/") {
			return true
		}
	}
	return false
}

// containsAbsolutePathInPart checks if a single command part contains an absolute path.
func containsAbsolutePathInPart(part string) bool {
	// Common safe absolute paths that don't require extra approval
	safePaths := []string{
		"/dev/null",
		"/dev/stdin",
		"/dev/stdout",
		"/dev/stderr",
	}

	// Find all potential absolute paths in the part (handles --flag=/path cases)
	paths := extractAbsolutePaths(part)
	for _, path := range paths {
		// Skip if it looks like code (awk programs, etc.) rather than a path
		if looksLikeCode(path) {
			continue
		}

		// Check against safe paths
		isSafe := false
		for _, safePath := range safePaths {
			if path == safePath || strings.HasPrefix(path, safePath+"/") || strings.HasPrefix(path, safePath) && len(path) == len(safePath) {
				isSafe = true
				break
			}
		}
		if !isSafe {
			return true
		}
	}
	return false
}

// extractAbsolutePaths finds all absolute paths within a string.
// Handles cases like "/etc/passwd", "--file=/etc/passwd", "prefix/etc" (not absolute).
func extractAbsolutePaths(s string) []string {
	var paths []string

	// Look for absolute paths starting with /
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			// Paths following command substitution $(...) are absolute at runtime
			// e.g., $(go env GOPATH)/pkg or $(pwd)/subdir
			if i > 0 && s[i-1] == ')' && isCommandSubstitution(s, i-1) {
				path := extractPathFrom(s, i)
				if len(path) > 1 {
					paths = append(paths, path)
				}
				continue
			}
			// Check if this is the start of an absolute path
			// It's absolute if it's at the start OR preceded by = or :
			if i == 0 || s[i-1] == '=' || s[i-1] == ':' {
				// Skip URL schemes like http:// or https://
				if i > 0 && s[i-1] == ':' && i+1 < len(s) && s[i+1] == '/' {
					continue
				}
				// Extract the path starting from this position
				path := extractPathFrom(s, i)
				if len(path) > 1 && looksLikePathContent(path) && !looksLikeRegex(path) {
					paths = append(paths, path)
				}
			}
		}
	}

	return paths
}

// isCommandSubstitution checks if the closing paren at position closeParenIdx
// is part of a $(...) command substitution.
func isCommandSubstitution(s string, closeParenIdx int) bool {
	if closeParenIdx < 2 || s[closeParenIdx] != ')' {
		return false
	}
	// Find the matching opening paren, tracking nesting
	depth := 1
	for i := closeParenIdx - 1; i >= 0; i-- {
		if s[i] == ')' {
			depth++
		} else if s[i] == '(' {
			depth--
			if depth == 0 {
				// Found matching open paren, check if preceded by $
				if i > 0 && s[i-1] == '$' {
					return true
				}
				return false
			}
		}
	}
	return false
}

// looksLikeRegex checks if a string looks like a regex pattern (e.g., /pattern/)
// rather than an absolute path. Only returns true for patterns that contain
// obvious regex metacharacters, to avoid false positives on paths like /data/.
func looksLikeRegex(s string) bool {
	if len(s) < 3 || s[0] != '/' {
		return false
	}

	// Must end with / to be a regex delimiter
	if s[len(s)-1] != '/' {
		return false
	}

	// Check if there are any slashes in the middle (would indicate a multi-level path)
	middle := s[1 : len(s)-1]
	if strings.Contains(middle, "/") {
		return false
	}

	// Only treat as regex if it contains obvious regex metacharacters
	// This is conservative to avoid false negatives on real paths like /data/
	regexChars := []byte{'^', '$', '*', '+', '?', '[', ']', '(', ')', '{', '}', '|', '\\', '.'}
	for _, c := range regexChars {
		if strings.ContainsRune(middle, rune(c)) {
			return true
		}
	}

	// No regex metacharacters found - treat as a path, not a regex
	return false
}

// extractPathFrom extracts a path starting at the given index.
func extractPathFrom(s string, start int) string {
	end := start
	for end < len(s) {
		c := s[end]
		// Stop at characters that typically end a path in shell contexts
		if c == ':' || c == '=' || c == ',' || c == ';' || c == ' ' || c == '\t' {
			break
		}
		end++
	}
	return s[start:end]
}

// looksLikePathContent checks if a string looks like path content (not code).
// Allows typical path chars including globs and shell variable expansions,
// but rejects obvious code constructs like pipes, redirects, and semicolons.
func looksLikePathContent(s string) bool {
	if len(s) == 0 {
		return false
	}

	// Must start with /
	if s[0] != '/' {
		return false
	}

	// Check for obvious code patterns that indicate this isn't a path
	// Pipes, redirects, semicolons, and backticks indicate code, not paths
	// We allow $, {, }, (, ) since they're used in shell variable expansions
	for _, c := range s {
		if c == '`' || c == '\'' || c == '"' ||
			c == '|' || c == '&' || c == '<' || c == '>' ||
			c == ';' || c == '#' || c == '+' {
			return false
		}
	}

	return true
}

// looksLikeCode checks if a string looks like code rather than a path
// (contains programming constructs like pipes, redirects, semicolons, etc.)
// We allow $, {, }, (, ) since they're used in shell variable expansions
func looksLikeCode(s string) bool {
	for _, c := range s {
		if c == '`' || c == '\'' || c == '"' ||
			c == '|' || c == '&' || c == '<' || c == '>' ||
			c == ';' || c == '#' || c == '+' {
			return true
		}
	}
	return false
}

// parseCommandForPaths splits a command into parts for path detection,
// handling quotes, escapes, and command substitutions.
func parseCommandForPaths(cmd string) []string {
	var parts []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	escaped := false
	parenDepth := 0 // Track depth inside $(...) command substitutions

	for i := 0; i < len(cmd); i++ {
		c := cmd[i]

		if escaped {
			current.WriteByte(c)
			escaped = false
			continue
		}

		if c == '\\' && !inSingleQuote {
			escaped = true
			continue
		}

		if c == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			continue
		}

		if c == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			continue
		}

		// Track $(...) command substitutions to avoid splitting inside them
		if !inSingleQuote && c == '$' && i+1 < len(cmd) && cmd[i+1] == '(' {
			parenDepth++
			current.WriteByte(c)
			current.WriteByte('(')
			i++
			continue
		}

		if !inSingleQuote && parenDepth > 0 {
			if c == '(' {
				parenDepth++
			} else if c == ')' {
				parenDepth--
			}
			current.WriteByte(c)
			continue
		}

		// Split on whitespace and shell operators
		if !inSingleQuote && !inDoubleQuote && (c == ' ' || c == '\t' || c == ';' || c == '|' || c == '&' || c == '>' || c == '<') {
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
			continue
		}

		current.WriteByte(c)
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}

// EvaluatePermissionOptions controls behavior of permission evaluation functions.
type EvaluatePermissionOptions struct {
	// StripEnvVarPrefix controls whether leading env var assignments are stripped
	// from commands before matching against patterns that don't reference env vars.
	StripEnvVarPrefix bool
}

// EvaluateCommandPermission evaluates a single command against the permission config.
// It checks deny patterns first, then require-approval, then auto-approve.
// Returns the permission result and any associated message.
// This version does NOT strip env var prefixes for backward compatibility.
func EvaluateCommandPermission(config CommandPermissionConfig, command string) (PermissionResult, string) {
	return EvaluateCommandPermissionWithOptions(config, command, EvaluatePermissionOptions{
		StripEnvVarPrefix: false,
	})
}

// EvaluateCommandPermissionWithOptions evaluates a single command against the permission config
// with configurable options.
func EvaluateCommandPermissionWithOptions(config CommandPermissionConfig, command string, opts EvaluatePermissionOptions) (PermissionResult, string) {
	strippedCommand := command
	if opts.StripEnvVarPrefix {
		strippedCommand = stripEnvVarPrefix(command)
	}

	// Check deny patterns first
	for _, p := range config.Deny {
		// Use original command if pattern contains env vars, otherwise use stripped
		cmdToMatch := command
		if !patternContainsEnvVar(p.Pattern) {
			cmdToMatch = strippedCommand
		}
		if matched, matches := matchPattern(p.Pattern, cmdToMatch); matched {
			msg := p.Message
			if msg != "" && len(matches) > 0 {
				msg = interpolateMessage(msg, matches)
			}
			return PermissionDeny, msg
		}
	}

	// Check require-approval patterns
	for _, p := range config.RequireApproval {
		// Use original command if pattern contains env vars, otherwise use stripped
		cmdToMatch := command
		if !patternContainsEnvVar(p.Pattern) {
			cmdToMatch = strippedCommand
		}
		if matched, _ := matchPattern(p.Pattern, cmdToMatch); matched {
			return PermissionRequireApproval, ""
		}
	}

	// Check auto-approve patterns
	for _, p := range config.AutoApprove {
		// Use original command if pattern contains env vars, otherwise use stripped
		cmdToMatch := command
		if !patternContainsEnvVar(p.Pattern) {
			cmdToMatch = strippedCommand
		}
		if matched, matches := matchPattern(p.Pattern, cmdToMatch); matched {
			// Even if auto-approved, require approval for commands with absolute paths
			if containsAbsolutePath(command) {
				return PermissionRequireApproval, ""
			}
			msg := p.Message
			if msg != "" && len(matches) > 0 {
				msg = interpolateMessage(msg, matches)
			}
			return PermissionAutoApprove, msg
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
// This version does NOT strip env var prefixes for backward compatibility.
func EvaluateScriptPermission(config CommandPermissionConfig, script string) (PermissionResult, string) {
	return EvaluateScriptPermissionWithOptions(config, script, EvaluatePermissionOptions{
		StripEnvVarPrefix: false,
	})
}

// EvaluateScriptPermissionWithOptions evaluates a shell script with configurable options.
func EvaluateScriptPermissionWithOptions(config CommandPermissionConfig, script string, opts EvaluatePermissionOptions) (PermissionResult, string) {
	commands := permission.ExtractCommands(script)

	// If no commands extracted, default to require approval
	if len(commands) == 0 {
		return PermissionRequireApproval, ""
	}

	hasRequireApproval := false

	for _, cmd := range commands {
		result, msg := EvaluateCommandPermissionWithOptions(config, cmd, opts)
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
