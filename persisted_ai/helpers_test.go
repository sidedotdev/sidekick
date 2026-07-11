package persisted_ai

import (
	"testing"

	"sidekick/common"
	"sidekick/llm2"

	"github.com/stretchr/testify/assert"
)

func TestResponseHasRefusal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		response common.MessageResponse
		want     bool
	}{
		{
			name:     "nil response",
			response: nil,
			want:     false,
		},
		{
			name: "refusal stop reason with no content blocks",
			response: &llm2.MessageResponse{
				StopReason: "refusal",
				Output:     llm2.Message{Role: llm2.RoleAssistant},
			},
			want: true,
		},
		{
			name: "refusal content block",
			response: &llm2.MessageResponse{
				StopReason: "stop",
				Output: llm2.Message{
					Role: llm2.RoleAssistant,
					Content: []llm2.ContentBlock{{
						Type:    llm2.ContentBlockTypeRefusal,
						Refusal: &llm2.RefusalBlock{Reason: "I can't help with that."},
					}},
				},
			},
			want: true,
		},
		{
			name: "no refusal",
			response: &llm2.MessageResponse{
				StopReason: "stop",
				Output: llm2.Message{
					Role:    llm2.RoleAssistant,
					Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "hi"}},
				},
			},
			want: false,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, responseHasRefusal(tc.response))
		})
	}
}
