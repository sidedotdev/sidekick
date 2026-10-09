// inspect_flow_actions summarizes the persisted LLM response actions of a
// flow: status, response format and the content block types in the saved
// actionResult. Use -action to print a single action's full actionResult.
//
// Opening storage runs pending schema migrations like the server does, but
// the script itself never writes flow data.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"sidekick"
	"sidekick/domain"
)

type llm2Result struct {
	Output struct {
		Content []struct {
			Type           string `json:"type"`
			Text           string `json:"text"`
			BuiltinToolUse *struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				Status    string `json:"status"`
			} `json:"builtinToolUse"`
			BuiltinToolResult *struct {
				Name          string `json:"name"`
				SearchResults []any  `json:"searchResults"`
				IsError       bool   `json:"isError"`
			} `json:"builtinToolResult"`
		} `json:"content"`
	} `json:"output"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	StopReason string `json:"stopReason"`
}

func main() {
	workspaceId := flag.String("workspace", "", "workspace id (required)")
	flowId := flag.String("flow", "", "flow id (required)")
	actionId := flag.String("action", "", "print the full actionResult of this action id")
	builtinOnly := flag.Bool("builtin-only", false, "only list actions whose result contains builtin tool blocks")
	flag.Parse()
	if *workspaceId == "" || *flowId == "" {
		flag.Usage()
		os.Exit(1)
	}

	service, err := sidekick.GetService()
	if err != nil {
		fail("failed to initialize service: %v", err)
	}
	ctx := context.Background()
	if _, err := service.GetFlow(ctx, *workspaceId, *flowId); err != nil {
		fail("flow %s not found in workspace %s: %v", *flowId, *workspaceId, err)
	}
	actions, err := service.GetFlowActions(ctx, *workspaceId, *flowId)
	if err != nil {
		fail("failed to get flow actions: %v", err)
	}

	if *actionId != "" {
		for _, action := range actions {
			if action.Id == *actionId {
				fmt.Println(action.ActionResult)
				return
			}
		}
		fail("action %s not found among %d actions of flow %s", *actionId, len(actions), *flowId)
	}

	// Actions are classified by the persisted result shape rather than by
	// action type, since every tracked LLM call records an llm2 MessageResponse
	// regardless of which producer named the action.
	matched := 0
	otherTypes := map[string]int{}
	for _, action := range actions {
		result, ok := parseLlm2Result(action)
		if !ok {
			otherTypes[action.ActionType]++
			continue
		}
		summary, hasBuiltin := summarize(action, result)
		if *builtinOnly && !hasBuiltin {
			continue
		}
		matched++
		fmt.Println(summary)
	}

	fmt.Printf("\n%d actions total, %d llm2 responses listed\n", len(actions), matched)
	if len(otherTypes) > 0 {
		names := make([]string, 0, len(otherTypes))
		for name := range otherTypes {
			names = append(names, fmt.Sprintf("%s=%d", name, otherTypes[name]))
		}
		sort.Strings(names)
		fmt.Printf("non-llm2 actions by type: %s\n", strings.Join(names, ", "))
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func parseLlm2Result(action domain.FlowAction) (llm2Result, bool) {
	var result llm2Result
	if err := json.Unmarshal([]byte(action.ActionResult), &result); err != nil {
		return result, false
	}
	return result, result.Output.Content != nil
}

func summarize(action domain.FlowAction, result llm2Result) (string, bool) {
	header := fmt.Sprintf("%s %-8s %-30s", action.Id, action.ActionStatus, action.ActionType)
	var blocks []string
	hasBuiltin := false
	for _, block := range result.Output.Content {
		desc := block.Type
		switch {
		case block.BuiltinToolUse != nil:
			hasBuiltin = true
			desc += fmt.Sprintf("[%s status=%q args=%s]", block.BuiltinToolUse.Name, block.BuiltinToolUse.Status, truncate(block.BuiltinToolUse.Arguments, 60))
		case block.BuiltinToolResult != nil:
			hasBuiltin = true
			desc += fmt.Sprintf("[%s results=%d error=%v]", block.BuiltinToolResult.Name, len(block.BuiltinToolResult.SearchResults), block.BuiltinToolResult.IsError)
		case block.Type == "text":
			desc += fmt.Sprintf("[%d chars]", len(block.Text))
		}
		blocks = append(blocks, desc)
	}
	return fmt.Sprintf("%s %s/%s stop=%s\n    %s", header, result.Provider, result.Model, result.StopReason, strings.Join(blocks, ", ")), hasBuiltin
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}