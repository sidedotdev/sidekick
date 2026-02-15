package persisted_ai

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"sidekick/coding/tree_sitter"
	"sidekick/common"
	"sidekick/llm2"

	"github.com/rs/zerolog/log"
	"github.com/segmentio/ksuid"
)

// ChatHistoryActivities provides activities for managing chat history with KV storage.
type ChatHistoryActivities struct {
	Storage common.KeyValueStorage
}

// ManageV3 manages chat history using the llm2 message format with KV storage.
// It hydrates the history, runs management logic, and persists the result.
// It preserves refs for unchanged messages to avoid regenerating KV block IDs.
func (ca *ChatHistoryActivities) ManageV3(
	ctx context.Context,
	chatHistory *ChatHistoryContainer,
	workspaceId string,
	maxLength int,
) (*ChatHistoryContainer, error) {
	llm2History, ok := chatHistory.History.(*Llm2ChatHistory)
	if !ok {
		return nil, fmt.Errorf("ManageV3 requires Llm2ChatHistory, got %T", chatHistory.History)
	}

	if err := llm2History.Hydrate(ctx, ca.Storage); err != nil {
		return nil, fmt.Errorf("failed to hydrate chat history: %w", err)
	}

	// Snapshot original state for ref preservation
	originalRefs := llm2History.Refs()
	originalMessages := deepCopyMessages(llm2History.Llm2Messages())

	messages := llm2History.Llm2Messages()
	managedMessages, err := ca.ManageLlm2ChatHistory(messages, maxLength)
	if err != nil {
		return nil, fmt.Errorf("failed to manage chat history: %w", err)
	}

	// Apply cache-control breakpoints (mirrors legacy behavior)
	applyCacheControlBreakpointsLlm2(managedMessages)

	// Preserve refs for unchanged messages, compute changed indices
	newRefs, changedIndices := preserveRefsForUnchangedMessages(
		managedMessages,
		originalMessages,
		originalRefs,
	)

	// Apply results using SetMessages (which resets refs/unpersisted)
	llm2History.SetMessages(managedMessages)
	// Restore preserved refs
	llm2History.SetRefs(newRefs)
	// Mark history as hydrated since we have the messages in memory
	llm2History.SetHydratedWithMessages(managedMessages)
	// Narrow persistence to only changed messages
	llm2History.SetUnpersisted(changedIndices)

	if err := llm2History.Persist(ctx, ca.Storage, NewKsuidGenerator()); err != nil {
		return nil, fmt.Errorf("failed to persist chat history: %w", err)
	}

	return chatHistory, nil
}

// AppendMessageInput is the input for the AppendMessage activity.
type AppendMessageInput struct {
	FlowId      string
	WorkspaceId string
	Message     llm2.Message
}

// AppendMessage persists a single message to KV storage and returns its ref.
func (ca *ChatHistoryActivities) AppendMessage(
	ctx context.Context,
	input AppendMessageInput,
) (*MessageRef, error) {
	blockIds := make([]string, len(input.Message.Content))
	storageValues := make(map[string][]byte)

	for i, block := range input.Message.Content {
		blockId := ksuid.New().String()
		blockIds[i] = blockId
		blockBytes, err := json.Marshal(block)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal content block: %w", err)
		}
		storageValues[StorageKey(input.FlowId, blockId)] = blockBytes
	}

	if len(storageValues) > 0 {
		if err := ca.Storage.MSetRaw(ctx, input.WorkspaceId, storageValues); err != nil {
			return nil, fmt.Errorf("failed to persist message blocks: %w", err)
		}
	}

	ref := &MessageRef{
		BlockIds: blockIds,
		Role:     string(input.Message.Role),
	}
	return ref, nil
}

// ExtractVisibleCodeBlocks hydrates the chat history and extracts code blocks
// from non-assistant messages for edit-block visibility tracking.
func (ca *ChatHistoryActivities) ExtractVisibleCodeBlocks(
	ctx context.Context,
	chatHistory *ChatHistoryContainer,
) ([]tree_sitter.CodeBlock, error) {
	if err := chatHistory.Hydrate(ctx, ca.Storage); err != nil {
		return nil, fmt.Errorf("failed to hydrate chat history: %w", err)
	}

	var codeBlocks []tree_sitter.CodeBlock
	for _, msg := range chatHistory.Messages() {
		if msg.GetRole() == string(common.ChatMessageRoleAssistant) {
			continue
		}
		content := msg.GetContentString()
		symDefBlocks, err := tree_sitter.ExtractSymbolDefinitionBlocks(content)
		if err != nil {
			log.Warn().Err(err).Msg("failed to extract symbol definition blocks")
		}
		searchBlocks, err := tree_sitter.ExtractSearchCodeBlocks(content)
		if err != nil {
			log.Warn().Err(err).Msg("failed to extract search code blocks")
		}
		gitDiffBlocks, err := tree_sitter.ExtractGitDiffCodeBlocks(content)
		if err != nil {
			log.Warn().Err(err).Msg("failed to extract git diff code blocks")
		}
		codeBlocks = append(codeBlocks, symDefBlocks...)
		codeBlocks = append(codeBlocks, searchBlocks...)
		codeBlocks = append(codeBlocks, gitDiffBlocks...)
	}
	return codeBlocks, nil
}

// applyCacheControlBreakpointsLlm2 sets CacheControl on llm2 messages.
// It clears all existing CacheControl values and sets "ephemeral" on the
// first and last messages (mirroring the current legacy behavior).
func applyCacheControlBreakpointsLlm2(messages []llm2.Message) {
	if len(messages) == 0 {
		return
	}

	// Clear all existing CacheControl values
	for i := range messages {
		for j := range messages[i].Content {
			messages[i].Content[j].CacheControl = ""
		}
	}

	// Set ephemeral on all blocks of the first message
	for j := range messages[0].Content {
		messages[0].Content[j].CacheControl = "ephemeral"
	}

	// Set ephemeral on all blocks of the last message (if different from first)
	lastIdx := len(messages) - 1
	if lastIdx > 0 {
		for j := range messages[lastIdx].Content {
			messages[lastIdx].Content[j].CacheControl = "ephemeral"
		}
	}
}

// deepCopyMessages creates a deep copy of a message slice for equality comparison.
func deepCopyMessages(messages []llm2.Message) []llm2.Message {
	result := make([]llm2.Message, len(messages))
	for i, msg := range messages {
		result[i] = llm2.Message{
			Role:    msg.Role,
			Content: make([]llm2.ContentBlock, len(msg.Content)),
		}
		copy(result[i].Content, msg.Content)
	}
	return result
}

// preserveRefsForUnchangedMessages matches managed messages to original messages
// and reuses refs for unchanged messages. Returns the new refs slice and indices
// of messages that need to be persisted.
func preserveRefsForUnchangedMessages(
	managedMessages []llm2.Message,
	originalMessages []llm2.Message,
	originalRefs []MessageRef,
) ([]MessageRef, []int) {
	newRefs := make([]MessageRef, len(managedMessages))
	var changedIndices []int

	// Track which original messages have been used (to avoid reusing the same ref twice)
	used := make([]bool, len(originalMessages))

	for i, managed := range managedMessages {
		found := false
		// Reverse scan to prefer newest matches
		for j := len(originalMessages) - 1; j >= 0; j-- {
			if used[j] {
				continue
			}
			if j < len(originalRefs) && messagesEqual(managed, originalMessages[j]) {
				newRefs[i] = originalRefs[j]
				used[j] = true
				found = true
				break
			}
		}
		if !found {
			// No match found, this message needs to be persisted
			changedIndices = append(changedIndices, i)
		}
	}

	return newRefs, changedIndices
}

// messagesEqual performs deep equality comparison of two messages.
func messagesEqual(a, b llm2.Message) bool {
	return reflect.DeepEqual(a, b)
}
