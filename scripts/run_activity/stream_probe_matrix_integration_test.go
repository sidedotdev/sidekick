package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/llm2"
	"sidekick/openai_oauth"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
	"sidekick/srv/sqlite"

	"github.com/stretchr/testify/require"
)

type matrixTestSecrets struct {
	secret_manager.MockSecretManager
	oauth string
}

func (s matrixTestSecrets) GetSecret(name string) (string, error) {
	if name == openai_oauth.SecretName {
		return s.oauth, nil
	}
	return s.MockSecretManager.GetSecret(name)
}

func TestExecuteProbeMatrix(t *testing.T) {
	// The probe swaps http.DefaultTransport, so these tests must run sequentially.
	for _, tc := range []struct {
		name       string
		auth       common.ProviderAuthType
		hostedFail bool
	}{
		{"subscription", common.ProviderAuthTypeSubscription, false},
		{"subscription_hosted_error", common.ProviderAuthTypeSubscription, true},
		{"api", common.ProviderAuthTypeAPI, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var alphaCalls, clientCalls, hostedCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
					t.Error("missing bearer authentication")
					http.Error(w, "missing authentication", http.StatusUnauthorized)
					return
				}
				if tc.auth == common.ProviderAuthTypeSubscription && r.Header.Get("ChatGPT-Account-Id") != "matrix-account" {
					t.Error("missing subscription account")
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				if r.URL.Path == "/alpha/search" {
					alphaCalls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"output":"Search fixture result","results":[{"type":"text_result","url":"https://go.dev/doc/","title":"Go documentation"}]}`)
					return
				}
				if r.URL.Path != "/responses" {
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				var tools []struct {
					Type string `json:"type"`
					Name string `json:"name"`
				}
				if err := json.Unmarshal(body["tools"], &tools); err != nil {
					t.Errorf("decode tools: %v", err)
					return
				}
				hosted, advisor, clientSearch := false, false, ""
				for _, tool := range tools {
					hosted = hosted || tool.Type == "web_search"
					advisor = advisor || tool.Name == "advisor_guide"
					if name, ok := llm2.BuiltinToolNameFromClientName(tool.Name); ok && name == "web_search" {
						clientSearch = tool.Name
					}
				}
				if hosted == (clientSearch != "") {
					t.Error("expected exactly one hosted or client search tool")
				}
				if hosted {
					hostedCalls.Add(1)
				} else {
					clientCalls.Add(1)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				emit := func(event map[string]any) {
					data, err := json.Marshal(event)
					if err != nil {
						t.Errorf("encode fixture event: %v", err)
						return
					}
					fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
				}
				emit(map[string]any{"type": "response.created", "response": map[string]any{
					"id": "resp_matrix", "model": "gpt-6-astra", "status": "in_progress",
				}})
				item := func(kind, name, arguments string) {
					call := map[string]any{
						"id": "item_" + kind, "type": kind, "status": "in_progress",
					}
					if name != "" {
						call["name"], call["call_id"], call["arguments"] = name, "call_"+name, ""
					}
					emit(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": call})
					if name != "" {
						call["arguments"] = arguments
						emit(map[string]any{"type": "response.function_call_arguments.done", "output_index": 0,
							"item_id": call["id"], "arguments": arguments})
					}
					call["status"] = "completed"
					emit(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": call})
				}
				if hosted {
					item("web_search_call", "", "")
					if tc.hostedFail {
						emit(map[string]any{"type": "error", "code": "server_error", "message": "hosted fixture failure"})
						return
					}
				}
				continuation := strings.Contains(string(body["input"]), `"function_call_output"`)
				if clientSearch != "" && !continuation {
					item("function_call", clientSearch, `{"query":"Go documentation"}`)
				} else if advisor {
					item("function_call", "advisor_guide", `{"feedback":"Search fixture result"}`)
				} else {
					emit(map[string]any{"type": "response.content_part.added", "output_index": 1,
						"item_id": "msg_matrix", "content_index": 0, "part": map[string]any{"type": "output_text", "text": ""}})
					emit(map[string]any{"type": "response.output_text.delta", "output_index": 1,
						"item_id": "msg_matrix", "content_index": 0, "delta": "Search fixture result"})
				}
				emit(map[string]any{"type": "response.completed", "response": map[string]any{
					"id": "resp_matrix", "model": "gpt-6-astra", "status": "completed", "output": []any{},
					"usage": map[string]any{"input_tokens": 10, "output_tokens": 5},
				}})
			}))
			defer server.Close()

			oauth, err := json.Marshal(openai_oauth.Credentials{
				AccessToken: "matrix-token", RefreshToken: "matrix-refresh",
				AccountID: "matrix-account", ExpiresAt: 9999999999,
			})
			require.NoError(t, err)
			tools, err := minimalProbeTools()
			require.NoError(t, err)
			history := persisted_ai.NewLlm2ChatHistory("matrix-flow", "matrix-workspace")
			history.Append(&llm2.Message{Role: llm2.RoleUser, Content: llm2.TextContentBlocks("Search, then call advisor_guide.")})
			parallel := true
			input := persisted_ai.StreamInput{
				Options: llm2.Options{
					ModelConfig: common.ModelConfig{Provider: "openai", Model: "gpt-6-astra", ReasoningEffort: "high"},
					Tools:       tools, ParallelToolCalls: &parallel, ToolChoice: common.ToolChoice{Type: "required"},
				},
				Secrets:     secret_manager.SecretManagerContainer{SecretManager: matrixTestSecrets{oauth: string(oauth)}},
				ChatHistory: &persisted_ai.ChatHistoryContainer{History: history},
				FlowId:      "matrix-flow", WorkspaceId: "matrix-workspace",
				Providers: []common.ModelProviderPublicConfig{{
					Name: "openai", Type: "openai_responses_compatible", BaseURL: server.URL,
					AuthType: tc.auth, BuiltinTools: []string{"web_search"},
				}},
			}
			activities := &persisted_ai.Llm2Activities{Storage: sqlite.NewTestSqliteStorage(t, "matrix")}
			dir := filepath.Join(t.TempDir(), "matrix")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			report, err := executeProbeMatrix(ctx, activities, input, dir)
			if tc.hostedFail {
				require.ErrorContains(t, err, "hosted fixture failure")
			} else {
				require.NoError(t, err)
			}
			var runs []probeMatrixRun
			require.NoError(t, json.Unmarshal(report, &runs))
			require.Len(t, runs, 17)
			saved, err := os.ReadFile(filepath.Join(dir, "report.json"))
			require.NoError(t, err)
			require.JSONEq(t, string(report), string(saved))

			providerRuns, hostedRuns := 0, 0
			for _, run := range runs {
				require.Equal(t, string(tc.auth), run.AuthMode)
				provider := strings.HasPrefix(run.Name, "provider-")
				if provider {
					providerRuns++
				} else {
					require.True(t, strings.HasPrefix(run.Name, "hosted-"), run.Name)
					hostedRuns++
				}
				if tc.hostedFail && !provider {
					require.Equal(t, "providerError", run.Trace.Outcome, run.Name)
					require.Contains(t, run.Error, "hosted fixture failure")
				} else {
					require.Empty(t, run.Error, run.Name)
					require.Equal(t, "success", run.Trace.Outcome, run.Name)
				}
				require.Positive(t, run.Trace.SearchesCompleted, run.Name)
				request, err := os.ReadFile(filepath.Join(dir, run.Name+".request.json"))
				require.NoError(t, err)
				if provider && tc.auth == common.ProviderAuthTypeSubscription {
					require.Contains(t, string(request), `"builtin_web_search"`)
				} else {
					require.Contains(t, string(request), `"web_search"`)
					require.NotContains(t, string(request), `"builtin_web_search"`)
				}
			}
			require.Equal(t, 4, providerRuns)
			require.Equal(t, 13, hostedRuns)
			if tc.auth == common.ProviderAuthTypeSubscription {
				require.EqualValues(t, 4, alphaCalls.Load())
				require.EqualValues(t, 8, clientCalls.Load())
				require.EqualValues(t, 13, hostedCalls.Load())
			} else {
				require.Zero(t, alphaCalls.Load())
				require.Zero(t, clientCalls.Load())
				require.EqualValues(t, 17, hostedCalls.Load())
			}
		})
	}
}
