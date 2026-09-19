package dev

import (
	"testing"

	"sidekick/common"
	"sidekick/persisted_ai"

	"github.com/stretchr/testify/require"
)

func TestVerifierSessionHistoryReuse(t *testing.T) {
	t.Parallel()

	for _, reuse := range []bool{false, true} {
		name := "discard"
		if reuse {
			name = "reuse"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			session := verifierSession{}
			first := session.historyForReview(reuse, "flow", "workspace")
			first.History.Append(common.ChatMessage{
				Role: "user", Content: "previous review",
			})
			second := session.historyForReview(reuse, "flow", "workspace")
			require.IsType(t, &persisted_ai.Llm2ChatHistory{}, second.History)
			if reuse {
				require.Same(t, first, second)
				require.Equal(t, 1, second.Len())
			} else {
				require.NotSame(t, first, second)
				require.Zero(t, second.Len())
				require.Nil(t, session.history)
			}
		})
	}
}

func TestVerifierSessionReplacementPreservesIDs(t *testing.T) {
	t.Parallel()

	session := verifierSession{}
	require.Equal(t, "1", session.index.identify("tool:old"))
	first := session.historyForReview(true, "flow", "workspace")
	first.History.Append(common.ChatMessage{
		Role: "user", Content: "review of discarded coding history",
	})

	session.resetHistory()
	second := session.historyForReview(true, "flow", "workspace")
	require.NotSame(t, first, second)
	require.Zero(t, second.Len())
	require.Equal(t, "2", session.index.identify("tool:new"))
	require.Equal(t, "1", session.index.identify("tool:old"))

	current := verifierChatHistory{Allocated: session.index.Next}
	_, err := current.Lookup("01")
	require.ErrorContains(t, err, "no longer available")
}

func TestVerifierSessionDisablingReuseDropsRetainedHistory(t *testing.T) {
	t.Parallel()

	session := verifierSession{}
	first := session.historyForReview(true, "flow", "workspace")
	first.History.Append(common.ChatMessage{
		Role: "user", Content: "previous review",
	})
	discarded := session.historyForReview(false, "flow", "workspace")
	require.Zero(t, discarded.Len())
	require.Nil(t, session.history)

	next := session.historyForReview(true, "flow", "workspace")
	require.NotSame(t, first, next)
	require.NotSame(t, discarded, next)
	require.Zero(t, next.Len())
}
