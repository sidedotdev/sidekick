package persisted_ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sidekick/common"
	"sidekick/fflag"
	"testing"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/stretchr/testify/require"
	ffclient "github.com/thomaspoignant/go-feature-flag"
	"github.com/thomaspoignant/go-feature-flag/retriever/fileretriever"
)

func TestStreamAnthropicForcedToolChoiceExclusions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		model      string
		flagValue  string
		compatible bool
		wantForced bool
	}{
		{name: "default haiku exclusion", model: "claude-haiku-5-5", wantForced: true},
		{name: "default sonnet downgrade", model: "claude-sonnet-5-5"},
		{name: "empty exclusions downgrade haiku", model: "claude-haiku-5-5", flagValue: "[]"},
		{name: "flag excludes sonnet", model: "claude-sonnet-5-5", flagValue: "[sonnet]", wantForced: true},
		{name: "flag replaces defaults", model: "claude-haiku-5-5", flagValue: "[sonnet]"},
		{name: "case insensitive exclusion", model: "claude-sonnet-5-5", flagValue: "[SONNET]", wantForced: true},
		{name: "blank exclusion is ignored", model: "claude-sonnet-5-5", flagValue: `[""]`},
		{name: "invalid flag falls back", model: "claude-haiku-5-5", flagValue: "[42]", wantForced: true},
		{name: "old model retains forced choice", model: "claude-opus-4-5", flagValue: "[]", wantForced: true},
		{name: "compatible ignores exclusions", model: "claude-haiku-5-5", flagValue: "[haiku]", compatible: true},
		{name: "compatible old model downgrades", model: "claude-opus-4-5", flagValue: "[opus]", compatible: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, choiceType := range []common.ToolChoiceType{common.ToolChoiceTypeTool, common.ToolChoiceTypeRequired} {
				t.Run(string(choiceType), func(t *testing.T) {
					t.Parallel()

					var flags *fflag.FFlagActivities
					if tt.flagValue != "" {
						path := filepath.Join(t.TempDir(), "flags.yml")
						data := fmt.Sprintf(`anthropic-forced-tool-choice-exclusions:
  variations:
    selected: %s
  defaultRule:
    variation: selected
`, tt.flagValue)
						require.NoError(t, os.WriteFile(path, []byte(data), 0600))
						client, err := ffclient.New(ffclient.Config{
							Retriever: &fileretriever.Retriever{Path: path},
						})
						require.NoError(t, err)
						t.Cleanup(client.Close)
						flags = &fflag.FFlagActivities{FFlag: fflag.FFlag{Client: client}}
					}

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
					input.Options.ToolChoice = common.ToolChoice{Type: choiceType}
					if choiceType == common.ToolChoiceTypeTool {
						input.Options.ToolChoice.Name = "record_plan"
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

					activities := Llm2Activities{FeatureFlags: flags}
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					_, err := activities.Stream(ctx, input)
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
