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
