package dev

import (
	"context"
	"encoding/json"
	"testing"

	"sidekick/common"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/srv/sqlite"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSummarizeExecutorHistoryActivity_NonHydrated reproduces the advisor bug
// where summarizing a refs-only (non-hydrated) executor history panicked with
// "cannot get messages from non-hydrated Llm2ChatHistory". The activity must
// hydrate from storage before rendering the transcript.
func TestSummarizeExecutorHistoryActivity_NonHydrated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	storage := sqlite.NewTestSqliteStorage(t, "advisor")
	const flowId = "flow_test"
	const workspaceId = "ws_test"

	// Persist an executor history, then reconstruct it as refs-only to mirror
	// the in-workflow state that triggered the panic.
	hydrated := persisted_ai.NewLlm2ChatHistory(flowId, workspaceId)
	hydrated.Append(common.ChatMessage{Role: common.ChatMessageRoleUser, Content: "please implement the feature"})
	hydrated.Append(common.ChatMessage{Role: common.ChatMessageRoleAssistant, Content: "working on it now"})
	require.NoError(t, hydrated.Persist(ctx, storage, persisted_ai.NewKsuidGenerator()))

	refsOnly := persisted_ai.NewLlm2ChatHistory(flowId, workspaceId)
	refsOnly.SetRefs(hydrated.Refs())
	require.False(t, refsOnly.IsHydrated())

	executorHistory := &persisted_ai.ChatHistoryContainer{History: refsOnly}

	a := &AdvisorActivities{Storage: storage}
	ref, err := a.SummarizeExecutorHistoryActivity(ctx, SummarizeExecutorHistoryInput{
		ExecutorHistory:   executorHistory,
		MaxRecentMessages: advisorMaxRecentMessages,
		FlowId:            flowId,
		WorkspaceId:       workspaceId,
	})
	require.NoError(t, err)
	require.NotNil(t, ref)
	require.Len(t, ref.BlockKeys, 1)

	// The persisted turn prompt should embed the transcript rendered from the
	// previously-persisted (non-hydrated) executor messages.
	values, err := storage.MGet(ctx, workspaceId, []string{persisted_ai.StorageKey(flowId, ref.BlockKeys[0])})
	require.NoError(t, err)
	require.Len(t, values, 1)
	require.NotNil(t, values[0])
	var block llm2.ContentBlock
	require.NoError(t, json.Unmarshal(values[0], &block))
	assert.Contains(t, block.Text, "please implement the feature")
	assert.Contains(t, block.Text, "working on it now")
}

// TestSummarizeExecutorHistoryActivity_NilHistory ensures a nil executor
// history yields a valid turn prompt ref instead of erroring.
func TestSummarizeExecutorHistoryActivity_NilHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	storage := sqlite.NewTestSqliteStorage(t, "advisor")

	a := &AdvisorActivities{Storage: storage}
	ref, err := a.SummarizeExecutorHistoryActivity(ctx, SummarizeExecutorHistoryInput{
		ExecutorHistory:   nil,
		MaxRecentMessages: advisorMaxRecentMessages,
		FlowId:            "flow_test",
		WorkspaceId:       "ws_test",
	})
	require.NoError(t, err)
	require.NotNil(t, ref)
	require.Len(t, ref.BlockKeys, 1)
}

// TestSeedAdvisorRequirementsActivity_ExtractsInitialInstructions verifies the
// activity hydrates a refs-only executor history, extracts only the message
// marked ContextTypeInitialInstructions, persists it (preserving the marker),
// and reports its retention-accounting length.
func TestSeedAdvisorRequirementsActivity_ExtractsInitialInstructions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	storage := sqlite.NewTestSqliteStorage(t, "advisor")
	const flowId = "flow_seed"
	const workspaceId = "ws_test"

	hydrated := persisted_ai.NewLlm2ChatHistory(flowId, workspaceId)
	hydrated.Append(common.ChatMessage{
		Role:        common.ChatMessageRoleUser,
		Content:     "implement the login feature with these requirements",
		ContextType: ContextTypeInitialInstructions,
	})
	hydrated.Append(common.ChatMessage{Role: common.ChatMessageRoleAssistant, Content: "working on it now"})
	require.NoError(t, hydrated.Persist(ctx, storage, persisted_ai.NewKsuidGenerator()))

	refsOnly := persisted_ai.NewLlm2ChatHistory(flowId, workspaceId)
	refsOnly.SetRefs(hydrated.Refs())
	require.False(t, refsOnly.IsHydrated())
	executorHistory := &persisted_ai.ChatHistoryContainer{History: refsOnly}

	a := &AdvisorActivities{Storage: storage}
	out, err := a.SeedAdvisorRequirementsActivity(ctx, SeedAdvisorRequirementsInput{
		ExecutorHistory: executorHistory,
		Provider:        "openai",
		FlowId:          flowId,
		WorkspaceId:     workspaceId,
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.Ref)
	require.NotEmpty(t, out.Ref.BlockKeys)
	assert.Greater(t, out.Length, 0)

	values, err := storage.MGet(ctx, workspaceId, []string{persisted_ai.StorageKey(flowId, out.Ref.BlockKeys[0])})
	require.NoError(t, err)
	require.Len(t, values, 1)
	require.NotNil(t, values[0])
	var block llm2.ContentBlock
	require.NoError(t, json.Unmarshal(values[0], &block))
	assert.Equal(t, ContextTypeInitialInstructions, persisted_ai.GetContextType(block))
	assert.Contains(t, block.Text, "implement the login feature")
	assert.NotContains(t, block.Text, "working on it now")
}

// TestSeedAdvisorRequirementsActivity_NoInitialInstructions ensures that when
// the executor history lacks an initial-instructions message, seeding is
// skipped: no ref is returned and the length is zero.
func TestSeedAdvisorRequirementsActivity_NoInitialInstructions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	storage := sqlite.NewTestSqliteStorage(t, "advisor")
	const flowId = "flow_seed_none"
	const workspaceId = "ws_test"

	hydrated := persisted_ai.NewLlm2ChatHistory(flowId, workspaceId)
	hydrated.Append(common.ChatMessage{Role: common.ChatMessageRoleUser, Content: "no marker here"})
	require.NoError(t, hydrated.Persist(ctx, storage, persisted_ai.NewKsuidGenerator()))

	refsOnly := persisted_ai.NewLlm2ChatHistory(flowId, workspaceId)
	refsOnly.SetRefs(hydrated.Refs())
	executorHistory := &persisted_ai.ChatHistoryContainer{History: refsOnly}

	a := &AdvisorActivities{Storage: storage}
	out, err := a.SeedAdvisorRequirementsActivity(ctx, SeedAdvisorRequirementsInput{
		ExecutorHistory: executorHistory,
		Provider:        "openai",
		FlowId:          flowId,
		WorkspaceId:     workspaceId,
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Nil(t, out.Ref)
	assert.Equal(t, 0, out.Length)
}

// TestSeedAdvisorRequirementsActivity_NilHistory ensures a nil executor history
// is handled gracefully without error and without seeding a requirements block.
func TestSeedAdvisorRequirementsActivity_NilHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	storage := sqlite.NewTestSqliteStorage(t, "advisor")

	a := &AdvisorActivities{Storage: storage}
	out, err := a.SeedAdvisorRequirementsActivity(ctx, SeedAdvisorRequirementsInput{
		ExecutorHistory: nil,
		FlowId:          "flow",
		WorkspaceId:     "ws",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Nil(t, out.Ref)
}
