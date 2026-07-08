package dev

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"sidekick/common"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/persisted_ai"

	"github.com/invopop/jsonschema"
	"go.temporal.io/sdk/workflow"
)

const (
	advisorProceedToolName = "advisor_proceed"
	advisorGuideToolName   = "advisor_guide"
)

// defaultAdvisorEveryNTurns is the cadence used when the "advising" use case
// does not configure auto_iterations. It sits within the desired 5-10 turn range.
const defaultAdvisorEveryNTurns = 7

// advisorDefaultModel is the Anthropic model used for advising when the
// "advising" use case is not explicitly configured.
const advisorDefaultModel = "claude-opus-4-8"

// advisorMaxRecentMessages caps how many trailing executor messages are
// summarized into the advisor prompt each turn.
const advisorMaxRecentMessages = 20

type AdvisorProceedToolArgs struct{}

type AdvisorGuideToolArgs struct {
	Feedback string `json:"feedback" jsonschema:"description=Specific feedback for the executor: a mistake it made\\, a misunderstanding to correct\\, or concrete direction on what to do next. Keep it terse and actionable."`
}

var advisorProceedTool = llm.Tool{
	Name:        advisorProceedToolName,
	Description: "Take no action this turn. Use when the executor is on track and needs no interjection.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&AdvisorProceedToolArgs{}),
}

var advisorGuideTool = llm.Tool{
	Name:        advisorGuideToolName,
	Description: "Send specific guidance to the executor about mistakes, misunderstandings, or what to do next. Use only when a concrete correction or direction is warranted.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&AdvisorGuideToolArgs{}),
}

// Advisor runs in parallel with an executor loop, periodically reviewing the
// executor's conversation and optionally interjecting. It maintains its own
// chat history, distinct from the executor's.
type Advisor struct {
	Enabled     bool
	EveryNTurns int
	ModelConfig common.ModelConfig
	ChatHistory *persisted_ai.ChatHistoryContainer

	turnsSinceAdvice int
}

// newAdvisor constructs an Advisor, resolving the advising model (falling back
// to the default Anthropic opus model when the "advising" use case is
// unconfigured) and the advising cadence from repo config.
func newAdvisor(dCtx DevContext, enabled bool) *Advisor {
	_, isDefault := dCtx.LLMConfig.GetModelConfig(common.AdvisingKey, 0)
	var modelConfig common.ModelConfig
	if isDefault {
		modelConfig = common.ModelConfig{
			Provider: string(common.AnthropicChatProvider),
			Model:    advisorDefaultModel,
		}
	} else {
		modelConfig = dCtx.GetModelConfig(common.AdvisingKey, 0, "default")
	}

	everyN := defaultAdvisorEveryNTurns
	if cfg, ok := dCtx.RepoConfig.AgentConfig[common.AdvisingKey]; ok && cfg.AutoIterations > 0 {
		everyN = cfg.AutoIterations
	}

	return &Advisor{
		Enabled:     enabled,
		EveryNTurns: everyN,
		ModelConfig: modelConfig,
		ChatHistory: NewVersionedChatHistory(dCtx, dCtx.WorkspaceId),
	}
}

// shouldAdvise increments the turn counter and reports whether the advisor
// should run this turn, resetting the counter when it does.
func (a *Advisor) shouldAdvise() bool {
	a.turnsSinceAdvice++
	if !a.Enabled {
		return false
	}
	if a.turnsSinceAdvice < a.EveryNTurns {
		return false
	}
	a.turnsSinceAdvice = 0
	return true
}

// MaybeAdvise runs one advisor turn when the cadence is due. It reviews the
// executor's recent history, forces a single tool call over the advisor tools
// plus the executor's own tools, and applies the result: proceed is a no-op,
// guide appends feedback to the executor history, and an executor tool call is
// injected into the executor history while the advisor history records a
// synthetic success.
func (a *Advisor) MaybeAdvise(dCtx DevContext, executorHistory *persisted_ai.ChatHistoryContainer, executorTools []*llm.Tool) error {
	if !a.shouldAdvise() {
		return nil
	}

	if a.ChatHistory == nil {
		a.ChatHistory = NewVersionedChatHistory(dCtx, dCtx.WorkspaceId)
	}

	if a.ChatHistory.Len() == 0 {
		if err := AppendChatHistory(dCtx.ExecContext, a.ChatHistory, llm.ChatMessage{
			Role:    llm.ChatMessageRoleSystem,
			Content: RenderPrompt(AdvisorSystem, nil),
		}); err != nil {
			return fmt.Errorf("advisor: failed to seed system prompt: %w", err)
		}
	}

	if llm2Hist, ok := a.ChatHistory.History.(*persisted_ai.Llm2ChatHistory); ok {
		ref, err := summarizeExecutorHistory(dCtx, executorHistory, llm2Hist.FlowId(), llm2Hist.WorkspaceId())
		if err != nil {
			return fmt.Errorf("advisor: failed to summarize executor history: %w", err)
		}
		llm2Hist.AppendRef(*ref)
	} else {
		turnPrompt := RenderPrompt(AdvisorTurn, map[string]string{
			"recentHistory": legacyExecutorTranscript(executorHistory),
		})
		if err := AppendChatHistory(dCtx.ExecContext, a.ChatHistory, llm.ChatMessage{
			Role:    llm.ChatMessageRoleUser,
			Content: turnPrompt,
		}); err != nil {
			return fmt.Errorf("advisor: failed to append turn prompt: %w", err)
		}
	}

	tools := append([]*llm.Tool{&advisorProceedTool, &advisorGuideTool}, executorTools...)
	actionCtx := dCtx.ExecContext.NewActionContext("generate.advise")
	response, err := persisted_ai.ForceParallelToolCall(actionCtx, a.ModelConfig, a.ChatHistory, tools...)
	if err != nil {
		// Treat a refusal like "proceed": skip advising this turn so the
		// executor continues unchanged rather than failing the workflow.
		if errors.Is(err, persisted_ai.ErrLLMRefusal) {
			return nil
		}
		return fmt.Errorf("advisor: force tool call failed: %w", err)
	}

	respMsg := response.GetMessage()
	injectedToolCalls, err := applyAdvisorToolCalls(dCtx.ExecContext, executorHistory, a.ChatHistory, respMsg.GetContentString(), respMsg.GetToolCalls())
	if err != nil {
		return err
	}

	// Tool calls the advisor puppeted into the executor history leave dangling
	// tool_use blocks. They must be resolved with matching tool results before
	// the executor's LLM runs again, otherwise the provider rejects the request.
	if len(injectedToolCalls) > 0 {
		if _, err := handleToolCalls(dCtx, injectedToolCalls, executorHistory, nil); err != nil {
			return fmt.Errorf("advisor: failed to handle injected executor tool calls: %w", err)
		}
	}
	return nil
}

// executorPuppetRegexp matches the segments of the advisor's output that it
// explicitly marks to be spoken as the executor via <executor>...</executor>
// delimiters.
var executorPuppetRegexp = regexp.MustCompile(`(?s)<executor>(.*?)</executor>`)

// executorPuppetContent extracts the delimited portions of the advisor's output
// that should be injected as the executor's own assistant content alongside the
// puppeted tool calls. Text outside the delimiters is the advisor's private
// reasoning and is intentionally dropped from the executor history.
func executorPuppetContent(advisorContent string) string {
	matches := executorPuppetRegexp.FindAllStringSubmatch(advisorContent, -1)
	parts := make([]string, 0, len(matches))
	for _, m := range matches {
		if s := strings.TrimSpace(m[1]); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

// applyAdvisorToolCalls routes each advisor tool call to the appropriate side
// effect and records the corresponding tool result in the advisor history.
func applyAdvisorToolCalls(eCtx flow_action.ExecContext, executorHistory, advisorHistory *persisted_ai.ChatHistoryContainer, advisorContent string, toolCalls []llm.ToolCall) ([]llm.ToolCall, error) {
	var executorToolCalls []llm.ToolCall
	for _, tc := range toolCalls {
		switch tc.Name {
		case advisorProceedToolName:
			if err := addToolCallResponse(eCtx, advisorHistory, llm2.ToolResultBlock{
				Name:       tc.Name,
				ToolCallId: tc.Id,
				Content:    llm2.TextContentBlocks("Acknowledged; no interjection this turn."),
			}); err != nil {
				return nil, err
			}
		case advisorGuideToolName:
			var args AdvisorGuideToolArgs
			feedback := ""
			if err := json.Unmarshal([]byte(tc.Arguments), &args); err == nil {
				feedback = strings.TrimSpace(args.Feedback)
			}
			if feedback != "" {
				if err := AppendChatHistory(eCtx, executorHistory, llm.ChatMessage{
					Role:    llm.ChatMessageRoleUser,
					Content: "Guidance from your advisor: " + feedback,
				}); err != nil {
					return nil, err
				}
			}
			if err := addToolCallResponse(eCtx, advisorHistory, llm2.ToolResultBlock{
				Name:       tc.Name,
				ToolCallId: tc.Id,
				Content:    llm2.TextContentBlocks("Guidance delivered to executor."),
			}); err != nil {
				return nil, err
			}
		default:
			executorToolCalls = append(executorToolCalls, tc)
			if err := addToolCallResponse(eCtx, advisorHistory, llm2.ToolResultBlock{
				Name:       tc.Name,
				ToolCallId: tc.Id,
				Content:    llm2.TextContentBlocks("Submitted to executor."),
			}); err != nil {
				return nil, err
			}
		}
	}

	if len(executorToolCalls) > 0 {
		if err := AppendChatHistory(eCtx, executorHistory, llm.ChatMessage{
			Role:      llm.ChatMessageRoleAssistant,
			Content:   executorPuppetContent(advisorContent),
			ToolCalls: executorToolCalls,
		}); err != nil {
			return nil, err
		}
	}
	return executorToolCalls, nil
}

// summarizeExecutorHistory renders the trailing executor messages into the
// advisor turn prompt, persists it, and returns a ref to the persisted content.
// The in-workflow chat history is refs-only (not hydrated), so rendering and
// persistence happen inside an activity that hydrates from storage.
func summarizeExecutorHistory(dCtx DevContext, executorHistory *persisted_ai.ChatHistoryContainer, flowId, workspaceId string) (*persisted_ai.MessageRef, error) {
	var aa *AdvisorActivities
	var ref *persisted_ai.MessageRef
	err := workflow.ExecuteActivity(dCtx, aa.SummarizeExecutorHistoryActivity, SummarizeExecutorHistoryInput{
		ExecutorHistory:   executorHistory,
		MaxRecentMessages: advisorMaxRecentMessages,
		FlowId:            flowId,
		WorkspaceId:       workspaceId,
	}).Get(dCtx, &ref)
	if err != nil {
		return nil, fmt.Errorf("failed to summarize executor history: %w", err)
	}
	return ref, nil
}

// legacyExecutorTranscript renders the trailing messages of a legacy history
// directly, since legacy content lives in-memory in the workflow and does not
// require hydration.
func legacyExecutorTranscript(history *persisted_ai.ChatHistoryContainer) string {
	if history == nil {
		return "(no executor history yet)"
	}
	if rendered := renderExecutorTranscript(history.Messages(), advisorMaxRecentMessages); rendered != "" {
		return rendered
	}
	return "(no executor history yet)"
}
