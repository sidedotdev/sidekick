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
