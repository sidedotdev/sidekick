package persisted_ai

import (
	"fmt"
	"testing"

	"sidekick/llm2"

	"github.com/stretchr/testify/require"
)

func TestCacheControlBreakpointsAtMessageBoundaries(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		sizes  []int
		marked [][2]int
	}{
		{name: "empty"},
		{name: "single message", sizes: []int{6}, marked: [][2]int{{0, 5}}},
		{name: "multi block boundaries", sizes: []int{3, 2, 3}, marked: [][2]int{{0, 2}, {2, 2}}},
		{name: "empty boundaries", sizes: []int{0, 2, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var messages []llm2.Message
			for i, size := range tc.sizes {
				message := llm2.Message{Role: llm2.RoleUser}
				for j := 0; j < size; j++ {
					message.Content = append(message.Content, llm2.ContentBlock{
						Type:         llm2.ContentBlockTypeText,
						Text:         fmt.Sprintf("%d/%d", i, j),
						CacheControl: "ephemeral",
					})
				}
				messages = append(messages, message)
			}

			for attempt := 0; attempt < 2; attempt++ {
				applyCacheControlBreakpointsLlm2(messages)
				var marked [][2]int
				for i, message := range messages {
					for j, block := range message.Content {
						require.Equal(t, fmt.Sprintf("%d/%d", i, j), block.Text)
						if block.CacheControl != "" {
							require.Equal(t, "ephemeral", block.CacheControl)
							marked = append(marked, [2]int{i, j})
						}
					}
				}
				require.Equal(t, tc.marked, marked)
			}
		})
	}
}
