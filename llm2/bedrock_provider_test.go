package llm2

import (
	"context"
	"fmt"
	"os"
	"sidekick/common"
	"strings"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
)

func isBedrockAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, needle := range []string{
		"UnrecognizedClient",
		"InvalidSignature",
		"InvalidClientTokenId",
		"AccessDenied",
		"NotAuthorized",
		"403",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func TestBedrockProvider_Unauthorized(t *testing.T) {
	// Point shared-config/credentials at empty files so the SDK can't pick
	// up real credentials from the developer's machine; that way the
	// (invalid) env credentials below are what actually sign the request.
	emptyDir := t.TempDir()
	emptyConfig := emptyDir + "/config"
	emptyCreds := emptyDir + "/credentials"
	if err := os.WriteFile(emptyConfig, []byte("[default]\nregion = us-east-1\n"), 0o600); err != nil {
		t.Fatalf("write empty config: %v", err)
	}
	if err := os.WriteFile(emptyCreds, []byte("[default]\naws_access_key_id = not-a-real-key\naws_secret_access_key = not-a-real-secret\n"), 0o600); err != nil {
		t.Fatalf("write empty credentials: %v", err)
	}
	t.Setenv("AWS_CONFIG_FILE", emptyConfig)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", emptyCreds)
	t.Setenv("AWS_ACCESS_KEY_ID", "not-a-real-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "not-a-real-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "default")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_DEFAULT_REGION", "us-east-1")

	ctx := context.Background()
	provider := BedrockProvider{
		Region:  "us-east-1",
		Profile: "default",
	}

	messages := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{
					Type: ContentBlockTypeText,
					Text: "Hello",
				},
			},
		},
	}

	options := Options{
		ModelConfig: common.ModelConfig{
			Provider: "bedrock",
			Model:    "global.anthropic.claude-haiku-4-5-20251001-v1:0",
		},
	}

	request := StreamRequest{
		Messages: messages,
		Options:  options,
	}

	eventChan := make(chan Event, 10)
	defer close(eventChan)

	_, err := provider.Stream(ctx, request, eventChan)
	assert.Error(t, err)
	if err != nil {
		assert.Truef(t, isBedrockAuthError(err),
			"expected auth-related error, got: %v", err)
	}
}

func TestBedrockProvider_Integration_Gemma(t *testing.T) {
	t.Parallel()
	profile := requireAWSCredentialsForIntegration(t)

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)
	ctx := context.Background()
	provider := BedrockProvider{Profile: profile}

	messages := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{
					Type: ContentBlockTypeText,
					Text: "Say hi.",
				},
			},
		},
	}

	options := Options{
		ModelConfig: common.ModelConfig{
			Provider: "bedrock",
			Model:    "google.gemma-3-27b-it",
		},
	}

	eventChan := make(chan Event, 100)
	var allEvents []Event
	var sawTextDelta bool

	fmt.Println("\n=== Bedrock Provider Integration Test (Gemma) ===")

	go func() {
		for event := range eventChan {
			allEvents = append(allEvents, event)
			switch event.Type {
			case EventTextDelta:
				sawTextDelta = true
				deltaPreview := event.Delta
				if len(deltaPreview) > 50 {
					deltaPreview = deltaPreview[:50] + "..."
				}
				fmt.Printf("Event[%d]: type=text_delta delta=%q\n", event.Index, deltaPreview)
			default:
				fmt.Printf("Event[%d]: type=%s\n", event.Index, event.Type)
			}
		}
	}()

	request := StreamRequest{
		Messages: messages,
		Options:  options,
	}

	response, err := provider.Stream(ctx, request, eventChan)
	close(eventChan)

	if err != nil {
		t.Fatalf("Stream returned an error: %v", err)
	}
	if response == nil {
		t.Fatal("Stream returned a nil response")
	}

	assert.NotEmpty(t, allEvents, "Expected at least one event")
	assert.True(t, sawTextDelta, "Expected at least one EventTextDelta")

	var accumulatedText string
	for _, block := range response.Output.Content {
		if block.Type == ContentBlockTypeText {
			accumulatedText += block.Text
		}
	}
	assert.NotEmpty(t, strings.TrimSpace(accumulatedText),
		"Expected non-empty accumulated text in response.Output.Content")

	t.Logf("Model: %s, Provider: %s", response.Model, response.Provider)
	t.Logf("StopReason: %s", response.StopReason)
	t.Logf("Usage: InputTokens=%d, OutputTokens=%d",
		response.Usage.InputTokens, response.Usage.OutputTokens)
}

func TestBedrockProvider_Integration_ClaudeHaiku(t *testing.T) {
	t.Parallel()
	profile := requireAWSCredentialsForIntegration(t)

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)
	ctx := context.Background()
	provider := BedrockProvider{Profile: profile}

	mockTool := &common.Tool{
		Name:        "get_current_weather",
		Description: "Get the current weather in a given location",
		Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&getCurrentWeather{}),
	}

	messages := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{
					Type: ContentBlockTypeText,
					Text: "Look up the current weather in New York in celsius using the provided tool.",
				},
			},
		},
	}

	options := Options{
		ModelConfig: common.ModelConfig{
			Provider: "bedrock",
			Model:    "global.anthropic.claude-haiku-4-5-20251001-v1:0",
		},
		Tools:      []*common.Tool{mockTool},
		ToolChoice: common.ToolChoice{Type: common.ToolChoiceTypeAuto},
	}

	eventChan := make(chan Event, 100)
	var allEvents []Event
	var sawToolUseBlockEvent bool

	fmt.Println("\n=== Bedrock Provider Integration Test (Claude Haiku) ===")

	go func() {
		for event := range eventChan {
			allEvents = append(allEvents, event)
			switch event.Type {
			case EventBlockStarted:
				blockType := ""
				if event.ContentBlock != nil {
					blockType = string(event.ContentBlock.Type)
					if event.ContentBlock.Type == ContentBlockTypeToolUse {
						sawToolUseBlockEvent = true
					}
				}
				fmt.Printf("Event[%d]: type=block_started block_type=%s\n", event.Index, blockType)
			case EventTextDelta:
				deltaPreview := event.Delta
				if len(deltaPreview) > 50 {
					deltaPreview = deltaPreview[:50] + "..."
				}
				fmt.Printf("Event[%d]: type=text_delta delta=%q\n", event.Index, deltaPreview)
			case EventBlockDone:
				if event.ContentBlock != nil && event.ContentBlock.Type == ContentBlockTypeToolUse {
					sawToolUseBlockEvent = true
				}
				fmt.Printf("Event[%d]: type=block_done\n", event.Index)
			default:
				fmt.Printf("Event[%d]: type=%s\n", event.Index, event.Type)
			}
		}
	}()

	request := StreamRequest{
		Messages: messages,
		Options:  options,
	}

	response, err := provider.Stream(ctx, request, eventChan)
	close(eventChan)

	if err != nil {
		t.Fatalf("Stream returned an error: %v", err)
	}
	if response == nil {
		t.Fatal("Stream returned a nil response")
	}

	assert.NotEmpty(t, allEvents, "Expected at least one event")
	assert.True(t, sawToolUseBlockEvent,
		"Expected to see a block_started or block_done event with ContentBlockTypeToolUse")

	var foundWeatherToolUse bool
	for _, block := range response.Output.Content {
		if block.Type == ContentBlockTypeToolUse && block.ToolUse != nil &&
			block.ToolUse.Name == "get_current_weather" {
			foundWeatherToolUse = true
			t.Logf("Found tool_use block: id=%s name=%s args=%s",
				block.ToolUse.Id, block.ToolUse.Name, block.ToolUse.Arguments)
		}
	}
	assert.True(t, foundWeatherToolUse,
		"Expected response.Output.Content to include a get_current_weather tool_use block")

	assert.NotNil(t, response.Usage, "Usage field should not be nil")
	assert.Greater(t, response.Usage.InputTokens, 0, "InputTokens should be greater than 0")
	assert.Greater(t, response.Usage.OutputTokens, 0, "OutputTokens should be greater than 0")

	t.Logf("Model: %s, Provider: %s", response.Model, response.Provider)
	t.Logf("StopReason: %s", response.StopReason)
	t.Logf("Usage: InputTokens=%d, OutputTokens=%d",
		response.Usage.InputTokens, response.Usage.OutputTokens)
}
