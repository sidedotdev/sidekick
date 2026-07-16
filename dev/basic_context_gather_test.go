package dev

import (
	"context"
	"errors"
	"sidekick/coding/tree_sitter"
	"sidekick/common"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
	"sidekick/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type basicContextCallSiteResult struct {
	LegacyCalls    int
	Models         []string
	HandoffRefs    []persisted_ai.MessageRef
	AppendedInputs []persisted_ai.AppendMessageInput
	StopAtCoding   bool
}

func TestBasicContextGatherCallSiteSelection(t *testing.T) {
	tests := []struct {
		name              string
		contextGatherType ContextGatherType
		explore           bool
	}{
		{name: "absent"},
		{name: "legacy", contextGatherType: ContextGatherTypeLegacy},
		{name: "unsupported", contextGatherType: ContextGatherType("unsupported")},
		{name: "explore", contextGatherType: ContextGatherTypeExplore, explore: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())

			result := basicContextCallSiteResult{}
			wrapper := func(ctx workflow.Context) (basicContextCallSiteResult, error) {
				dCtx := contextGatherCallSiteDevContext(ctx, tt.contextGatherType)
				history := NewVersionedChatHistory(ctx, dCtx.WorkspaceId)
				legacy := func(DevContext, string, *DevPlanExecution, *DevStep, ...persisted_ai.WeightedRankQuery) (string, string, error) {
					result.LegacyCalls++
					return "legacy context", "legacy context extended", nil
				}
				codeContext, extension, err := prepareBasicCodingContext(dCtx, "complete basic requirements", history, legacy)
				if err != nil {
					return result, err
				}
				if tt.explore {
					resolve := func() common.ModelConfig {
						return common.ModelConfig{Provider: "test", Model: "coding-model"}
					}
					err = EditCodeWithModelConfigResolver(
						dCtx,
						resolve,
						extension,
						history,
						InitialCodeInfo{CodeContext: codeContext, Requirements: "complete basic requirements"},
						nil,
					)
				}
				return result, err
			}
			env.RegisterWorkflow(wrapper)
			registerContextGatherCallSiteActivities(t, env, &result)

			env.ExecuteWorkflow(wrapper)
			require.True(t, env.IsWorkflowCompleted())
			require.NoError(t, env.GetWorkflowError())
			require.NoError(t, env.GetWorkflowResult(&result))

			if tt.explore {
				assert.Zero(t, result.LegacyCalls)
				assert.Equal(t, []string{"localization-model", "localization-model", "coding-model"}, result.Models)
				require.Len(t, result.HandoffRefs, 3)
				assert.Equal(t, []string{"assistant", "user", "user"}, []string{
					result.HandoffRefs[0].Role,
					result.HandoffRefs[1].Role,
					result.HandoffRefs[2].Role,
				})
				assert.Equal(t, []string{
					"filtered-context-call",
					"filtered-context-result",
					"coding-instructions",
				}, []string{
					result.HandoffRefs[0].BlockKeys[0],
					result.HandoffRefs[1].BlockKeys[0],
					result.HandoffRefs[2].BlockKeys[0],
				})
			} else {
				assert.Equal(t, 1, result.LegacyCalls)
				assert.Empty(t, result.Models)
			}
		})
	}
}

func TestBasicContextGatherRunsForEachCodingInvocation(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())

	result := basicContextCallSiteResult{StopAtCoding: true}
	wrapper := func(ctx workflow.Context) (basicContextCallSiteResult, error) {
		dCtx := contextGatherCallSiteDevContext(ctx, ContextGatherTypeExplore)

		if _, err := codingSubflow(dCtx, "complete basic requirements", nil, ""); err == nil {
			return result, errors.New("expected initial coding invocation to stop at the coding model")
		}

		startBranch := "review-target"
		feedbackRankQueries := []persisted_ai.WeightedRankQuery{{
			Query:  "address the review feedback",
			Weight: rankWeightLatestReview,
		}}
		if _, err := codingSubflow(
			dCtx,
			"review-updated requirements",
			&startBranch,
			"review-tree-hash",
			feedbackRankQueries...,
		); err == nil {
			return result, errors.New("expected review coding invocation to stop at the coding model")
		}

		return result, nil
	}
	env.RegisterWorkflow(wrapper)
	registerContextGatherCallSiteActivities(t, env, &result)

	env.ExecuteWorkflow(wrapper)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	assert.Equal(t, 4, countModelCalls(result.Models, "localization-model"))
	assert.GreaterOrEqual(t, countModelCalls(result.Models, "coding-model"), 2)
}

func countModelCalls(models []string, model string) int {
	count := 0
	for _, calledModel := range models {
		if calledModel == model {
			count++
		}
	}
	return count
}

func contextGatherCallSiteDevContext(ctx workflow.Context, gatherType ContextGatherType) DevContext {
	dCtx := DevContext{
		ExecContext: flow_action.ExecContext{
			Context:     utils.NoRetryCtx(ctx),
			WorkspaceId: "context-call-site-workspace",
			GlobalState: &flow_action.GlobalState{},
			FlowScope:   &flow_action.FlowScope{SubflowName: "call-site-test"},
			Secrets: &secret_manager.SecretManagerContainer{
				SecretManager: secret_manager.MockSecretManager{},
			},
		},
		ContextGatherType: gatherType,
	}
	dCtx.SetLLMConfig(common.LLMConfig{
		Defaults: []common.ModelConfig{{Provider: "test", Model: "default-model"}},
		UseCaseConfigs: map[string][]common.ModelConfig{
			common.CodeLocalizationKey: {{Provider: "test", Model: "localization-model"}},
			common.CodingKey:           {{Provider: "test", Model: "coding-model"}},
		},
	})
	return dCtx
}

func registerContextGatherCallSiteActivities(
	t *testing.T,
	env *testsuite.TestWorkflowEnvironment,
	result *basicContextCallSiteResult,
) {
	t.Helper()
	env.RegisterActivity(persisted_ai.RepairToolCallArgumentsActivity)
	env.OnGetVersion("done-required-protocol", workflow.DefaultVersion, 1).
		Return(workflow.DefaultVersion).
		Maybe()

	var flags *fflag.FFlagActivities
	env.OnActivity(
		flags.EvalBoolFlag,
		mock.Anything,
		mock.MatchedBy(func(params fflag.EvaluateFeatureFlagParams) bool {
			return params.FlagName == fflag.ContextGatheringForget
		}),
	).Return(false, nil).Maybe()

	var flowActivities *flow_action.FlowActivities
	env.OnActivity(flowActivities.PersistSubflow, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(flowActivities.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(flowActivities.GetModelMetadata, mock.Anything, mock.Anything, mock.Anything).
		Return(common.ModelMetadata{}, nil).
		Maybe()

	var historyActivities *persisted_ai.ChatHistoryActivities
	env.OnActivity(historyActivities.AppendMessage, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input persisted_ai.AppendMessageInput) (*persisted_ai.MessageRef, error) {
			result.AppendedInputs = append(result.AppendedInputs, input)
			blockKey := "gatherer-message"
			if len(input.Message.Content) == 1 {
				block := input.Message.Content[0]
				switch {
				case block.ToolUse != nil && block.ToolUse.Name == bulkReadFileTool.Name:
					blockKey = "filtered-context-call"
				case block.ToolResult != nil && block.ToolResult.Name == bulkReadFileTool.Name:
					blockKey = "filtered-context-result"
				case block.Type == llm2.ContentBlockTypeText &&
					(strings.Contains(block.Text, "complete basic requirements") ||
						strings.Contains(block.Text, "complete planned requirements")):
					blockKey = "coding-instructions"
				}
			}
			return &persisted_ai.MessageRef{
				BlockKeys: []string{blockKey},
				Role:      string(input.Message.Role),
			}, nil
		}).
		Maybe()
	env.OnActivity(historyActivities.ManageV4, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input persisted_ai.ManageInput) (*persisted_ai.ManageOutput, error) {
			history, ok := input.ChatHistory.History.(*persisted_ai.Llm2ChatHistory)
			require.True(t, ok)
			managedHistory := persisted_ai.NewLlm2ChatHistory(history.FlowId(), history.WorkspaceId())
			managedHistory.SetRefs(history.Refs())
			return &persisted_ai.ManageOutput{
				ChatHistory: &persisted_ai.ChatHistoryContainer{History: managedHistory},
			}, nil
		}).
		Maybe()
	env.OnActivity(historyActivities.ExtractVisibleCodeBlocks, mock.Anything, mock.Anything).
		Return([]tree_sitter.CodeBlock{}, nil).
		Maybe()

	localizationCalls := 0
	var llmActivities *persisted_ai.Llm2Activities
	env.OnActivity(llmActivities.Stream, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input persisted_ai.StreamInput) (*llm2.MessageResponse, error) {
			result.Models = append(result.Models, input.Options.ModelConfig.Model)
			if input.Options.ModelConfig.Model == "coding-model" {
				history, ok := input.ChatHistory.History.(*persisted_ai.Llm2ChatHistory)
				require.True(t, ok)
				result.HandoffRefs = history.Refs()
				if result.StopAtCoding {
					return nil, errors.New("stop after context gathering")
				}
				return contextGatherTerminalResponse("done-call", doneTool.Name), nil
			}

			localizationCalls++
			if localizationCalls%2 == 1 {
				return &llm2.MessageResponse{
					StopReason: "tool_use",
					Output: llm2.Message{
						Role: llm2.RoleAssistant,
						Content: []llm2.ContentBlock{{
							Type: llm2.ContentBlockTypeToolUse,
							ToolUse: &llm2.ToolUseBlock{
								Id:        "read-call",
								Name:      bulkReadFileTool.Name,
								Arguments: `{}`,
							},
						}},
					},
				}, nil
			}
			return contextGatherTerminalResponse("ready-call", contextGatherReadyTool.Name), nil
		}).
		Maybe()
}
