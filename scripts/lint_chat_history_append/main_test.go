package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sidekick/coding/lsp"
)

// writeGoFileAndMakeItem writes a Go source file and returns a CallHierarchyItem
// pointing at the given function on the specified line (0-indexed).
func writeGoFileAndMakeItem(t *testing.T, dir, filename, content, funcName string, line int) lsp.CallHierarchyItem {
	t.Helper()
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	uri := "file://" + path
	return lsp.CallHierarchyItem{
		Name: funcName,
		URI:  uri,
		Range: lsp.Range{
			Start: lsp.Position{Line: line, Character: 0},
			End:   lsp.Position{Line: line, Character: 0},
		},
		SelectionRange: lsp.Range{
			Start: lsp.Position{Line: line, Character: 0},
			End:   lsp.Position{Line: line, Character: 0},
		},
	}
}

func TestHasWorkflowContextParam(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"workflow.Context", "func doWork(ctx workflow.Context) error", true},
		{"DevContext", "func (d *Dev) plan(dCtx DevContext) error", true},
		{"dev.DevContext", "func helper(dCtx dev.DevContext) error", true},
		{"DevActionContext", "func act(ctx DevActionContext, msg string)", true},
		{"dev.DevActionContext", "func act(ctx dev.DevActionContext, msg string)", true},
		{"ExecContext", "func run(ec ExecContext)", true},
		{"flow_action.ExecContext", "func run(ec flow_action.ExecContext)", true},
		{"ActionContext", "func handle(ac ActionContext)", true},
		{"flow_action.ActionContext", "func handle(ac flow_action.ActionContext)", true},
		{"context.Context only", "func doStuff(ctx context.Context) error", false},
		{"empty string", "", false},
		{"no params", "func noParams()", false},
		{"similar but not matching", "func withCtx(ctx SomeOtherContext)", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := hasWorkflowContextParam(tt.text)
			if got != tt.want {
				t.Errorf("hasWorkflowContextParam(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestCallSiteLinesFromRanges(t *testing.T) {
	t.Parallel()
	t.Run("with FromRanges", func(t *testing.T) {
		t.Parallel()
		ranges := []lsp.Range{
			{Start: lsp.Position{Line: 10, Character: 5}},
			{Start: lsp.Position{Line: 25, Character: 8}},
		}
		caller := lsp.CallHierarchyItem{
			Range: lsp.Range{Start: lsp.Position{Line: 5}},
		}
		lines := callSiteLinesFromRanges(ranges, caller)
		if len(lines) != 2 || lines[0] != 11 || lines[1] != 26 {
			t.Errorf("got %v, want [11 26]", lines)
		}
	})

	t.Run("empty FromRanges falls back to caller start", func(t *testing.T) {
		t.Parallel()
		caller := lsp.CallHierarchyItem{
			Range: lsp.Range{Start: lsp.Position{Line: 42}},
		}
		lines := callSiteLinesFromRanges(nil, caller)
		if len(lines) != 1 || lines[0] != 43 {
			t.Errorf("got %v, want [43]", lines)
		}
	})
}

func TestIsSanctioned(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		callerURI  string
		callerName string
		want       bool
	}{
		{
			"sanctioned AppendChatHistory in persisted_ai/helpers.go",
			"file:///home/user/project/persisted_ai/helpers.go",
			"AppendChatHistory",
			true,
		},
		{
			"wrong function name in same file",
			"file:///home/user/project/persisted_ai/helpers.go",
			"SomeOtherFunc",
			false,
		},
		{
			"right function name in wrong file",
			"file:///home/user/project/dev/helpers.go",
			"AppendChatHistory",
			false,
		},
		{
			"completely unrelated",
			"file:///home/user/project/dev/llm_loop.go",
			"runLoop",
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isSanctioned(tt.callerURI, tt.callerName)
			if got != tt.want {
				t.Errorf("isSanctioned(%q, %q) = %v, want %v", tt.callerURI, tt.callerName, got, tt.want)
			}
		})
	}
}

func TestUriToPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		uri  string
		want string
	}{
		{"file URI", "file:///home/user/project/main.go", "/home/user/project/main.go"},
		{"invalid URI returns as-is", "://broken", "://broken"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := uriToPath(tt.uri)
			if got != tt.want {
				t.Errorf("uriToPath(%q) = %q, want %q", tt.uri, got, tt.want)
			}
		})
	}
}

func TestFindMethodInFile(t *testing.T) {
	t.Parallel()

	// Create a temp file with realistic Go code similar to llm2_chat_history.go
	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "chat_history.go")
	content := `package persisted_ai

type ChatHistory interface {
	Append(msg common.Message)
	Len() int
	Get(index int) common.Message
}

type LegacyChatHistory struct {
	messages []common.ChatMessage
}

func (h *LegacyChatHistory) Append(msg common.Message) {
	h.messages = append(h.messages, msg.(common.ChatMessage))
}

type Llm2ChatHistory struct {
	refs     []MessageRef
	messages []llm2.Message
}

func (h *Llm2ChatHistory) Append(msg common.Message) {
	m := MessageFromCommon(msg)
	h.messages = append(h.messages, m)
}

type ChatHistoryContainer struct {
	History ChatHistory
}

func (c *ChatHistoryContainer) Append(msg common.Message) {
	c.History.Append(msg)
}
`
	if err := os.WriteFile(goFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	t.Run("finds interface method", func(t *testing.T) {
		t.Parallel()
		pos, err := findMethodInFile(goFile, "Append", "ChatHistory interface")
		if err != nil {
			t.Fatal(err)
		}
		if pos.Line != 3 {
			t.Errorf("expected line 3, got %d", pos.Line)
		}
	})

	t.Run("finds LegacyChatHistory.Append", func(t *testing.T) {
		t.Parallel()
		pos, err := findMethodInFile(goFile, "Append", "*LegacyChatHistory)")
		if err != nil {
			t.Fatal(err)
		}
		if pos.Line != 12 {
			t.Errorf("expected line 12, got %d", pos.Line)
		}
	})

	t.Run("finds Llm2ChatHistory.Append", func(t *testing.T) {
		t.Parallel()
		pos, err := findMethodInFile(goFile, "Append", "*Llm2ChatHistory)")
		if err != nil {
			t.Fatal(err)
		}
		if pos.Line != 21 {
			t.Errorf("expected line 21, got %d", pos.Line)
		}
	})

	t.Run("finds ChatHistoryContainer.Append", func(t *testing.T) {
		t.Parallel()
		pos, err := findMethodInFile(goFile, "Append", "*ChatHistoryContainer)")
		if err != nil {
			t.Fatal(err)
		}
		if pos.Line != 30 {
			t.Errorf("expected line 30, got %d", pos.Line)
		}
	})

	t.Run("error when method not found", func(t *testing.T) {
		t.Parallel()
		_, err := findMethodInFile(goFile, "Append", "*NonExistent)")
		if err == nil {
			t.Error("expected error for missing method")
		}
	})

	t.Run("error when interface method not found", func(t *testing.T) {
		t.Parallel()
		_, err := findMethodInFile(goFile, "Missing", "ChatHistory interface")
		if err == nil {
			t.Error("expected error for missing interface method")
		}
	})
}

func TestIsReachableFromWorkflow_DirectWorkflowCaller(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmpDir := t.TempDir()

	workflowItem := writeGoFileAndMakeItem(t, tmpDir, "workflow.go",
		`package dev

func (d *Dev) plannedDevWorkflow(ctx workflow.Context, input DevInput) error {
	chatHistory.Append(msg)
	return nil
}
`, "plannedDevWorkflow", 2)
	workflowItem.Detail = "func(ctx workflow.Context, input DevInput) error"

	client := lsp.MockLSPClient{
		CallHierarchyIncomingCallsFunc: func(ctx context.Context, item lsp.CallHierarchyItem) ([]lsp.CallHierarchyIncomingCall, error) {
			return nil, nil
		},
	}

	got := isReachableFromWorkflow(ctx, client, workflowItem, tmpDir, make(map[string]bool))
	if !got {
		t.Error("expected function with workflow.Context to be reachable from workflow")
	}
}

func TestIsReachableFromWorkflow_TransitiveWorkflowCaller(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmpDir := t.TempDir()

	helperItem := writeGoFileAndMakeItem(t, tmpDir, "helper.go",
		`package dev

func appendToHistory(chatHistory *ChatHistoryContainer, msg common.Message) {
	chatHistory.Append(msg)
}
`, "appendToHistory", 2)

	workflowItem := writeGoFileAndMakeItem(t, tmpDir, "workflow.go",
		`package dev

func (d *Dev) executeWorkflow(ctx workflow.Context) error {
	appendToHistory(d.chatHistory, msg)
	return nil
}
`, "executeWorkflow", 2)
	workflowItem.Detail = "func(ctx workflow.Context) error"

	client := lsp.MockLSPClient{
		CallHierarchyIncomingCallsFunc: func(ctx context.Context, item lsp.CallHierarchyItem) ([]lsp.CallHierarchyIncomingCall, error) {
			if item.Name == "appendToHistory" {
				return []lsp.CallHierarchyIncomingCall{
					{
						From:       workflowItem,
						FromRanges: []lsp.Range{{Start: lsp.Position{Line: 3, Character: 1}}},
					},
				}, nil
			}
			return nil, nil
		},
	}

	got := isReachableFromWorkflow(ctx, client, helperItem, tmpDir, make(map[string]bool))
	if !got {
		t.Error("expected helper called from workflow function to be reachable")
	}
}

func TestIsReachableFromWorkflow_NotReachable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmpDir := t.TempDir()

	activityItem := writeGoFileAndMakeItem(t, tmpDir, "activity.go",
		`package coding

func (a *Activities) DoSomething(ctx context.Context) error {
	return nil
}
`, "DoSomething", 2)

	client := lsp.MockLSPClient{
		CallHierarchyIncomingCallsFunc: func(ctx context.Context, item lsp.CallHierarchyItem) ([]lsp.CallHierarchyIncomingCall, error) {
			return nil, nil
		},
	}

	got := isReachableFromWorkflow(ctx, client, activityItem, tmpDir, make(map[string]bool))
	if got {
		t.Error("expected activity function to NOT be reachable from workflow")
	}
}

func TestIsReachableFromWorkflow_CycleDetection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmpDir := t.TempDir()

	funcA := writeGoFileAndMakeItem(t, tmpDir, "a.go",
		`package pkg

func funcA() {
	funcB()
}
`, "funcA", 2)

	funcB := writeGoFileAndMakeItem(t, tmpDir, "b.go",
		`package pkg

func funcB() {
	funcA()
}
`, "funcB", 2)

	client := lsp.MockLSPClient{
		CallHierarchyIncomingCallsFunc: func(ctx context.Context, item lsp.CallHierarchyItem) ([]lsp.CallHierarchyIncomingCall, error) {
			if item.Name == "funcA" {
				return []lsp.CallHierarchyIncomingCall{
					{From: funcB, FromRanges: []lsp.Range{{Start: lsp.Position{Line: 3}}}},
				}, nil
			}
			if item.Name == "funcB" {
				return []lsp.CallHierarchyIncomingCall{
					{From: funcA, FromRanges: []lsp.Range{{Start: lsp.Position{Line: 3}}}},
				}, nil
			}
			return nil, nil
		},
	}

	got := isReachableFromWorkflow(ctx, client, funcA, tmpDir, make(map[string]bool))
	if got {
		t.Error("expected cycle without workflow context to return false")
	}
}

func TestIsReachableFromWorkflow_SanctionedCallerStopsTraversal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmpDir := t.TempDir()

	appendItem := writeGoFileAndMakeItem(t, tmpDir, "container.go",
		`package persisted_ai

func (c *ChatHistoryContainer) Append(msg common.Message) {
	c.History.Append(msg)
}
`, "Append", 2)

	helpersDir := filepath.Join(tmpDir, "persisted_ai")
	if err := os.MkdirAll(helpersDir, 0755); err != nil {
		t.Fatal(err)
	}
	sanctionedItem := writeGoFileAndMakeItem(t, helpersDir, "helpers.go",
		`package persisted_ai

func AppendChatHistory(ctx workflow.Context, chatHistory *ChatHistoryContainer, msg common.Message) {
	chatHistory.Append(msg)
}
`, "AppendChatHistory", 2)
	sanctionedItem.URI = "file://" + filepath.Join(helpersDir, "helpers.go")

	workflowItem := writeGoFileAndMakeItem(t, tmpDir, "workflow.go",
		`package dev

func (d *Dev) runWorkflow(ctx workflow.Context) error {
	persisted_ai.AppendChatHistory(ctx, d.chatHistory, msg)
	return nil
}
`, "runWorkflow", 2)
	workflowItem.Detail = "func(ctx workflow.Context) error"

	client := lsp.MockLSPClient{
		CallHierarchyIncomingCallsFunc: func(ctx context.Context, item lsp.CallHierarchyItem) ([]lsp.CallHierarchyIncomingCall, error) {
			if item.Name == "Append" {
				return []lsp.CallHierarchyIncomingCall{
					{
						From:       sanctionedItem,
						FromRanges: []lsp.Range{{Start: lsp.Position{Line: 3}}},
					},
				}, nil
			}
			if item.Name == "AppendChatHistory" {
				return []lsp.CallHierarchyIncomingCall{
					{
						From:       workflowItem,
						FromRanges: []lsp.Range{{Start: lsp.Position{Line: 3}}},
					},
				}, nil
			}
			return nil, nil
		},
	}

	got := isReachableFromWorkflow(ctx, client, appendItem, tmpDir, make(map[string]bool))
	if got {
		t.Error("expected sanctioned caller to stop traversal, preventing workflow reachability")
	}
}

func TestIsReachableFromWorkflow_SkipsTestFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmpDir := t.TempDir()

	activityItem := writeGoFileAndMakeItem(t, tmpDir, "activity.go",
		`package pkg

func doAppend(h *ChatHistoryContainer, msg common.Message) {
	h.Append(msg)
}
`, "doAppend", 2)

	testItem := writeGoFileAndMakeItem(t, tmpDir, "activity_test.go",
		`package pkg

func TestDoAppend(ctx workflow.Context) {
	doAppend(h, msg)
}
`, "TestDoAppend", 2)
	testItem.Detail = "func(ctx workflow.Context)"

	client := lsp.MockLSPClient{
		CallHierarchyIncomingCallsFunc: func(ctx context.Context, item lsp.CallHierarchyItem) ([]lsp.CallHierarchyIncomingCall, error) {
			if item.Name == "doAppend" {
				return []lsp.CallHierarchyIncomingCall{
					{
						From:       testItem,
						FromRanges: []lsp.Range{{Start: lsp.Position{Line: 3}}},
					},
				}, nil
			}
			return nil, nil
		},
	}

	got := isReachableFromWorkflow(ctx, client, activityItem, tmpDir, make(map[string]bool))
	if got {
		t.Error("expected test file callers to be skipped")
	}
}

func TestIsReachableFromWorkflow_DetectsDevContext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmpDir := t.TempDir()

	callerItem := writeGoFileAndMakeItem(t, tmpDir, "dev_handler.go",
		`package dev

func (d *Dev) handleUserAction(dCtx DevContext, action UserAction) error {
	d.chatHistory.Append(msg)
	return nil
}
`, "handleUserAction", 2)
	callerItem.Detail = "func(dCtx DevContext, action UserAction) error"

	client := lsp.MockLSPClient{
		CallHierarchyIncomingCallsFunc: func(ctx context.Context, item lsp.CallHierarchyItem) ([]lsp.CallHierarchyIncomingCall, error) {
			return nil, nil
		},
	}

	got := isReachableFromWorkflow(ctx, client, callerItem, tmpDir, make(map[string]bool))
	if !got {
		t.Error("expected function with DevContext to be reachable from workflow")
	}
}

func TestIsReachableFromWorkflow_DetectsViaFuncSignatureFallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmpDir := t.TempDir()

	item := writeGoFileAndMakeItem(t, tmpDir, "exec.go",
		`package flow_action

func (e *Executor) RunAction(ec ExecContext, params ActionParams) error {
	chatHistory.Append(msg)
	return nil
}
`, "RunAction", 2)
	// Detail intentionally omits ExecContext to exercise the readFuncSignature fallback
	item.Detail = ""

	client := lsp.MockLSPClient{
		CallHierarchyIncomingCallsFunc: func(ctx context.Context, item lsp.CallHierarchyItem) ([]lsp.CallHierarchyIncomingCall, error) {
			return nil, nil
		},
	}

	got := isReachableFromWorkflow(ctx, client, item, tmpDir, make(map[string]bool))
	if !got {
		t.Error("expected readFuncSignature fallback to detect ExecContext")
	}
}

func TestIsReachableFromWorkflow_Fixture_Integration(t *testing.T) {
	client, fixtureRoot := initFixtureGoplsClient(t)

	ctx := context.Background()

	containerFile := filepath.Join(fixtureRoot, "chathistory", "container.go")
	pos, err := findMethodInFile(containerFile, "Append", "*ChatHistoryContainer)")
	if err != nil {
		t.Fatalf("findMethodInFile: %v", err)
	}

	uri := "file://" + containerFile
	items, err := client.PrepareCallHierarchy(ctx, uri, pos.Line, pos.Character)
	if err != nil {
		t.Fatalf("PrepareCallHierarchy: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("PrepareCallHierarchy returned no items")
	}

	calls, err := client.CallHierarchyIncomingCalls(ctx, items[0])
	if err != nil {
		t.Fatalf("CallHierarchyIncomingCalls: %v", err)
	}

	callerByName := make(map[string]lsp.CallHierarchyItem)
	for _, call := range calls {
		callerByName[call.From.Name] = call.From
	}

	t.Run("DirectWorkflowCaller", func(t *testing.T) {
		item, ok := callerByName["DirectWorkflowCaller"]
		if !ok {
			t.Fatal("DirectWorkflowCaller not found among incoming callers")
		}
		got := isReachableFromWorkflow(ctx, client, item, fixtureRoot, make(map[string]bool))
		if !got {
			t.Error("expected DirectWorkflowCaller (with workflow.Context) to be reachable")
		}
	})

	t.Run("TransitiveWorkflowCaller", func(t *testing.T) {
		item, ok := callerByName["HelperAppend"]
		if !ok {
			t.Fatal("HelperAppend not found among incoming callers")
		}
		got := isReachableFromWorkflow(ctx, client, item, fixtureRoot, make(map[string]bool))
		if !got {
			t.Error("expected HelperAppend (called by TransitiveWorkflowCaller) to be reachable")
		}
	})

	t.Run("NotReachable_Activity", func(t *testing.T) {
		// ActivityFunc does not call Append, so it won't be in callerByName.
		// Instead we verify it via a PrepareCallHierarchy + isReachableFromWorkflow.
		activityFile := filepath.Join(fixtureRoot, "callers", "activity.go")
		activityPos, err := findMethodInFile(activityFile, "ActivityFunc", "func ActivityFunc")
		if err != nil {
			t.Fatalf("findMethodInFile: %v", err)
		}
		activityItem := lsp.CallHierarchyItem{
			Name: "ActivityFunc",
			URI:  "file://" + activityFile,
			Range: lsp.Range{
				Start: activityPos,
				End:   activityPos,
			},
			SelectionRange: lsp.Range{
				Start: activityPos,
				End:   activityPos,
			},
		}
		got := isReachableFromWorkflow(ctx, client, activityItem, fixtureRoot, make(map[string]bool))
		if got {
			t.Error("expected ActivityFunc (no workflow context, no workflow callers) to NOT be reachable")
		}
	})

	t.Run("CycleDetection", func(t *testing.T) {
		cycleFile := filepath.Join(fixtureRoot, "callers", "cycle_a.go")
		cyclePos, err := findMethodInFile(cycleFile, "CycleA", "func CycleA")
		if err != nil {
			t.Fatalf("findMethodInFile: %v", err)
		}
		cycleItem := lsp.CallHierarchyItem{
			Name: "CycleA",
			URI:  "file://" + cycleFile,
			Range: lsp.Range{
				Start: cyclePos,
				End:   cyclePos,
			},
			SelectionRange: lsp.Range{
				Start: cyclePos,
				End:   cyclePos,
			},
		}
		got := isReachableFromWorkflow(ctx, client, cycleItem, fixtureRoot, make(map[string]bool))
		if got {
			t.Error("expected CycleA (mutual recursion, no workflow context) to NOT be reachable")
		}
	})

	t.Run("SanctionedCallerStopsTraversal", func(t *testing.T) {
		item, ok := callerByName["AppendChatHistory"]
		if !ok {
			t.Fatal("AppendChatHistory not found among incoming callers of Append")
		}
		if !isSanctioned(item.URI, item.Name) {
			t.Fatalf("expected AppendChatHistory at %s to be sanctioned", uriToPath(item.URI))
		}

		// AppendChatHistory IS reachable from workflow (WorkflowCallsSanctioned
		// calls it with workflow.Context). This proves the sanctioned skip is
		// the only thing preventing a false violation on Append via this path.
		got := isReachableFromWorkflow(ctx, client, item, fixtureRoot, make(map[string]bool))
		if !got {
			t.Error("expected AppendChatHistory to be reachable from workflow (proves the sanctioned skip matters)")
		}

		// Verify that isSanctioned correctly identifies AppendChatHistory,
		// which is what findViolations uses to skip transitive checking.
		found := false
		for _, call := range calls {
			if call.From.Name == "AppendChatHistory" {
				found = true
				if !isSanctioned(call.From.URI, call.From.Name) {
					t.Error("expected AppendChatHistory caller to be sanctioned")
				}
			}
		}
		if !found {
			t.Error("AppendChatHistory should be among incoming callers of Append")
		}
	})

	t.Run("SkipsTestFiles", func(t *testing.T) {
		// testWorkflowHelper is in callers_test.go, has workflow.Context,
		// and calls Append. Verify it appears as a caller.
		var testCaller *lsp.CallHierarchyItem
		for _, call := range calls {
			path := uriToPath(call.From.URI)
			if strings.HasSuffix(path, "_test.go") {
				from := call.From
				testCaller = &from
				break
			}
		}
		if testCaller == nil {
			t.Fatal("expected to find a _test.go caller among incoming calls")
		}

		// ActivityFunc has no workflow context and its only callers are in
		// test files. isReachableFromWorkflow should skip _test.go callers
		// and return false.
		activityFile := filepath.Join(fixtureRoot, "callers", "activity.go")
		activityPos, err := findMethodInFile(activityFile, "ActivityFunc", "func ActivityFunc")
		if err != nil {
			t.Fatalf("findMethodInFile: %v", err)
		}
		activityItems, err := client.PrepareCallHierarchy(ctx, "file://"+activityFile, activityPos.Line, activityPos.Character)
		if err != nil {
			t.Fatalf("PrepareCallHierarchy: %v", err)
		}
		if len(activityItems) == 0 {
			t.Fatal("PrepareCallHierarchy returned no items for ActivityFunc")
		}
		got := isReachableFromWorkflow(ctx, client, activityItems[0], fixtureRoot, make(map[string]bool))
		if got {
			t.Error("expected ActivityFunc (only called from _test.go) to NOT be reachable from workflow")
		}
	})

	t.Run("DetectsDevContext", func(t *testing.T) {
		item, ok := callerByName["DevContextCaller"]
		if !ok {
			t.Fatal("DevContextCaller not found among incoming callers")
		}
		got := isReachableFromWorkflow(ctx, client, item, fixtureRoot, make(map[string]bool))
		if !got {
			t.Error("expected DevContextCaller (with DevContext param) to be reachable")
		}
	})

	t.Run("DetectsExecContext", func(t *testing.T) {
		item, ok := callerByName["ExecContextCaller"]
		if !ok {
			t.Fatal("ExecContextCaller not found among incoming callers")
		}
		got := isReachableFromWorkflow(ctx, client, item, fixtureRoot, make(map[string]bool))
		if !got {
			t.Error("expected ExecContextCaller (with ExecContext param) to be reachable")
		}
	})

	t.Run("DetectsViaReadFuncSignature", func(t *testing.T) {
		// gopls Detail for CallHierarchyItems contains "pkg • file.go", not
		// the Go function signature, so hasWorkflowContextParam(Detail) is
		// always false. Detection must come from readFuncSignature.
		item, ok := callerByName["DirectWorkflowCaller"]
		if !ok {
			t.Fatal("DirectWorkflowCaller not found among incoming callers")
		}
		if hasWorkflowContextParam(item.Detail) {
			t.Fatal("expected gopls Detail to NOT contain workflow context (it contains package info)")
		}
		sig := readFuncSignature(item)
		if !hasWorkflowContextParam(sig) {
			t.Errorf("expected readFuncSignature to detect workflow.Context in source, got %q", sig)
		}
	})
}

// TestFindViolations_Fixture runs findViolations against the fixture module
// using the snapshot-backed LSP client.
func TestFindViolations_Fixture(t *testing.T) {
	client, fixtureRoot := initFixtureGoplsClient(t)
	ctx := context.Background()

	fixtureTargets := []appendTarget{
		{file: "chathistory/container.go", methodName: "Append", receiverHint: "*ChatHistoryContainer)"},
	}

	violations, err := findViolations(ctx, client, fixtureRoot, fixtureTargets)
	if err != nil {
		t.Fatalf("findViolations error: %v", err)
	}

	// The fixture has several direct .Append() callers reachable from workflow code:
	// DirectWorkflowCaller, HelperAppend (via TransitiveWorkflowCaller),
	// DevContextCaller, and ExecContextCaller.
	// AppendChatHistory is sanctioned and should NOT appear.
	// testWorkflowHelper is in _test.go and should be skipped.
	expectedCallers := map[string]bool{
		"DirectWorkflowCaller": false,
		"HelperAppend":         false,
		"DevContextCaller":     false,
		"ExecContextCaller":    false,
	}

	for _, v := range violations {
		for name := range expectedCallers {
			if strings.Contains(v, name) {
				expectedCallers[name] = true
			}
		}
	}

	for name, found := range expectedCallers {
		if !found {
			t.Errorf("expected violation for %s but not found in: %v", name, violations)
		}
	}

	// Verify sanctioned caller is NOT reported
	for _, v := range violations {
		if strings.Contains(v, "AppendChatHistory") {
			t.Errorf("sanctioned AppendChatHistory should not be a violation: %s", v)
		}
	}
}
