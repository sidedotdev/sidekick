package persisted_ai

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"sidekick/llm2"
	"sidekick/utils"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestStreamActivity_BadRequestRetryPolicy(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusBadRequest, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()

			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"provider diagnostic"}}`))
			}))
			defer server.Close()

			activities := &Llm2Activities{
				providerFactory: func(common.ModelConfig, []common.ModelProviderPublicConfig) (llm2.Provider, error) {
					return llm2.OpenAIProvider{BaseURL: server.URL}, nil
				},
			}
			input := newRunawayStreamActivityInput(t)
			input.Options.ModelConfig = common.ModelConfig{Provider: "openai", Model: "gpt-4.1-nano"}

			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())
			env.RegisterActivity(activities.Stream)
			env.ExecuteWorkflow(func(ctx workflow.Context) error {
				ctx = utils.LlmHeartbeatCtx(ctx)
				return workflow.ExecuteActivity(ctx, activities.Stream, input).Get(ctx, nil)
			})

			require.True(t, env.IsWorkflowCompleted())
			require.Error(t, env.GetWorkflowError())
			assert.Contains(t, env.GetWorkflowError().Error(), "provider diagnostic")
			wantRequests := int32(utils.LlmNumRetries)
			if status == http.StatusBadRequest {
				wantRequests = 1
			}
			assert.Equal(t, wantRequests, requests.Load())
		})
	}
}
