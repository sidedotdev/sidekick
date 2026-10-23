package dev

import (
	"context"
	"fmt"
	"strings"

	"sidekick/common"
	"sidekick/llm2"
	"sidekick/persisted_ai"
)

// AdvisorActivities holds dependencies for advisor-specific activities.
type AdvisorActivities struct {
	Storage common.KeyValueStorage
}

// SummarizeExecutorHistoryInput is the activity input for rendering the advisor
// turn prompt from the executor's chat history and persisting it to KV.
type SummarizeExecutorHistoryInput struct {
	ExecutorHistory *persisted_ai.ChatHistoryContainer `json:"executorHistory"`
	// FirstUnreviewedMessage is the index of the first executor message not
	// covered by an earlier advisor turn prompt.
	FirstUnreviewedMessage int    `json:"firstUnreviewedMessage"`
	FlowId                 string `json:"flowId"`
	WorkspaceId            string `json:"workspaceId"`
}

// SummarizeExecutorHistoryActivity hydrates the executor chat history, renders
// the advisor turn prompt from its unreviewed messages, persists it as a
// message, and returns a ref to it. Hydration happens inside the activity
// because the in-workflow chat history is refs-only (not hydrated).
func (a *AdvisorActivities) SummarizeExecutorHistoryActivity(ctx context.Context, input SummarizeExecutorHistoryInput) (*persisted_ai.MessageRef, error) {
	return a.summarizeExecutorHistory(ctx, input, true)
}

// SummarizeExecutorRecentHistoryActivity omits initial instructions because
// they are persisted separately in the advisor history.
func (a *AdvisorActivities) SummarizeExecutorRecentHistoryActivity(ctx context.Context, input SummarizeExecutorHistoryInput) (*persisted_ai.MessageRef, error) {
	return a.summarizeExecutorHistory(ctx, input, false)
}

func (a *AdvisorActivities) summarizeExecutorHistory(
	ctx context.Context,
	input SummarizeExecutorHistoryInput,
	includeInitialInstructions bool,
) (*persisted_ai.MessageRef, error) {
	var msgs []common.Message
	if input.ExecutorHistory != nil {
		if err := input.ExecutorHistory.Hydrate(ctx, a.Storage); err != nil {
			return nil, fmt.Errorf("failed to hydrate executor history: %w", err)
		}
		msgs = input.ExecutorHistory.Messages()
	}

	blocks := renderAdvisorTurn(msgs, executorTranscriptWindow{
		firstUnreviewed:            input.FirstUnreviewedMessage,
		includeInitialInstructions: includeInitialInstructions,
	})
	cha := &persisted_ai.ChatHistoryActivities{Storage: a.Storage}
	ref, err := cha.AppendMessage(ctx, persisted_ai.AppendMessageInput{
		FlowId:      input.FlowId,
		WorkspaceId: input.WorkspaceId,
		Message:     llm2.Message{Role: llm2.RoleUser, Content: blocks},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to persist advisor turn prompt: %w", err)
	}
	return ref, nil
}

type executorTranscriptWindow struct {
	firstUnreviewed            int
	includeInitialInstructions bool
}

// renderAdvisorTurn renders executor messages no earlier turn prompt covered.
// Always-retained blocks carry metadata so the advisor's history management
// preserves them just as the executor's does.
func renderAdvisorTurn(msgs []common.Message, w executorTranscriptWindow) []llm2.ContentBlock {
	executorMsgs := make([]llm2.Message, len(msgs))
	for i, m := range msgs {
		executorMsgs[i] = toLlm2Message(m)
	}
	start := min(max(w.firstUnreviewed, 0), len(executorMsgs))

	turn := newAdvisorTurnBuilder(executorMsgs)
	turn.text.WriteString(strings.TrimSpace(RenderPrompt(AdvisorTurnHistoryHeader, nil)))
	turn.text.WriteString("\n\n")
	wrote := false
	for i := start; i < len(executorMsgs); i++ {
		if !w.includeInitialInstructions && llm2MessageContextType(executorMsgs[i]) == persisted_ai.ContextTypeInitialInstructions {
			continue
		}
		if turn.writeMessage(i) {
			wrote = true
		}
	}
	if !wrote {
		turn.text.WriteString("(no new executor messages)\n")
	}
	turn.text.WriteString("\n")
	turn.text.WriteString(strings.TrimSpace(RenderPrompt(AdvisorTurn, nil)))
	turn.flush()
	return turn.blocks
}

func toLlm2Message(msg common.Message) llm2.Message {
	switch m := msg.(type) {
	case llm2.Message:
		return m
	case *llm2.Message:
		if m == nil {
			return llm2.Message{}
		}
	}
	return persisted_ai.MessageFromCommon(msg)
}

type executorBlockPosition struct {
	message int
	block   int
}

type advisorTurnBuilder struct {
	executorMsgs    []llm2.Message
	retained        [][]bool
	toolUses        map[string]executorBlockPosition
	toolResults     map[string]executorBlockPosition
	renderedCallIds map[string]bool

	blocks []llm2.ContentBlock
	text   strings.Builder
}

func newAdvisorTurnBuilder(executorMsgs []llm2.Message) *advisorTurnBuilder {
	b := &advisorTurnBuilder{
		executorMsgs:    executorMsgs,
		retained:        persisted_ai.AlwaysRetainedBlocks(executorMsgs),
		toolUses:        make(map[string]executorBlockPosition),
		toolResults:     make(map[string]executorBlockPosition),
		renderedCallIds: make(map[string]bool),
	}
	for i, msg := range executorMsgs {
		for j, block := range msg.Content {
			if block.ToolUse != nil {
				b.toolUses[block.ToolUse.Id] = executorBlockPosition{message: i, block: j}
			}
			if block.ToolResult != nil {
				b.toolResults[block.ToolResult.ToolCallId] = executorBlockPosition{message: i, block: j}
			}
		}
	}
	return b
}

func (b *advisorTurnBuilder) flush() {
	if b.text.Len() == 0 {
		return
	}
	b.blocks = append(b.blocks, llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: b.text.String()})
	b.text.Reset()
}

func (b *advisorTurnBuilder) writeMessage(index int) bool {
	msg := b.executorMsgs[index]
	wrote := false
	var ordinary []llm2.ContentBlock
	writeOrdinary := func() {
		before := b.text.Len()
		writeExecutorMessage(&b.text, llm2.Message{Role: msg.Role, Content: ordinary})
		wrote = wrote || b.text.Len() > before
		ordinary = nil
	}
	for j, block := range msg.Content {
		if !b.retained[index][j] {
			ordinary = append(ordinary, block)
			continue
		}
		writeOrdinary()
		if b.writeRetained(executorBlockPosition{message: index, block: j}) {
			wrote = true
		}
	}
	writeOrdinary()
	return wrote
}

// writeRetained renders an always-retained executor block as its own advisor
// block. Tool-call pairing does not survive rendering to text, so a retained
// result is preceded by its call whenever an earlier turn rendered that call
// before it was retained, keeping e.g. a question together with its answer.
func (b *advisorTurnBuilder) writeRetained(pos executorBlockPosition) bool {
	msg := b.executorMsgs[pos.message]
	block := msg.Content[pos.block]
	if block.ToolResult != nil && !b.renderedCallIds[block.ToolResult.ToolCallId] {
		if callPos, ok := b.toolUses[block.ToolResult.ToolCallId]; ok && b.retained[callPos.message][callPos.block] {
			b.writeRetained(callPos)
		}
	}
	if block.ToolUse != nil {
		if b.renderedCallIds[block.ToolUse.Id] {
			return false
		}
		b.renderedCallIds[block.ToolUse.Id] = true
	}

	var text strings.Builder
	writeExecutorMessage(&text, llm2.Message{Role: msg.Role, Content: []llm2.ContentBlock{block}})
	if text.Len() == 0 {
		return false
	}
	b.flush()
	rendered := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: text.String()}
	b.markRetained(&rendered, block)
	b.blocks = append(b.blocks, rendered)
	return true
}

// markRetained carries over the metadata the executor's history management
// retains the source block by. A tool call retained only through its paired
// result borrows that result's human context type instead.
func (b *advisorTurnBuilder) markRetained(rendered *llm2.ContentBlock, source llm2.ContentBlock) {
	if contextType := persisted_ai.HumanContextType(source); contextType != "" {
		persisted_ai.SetContextType(rendered, contextType)
		return
	}
	if deliverable := persisted_ai.GetDeliverable(source); deliverable != nil {
		persisted_ai.SetDeliverable(rendered, *deliverable)
		return
	}
	if source.ToolUse == nil {
		return
	}
	if resultPos, ok := b.toolResults[source.ToolUse.Id]; ok {
		result := b.executorMsgs[resultPos.message].Content[resultPos.block]
		if contextType := persisted_ai.HumanContextType(result); contextType != "" {
			persisted_ai.SetContextType(rendered, contextType)
		}
	}
}

func writeExecutorMessage(b *strings.Builder, m common.Message) {
	var contentBlocks []llm2.ContentBlock
	switch message := m.(type) {
	case llm2.Message:
		contentBlocks = message.Content
	case *llm2.Message:
		if message != nil {
			contentBlocks = message.Content
		}
	}
	for _, block := range contentBlocks {
		if block.Reasoning == nil {
			continue
		}
		summary := strings.TrimSpace(block.Reasoning.Summary)
		if summary != "" {
			b.WriteString(fmt.Sprintf("[%s] thinking_summary %s\n", m.GetRole(), summary))
		}
	}
	content := strings.TrimSpace(m.GetContentString())
	if content != "" {
		b.WriteString(fmt.Sprintf("[%s] %s\n", m.GetRole(), content))
	}
	for _, tc := range m.GetToolCalls() {
		b.WriteString(fmt.Sprintf("[%s] tool_call %s(%s)\n", m.GetRole(), tc.Name, tc.Arguments))
	}
}

// SeedAdvisorRequirementsInput is the activity input for extracting the
// executor's initial-instructions (requirements) message and persisting it as a
// standalone block for the advisor history.
type SeedAdvisorRequirementsInput struct {
	ExecutorHistory *persisted_ai.ChatHistoryContainer `json:"executorHistory"`
	Provider        string                             `json:"provider"`
	FlowId          string                             `json:"flowId"`
	WorkspaceId     string                             `json:"workspaceId"`
}

// SeedAdvisorRequirementsOutput carries the persisted requirements ref and its
// length, measured with the same accounting used by the retention logic. Ref is
// nil when the executor history has no initial-instructions message.
type SeedAdvisorRequirementsOutput struct {
	Ref    *persisted_ai.MessageRef `json:"ref"`
	Length int                      `json:"length"`
}

// SeedAdvisorRequirementsActivity hydrates the executor chat history, locates
// the message whose content is marked ContextTypeInitialInstructions, persists
// it as a standalone block (preserving the marker), and returns a ref plus the
// message length. Hydration happens inside the activity because the in-workflow
// chat history is refs-only (not hydrated).
func (a *AdvisorActivities) SeedAdvisorRequirementsActivity(ctx context.Context, input SeedAdvisorRequirementsInput) (*SeedAdvisorRequirementsOutput, error) {
	if input.ExecutorHistory == nil {
		return &SeedAdvisorRequirementsOutput{}, nil
	}
	if err := input.ExecutorHistory.Hydrate(ctx, a.Storage); err != nil {
		return nil, fmt.Errorf("failed to hydrate executor history: %w", err)
	}

	for _, msg := range input.ExecutorHistory.Llm2Messages() {
		if llm2MessageContextType(msg) != persisted_ai.ContextTypeInitialInstructions {
			continue
		}
		cha := &persisted_ai.ChatHistoryActivities{Storage: a.Storage}
		ref, err := cha.AppendMessage(ctx, persisted_ai.AppendMessageInput{
			FlowId:      input.FlowId,
			WorkspaceId: input.WorkspaceId,
			Message:     msg,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to persist advisor requirements: %w", err)
		}
		return &SeedAdvisorRequirementsOutput{
			Ref:    ref,
			Length: persisted_ai.Llm2MessageLength(input.Provider, msg),
		}, nil
	}
	return &SeedAdvisorRequirementsOutput{}, nil
}

// llm2MessageContextType returns the context type of the first content block
// that carries one, matching the accounting used by the retention logic.
func llm2MessageContextType(msg llm2.Message) string {
	for _, block := range msg.Content {
		if ct := persisted_ai.GetContextType(block); ct != "" {
			return ct
		}
	}
	return ""
}
