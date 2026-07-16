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

// GatherContextForCoding explores the repository in a visible, read-only
// localization subflow and leaves only retained context exchanges in history.
func GatherContextForCoding(dCtx DevContext, chatHistory *persisted_ai.ChatHistoryContainer, promptInfo PromptInfo) error {
	if chatHistory == nil {
		return fmt.Errorf("context gathering chat history is required")
	}

	return RunSubflowWithoutResult(dCtx, "gather_context", "Gather Context", func(_ domain.Subflow) error {
		return gatherContextForCodingSubflow(dCtx, chatHistory, promptInfo)
	})
}

func gatherContextForCodingSubflow(dCtx DevContext, chatHistory *persisted_ai.ChatHistoryContainer, promptInfo PromptInfo) error {
	forgettingEnabled := fflag.IsEnabled(dCtx, fflag.ContextGatheringForget)
	tracker := newContextGatherHistory(chatHistory, forgettingEnabled)

	prompt, err := renderContextGatherPrompt(promptInfo, forgettingEnabled)
	if err != nil {
		return err
	}
	if err := AppendChatHistory(dCtx.ExecContext, chatHistory, llm.ChatMessage{
		Role:         llm.ChatMessageRoleUser,
		Content:      prompt,
		CacheControl: "ephemeral",
		ContextType:  ContextTypeInitialInstructions,
	}); err != nil {
		return fmt.Errorf("failed to append context gathering instructions: %w", err)
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
			return fmt.Errorf("failed to gather repository context: %w", err)
		}
		if dCtx.GlobalState != nil && dCtx.GlobalState.Paused {
			continue
		}

		message := response.GetMessage()
		if err := AppendChatHistory(dCtx.ExecContext, chatHistory, message); err != nil {
			return fmt.Errorf("failed to append context gathering response: %w", err)
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
				return err
			}
			terminal = terminal || toolTerminal

			if contextResult {
				if _, err := tracker.registerContextToolResult(&result); err != nil {
					return fmt.Errorf("failed to register context result: %w", err)
				}
			}
			if err := appendToolCallResult(dCtx.ExecContext, chatHistory, result, nil); err != nil {
				return fmt.Errorf("failed to append context gathering tool result: %w", err)
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
				return err
			}
			return persistFilteredContextGatherHistory(dCtx, chatHistory, tracker.llm2Transcript)
		}
	}
}

func persistFilteredContextGatherHistory(
	dCtx DevContext,
	chatHistory *persisted_ai.ChatHistoryContainer,
	messages []llm2.Message,
) error {
	history, ok := chatHistory.History.(*persisted_ai.Llm2ChatHistory)
	if !ok {
		return nil
	}

	chatHistory.History = persisted_ai.NewLlm2ChatHistory(history.FlowId(), history.WorkspaceId())
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

func renderContextGatherPrompt(promptInfo PromptInfo, forgettingEnabled bool) (string, error) {
	var taskContext string
	switch info := promptInfo.(type) {
	case InitialCodeInfo:
		taskContext = fmt.Sprintf(`# Task Requirements

%s`, info.Requirements)
	case InitialDevStepInfo:
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
