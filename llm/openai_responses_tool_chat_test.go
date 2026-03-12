package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"sidekick/secret_manager"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOpenaiResponsesChatStream_Unauthorized(t *testing.T) {
	ctx := context.Background()
	mockSecretManager := &secret_manager.MockSecretManager{}
	openaiResponsesToolChat := OpenaiResponsesToolChat{}
	options := ToolChatOptions{
		Params: ToolChatParams{
			Messages: []ChatMessage{
				{
					Role:    ChatMessageRoleUser,
					Content: "Hello",
				},
			},
			ModelConfig: common.ModelConfig{
				Provider: "openai",
				Model:    OpenaiResponsesDefaultModel,
			},
		},
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: mockSecretManager,
		},
	}

	deltaChan := make(chan ChatMessageDelta)
	defer close(deltaChan)
	progressChan := make(chan ProgressInfo)
	defer close(progressChan)
	_, err := openaiResponsesToolChat.ChatStream(ctx, options, deltaChan, progressChan)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestOpenaiResponsesProgressFromReasoningSummaryDelta(t *testing.T) {
	t.Parallel()

	t.Run("MultilineAndBoldTitle", func(t *testing.T) {
		t.Parallel()

		progress := openaiProgressFromReasoningSummaryDelta("  **Plan**\nStep 1\nStep 2  ")
		assert.NotNil(t, progress)
		assert.Equal(t, "Plan", progress.Title)
		assert.Equal(t, "Step 1\nStep 2", progress.Details)
	})

	t.Run("TitleTruncation", func(t *testing.T) {
		t.Parallel()

		longTitle := "**" + strings.Repeat("a", 200) + "**\nrest"
		progress := openaiProgressFromReasoningSummaryDelta(longTitle)
		assert.NotNil(t, progress)
		assert.True(t, strings.HasSuffix(progress.Title, "..."))
		assert.LessOrEqual(t, len(progress.Title), 123)
	})
}

func TestOpenaiResponsesChatStream_EmitsProgressFromReasoningSummary(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The openai-go streaming client uses SSE.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		// Minimal SSE that includes a reasoning summary delta, then a completed event.
		// We don't require any assistant text deltas for this test.
		_, _ = w.Write([]byte(`event: response.reasoning_summary_text.delta
data: {"type":"response.reasoning_summary_text.delta","delta":"**Doing work**\nLine 2"}

event: response.completed
data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}

`))
	}))

	defer srv.Close()

	ctx := context.Background()
	mockSecretManager := &secret_manager.MockSecretManager{}
	chat := OpenaiResponsesToolChat{BaseURL: srv.URL}

	options := ToolChatOptions{
		Params: ToolChatParams{
			Messages: []ChatMessage{
				{Role: ChatMessageRoleUser, Content: "Hello"},
			},
			ModelConfig: common.ModelConfig{
				Provider: "openai",
				Model:    OpenaiResponsesDefaultModel,
			},
		},
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: mockSecretManager,
		},
	}

	deltaChan := make(chan ChatMessageDelta)
	defer close(deltaChan)

	progressChan := make(chan ProgressInfo, 10)
	resp, err := chat.ChatStream(ctx, options, deltaChan, progressChan)
	assert.NoError(t, err)
	assert.NotNil(t, resp)

	select {
	case p := <-progressChan:
		assert.Equal(t, "Doing work", p.Title)
		assert.Equal(t, "Line 2", p.Details)
	default:
		t.Fatalf("expected at least one ProgressInfo event")
	}
}
