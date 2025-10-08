package llm

import (
	"context"
	"os"
	"sidekick/common"
	"sidekick/secret_manager"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
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

func TestOpenaiResponsesToolChatIntegration_Basic(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)
	ctx := context.Background()
	chat := OpenaiResponsesToolChat{}

	temperature := float32(0)
	options := ToolChatOptions{
		Params: ToolChatParams{
			ModelConfig: common.ModelConfig{
				Provider: "openai",
				Model:    "gpt-4o-mini",
			},
			Messages: []ChatMessage{
				{Role: ChatMessageRoleUser, Content: "Say hello in one sentence."},
			},
			Temperature: &temperature,
		},
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
				&secret_manager.EnvSecretManager{},
				&secret_manager.KeyringSecretManager{},
				&secret_manager.LocalConfigSecretManager{},
			}),
		},
	}

	deltaChan := make(chan ChatMessageDelta)
	var allDeltas []ChatMessageDelta

	go func() {
		for delta := range deltaChan {
			allDeltas = append(allDeltas, delta)
		}
	}()

	progressChan := make(chan ProgressInfo)
	defer close(progressChan)
	response, err := chat.ChatStream(ctx, options, deltaChan, progressChan)
	close(deltaChan)

	if err != nil {
		t.Fatalf("ChatStream returned an error: %v", err)
	}

	if response == nil {
		t.Fatal("ChatStream returned a nil response")
	}

	if len(allDeltas) == 0 {
		t.Error("No deltas received")
	}

	if response.Content == "" {
		t.Error("Response content is empty")
	}

	if response.Usage.InputTokens == 0 && response.Usage.OutputTokens == 0 {
		t.Log("Warning: Usage tokens are zero (may not be provided by model)")
	}

	t.Logf("Response content: %s", response.Content)
	t.Logf("Usage: %+v", response.Usage)
	t.Logf("Deltas received: %d", len(allDeltas))
}
