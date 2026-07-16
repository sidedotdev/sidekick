package dev

import (
	"encoding/json"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/fflag"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"strings"

	"github.com/invopop/jsonschema"
	"go.temporal.io/sdk/workflow"
)

type contextGatherReadyArguments struct{}

var contextGatherReadyTool = llm.Tool{
	Name:        "ready",
	Description: "Call this zero-argument tool once all repository context needed for coding or command execution has been gathered. This ends context gathering and transitions to coding.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&contextGatherReadyArguments{}),
}

var contextGatherForgetTool = llm.Tool{
	Name:        "forget",
	Description: "Schedule one or more completed repository-context exchanges for omission from the coding handoff. Use the short context references exposed in tool results.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&contextGatherReferenceArgs{}),
}

var contextGatherRememberTool = llm.Tool{
	Name:        "remember",
	Description: "Cancel scheduled omission for one or more repository-context exchanges. Use references that are currently scheduled for forgetting.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&contextGatherReferenceArgs{}),
}

// ContextGatherOptions carries optional inputs for context gathering.
type ContextGatherOptions struct {
	// InitialMessage, when set, becomes the first message of the filtered
	// handoff history, replacing the temporary gather instructions with the
	// real initial prompt of the consuming flow.
	InitialMessage *llm.ChatMessage
	// EnvironmentContext enriches the gather prompt with the same environment
	// details the consuming flow sees.
	EnvironmentContext string
}

// contextGatherHandoffEnabled reports whether this workflow builds the
// gathered history handoff with the real initial instructions and ranked repo
// summary; older executions replay the previous explore behavior.
func contextGatherHandoffEnabled(dCtx DevContext) bool {
	return workflow.GetVersion(dCtx, "context-gather-handoff", workflow.DefaultVersion, 1) >= 1
}

// gatherPlanningContext runs the explore context gather for planning-phase
// subflows (dev requirements and dev plan building) when enabled. With the
// handoff enabled, the ranked repo summary feeds both the gather prompt and
// the initial planning instructions built via buildInitialMessage, and the
// returned chat history starts with those instructions (seeded=true) followed
// by the retained context exchanges. It returns a nil history when the legacy
// initial code context preparation should run instead. The returned int is a
// chat history keep-length extension derived from the gathered context size.
func gatherPlanningContext(dCtx DevContext, requirements string, buildInitialMessage func(codeContext string) llm.ChatMessage) (*persisted_ai.ChatHistoryContainer, bool, int, error) {
	if !shouldGatherContext(dCtx.ContextGatherType, true) ||
		workflow.GetVersion(dCtx, "planning-context-gather", workflow.DefaultVersion, 1) != 1 {
		return nil, false, 0, nil
	}

	promptInfo := InitialCodeInfo{Requirements: requirements}
	opts := ContextGatherOptions{}
	seeded := contextGatherHandoffEnabled(dCtx)
	if seeded {
		repoSummary, _, err := PrepareRepoSummary(dCtx, requirements)
		if err != nil {
			return nil, false, 0, fmt.Errorf("failed to prepare repo summary: %w", err)
		}
		promptInfo.CodeContext = repoSummary
		initialMessage := buildInitialMessage(repoSummary)
		opts.InitialMessage = &initialMessage
	}

	chatHistory := NewVersionedChatHistory(dCtx, dCtx.WorkspaceId)
	contextSizeExtension, err := GatherContextForCoding(dCtx, chatHistory, promptInfo, opts)
	if err != nil {
		return nil, false, 0, err
	}
	return chatHistory, seeded, contextSizeExtension, nil
}

// gatheredContextExtensionRatio determines how much of the gathered context
// size extends the managed chat history keep length. The full gathered
// history is handed off, but older gathered context gradually becomes
// redundant as newer coding context duplicates or supersedes it, so only a
// fraction of its size is protected from history management.
const gatheredContextExtensionRatio = 0.5

// GatherContextForCoding explores the repository in a visible, read-only
// localization subflow and leaves only retained context exchanges in history,
// preceded by opts.InitialMessage when provided. It returns a chat history
// keep-length extension: a fraction of the gathered context size, per
// gatheredContextExtensionRatio.
func GatherContextForCoding(dCtx DevContext, chatHistory *persisted_ai.ChatHistoryContainer, promptInfo PromptInfo, opts ContextGatherOptions) (int, error) {
	if chatHistory == nil {
		return 0, fmt.Errorf("context gathering chat history is required")
	}

	return RunSubflow(dCtx, "gather_context", "Gather Context", func(_ domain.Subflow) (int, error) {
		return gatherContextForCodingSubflow(dCtx, chatHistory, promptInfo, opts)
	})
}

func gatherContextForCodingSubflow(dCtx DevContext, chatHistory *persisted_ai.ChatHistoryContainer, promptInfo PromptInfo, opts ContextGatherOptions) (int, error) {
	forgettingEnabled := fflag.IsEnabled(dCtx, fflag.ContextGatheringForget)
	tracker := newContextGatherHistory(chatHistory, forgettingEnabled)

	prompt, err := renderContextGatherPrompt(dCtx, promptInfo, forgettingEnabled, opts.EnvironmentContext)
	if err != nil {
		return 0, err
	}
	if err := AppendChatHistory(dCtx.ExecContext, chatHistory, llm.ChatMessage{
		Role:         llm.ChatMessageRoleUser,
		Content:      prompt,
		CacheControl: "ephemeral",
		ContextType:  ContextTypeInitialInstructions,
	}); err != nil {
		return 0, fmt.Errorf("failed to append context gathering instructions: %w", err)
	}

	modelConfig := dCtx.GetModelConfig(common.CodeLocalizationKey, 0, "default")
	options := llm2.Options{
		Tools: contextGatherTools(modelConfig, forgettingEnabled),
		ToolChoice: llm.ToolChoice{
			Type: llm.ToolChoiceTypeAuto,
		},
		ModelConfig: modelConfig,
	}

	for {
		response, err := TrackedToolChat(dCtx.WithCancelOnPause(), "context_gather", options, chatHistory)
		if err != nil {
			return 0, fmt.Errorf("failed to gather repository context: %w", err)
		}
		if dCtx.GlobalState != nil && dCtx.GlobalState.Paused {
			continue
		}

		message := response.GetMessage()
		if err := AppendChatHistory(dCtx.ExecContext, chatHistory, message); err != nil {
			return 0, fmt.Errorf("failed to append context gathering response: %w", err)
		}
		if llm2Message, ok := message.(llm2.Message); ok {
			tracker.llm2Transcript = append(tracker.llm2Transcript, llm2Message)
		} else if llm2Message, ok := message.(*llm2.Message); ok {
			tracker.llm2Transcript = append(tracker.llm2Transcript, *llm2Message)
		}

		terminal := false
		for _, toolCall := range message.GetToolCalls() {
			result, contextResult, toolTerminal, err := handleContextGatherToolCall(dCtx, tracker, toolCall)
			if err != nil {
				return 0, err
			}
			terminal = terminal || toolTerminal

			if contextResult {
				if _, err := tracker.registerContextToolResult(&result); err != nil {
					return 0, fmt.Errorf("failed to register context result: %w", err)
				}
			}
			if err := appendToolCallResult(dCtx.ExecContext, chatHistory, result, nil); err != nil {
				return 0, fmt.Errorf("failed to append context gathering tool result: %w", err)
			}
			tracker.llm2Transcript = append(tracker.llm2Transcript, llm2.Message{
				Role: llm2.RoleUser,
				Content: []llm2.ContentBlock{{
					Type:       llm2.ContentBlockTypeToolResult,
					ToolResult: &result,
				}},
			})
		}

		if terminal {
			if err := tracker.filterForCoding(); err != nil {
				return 0, err
			}
			contextSizeExtension := int(float64(tracker.gatheredContextSize()) * gatheredContextExtensionRatio)
			if err := persistFilteredContextGatherHistory(dCtx, chatHistory, opts.InitialMessage, tracker.llm2Transcript); err != nil {
				return 0, err
			}
			return contextSizeExtension, nil
		}
	}
}

func persistFilteredContextGatherHistory(
	dCtx DevContext,
	chatHistory *persisted_ai.ChatHistoryContainer,
	initialMessage *llm.ChatMessage,
	messages []llm2.Message,
) error {
	history, ok := chatHistory.History.(*persisted_ai.Llm2ChatHistory)
	if !ok {
		return nil
	}

	chatHistory.History = persisted_ai.NewLlm2ChatHistory(history.FlowId(), history.WorkspaceId())
	if initialMessage != nil {
		if err := AppendChatHistory(dCtx.ExecContext, chatHistory, *initialMessage); err != nil {
			return fmt.Errorf("failed to persist handoff initial instructions: %w", err)
		}
	}
	for i := range messages {
		if err := AppendChatHistory(dCtx.ExecContext, chatHistory, &messages[i]); err != nil {
			return fmt.Errorf("failed to persist filtered context gathering history: %w", err)
		}
	}
	return nil
}

func contextGatherTools(modelConfig common.ModelConfig, forgettingEnabled bool) []*llm.Tool {
	tools := []*llm.Tool{
		&bulkSearchRepositoryTool,
		currentGetSymbolDefinitionsTool(),
		&bulkReadFileTool,
		&contextGatherReadyTool,
		&getHelpOrInputTool,
	}
	if supportsImageToolResults(modelConfig) {
		tools = append(tools, &readImageTool)
	}
	if forgettingEnabled {
		tools = append(tools, &contextGatherForgetTool, &contextGatherRememberTool)
	}
	return tools
}

func handleContextGatherToolCall(dCtx DevContext, tracker *contextGatherHistory, toolCall llm.ToolCall) (llm2.ToolResultBlock, bool, bool, error) {
	switch toolCall.Name {
	case bulkSearchRepositoryTool.Name, currentGetSymbolDefinitionsTool().Name, bulkReadFileTool.Name, readImageTool.Name:
		output, toolErr := handleToolCall(dCtx, toolCall)
		result := output.ToolResultBlock
		if toolErr != nil {
			result = llm2.ToolResultBlock{
				Name:       toolCall.Name,
				ToolCallId: toolCall.Id,
				Content:    llm2.TextContentBlocks(toolErr.Error()),
				IsError:    true,
			}
		}
		results := cleanupWorkingDirFromResults(dCtx, []llm2.ToolResultBlock{result})
		return results[0], true, false, nil
	case contextGatherReadyTool.Name, getHelpOrInputTool.Name:
		return llm2.ToolResultBlock{
			Name:       toolCall.Name,
			ToolCallId: toolCall.Id,
			Content:    llm2.TextContentBlocks("Context gathering complete. Transitioning to coding."),
		}, false, true, nil
	case contextGatherForgetTool.Name:
		state, err := applyContextGatherReferenceChange(toolCall, tracker.forgetReferences)
		return contextGatherControlResult(toolCall, state, err), false, false, nil
	case contextGatherRememberTool.Name:
		state, err := applyContextGatherReferenceChange(toolCall, tracker.rememberReferences)
		return contextGatherControlResult(toolCall, state, err), false, false, nil
	default:
		return llm2.ToolResultBlock{
			Name:       toolCall.Name,
			ToolCallId: toolCall.Id,
			Content:    llm2.TextContentBlocks(fmt.Sprintf("Tool %q is not available during context gathering.", toolCall.Name)),
			IsError:    true,
		}, false, false, nil
	}
}

func applyContextGatherReferenceChange(
	toolCall llm.ToolCall,
	change func([]string) (contextGatherReferenceState, error),
) (contextGatherReferenceState, error) {
	var args contextGatherReferenceArgs
	if err := json.Unmarshal([]byte(llm.RepairJson(toolCall.Arguments)), &args); err != nil {
		return contextGatherReferenceState{}, fmt.Errorf("invalid %s arguments: %w", toolCall.Name, err)
	}
	return change(args.References)
}

func contextGatherControlResult(toolCall llm.ToolCall, state contextGatherReferenceState, err error) llm2.ToolResultBlock {
	if err != nil {
		return llm2.ToolResultBlock{
			Name:       toolCall.Name,
			ToolCallId: toolCall.Id,
			Content:    llm2.TextContentBlocks(err.Error()),
			IsError:    true,
		}
	}
	return llm2.ToolResultBlock{
		Name:       toolCall.Name,
		ToolCallId: toolCall.Id,
		Content:    llm2.TextContentBlocks(formatContextGatherReferenceState(state)),
	}
}

func renderContextGatherPrompt(dCtx DevContext, promptInfo PromptInfo, forgettingEnabled bool, environmentContext string) (string, error) {
	var taskContext string
	codeContext := ""
	switch info := promptInfo.(type) {
	case InitialCodeInfo:
		codeContext = info.CodeContext
		taskContext = fmt.Sprintf(`# Task Requirements

%s`, info.Requirements)
	case InitialDevStepInfo:
		codeContext = info.CodeContext
		taskContext = fmt.Sprintf(`# Complete Task Requirements

%s

# Current Plan Execution

%s

# Current Development Step

Step: %s
Title: %s
Type: %s

%s

Completion analysis: %s`,
			info.Requirements,
			info.PlanExecution.String(),
			info.Step.StepNumber,
			info.Step.Title,
			info.Step.Type,
			info.Step.Definition,
			info.Step.CompletionAnalysis,
		)
	default:
		return "", fmt.Errorf("unsupported context gathering prompt type %q", promptInfo.GetType())
	}

	if hints := dCtx.RepoConfig.EditCode.Hints; hints != "" {
		taskContext += fmt.Sprintf("\n\n# Repository Guidance\n\n%s", hints)
	}
	if environmentContext != "" {
		taskContext += fmt.Sprintf("\n\nEnvironment: %s", environmentContext)
	}
	if codeContext != "" {
		taskContext += fmt.Sprintf("\n\n%s\n%s\n%s", startInitialCodeContext, codeContext, endInitialCodeContext)
	}

	forgettingInstructions := ""
	if forgettingEnabled {
		forgettingInstructions = fmt.Sprintf(`
Each completed repository-context tool result includes a short reference such as %s1. Preferentially use the %s tool to schedule exploration that is not needed for coding for omission from the handoff. Use %s if an omitted exchange becomes relevant again.
`, contextGatherReferencePrefix, contextGatherForgetTool.Name, contextGatherRememberTool.Name)
	}

	return strings.TrimSpace(fmt.Sprintf(`You are the repository-context gathering phase for a software development task.

Explore the repository thoroughly enough that the coding agent can begin implementation without repeating the same discovery work. Gather all repository context needed to understand the existing behavior, relevant symbols, call sites, data structures, tests, and conventions.

Use only the provided read-only repository exploration tools. Do not attempt to edit files, run commands, change branches, or implement the task during this phase.%s
Assistant prose and ordinary responses do not end this phase. Once enough context has been gathered and coding or command execution should begin, call the zero-argument %s tool. If genuinely blocked on information only the user can provide, call %s; in this phase that also transitions directly to coding without creating a user request.

%s`, forgettingInstructions, contextGatherReadyTool.Name, getHelpOrInputTool.Name, taskContext)), nil
}
