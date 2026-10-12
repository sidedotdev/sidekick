package persisted_ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sidekick/common"
	"sidekick/llm2"
	"sidekick/secret_manager"
	"testing"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

func TestForceToolCallAnthropicCompatibility(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		model      string
		compatible bool
		wantForced bool
	}{
		{name: "haiku 5.5 uses auto", model: "claude-haiku-5-5"},
		{name: "sonnet 5.5 uses auto", model: "claude-sonnet-5-5"},
		{name: "dotted haiku 5.5 uses auto", model: "claude-haiku-5.5"},
		{name: "haiku 5.1 uses auto", model: "claude-haiku-5-1"},
		{name: "5.0 retains forced choice", model: "claude-sonnet-5-0", wantForced: true},
		{name: "old model retains forced choice", model: "claude-opus-4-5", wantForced: true},
		{name: "compatible haiku uses auto", model: "claude-haiku-5-5", compatible: true},
		{name: "compatible old model uses auto", model: "claude-opus-4-5", compatible: true},
		{name: "compatible non claude model uses auto", model: "glm-4.6", compatible: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, choiceType := range []common.ToolChoiceType{common.ToolChoiceTypeTool, common.ToolChoiceTypeRequired} {
				t.Run(string(choiceType), func(t *testing.T) {
					t.Parallel()

					requests := make(chan json.RawMessage, 1)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var body json.RawMessage
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Errorf("decode request: %v", err)
							http.Error(w, "invalid JSON", http.StatusBadRequest)
							return
						}
						select {
						case requests <- body:
						default:
							t.Error("unexpected retry")
						}
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"stop after request inspection"}}`))
					}))
					t.Cleanup(server.Close)

					input := newRunawayStreamActivityInput(t)
					input.Options.Provider = "anthropic"
					input.Options.Model = tt.model
					input.Options.ReasoningEffort = "high"
					input.Options.Tools = []*common.Tool{{Name: "record_plan", Parameters: &jsonschema.Schema{Type: "object"}}}
					if choiceType == common.ToolChoiceTypeRequired {
						input.Options.Tools = append(input.Options.Tools, &common.Tool{
							Name: "cancel_plan", Parameters: &jsonschema.Schema{Type: "object"},
						})
					}
					parallel := true
					input.Options.ParallelToolCalls = &parallel
					providerType := "anthropic"
					if tt.compatible {
						providerType = "anthropic_compatible"
					}
					input.Providers = []common.ModelProviderPublicConfig{{
						Name:     "anthropic",
						Type:     providerType,
						BaseURL:  server.URL,
						AuthType: common.ProviderAuthTypeAPI,
					}}

					_, _, err := runForcedToolCallTest(t, forcedToolCallTestConfig{StreamInput: input},
						func(ctx context.Context, request StreamInput) (*llm2.MessageResponse, error) {
							ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
							defer cancel()
							request.FlowActionId = ""
							activities := Llm2Activities{}
							response, err := activities.Stream(ctx, request)
							if err != nil {
								return nil, temporal.NewNonRetryableApplicationError(err.Error(), "request-inspection", err)
							}
							return response, nil
						})
					require.ErrorContains(t, err, "stop after request inspection")

					var body json.RawMessage
					select {
					case body = <-requests:
					default:
						t.Fatal("no provider request received")
					}
					var request struct {
						ToolChoice struct {
							Type                   string `json:"type"`
							Name                   string `json:"name"`
							DisableParallelToolUse *bool  `json:"disable_parallel_tool_use"`
						} `json:"tool_choice"`
						Messages []struct {
							Role    string `json:"role"`
							Content []struct {
								Type string `json:"type"`
								Text string `json:"text"`
							} `json:"content"`
						} `json:"messages"`
						Thinking *struct {
							Type string `json:"type"`
						} `json:"thinking"`
					}
					require.NoError(t, json.Unmarshal(body, &request))

					wantType := "auto"
					if tt.wantForced {
						wantType = "any"
						if choiceType == common.ToolChoiceTypeTool {
							wantType = "tool"
							require.Equal(t, "record_plan", request.ToolChoice.Name)
						}
					} else {
						require.Empty(t, request.ToolChoice.Name)
					}
					require.Equal(t, wantType, request.ToolChoice.Type)
					require.NotNil(t, request.ToolChoice.DisableParallelToolUse)
					require.False(t, *request.ToolChoice.DisableParallelToolUse)
					require.NotEmpty(t, request.Messages)
					lastMessage := request.Messages[len(request.Messages)-1]
					require.Equal(t, "user", lastMessage.Role)
					require.NotEmpty(t, lastMessage.Content)
					instruction := lastMessage.Content[len(lastMessage.Content)-1]
					require.Equal(t, "text", instruction.Type)
					require.Contains(t, instruction.Text, "MUST")
					if choiceType == common.ToolChoiceTypeTool {
						require.Contains(t, instruction.Text, "record_plan")
					} else {
						require.Contains(t, instruction.Text, "one of the provided tools")
					}
					if tt.model == "claude-sonnet-5-5" && !tt.wantForced {
						require.NotNil(t, request.Thinking)
						require.Equal(t, "adaptive", request.Thinking.Type)
					}
					if tt.wantForced {
						require.Nil(t, request.Thinking)
					}
				})
			}
		})
	}
}

func TestForceToolCallAnthropic55Integration(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}
	if common.IsActiveEnvNonLocal() {
		t.Skip("Skipping integration test; LLM API keys are unavailable in non-local sidekick environments")
	}
	secretManager := secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
		&secret_manager.EnvSecretManager{},
		&secret_manager.KeyringSecretManager{},
		&secret_manager.LocalConfigSecretManager{},
	})
	_, err := secretManager.GetSecret("ANTHROPIC_API_KEY")
	require.NoError(t, err, "SIDE_INTEGRATION_TEST=true requires ANTHROPIC_API_KEY")

	for _, model := range []string{"claude-haiku-5-5", "claude-sonnet-5-5"} {
		t.Run(model, func(t *testing.T) {
			t.Parallel()
			for _, choiceType := range []common.ToolChoiceType{common.ToolChoiceTypeTool, common.ToolChoiceTypeRequired} {
				t.Run(string(choiceType), func(t *testing.T) {
					t.Parallel()
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					defer cancel()

					weatherTool := &common.Tool{
						Name:        "get_current_weather",
						Description: "Get the current weather in a given location",
						Parameters: (&jsonschema.Reflector{DoNotReference: true}).Reflect(&struct {
							Location string `json:"location"`
							Unit     string `json:"unit" jsonschema:"enum=celsius,enum=fahrenheit"`
						}{}),
					}
					tools := []*common.Tool{weatherTool}
					if choiceType == common.ToolChoiceTypeRequired {
						tools = append(tools, &common.Tool{
							Name:        "get_local_time",
							Description: "Get the current time in a given location",
							Parameters: (&jsonschema.Reflector{DoNotReference: true}).Reflect(&struct {
								Location string `json:"location"`
							}{}),
						})
					}
					history := NewLlm2ChatHistory("flow-tool-choice", "workspace-tool-choice")
					history.Append(&llm2.Message{
						Role:    llm2.RoleUser,
						Content: llm2.TextContentBlocks("What's the weather like in Paris in celsius?"),
					})
					input := StreamInput{
						Options: llm2.Options{
							ModelConfig: common.ModelConfig{Provider: "anthropic", Model: model},
							Tools:       tools,
						},
						Secrets:     secret_manager.SecretManagerContainer{SecretManager: secretManager},
						ChatHistory: &ChatHistoryContainer{History: history},
						WorkspaceId: "workspace-tool-choice",
						FlowId:      "flow-tool-choice",
						Providers: []common.ModelProviderPublicConfig{{
							Name:     "anthropic",
							Type:     "anthropic",
							AuthType: common.ProviderAuthTypeAPI,
						}},
					}

					response, _, err := runForcedToolCallTest(t, forcedToolCallTestConfig{StreamInput: input},
						func(_ context.Context, request StreamInput) (*llm2.MessageResponse, error) {
							request.FlowActionId = ""
							request.Secrets = input.Secrets
							activities := Llm2Activities{}
							return activities.Stream(ctx, request)
						})
					requireWorkflowSuccess(t, err)
					require.NotNil(t, response)

					calls := response.GetMessage().GetToolCalls()
					require.NotEmpty(t, calls, "expected a tool call, got content: %s", response.GetMessage().GetContentString())
					require.Equal(t, weatherTool.Name, calls[0].Name)
					var arguments struct {
						Location string `json:"location"`
						Unit     string `json:"unit"`
					}
					require.NoError(t, json.Unmarshal([]byte(calls[0].Arguments), &arguments))
					require.NotEmpty(t, arguments.Location)
					require.Equal(t, "celsius", arguments.Unit)
				})
			}
		})
	}
}
