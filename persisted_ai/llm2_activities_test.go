package persisted_ai

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/secret_manager"
	"sidekick/srv"

	"github.com/stretchr/testify/require"
)

// recordingFlowEventStreamer captures flow events added during Stream and
// signals when the flow event stream is ended, since Stream may return before
// its event-forwarding goroutine has drained.
type recordingFlowEventStreamer struct {
	srv.Streamer
	mu     sync.Mutex
	events []domain.FlowEvent
	ended  chan struct{}
}

func newRecordingFlowEventStreamer() *recordingFlowEventStreamer {
	return &recordingFlowEventStreamer{ended: make(chan struct{})}
}

func (r *recordingFlowEventStreamer) AddFlowEvent(ctx context.Context, workspaceId string, flowId string, flowEvent domain.FlowEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, flowEvent)
	return nil
}

func (r *recordingFlowEventStreamer) EndFlowEventStream(ctx context.Context, workspaceId, flowId, eventStreamParentId string) error {
	close(r.ended)
	return nil
}

func (r *recordingFlowEventStreamer) waitForEvents(t *testing.T) []domain.FlowEvent {
	t.Helper()
	select {
	case <-r.ended:
	case <-time.After(10 * time.Second):
		t.Fatal("flow event stream was never ended")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]domain.FlowEvent(nil), r.events...)
}

func TestStreamActivity_ForwardsLlm2StreamEventsAlongsideLegacyEvents(t *testing.T) {
	t.Parallel()

	reasoningStart := llm2.Event{Type: llm2.EventBlockStarted, Index: 0, ContentBlock: &llm2.ContentBlock{
		Type: llm2.ContentBlockTypeReasoning, Reasoning: &llm2.ReasoningBlock{},
	}}
	reasoningDelta := llm2.Event{Type: llm2.EventTextDelta, Index: 0, Delta: "Let me think."}
	summaryDelta := llm2.Event{Type: llm2.EventSummaryTextDelta, Index: 0, Delta: "Thinking summary"}
	signatureDelta := llm2.Event{Type: llm2.EventSignatureDelta, Index: 0, Signature: []byte("sig")}
	reasoningDone := llm2.Event{Type: llm2.EventBlockDone, Index: 0, ContentBlock: &llm2.ContentBlock{
		Type: llm2.ContentBlockTypeReasoning, Reasoning: &llm2.ReasoningBlock{Text: "Let me think.", Summary: "Thinking summary"},
	}}
	textStart := llm2.Event{Type: llm2.EventBlockStarted, Index: 1, ContentBlock: &llm2.ContentBlock{Type: llm2.ContentBlockTypeText}}
	textDeltaEvent := llm2.Event{Type: llm2.EventTextDelta, Index: 1, Delta: "Hello"}
	textDone := llm2.Event{Type: llm2.EventBlockDone, Index: 1, ContentBlock: &llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: "Hello"}}
	builtinUseStart := llm2.Event{Type: llm2.EventBlockStarted, Index: 2, ContentBlock: &llm2.ContentBlock{
		Type:           llm2.ContentBlockTypeBuiltinToolUse,
		BuiltinToolUse: &llm2.BuiltinToolUseBlock{Id: "ws_1", Name: "web_search", Arguments: `{"query":"sidekick"}`},
	}}
	builtinUseDone := llm2.Event{Type: llm2.EventBlockDone, Index: 2, ContentBlock: builtinUseStart.ContentBlock}
	builtinResultStart := llm2.Event{Type: llm2.EventBlockStarted, Index: 3, ContentBlock: &llm2.ContentBlock{
		Type:              llm2.ContentBlockTypeBuiltinToolResult,
		BuiltinToolResult: &llm2.BuiltinToolResultBlock{ToolCallId: "ws_1", Name: "web_search", Content: "done"},
	}}
	builtinResultDone := llm2.Event{Type: llm2.EventBlockDone, Index: 3, ContentBlock: builtinResultStart.ContentBlock}
	toolUseStartEvent := llm2.Event{Type: llm2.EventBlockStarted, Index: 4, ContentBlock: &llm2.ContentBlock{
		Type: llm2.ContentBlockTypeToolUse, ToolUse: &llm2.ToolUseBlock{Id: "call_1", Name: "read_file"},
	}}
	toolArgsDelta := llm2.Event{Type: llm2.EventTextDelta, Index: 4, Delta: `{"path":"README.md"}`}
	toolUseDone := llm2.Event{Type: llm2.EventBlockDone, Index: 4, ContentBlock: &llm2.ContentBlock{
		Type: llm2.ContentBlockTypeToolUse, ToolUse: &llm2.ToolUseBlock{Id: "call_1", Name: "read_file", Arguments: `{"path":"README.md"}`},
	}}
	heartbeat := llm2.Event{Type: llm2.EventHeartbeat}

	scripted := []llm2.Event{
		heartbeat,
		reasoningStart, reasoningDelta, summaryDelta, signatureDelta, reasoningDone,
		textStart, textDeltaEvent, textDone,
		builtinUseStart, builtinUseDone,
		builtinResultStart, builtinResultDone,
		toolUseStartEvent, toolArgsDelta, toolUseDone,
	}
	provider := &scriptedProvider{attempts: [][]llm2.Event{scripted}}
	streamer := newRecordingFlowEventStreamer()
	la := &Llm2Activities{
		Streamer: streamer,
		providerFactory: func(common.ModelConfig, []common.ModelProviderPublicConfig) (llm2.Provider, error) {
			return provider, nil
		},
	}

	input := newRunawayStreamActivityInput(t)
	input.FlowActionId = "action-1"
	response, err := la.Stream(context.Background(), input)
	require.NoError(t, err)
	require.NotNil(t, response)

	events := streamer.waitForEvents(t)

	var forwarded []llm2.Event
	var legacyDeltas []domain.ChatMessageDeltaEvent
	var progressEvents []domain.ProgressTextEvent
	for _, event := range events {
		switch e := event.(type) {
		case domain.Llm2StreamEvent:
			require.Equal(t, domain.Llm2StreamEventType, e.EventType)
			require.Equal(t, "action-1", e.FlowActionId)
			forwarded = append(forwarded, e.Event)
		case domain.ChatMessageDeltaEvent:
			legacyDeltas = append(legacyDeltas, e)
		case domain.ProgressTextEvent:
			progressEvents = append(progressEvents, e)
		default:
			t.Fatalf("unexpected flow event type %T", event)
		}
	}

	// Every content-relevant llm2 event is forwarded verbatim in order;
	// heartbeats and signature deltas are not.
	var expectedForwarded []llm2.Event
	for _, event := range scripted {
		switch event.Type {
		case llm2.EventBlockStarted, llm2.EventTextDelta, llm2.EventSummaryTextDelta, llm2.EventBlockDone:
			expectedForwarded = append(expectedForwarded, event)
		}
	}
	require.Equal(t, expectedForwarded, forwarded)

	// Legacy emissions for other consumers remain alongside the new events.
	require.NotEmpty(t, legacyDeltas, "chat_message_delta events must still be emitted")
	var legacyText string
	var legacyToolCallIds []string
	for _, delta := range legacyDeltas {
		legacyText += delta.ChatMessageDelta.Content
		for _, call := range delta.ChatMessageDelta.ToolCalls {
			if call.Id != "" {
				legacyToolCallIds = append(legacyToolCallIds, call.Id)
			}
		}
	}
	require.Equal(t, "Hello", legacyText)
	require.Contains(t, legacyToolCallIds, "call_1")
	require.NotEmpty(t, progressEvents, "progress_text events for summaries/builtin results must still be emitted")
}

func newTestStreamInput(options llm2.Options) StreamInput {
	return StreamInput{
		Options: options,
		Secrets: secret_manager.SecretManagerContainer{SecretManager: secret_manager.MockSecretManager{}},
	}
}

func TestStreamInputActionParams_OmitsReasoningEffortWhenEmpty(t *testing.T) {
	t.Parallel()
	si := newTestStreamInput(llm2.Options{
		Tools: []*common.Tool{},
		ModelConfig: common.ModelConfig{
			Provider: "openai",
			Model:    "gpt-4",
		},
	})

	params := si.ActionParams()

	if _, exists := params["reasoningEffort"]; exists {
		t.Errorf("Expected reasoningEffort key to be absent when ReasoningEffort is empty, but it was present")
	}
	if params["provider"] != "openai" {
		t.Errorf("Expected provider to be 'openai', got %v", params["provider"])
	}
	if params["model"] != "gpt-4" {
		t.Errorf("Expected model to be 'gpt-4', got %v", params["model"])
	}
}

func TestStreamInputActionParams_IncludesReasoningEffortWhenSet(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		reasoningEffort string
	}{
		{"low reasoning effort", "low"},
		{"medium reasoning effort", "medium"},
		{"high reasoning effort", "high"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			si := newTestStreamInput(llm2.Options{
				Tools: []*common.Tool{},
				ModelConfig: common.ModelConfig{
					Provider:        "openai",
					Model:           "o1",
					ReasoningEffort: tt.reasoningEffort,
				},
			})

			params := si.ActionParams()

			reasoningEffort, exists := params["reasoningEffort"]
			if !exists {
				t.Errorf("Expected reasoningEffort key to be present when ReasoningEffort is '%s', but it was absent", tt.reasoningEffort)
			}
			if reasoningEffort != tt.reasoningEffort {
				t.Errorf("Expected reasoningEffort to be '%s', got %v", tt.reasoningEffort, reasoningEffort)
			}
			if params["provider"] != "openai" {
				t.Errorf("Expected provider to be 'openai', got %v", params["provider"])
			}
			if params["model"] != "o1" {
				t.Errorf("Expected model to be 'o1', got %v", params["model"])
			}
		})
	}
}

func TestStreamInputActionParams_OmitsMaxTokensWhenUnset(t *testing.T) {
	t.Parallel()
	si := newTestStreamInput(llm2.Options{
		Tools: []*common.Tool{},
		ModelConfig: common.ModelConfig{
			Provider: "anthropic",
			Model:    "claude-3-5-sonnet-latest",
		},
	})

	params := si.ActionParams()

	if _, exists := params["maxTokens"]; exists {
		t.Errorf("Expected maxTokens key to be absent when MaxTokens is unset, but it was present")
	}
	if params["provider"] != "anthropic" {
		t.Errorf("Expected provider to be 'anthropic', got %v", params["provider"])
	}
	if params["model"] != "claude-3-5-sonnet-latest" {
		t.Errorf("Expected model to be 'claude-3-5-sonnet-latest', got %v", params["model"])
	}
}

func TestStreamInputActionParams_IncludesMaxTokensWhenSet(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		maxTokens int
	}{
		{"small max tokens", 123},
		{"medium max tokens", 4000},
		{"large max tokens", 8000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			si := newTestStreamInput(llm2.Options{
				Tools:     []*common.Tool{},
				MaxTokens: tt.maxTokens,
				ModelConfig: common.ModelConfig{
					Provider: "anthropic",
					Model:    "claude-3-5-sonnet-latest",
				},
			})

			params := si.ActionParams()

			maxTokens, exists := params["maxTokens"]
			if !exists {
				t.Errorf("Expected maxTokens key to be present when MaxTokens is %d, but it was absent", tt.maxTokens)
			}
			if maxTokens != float64(tt.maxTokens) {
				t.Errorf("Expected maxTokens to be %d, got %v", tt.maxTokens, maxTokens)
			}
			if params["provider"] != "anthropic" {
				t.Errorf("Expected provider to be 'anthropic', got %v", params["provider"])
			}
			if params["model"] != "claude-3-5-sonnet-latest" {
				t.Errorf("Expected model to be 'claude-3-5-sonnet-latest', got %v", params["model"])
			}
		})
	}
}

func TestStreamInputActionParams_OmitsServiceTierWhenEmpty(t *testing.T) {
	t.Parallel()
	si := newTestStreamInput(llm2.Options{
		Tools: []*common.Tool{},
		ModelConfig: common.ModelConfig{
			Provider: "openai",
			Model:    "gpt-4",
		},
	})

	params := si.ActionParams()

	if _, exists := params["serviceTier"]; exists {
		t.Errorf("Expected serviceTier key to be absent when ServiceTier is empty, but it was present")
	}
	if params["provider"] != "openai" {
		t.Errorf("Expected provider to be 'openai', got %v", params["provider"])
	}
	if params["model"] != "gpt-4" {
		t.Errorf("Expected model to be 'gpt-4', got %v", params["model"])
	}
}

func TestStreamInputActionParams_IncludesServiceTierWhenSet(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		serviceTier string
	}{
		{"default service tier", "default"},
		{"flex service tier", "flex"},
		{"priority service tier", "priority"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			si := newTestStreamInput(llm2.Options{
				Tools: []*common.Tool{},
				ModelConfig: common.ModelConfig{
					Provider:    "openai",
					Model:       "gpt-4",
					ServiceTier: tt.serviceTier,
				},
			})

			params := si.ActionParams()

			serviceTier, exists := params["serviceTier"]
			if !exists {
				t.Errorf("Expected serviceTier key to be present when ServiceTier is '%s', but it was absent", tt.serviceTier)
			}
			if serviceTier != tt.serviceTier {
				t.Errorf("Expected serviceTier to be '%s', got %v", tt.serviceTier, serviceTier)
			}
			if params["provider"] != "openai" {
				t.Errorf("Expected provider to be 'openai', got %v", params["provider"])
			}
			if params["model"] != "gpt-4" {
				t.Errorf("Expected model to be 'gpt-4', got %v", params["model"])
			}
		})
	}
}

func TestStreamInputActionParams_IncludesMessagesAndSecretType(t *testing.T) {
	t.Parallel()
	si := StreamInput{
		Options: llm2.Options{
			Tools: []*common.Tool{},
			ModelConfig: common.ModelConfig{
				Provider: "openai",
				Model:    "gpt-4",
			},
		},
		Secrets:     secret_manager.SecretManagerContainer{SecretManager: secret_manager.MockSecretManager{}},
		ChatHistory: &ChatHistoryContainer{},
	}

	params := si.ActionParams()

	if _, exists := params["messages"]; !exists {
		t.Error("Expected messages key to be present")
	}
	if _, exists := params["secretManagerType"]; !exists {
		t.Error("Expected secretManagerType key to be present")
	}
}
func TestGetLlm2Provider_AnthropicVariants(t *testing.T) {
	t.Parallel()

	providers := []common.ModelProviderPublicConfig{
		{
			Name:       "anthropic-proxy",
			Type:       "anthropic",
			BaseURL:    "https://proxy.example.com",
			DefaultLLM: "claude-sonnet-4-5",
		},
		{
			Name:       "vendor-anthropic",
			Type:       "anthropic_compatible",
			BaseURL:    "https://vendor.example.com",
			DefaultLLM: "vendor-model-v1",
		},
	}

	tests := []struct {
		name   string
		config common.ModelConfig
		want   llm2.AnthropicProvider
	}{
		{
			name: "anthropic proxy preserves anthropic model assumptions",
			config: common.ModelConfig{
				Provider: "anthropic-proxy",
			},
			want: llm2.AnthropicProvider{
				BaseURL:      "https://proxy.example.com",
				DefaultModel: "claude-sonnet-4-5",
			},
		},
		{
			name: "anthropic compatible disables anthropic model assumptions",
			config: common.ModelConfig{
				Provider: "vendor-anthropic",
			},
			want: llm2.AnthropicProvider{
				BaseURL:             "https://vendor.example.com",
				DefaultModel:        "vendor-model-v1",
				AnthropicCompatible: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			provider, err := getLlm2Provider(tt.config, providers)
			if err != nil {
				t.Fatalf("getLlm2Provider returned error: %v", err)
			}

			anthropicProvider, ok := provider.(llm2.AnthropicProvider)
			if !ok {
				t.Fatalf("provider type = %T, want llm2.AnthropicProvider", provider)
			}
			if anthropicProvider.BaseURL != tt.want.BaseURL ||
				anthropicProvider.DefaultModel != tt.want.DefaultModel ||
				anthropicProvider.AnthropicCompatible != tt.want.AnthropicCompatible ||
				anthropicProvider.AuthType != tt.want.AuthType {
				t.Fatalf("provider = %#v, want %#v", anthropicProvider, tt.want)
			}
		})
	}
}

func TestGetLlm2Provider_AuthType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		config    common.ModelConfig
		providers []common.ModelProviderPublicConfig
		assert    func(t *testing.T, provider llm2.Provider)
	}{
		{
			name: "builtin anthropic defaults to any",
			config: common.ModelConfig{
				Provider: "anthropic",
			},
			assert: func(t *testing.T, provider llm2.Provider) {
				t.Helper()

				anthropicProvider, ok := provider.(llm2.AnthropicProvider)
				if !ok {
					t.Fatalf("expected llm2.AnthropicProvider, got %T", provider)
				}
				if anthropicProvider.AuthType != common.ProviderAuthTypeAny {
					t.Fatalf("expected auth type %q, got %q", common.ProviderAuthTypeAny, anthropicProvider.AuthType)
				}
			},
		},
		{
			name: "builtin anthropic config uses explicit api type",
			config: common.ModelConfig{
				Provider: "anthropic",
			},
			providers: []common.ModelProviderPublicConfig{
				{
					Name:     "anthropic",
					Type:     "anthropic",
					AuthType: common.ProviderAuthTypeAPI,
				},
			},
			assert: func(t *testing.T, provider llm2.Provider) {
				t.Helper()

				anthropicProvider, ok := provider.(llm2.AnthropicProvider)
				if !ok {
					t.Fatalf("expected llm2.AnthropicProvider, got %T", provider)
				}
				if anthropicProvider.AuthType != common.ProviderAuthTypeAPI {
					t.Fatalf("expected auth type %q, got %q", common.ProviderAuthTypeAPI, anthropicProvider.AuthType)
				}
			},
		},
		{
			name: "named anthropic alias uses explicit subscription type",
			config: common.ModelConfig{
				Provider: "anthropic-subscription",
			},
			providers: []common.ModelProviderPublicConfig{
				{
					Name:     "anthropic-subscription",
					Type:     "anthropic",
					AuthType: common.ProviderAuthTypeSubscription,
				},
			},
			assert: func(t *testing.T, provider llm2.Provider) {
				t.Helper()

				anthropicProvider, ok := provider.(llm2.AnthropicProvider)
				if !ok {
					t.Fatalf("expected llm2.AnthropicProvider, got %T", provider)
				}
				if anthropicProvider.AuthType != common.ProviderAuthTypeSubscription {
					t.Fatalf("expected auth type %q, got %q", common.ProviderAuthTypeSubscription, anthropicProvider.AuthType)
				}
			},
		},
		{
			name: "builtin openai config propagates auth type",
			config: common.ModelConfig{
				Provider: "openai",
			},
			providers: []common.ModelProviderPublicConfig{
				{
					Name:     "openai",
					Type:     "openai",
					AuthType: common.ProviderAuthTypeAPI,
				},
			},
			assert: func(t *testing.T, provider llm2.Provider) {
				t.Helper()

				openAIProvider, ok := provider.(llm2.OpenAIResponsesProvider)
				if !ok {
					t.Fatalf("expected llm2.OpenAIResponsesProvider, got %T", provider)
				}
				if openAIProvider.AuthType != common.ProviderAuthTypeAPI {
					t.Fatalf("expected auth type %q, got %q", common.ProviderAuthTypeAPI, openAIProvider.AuthType)
				}
			},
		},
		{
			name: "named openai compatible alias propagates auth type",
			config: common.ModelConfig{
				Provider: "workspace-openai",
			},
			providers: []common.ModelProviderPublicConfig{
				{
					Name:       "workspace-openai",
					Type:       "openai_compatible",
					BaseURL:    "https://example.com/v1",
					DefaultLLM: "gpt-4.1-mini",
					AuthType:   common.ProviderAuthTypeSubscription,
				},
			},
			assert: func(t *testing.T, provider llm2.Provider) {
				t.Helper()

				openAIProvider, ok := provider.(llm2.OpenAIProvider)
				if !ok {
					t.Fatalf("expected llm2.OpenAIProvider, got %T", provider)
				}
				if openAIProvider.AuthType != common.ProviderAuthTypeSubscription {
					t.Fatalf("expected auth type %q, got %q", common.ProviderAuthTypeSubscription, openAIProvider.AuthType)
				}
			},
		},
		{
			name: "named google alias propagates auth type",
			config: common.ModelConfig{
				Provider: "workspace-google",
			},
			providers: []common.ModelProviderPublicConfig{
				{
					Name:     "workspace-google",
					Type:     "google",
					AuthType: common.ProviderAuthTypeAPI,
				},
			},
			assert: func(t *testing.T, provider llm2.Provider) {
				t.Helper()

				googleProvider, ok := provider.(llm2.GoogleProvider)
				if !ok {
					t.Fatalf("expected llm2.GoogleProvider, got %T", provider)
				}
				if googleProvider.AuthType != common.ProviderAuthTypeAPI {
					t.Fatalf("expected auth type %q, got %q", common.ProviderAuthTypeAPI, googleProvider.AuthType)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			provider, err := getLlm2Provider(tt.config, tt.providers)
			if err != nil {
				t.Fatalf("getLlm2Provider returned error: %v", err)
			}

			tt.assert(t, provider)
		})
	}
}
func TestConvertLlm2EventToFlowEvent_TextFollowedByToolCall(t *testing.T) {
	t.Parallel()

	blocks := make(map[int]llm2.ContentBlock)
	flowActionId := "action-1"

	textStart := convertLlm2EventToFlowEvent(llm2.Event{
		Type:  llm2.EventBlockStarted,
		Index: 0,
		ContentBlock: &llm2.ContentBlock{
			Type: llm2.ContentBlockTypeText,
		},
	}, flowActionId, blocks)
	if textStart != nil {
		t.Fatalf("empty text block start produced event %#v", textStart)
	}

	textEvent := convertLlm2EventToFlowEvent(llm2.Event{
		Type:  llm2.EventTextDelta,
		Index: 0,
		Delta: "I will check that.",
	}, flowActionId, blocks)
	textDelta, ok := textEvent.(domain.ChatMessageDeltaEvent)
	if !ok {
		t.Fatalf("text delta event type = %T, want domain.ChatMessageDeltaEvent", textEvent)
	}
	if textDelta.ChatMessageDelta.Content != "I will check that." {
		t.Fatalf("text content = %q, want %q", textDelta.ChatMessageDelta.Content, "I will check that.")
	}
	if len(textDelta.ChatMessageDelta.ToolCalls) != 0 {
		t.Fatalf("text delta unexpectedly contained tool calls: %#v", textDelta.ChatMessageDelta.ToolCalls)
	}

	toolStart := convertLlm2EventToFlowEvent(llm2.Event{
		Type:  llm2.EventBlockStarted,
		Index: 1,
		ContentBlock: &llm2.ContentBlock{
			Type: llm2.ContentBlockTypeToolUse,
			ToolUse: &llm2.ToolUseBlock{
				Id:   "tool-1",
				Name: "read_file",
			},
		},
	}, flowActionId, blocks)
	toolStartDelta, ok := toolStart.(domain.ChatMessageDeltaEvent)
	if !ok {
		t.Fatalf("tool start event type = %T, want domain.ChatMessageDeltaEvent", toolStart)
	}
	if toolStartDelta.ChatMessageDelta.Content != "" {
		t.Fatalf("tool start was emitted as text %q", toolStartDelta.ChatMessageDelta.Content)
	}
	if len(toolStartDelta.ChatMessageDelta.ToolCalls) != 1 {
		t.Fatalf("tool start calls = %#v, want one call", toolStartDelta.ChatMessageDelta.ToolCalls)
	}
	if toolStartDelta.ChatMessageDelta.ToolCalls[0].Id != "tool-1" ||
		toolStartDelta.ChatMessageDelta.ToolCalls[0].Name != "read_file" {
		t.Fatalf("tool start call = %#v", toolStartDelta.ChatMessageDelta.ToolCalls[0])
	}

	argumentsEvent := convertLlm2EventToFlowEvent(llm2.Event{
		Type:  llm2.EventTextDelta,
		Index: 1,
		Delta: `{"path":"README.md"}`,
	}, flowActionId, blocks)
	argumentsDelta, ok := argumentsEvent.(domain.ChatMessageDeltaEvent)
	if !ok {
		t.Fatalf("tool arguments event type = %T, want domain.ChatMessageDeltaEvent", argumentsEvent)
	}
	if argumentsDelta.ChatMessageDelta.Content != "" {
		t.Fatalf("tool arguments were emitted as text %q", argumentsDelta.ChatMessageDelta.Content)
	}
	if len(argumentsDelta.ChatMessageDelta.ToolCalls) != 1 {
		t.Fatalf("tool argument calls = %#v, want one call", argumentsDelta.ChatMessageDelta.ToolCalls)
	}
	if argumentsDelta.ChatMessageDelta.ToolCalls[0].Arguments != `{"path":"README.md"}` {
		t.Fatalf("tool arguments = %q", argumentsDelta.ChatMessageDelta.ToolCalls[0].Arguments)
	}
}

func TestNewStreamResponseStampsResolvedProfile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		secrets       secret_manager.SecretManager
		wantProfileId string
	}{
		{
			name:          "profile-scoped secrets",
			secrets:       secret_manager.KeyringSecretManager{ProfileId: "work"},
			wantProfileId: "work",
		},
		{
			name:          "non-profile-scoped secrets fall back to the default profile",
			secrets:       secret_manager.MockSecretManager{},
			wantProfileId: common.DefaultProfileId,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input := StreamInput{
				Secrets: secret_manager.SecretManagerContainer{SecretManager: tt.secrets},
			}
			response := newStreamResponse(llm2.MessageResponse{Provider: "openai"}, input)
			if response.ProfileId != tt.wantProfileId {
				t.Errorf("expected profileId %q, got %q", tt.wantProfileId, response.ProfileId)
			}

			// The response is what gets persisted as the flow action result, so
			// the profile and the embedded provider response fields must both
			// appear at the top level of the serialized JSON.
			serialized, err := json.Marshal(response)
			if err != nil {
				t.Fatalf("failed to marshal response: %v", err)
			}
			var result map[string]any
			if err := json.Unmarshal(serialized, &result); err != nil {
				t.Fatalf("failed to unmarshal serialized response: %v", err)
			}
			if result["profileId"] != tt.wantProfileId {
				t.Errorf("expected serialized profileId %q, got %v", tt.wantProfileId, result["profileId"])
			}
			if result["provider"] != "openai" {
				t.Errorf("expected flattened provider field %q, got %v", "openai", result["provider"])
			}
		})
	}
}

func TestNewLegacyStreamResponseStampsResolvedProfile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		secrets       secret_manager.SecretManager
		wantProfileId string
	}{
		{
			name:          "profile-scoped secrets",
			secrets:       secret_manager.KeyringSecretManager{ProfileId: "work"},
			wantProfileId: "work",
		},
		{
			name:          "non-profile-scoped secrets fall back to the default profile",
			secrets:       secret_manager.MockSecretManager{},
			wantProfileId: common.DefaultProfileId,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			options := ChatStreamOptions{
				ToolChatOptions: llm.ToolChatOptions{
					Secrets: secret_manager.SecretManagerContainer{SecretManager: tt.secrets},
				},
			}
			response := newLegacyStreamResponse(llm.ChatMessageResponse{Provider: "openai"}, options)
			if response.ProfileId != tt.wantProfileId {
				t.Errorf("expected profileId %q, got %q", tt.wantProfileId, response.ProfileId)
			}

			// The response is what gets persisted as the flow action result, so
			// the profile and the embedded provider response fields must both
			// appear at the top level of the serialized JSON.
			serialized, err := json.Marshal(response)
			if err != nil {
				t.Fatalf("failed to marshal response: %v", err)
			}
			var result map[string]any
			if err := json.Unmarshal(serialized, &result); err != nil {
				t.Fatalf("failed to unmarshal serialized response: %v", err)
			}
			if result["profileId"] != tt.wantProfileId {
				t.Errorf("expected serialized profileId %q, got %v", tt.wantProfileId, result["profileId"])
			}
			if result["provider"] != "openai" {
				t.Errorf("expected flattened provider field %q, got %v", "openai", result["provider"])
			}
		})
	}
}

func TestStreamInputActionParamsSnapshotsHistory(t *testing.T) {
	t.Parallel()

	history := NewLlm2ChatHistory("flow", "workspace")
	history.AppendRef(MessageRef{Role: "user", BlockKeys: []string{"request"}})
	input := StreamInput{
		Secrets:     secret_manager.SecretManagerContainer{SecretManager: secret_manager.MockSecretManager{}},
		ChatHistory: &ChatHistoryContainer{History: history},
	}
	params := input.ActionParams()
	before, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}

	history.AppendRef(MessageRef{Role: "assistant", BlockKeys: []string{"response"}})
	history.AppendRef(MessageRef{Role: "user", BlockKeys: []string{"tool-result"}})

	after, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("request parameters changed after appending response:\nbefore: %s\nafter: %s", before, after)
	}

	next, err := json.Marshal(input.ActionParams())
	if err != nil {
		t.Fatal(err)
	}
	current, err := json.Marshal(input.ChatHistory)
	if err != nil {
		t.Fatal(err)
	}
	var nextParams map[string]json.RawMessage
	if err := json.Unmarshal(next, &nextParams); err != nil {
		t.Fatal(err)
	}
	if string(nextParams["messages"]) != string(current) {
		t.Fatalf("next request does not contain current history: %s", next)
	}
}
