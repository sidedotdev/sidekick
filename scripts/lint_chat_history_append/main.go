package main

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sidekick/coding/lsp"
)

// workflowContextTypes are parameter type substrings that identify workflow code.
// Both unqualified and package-qualified forms are included to handle signatures
// both inside and outside the defining package.
var workflowContextTypes = []string{
	"workflow.Context",
	"DevContext",
	"dev.DevContext",
	"DevActionContext",
	"dev.DevActionContext",
	"ExecContext",
	"flow_action.ExecContext",
	"ActionContext",
	"flow_action.ActionContext",
}

// appendTarget defines a chat history method definition to check via call hierarchy.
type appendTarget struct {
	file        string
	methodName  string
	displayName string
	guidance    string
	// receiver prefix to match, e.g. "func (h *Llm2ChatHistory)"
	receiverHint string
}

// All prohibited chat history method definitions that must not be called from
// workflow-reachable code.
var appendTargets = []appendTarget{
	{
		file:         "persisted_ai/llm2_chat_history.go",
		methodName:   "Append",
		displayName:  "Append()",
		guidance:     "Use persisted_ai.AppendChatHistory instead.",
		receiverHint: "ChatHistory interface",
	},
	{
		file:         "persisted_ai/llm2_chat_history.go",
		methodName:   "Append",
		displayName:  "Append()",
		guidance:     "Use persisted_ai.AppendChatHistory instead.",
		receiverHint: "*LegacyChatHistory)",
	},
	{
		file:         "persisted_ai/llm2_chat_history.go",
		methodName:   "Append",
		displayName:  "Append()",
		guidance:     "Use persisted_ai.AppendChatHistory instead.",
		receiverHint: "*Llm2ChatHistory)",
	},
	{
		file:         "persisted_ai/llm2_chat_history.go",
		methodName:   "Append",
		displayName:  "Append()",
		guidance:     "Use persisted_ai.AppendChatHistory instead.",
		receiverHint: "*ChatHistoryContainer)",
	},
	{
		file:         "persisted_ai/llm2_chat_history.go",
		methodName:   "Llm2Messages",
		displayName:  "Llm2Messages()",
		guidance:     "Keep hydrated-only llm2 message reads inside activities after hydration.",
		receiverHint: "*Llm2ChatHistory)",
	},
	{
		file:         "persisted_ai/llm2_chat_history.go",
		methodName:   "Llm2Messages",
		displayName:  "Llm2Messages()",
		guidance:     "Keep hydrated-only llm2 message reads inside activities after hydration.",
		receiverHint: "*ChatHistoryContainer)",
	},
}

// sanctionedCallers maps fully-qualified file-relative paths to function names
// that are allowed to call chat history methods directly. Their callers are NOT
// transitively checked.
var sanctionedCallers = []sanctionedCaller{
	{pathSuffix: "persisted_ai/helpers.go", funcName: "AppendChatHistory"},
}

type sanctionedCaller struct {
	pathSuffix string
	funcName   string
}

func isSanctioned(callerURI, callerName string) bool {
	path := uriToPath(callerURI)
	for _, sc := range sanctionedCallers {
		if strings.HasSuffix(path, sc.pathSuffix) && callerName == sc.funcName {
			return true
		}
	}
	return false
}

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get working directory: %v\n", err)
		os.Exit(1)
	}

	client := &lsp.Jsonrpc2LSPClient{LanguageName: "golang"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	rootURI := "file://" + cwd
	params := lsp.InitializeParams{
		ProcessID: os.Getpid(),
		RootURI:   rootURI,
		Capabilities: lsp.ClientCapabilities{
			TextDocument: lsp.TextDocumentClientCapabilities{},
		},
		WorkspaceFolders: &[]lsp.WorkspaceFolder{
			{URI: rootURI, Name: filepath.Base(cwd)},
		},
	}
	_, err = client.Initialize(ctx, params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize LSP client: %v\n", err)
		os.Exit(1)
	}

	violations, err := findViolations(ctx, client, cwd, appendTargets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: lint check could not complete: %v\n", err)
		os.Exit(1)
	}
	if len(violations) > 0 {
		fmt.Println("=== Chat history workflow lint violations ===")
		fmt.Println("Direct chat history method calls reachable from workflow code:")
		fmt.Println()
		for _, v := range violations {
			fmt.Println(v)
		}
		fmt.Printf(
			"\n%d violation(s) found. Use persisted_ai.AppendChatHistory for appends and keep hydrated-only llm2 message reads inside activities.\n",
			len(violations),
		)
		os.Exit(1)
	}

	fmt.Println("No chat history workflow lint violations found.")
}

type violation struct {
	relPath    string
	line       int
	funcName   string
	targetName string
}

func (v violation) String() string {
	return fmt.Sprintf("  %s:%d in %s (calls %s)", v.relPath, v.line, v.funcName, v.targetName)
}

func findViolations(ctx context.Context, client lsp.LSPClient, cwd string, targets []appendTarget) ([]string, error) {
	// Deduplicate violations by location
	seen := make(map[string]bool)
	var results []string

	// Collect all prepared CallHierarchyItems so we can skip internal
	// delegation calls between the append methods themselves (e.g.
	// ChatHistoryContainer.Append calling History.Append).
	type preparedEntry struct {
		item   lsp.CallHierarchyItem
		target appendTarget
	}
	var allPrepared []preparedEntry
	targetItemKeys := make(map[string]bool)

	for _, target := range targets {
		absPath := filepath.Join(cwd, target.file)
		uri := "file://" + absPath

		pos, err := findMethodInFile(absPath, target.methodName, target.receiverHint)
		if err != nil {
			return nil, fmt.Errorf("could not find %s (%s) in %s: %w",
				target.methodName, target.receiverHint, target.file, err)
		}

		items, err := client.PrepareCallHierarchy(ctx, uri, pos.Line, pos.Character)
		if err != nil {
			return nil, fmt.Errorf("prepareCallHierarchy failed for %s at %d:%d: %w",
				target.file, pos.Line, pos.Character, err)
		}

		if len(items) == 0 {
			return nil, fmt.Errorf("prepareCallHierarchy returned no items for %s (%s) in %s",
				target.methodName, target.receiverHint, target.file)
		}

		for _, item := range items {
			allPrepared = append(allPrepared, preparedEntry{item: item, target: target})
			targetItemKeys[fmt.Sprintf("%s:%d:%d", item.URI, item.SelectionRange.Start.Line, item.SelectionRange.Start.Character)] = true
		}
	}

	for _, entry := range allPrepared {
		incomingCalls, err := client.CallHierarchyIncomingCalls(ctx, entry.item)
		if err != nil {
			return nil, fmt.Errorf("callHierarchy/incomingCalls failed for %s: %w", entry.item.Name, err)
		}

		for _, call := range incomingCalls {
			callerPath := uriToPath(call.From.URI)
			relPath, _ := filepath.Rel(cwd, callerPath)

			if strings.HasSuffix(relPath, "_test.go") {
				continue
			}

			if isSanctioned(call.From.URI, call.From.Name) {
				continue
			}

			// Skip callers that are themselves append target definitions
			callerKey := fmt.Sprintf("%s:%d:%d", call.From.URI, call.From.SelectionRange.Start.Line, call.From.SelectionRange.Start.Character)
			if targetItemKeys[callerKey] {
				continue
			}

			// Use FromRanges to get exact call site lines
			callSiteLines := callSiteLinesFromRanges(call.FromRanges, call.From)

			for _, csLine := range callSiteLines {
				v := violation{
					relPath:    relPath,
					line:       csLine,
					funcName:   call.From.Name,
					targetName: entry.target.displayName,
				}
				key := v.String()
				if seen[key] {
					continue
				}

				if isReachableFromWorkflow(ctx, client, call.From, cwd, make(map[string]bool)) {
					seen[key] = true
					results = append(results, key)
				}
			}
		}
	}

	return results, nil
}

var activityInvocationHelpers = []string{
	"PerformWithUserRetry(",
	"PerformActivityWithUserRetry(",
	"workflow.ExecuteActivity(",
	"workflow.ExecuteLocalActivity(",
}

// callSiteLinesFromRanges extracts 1-indexed line numbers from FromRanges.
// Falls back to the caller function's start line if no ranges are available.
func callSiteLinesFromRanges(fromRanges []lsp.Range, callerItem lsp.CallHierarchyItem) []int {
	if len(fromRanges) > 0 {
		lines := make([]int, 0, len(fromRanges))
		for _, r := range fromRanges {
			lines = append(lines, r.Start.Line+1)
		}
		return lines
	}
	return []int{callerItem.Range.Start.Line + 1}
}

func readCallSiteSnippet(callerItem lsp.CallHierarchyItem, fromRanges []lsp.Range) string {
	if len(fromRanges) == 0 {
		return ""
	}

	path := uriToPath(callerItem.URI)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	lines := strings.Split(string(data), "\n")
	startLine := fromRanges[0].Start.Line
	endLine := startLine + 3
	if endLine >= len(lines) {
		endLine = len(lines) - 1
	}
	if startLine < 0 || startLine >= len(lines) || endLine < startLine {
		return ""
	}

	return strings.Join(lines[startLine:endLine+1], "\n")
}

func isActivityInvocationEdge(call lsp.CallHierarchyIncomingCall, calleeName string) bool {
	snippet := readCallSiteSnippet(call.From, call.FromRanges)
	if snippet == "" || !strings.Contains(snippet, "."+calleeName) {
		return false
	}

	for _, helper := range activityInvocationHelpers {
		if strings.Contains(snippet, helper) {
			return true
		}
	}
	return false
}

// isReachableFromWorkflow checks whether the given function has a workflow context
// parameter or any of its callers (transitively) do.
func isReachableFromWorkflow(ctx context.Context, client lsp.LSPClient, item lsp.CallHierarchyItem, cwd string, visited map[string]bool) bool {
	key := fmt.Sprintf("%s:%d:%d", item.URI, item.Range.Start.Line, item.Range.Start.Character)
	if visited[key] {
		return false
	}
	visited[key] = true

	// Check gopls Detail field for workflow context types
	if hasWorkflowContextParam(item.Detail) {
		return true
	}

	// Fallback: read the function signature from source to detect context types
	// that may not appear in Detail
	if sig := readFuncSignature(item); hasWorkflowContextParam(sig) {
		return true
	}

	incomingCalls, err := client.CallHierarchyIncomingCalls(ctx, item)
	if err != nil {
		return false
	}

	for _, call := range incomingCalls {
		callerPath := uriToPath(call.From.URI)
		relPath, _ := filepath.Rel(cwd, callerPath)

		if strings.HasSuffix(relPath, "_test.go") {
			continue
		}

		if isSanctioned(call.From.URI, call.From.Name) {
			continue
		}

		if isActivityInvocationEdge(call, item.Name) {
			continue
		}

		if isReachableFromWorkflow(ctx, client, call.From, cwd, visited) {
			return true
		}
	}

	return false
}

func hasWorkflowContextParam(text string) bool {
	for _, ct := range workflowContextTypes {
		if strings.Contains(text, ct) {
			return true
		}
	}
	return false
}

// readFuncSignature reads the function signature line(s) from source
// using the CallHierarchyItem's URI and Range.
func readFuncSignature(item lsp.CallHierarchyItem) string {
	path := uriToPath(item.URI)
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	startLine := item.Range.Start.Line
	// Read a few lines from the function start to capture full signature
	endLine := startLine + 5
	var sb strings.Builder
	for scanner.Scan() {
		if lineNum >= startLine && lineNum <= endLine {
			sb.WriteString(scanner.Text())
			sb.WriteString(" ")
			// Stop once we see the opening brace
			if strings.Contains(scanner.Text(), "{") {
				break
			}
		}
		if lineNum > endLine {
			break
		}
		lineNum++
	}
	return sb.String()
}

// findMethodInFile finds a method definition matching the given name and receiver hint.
// The receiverHint is a substring that must appear on the same line (or the preceding
// line for interface methods) to disambiguate multiple Append definitions.
func findMethodInFile(filePath, methodName, receiverHint string) (lsp.Position, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return lsp.Position{}, err
	}
	lines := strings.Split(string(data), "\n")

	// For interface methods, the hint is "ChatHistory interface" — match
	// a line that starts with the method name and a paren (interface method decl)
	// inside an interface block
	if strings.Contains(receiverHint, "interface") {
		inInterface := false
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(line, "type") && strings.Contains(line, "interface") {
				if strings.Contains(line, strings.Split(receiverHint, " ")[0]) {
					inInterface = true
					continue
				}
			}
			if inInterface {
				if trimmed == "}" {
					inInterface = false
					continue
				}
				if strings.HasPrefix(trimmed, methodName+"(") {
					col := strings.Index(line, methodName)
					return lsp.Position{Line: i, Character: col}, nil
				}
			}
		}
		return lsp.Position{}, fmt.Errorf("interface method %s not found", methodName)
	}

	// For concrete methods, match "func (receiver) MethodName("
	for i, line := range lines {
		if strings.Contains(line, receiverHint) && strings.Contains(line, methodName+"(") {
			col := strings.Index(line, methodName)
			if col >= 0 {
				return lsp.Position{Line: i, Character: col}, nil
			}
		}
	}
	return lsp.Position{}, fmt.Errorf("method %s with receiver %s not found in %s", methodName, receiverHint, filePath)
}

func uriToPath(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil {
		return uri
	}
	return parsed.Path
}
