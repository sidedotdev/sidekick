package permission

import (
	"context"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/bash"
)

// ExtractCommands parses a bash script string using tree-sitter and returns
// all executable commands found within it. Each command includes its full
// text with arguments, redirections, and background operator if present.
func ExtractCommands(script string) []string {
	parser := sitter.NewParser()
	parser.SetLanguage(bash.GetLanguage())
	tree, err := parser.ParseCtx(context.Background(), nil, []byte(script))
	if err != nil {
		return nil
	}

	sourceCode := []byte(script)
	var commands []string
	extractCommandsFromNode(tree.RootNode(), sourceCode, &commands)
	return commands
}

// extractCommandsFromNode recursively walks the AST and extracts commands
func extractCommandsFromNode(node *sitter.Node, sourceCode []byte, commands *[]string) {
	if node == nil {
		return
	}

	nodeType := node.Type()

	switch nodeType {
	case "command":
		cmdText := getFullCommandText(node, sourceCode)
		if cmdText != "" {
			*commands = append(*commands, cmdText)
			handleSpecialCommands(cmdText, commands)
		}
		// Still recurse into children for command substitutions
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			if child.Type() == "command_substitution" {
				extractCommandsFromNode(child, sourceCode, commands)
			}
		}
		return

	case "redirected_statement":
		// Extract the full text including redirections
		cmdText := strings.TrimSpace(node.Content(sourceCode))
		// Check for background operator at parent level
		cmdText = appendBackgroundIfPresent(node, sourceCode, cmdText)
		if cmdText != "" {
			*commands = append(*commands, cmdText)
			handleSpecialCommands(cmdText, commands)
		}
		// Recurse into children for command substitutions
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			if child.Type() == "command" {
				for j := 0; j < int(child.ChildCount()); j++ {
					grandchild := child.Child(j)
					if grandchild.Type() == "command_substitution" {
						extractCommandsFromNode(grandchild, sourceCode, commands)
					}
				}
			}
		}
		return

	case "subshell":
		// Extract commands from within subshell, don't add subshell itself
		for i := 0; i < int(node.ChildCount()); i++ {
			extractCommandsFromNode(node.Child(i), sourceCode, commands)
		}
		return

	case "command_substitution":
		// Recurse into command substitution to extract inner commands
		for i := 0; i < int(node.ChildCount()); i++ {
			extractCommandsFromNode(node.Child(i), sourceCode, commands)
		}
		return
	}

	// For all other node types, recurse into children
	for i := 0; i < int(node.ChildCount()); i++ {
		extractCommandsFromNode(node.Child(i), sourceCode, commands)
	}
}

// getFullCommandText returns the command text including any trailing & for background
func getFullCommandText(node *sitter.Node, sourceCode []byte) string {
	cmdText := strings.TrimSpace(node.Content(sourceCode))
	return appendBackgroundIfPresent(node, sourceCode, cmdText)
}

// appendBackgroundIfPresent checks if there's a sibling & node and appends it
func appendBackgroundIfPresent(node *sitter.Node, sourceCode []byte, cmdText string) string {
	parent := node.Parent()
	if parent != nil {
		for i := 0; i < int(parent.ChildCount()); i++ {
			sibling := parent.Child(i)
			if sibling.Type() == "&" {
				return cmdText + " &"
			}
		}
	}
	return cmdText
}

// handleSpecialCommands handles commands that execute other commands:
// sh/bash/zsh -c, eval, exec, xargs
func handleSpecialCommands(cmdText string, commands *[]string) {
	parts := parseCommandParts(cmdText)
	if len(parts) == 0 {
		return
	}

	cmdName := parts[0]

	switch cmdName {
	case "sh", "bash", "zsh":
		handleShellCommand(parts, commands)
	case "eval":
		handleEvalCommand(parts, commands)
	case "exec":
		handleExecCommand(parts, commands)
	case "xargs":
		handleXargsCommand(parts, commands)
	}
}

// parseCommandParts splits a command into parts, respecting quotes
func parseCommandParts(cmd string) []string {
	var parts []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	escaped := false

	for i := 0; i < len(cmd); i++ {
		c := cmd[i]

		if escaped {
			current.WriteByte(c)
			escaped = false
			continue
		}

		if c == '\\' && !inSingleQuote {
			escaped = true
			current.WriteByte(c)
			continue
		}

		if c == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			current.WriteByte(c)
			continue
		}

		if c == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			current.WriteByte(c)
			continue
		}

		if c == ' ' && !inSingleQuote && !inDoubleQuote {
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

// handleShellCommand handles sh -c, bash -c, zsh -c patterns
func handleShellCommand(parts []string, commands *[]string) {
	// Look for -c flag followed by a string argument
	for i := 1; i < len(parts)-1; i++ {
		if parts[i] == "-c" {
			scriptArg := parts[i+1]
			innerScript := unquoteString(scriptArg)
			innerCommands := ExtractCommands(innerScript)
			*commands = append(*commands, innerCommands...)
			return
		}
	}
}

// handleEvalCommand handles eval "..." patterns
func handleEvalCommand(parts []string, commands *[]string) {
	if len(parts) < 2 {
		return
	}
	// Join all arguments after eval and parse them
	scriptArg := strings.Join(parts[1:], " ")
	innerScript := unquoteString(scriptArg)
	innerCommands := ExtractCommands(innerScript)
	*commands = append(*commands, innerCommands...)
}

// handleExecCommand handles exec ... patterns
func handleExecCommand(parts []string, commands *[]string) {
	if len(parts) < 2 {
		return
	}
	// Everything after exec is the command to execute
	innerCmd := strings.Join(parts[1:], " ")
	if innerCmd != "" {
		*commands = append(*commands, innerCmd)
	}
}

// handleXargsCommand handles xargs ... patterns
func handleXargsCommand(parts []string, commands *[]string) {
	if len(parts) < 2 {
		return
	}

	// xargs flags that take an argument
	flagsWithArgs := map[string]bool{
		"-I": true, "-n": true, "-P": true, "-L": true,
		"-s": true, "-a": true, "-E": true, "-d": true,
	}

	// Find where the command starts (after xargs and its flags)
	cmdStart := 1
	for cmdStart < len(parts) {
		part := parts[cmdStart]
		if strings.HasPrefix(part, "-") {
			if flagsWithArgs[part] && cmdStart+1 < len(parts) {
				// Skip flag and its argument
				cmdStart += 2
			} else {
				// Flag without argument (like -0, -r, -t, -p, -x)
				cmdStart++
			}
		} else {
			break
		}
	}

	if cmdStart < len(parts) {
		innerCmd := strings.Join(parts[cmdStart:], " ")
		if innerCmd != "" {
			*commands = append(*commands, innerCmd)
		}
	}
}

// unquoteString removes surrounding quotes from a string
func unquoteString(s string) string {
	if len(s) < 2 {
		return s
	}
	if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

// handleSimpleWrapper handles commands where everything after the command name
// is the wrapped command (e.g., nohup, doas, command, builtin, time, ltrace)
func handleSimpleWrapper(parts []string, commands *[]string) {
	if len(parts) < 2 {
		return
	}
	innerCmd := strings.Join(parts[1:], " ")
	if innerCmd != "" {
		*commands = append(*commands, innerCmd)
	}
}

// handleWrapperWithFlags handles commands with optional flags that may take arguments.
// Skips flags (and their args if in flagsWithArgs map) until finding the actual command.
func handleWrapperWithFlags(parts []string, commands *[]string, flagsWithArgs map[string]bool) {
	if len(parts) < 2 {
		return
	}

	cmdStart := 1
	for cmdStart < len(parts) {
		part := parts[cmdStart]
		if strings.HasPrefix(part, "-") {
			if flagsWithArgs[part] && cmdStart+1 < len(parts) {
				// Skip flag and its argument
				cmdStart += 2
			} else {
				// Flag without argument or flag with attached value (like --flag=value)
				cmdStart++
			}
		} else {
			break
		}
	}

	if cmdStart < len(parts) {
		innerCmd := strings.Join(parts[cmdStart:], " ")
		if innerCmd != "" {
			*commands = append(*commands, innerCmd)
		}
	}
}

// handleWrapperWithPositionalArg handles commands with required positional args
// before the wrapped command (e.g., timeout has duration, flock has lockfile, ssh has host).
// Skips flags, then skips numPositionalArgs positional arguments, then extracts the remaining.
func handleWrapperWithPositionalArg(parts []string, commands *[]string, flagsWithArgs map[string]bool, numPositionalArgs int) {
	if len(parts) < 2 {
		return
	}

	cmdStart := 1
	positionalCount := 0

	for cmdStart < len(parts) {
		part := parts[cmdStart]
		if strings.HasPrefix(part, "-") {
			if flagsWithArgs[part] && cmdStart+1 < len(parts) {
				// Skip flag and its argument
				cmdStart += 2
			} else {
				// Flag without argument or flag with attached value
				cmdStart++
			}
		} else {
			// This is a positional argument
			positionalCount++
			cmdStart++
			if positionalCount >= numPositionalArgs {
				break
			}
		}
	}

	if cmdStart < len(parts) {
		innerCmd := strings.Join(parts[cmdStart:], " ")
		if innerCmd != "" {
			// Unquote if the command is a single quoted string (common for ssh)
			if len(parts[cmdStart:]) == 1 {
				unquoted := unquoteString(innerCmd)
				if unquoted != innerCmd {
					*commands = append(*commands, unquoted)
					return
				}
			}
			*commands = append(*commands, innerCmd)
		}
	}
}

// handleShellDashC handles -c "cmd" patterns like su -c.
// Looks for -c flag followed by a quoted string, unquotes it, and recursively parses.
func handleShellDashC(parts []string, commands *[]string) {
	for i := 1; i < len(parts)-1; i++ {
		if parts[i] == "-c" {
			scriptArg := parts[i+1]
			innerScript := unquoteString(scriptArg)
			innerCommands := ExtractCommands(innerScript)
			*commands = append(*commands, innerCommands...)
			return
		}
	}
}

// handleScriptExecution handles bash script.sh patterns.
// If no -c flag is present and there's a script argument, prepends ./ to the script path.
func handleScriptExecution(parts []string, commands *[]string) {
	if len(parts) < 2 {
		return
	}

	// Check if -c flag is present - if so, this is not script execution
	for _, part := range parts {
		if part == "-c" {
			return
		}
	}

	// Skip any flags to find the script argument
	scriptIdx := 1
	for scriptIdx < len(parts) && strings.HasPrefix(parts[scriptIdx], "-") {
		scriptIdx++
	}

	if scriptIdx < len(parts) {
		scriptPath := parts[scriptIdx]
		// Prepend ./ if not already an absolute or relative path
		if !strings.HasPrefix(scriptPath, "/") && !strings.HasPrefix(scriptPath, "./") && !strings.HasPrefix(scriptPath, "../") {
			scriptPath = "./" + scriptPath
		}
		*commands = append(*commands, scriptPath)
	}
}
