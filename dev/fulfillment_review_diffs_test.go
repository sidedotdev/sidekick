package dev

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/coding"
	"sidekick/coding/git"
	"sidekick/coding/tree_sitter"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
	"sidekick/srv"
	"sidekick/temporalmeta"
	"sidekick/utils"
)

// CriteriaFulfillmentDiffTestSuite covers which diff the auto-reviewer judges:
// the diff since the last review once there is one, the full diff otherwise,
// and a graceful full-diff fallback when git itself fails.
type CriteriaFulfillmentDiffTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment

	// globalTargetBranch is the branch the flow currently targets, which for
	// in-place work is the very branch the work sits on.
	globalTargetBranch string
}

func (s *CriteriaFulfillmentDiffTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()

	var flags *fflag.FFlagActivities
	settings := DefaultVerifierSettings()
	settings.Enabled = false
	s.env.OnActivity(flags.EvaluateFlags, mock.Anything, mock.Anything).
		Return(verifierFlagValues(settings), nil).Maybe()

	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()

	var srvActivities srv.Activities
	s.env.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
	s.env.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()
}

func (s *CriteriaFulfillmentDiffTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func (s *CriteriaFulfillmentDiffTestSuite) newDevContext(ctx workflow.Context) DevContext {
	gs := &flow_action.GlobalState{}
	gs.InitValues()
	if s.globalTargetBranch != "" {
		gs.SetValue(common.KeyCurrentTargetBranch, s.globalTargetBranch)
	}
	dCtx := DevContext{
		ExecContext: flow_action.ExecContext{
			WorkspaceId: "test-workspace",
			Context:     ctx,
			FlowScope: &flow_action.FlowScope{
				SubflowName: "test-subflow",
			},
			GlobalState: gs,
			EnvContainer: &env.EnvContainer{
				Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
			},
			Secrets: &secret_manager.SecretManagerContainer{
				SecretManager: secret_manager.MockSecretManager{},
			},
			EmbeddingConfig: common.EmbeddingConfig{
				Defaults: []common.ModelConfig{{Provider: "test", Model: "embedding-model"}},
			},
		},
		RepoConfig: common.RepoConfig{},
	}
	dCtx.SetLLMConfig(common.LLMConfig{
		Defaults: []common.ModelConfig{{Provider: "test", Model: "judging-model"}},
	})
	return dCtx
}

// mockCriteriaReviewer stands in for the auto-reviewer, reporting the prompt
// text it was shown so tests can tell which diff reached it.
func (s *CriteriaFulfillmentDiffTestSuite) mockCriteriaReviewer() *[]string {
	promptTexts := &[]string{}

	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistSubflow, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(fa.GetModelMetadata, mock.Anything, mock.Anything, mock.Anything).
		Return(common.ModelMetadata{}, nil).Maybe()

	var meta *temporalmeta.TemporalMetaActivities
	s.env.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).
		Return([]domain.TemporalActivityRef{}, nil).Maybe()

	s.env.OnActivity(env.GetEnvironmentInfoActivity, mock.Anything, mock.Anything).
		Return(env.GetEnvironmentInfoOutput{}, nil).Maybe()

	s.env.RegisterActivity(persisted_ai.RepairToolCallArgumentsActivity)

	var historyActivities *persisted_ai.ChatHistoryActivities
	s.env.OnActivity(historyActivities.AppendMessage, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input persisted_ai.AppendMessageInput) (*persisted_ai.MessageRef, error) {
			for _, block := range input.Message.Content {
				if block.Type == llm2.ContentBlockTypeText {
					*promptTexts = append(*promptTexts, block.Text)
				}
			}
			return &persisted_ai.MessageRef{
				BlockKeys: []string{"fulfillment-message"},
				Role:      string(input.Message.Role),
			}, nil
		}).Maybe()
	s.env.OnActivity(historyActivities.ManageV4, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input persisted_ai.ManageInput) (*persisted_ai.ManageOutput, error) {
			return &persisted_ai.ManageOutput{ChatHistory: input.ChatHistory}, nil
		}).Maybe()
	s.env.OnActivity(historyActivities.ExtractVisibleCodeBlocks, mock.Anything, mock.Anything).
		Return([]tree_sitter.CodeBlock{}, nil).Maybe()

	var llmActivities *persisted_ai.Llm2Activities
	s.env.OnActivity(llmActivities.Stream, mock.Anything, mock.Anything).
		Return(&llm2.MessageResponse{
			StopReason: "tool_use",
			Output: llm2.Message{
				Role: llm2.RoleAssistant,
				Content: []llm2.ContentBlock{{
					Type: llm2.ContentBlockTypeToolUse,
					ToolUse: &llm2.ToolUseBlock{
						Id:        "fulfillment-call",
						Name:      determineCriteriaFulfillmentTool.Name,
						Arguments: `{"whatWasActuallyDone":"the work","analysis":"looks complete","isFulfilled":true}`,
					},
				}},
			},
		}, nil).Maybe()

	return promptTexts
}

// runCheckWorkflow runs the whole criteria check, reporting the full diff the
// review was based on.
func (s *CriteriaFulfillmentDiffTestSuite) runCheckWorkflow(promptInfo CheckWorkInfo) (string, error) {
	testWorkflow := func(ctx workflow.Context) (string, error) {
		ctx = utils.NoRetryCtx(ctx)
		_, fullDiff, err := CheckWorkMeetsCriteriaWithDiff(s.newDevContext(ctx), promptInfo)
		return fullDiff, err
	}

	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)
	s.Require().True(s.env.IsWorkflowCompleted())
	if err := s.env.GetWorkflowError(); err != nil {
		return "", err
	}
	var fullDiff string
	s.Require().NoError(s.env.GetWorkflowResult(&fullDiff))
	return fullDiff, nil
}

// runDiffWorkflow executes criteriaFulfillmentReviewDiff in a workflow, also
// reporting whether any user prompt was issued along the way.
func (s *CriteriaFulfillmentDiffTestSuite) runDiffWorkflow(promptInfo CheckWorkInfo) (string, bool, error) {
	requestForUserSeen := false
	testWorkflow := func(ctx workflow.Context) (string, error) {
		ctx = utils.NoRetryCtx(ctx)
		signalCh := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
		workflow.Go(ctx, func(ctx workflow.Context) {
			var req flow_action.RequestForUser
			signalCh.Receive(ctx, &req)
			requestForUserSeen = true
		})

		reviewDiff, _, _, err := criteriaFulfillmentReviewDiff(s.newDevContext(ctx), promptInfo, true)
		return reviewDiff, err
	}

	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)
	s.Require().True(s.env.IsWorkflowCompleted())

	if err := s.env.GetWorkflowError(); err != nil {
		return "", requestForUserSeen, err
	}
	var diff string
	s.Require().NoError(s.env.GetWorkflowResult(&diff))
	return diff, requestForUserSeen, nil
}

func (s *CriteriaFulfillmentDiffTestSuite) TestUsesSinceDiffWhenPriorReviewExists() {
	var params []coding.GenerateReviewDiffsParams
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, p coding.GenerateReviewDiffsParams) (coding.GenerateReviewDiffsResult, error) {
			params = append(params, p)
			return coding.GenerateReviewDiffsResult{FullDiff: "full diff", SinceDiff: "since diff"}, nil
		},
	)

	diff, requestForUserSeen, err := s.runDiffWorkflow(CheckWorkInfo{
		BaseBranch:     "main",
		StartPoint:     "pinned-sha",
		LastReviewDiff: "prior review diff",
	})
	s.Require().NoError(err)
	s.Equal("since diff", diff)
	s.False(requestForUserSeen)

	s.Require().Len(params, 1)
	s.Equal("pinned-sha", params[0].StartPoint)
	s.Equal("main", params[0].BaseBranch)
	s.Equal("prior review diff", params[0].PriorReviewDiff)
}

// TestPinnedStartPointIsNotReplacedByTargetBranch covers in-place work, which
// sits on the branch it targets: attributing anything to that branch would hide
// the work, so the pinned start point alone bounds it.
func (s *CriteriaFulfillmentDiffTestSuite) TestPinnedStartPointIsNotReplacedByTargetBranch() {
	s.globalTargetBranch = "main"
	s.mockCriteriaReviewer()

	var params []coding.GenerateReviewDiffsParams
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, p coding.GenerateReviewDiffsParams) (coding.GenerateReviewDiffsResult, error) {
			params = append(params, p)
			return coding.GenerateReviewDiffsResult{FullDiff: "full diff"}, nil
		},
	)

	_, err := s.runCheckWorkflow(CheckWorkInfo{StartPoint: "in-place-start-sha"})
	s.Require().NoError(err)

	s.Require().Len(params, 1)
	s.Equal("in-place-start-sha", params[0].StartPoint)
	s.Empty(params[0].BaseBranch)
}

func (s *CriteriaFulfillmentDiffTestSuite) TestUsesFullDiffOnFirstRound() {
	var params []coding.GenerateReviewDiffsParams
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, p coding.GenerateReviewDiffsParams) (coding.GenerateReviewDiffsResult, error) {
			params = append(params, p)
			return coding.GenerateReviewDiffsResult{FullDiff: "full diff"}, nil
		},
	)

	diff, _, err := s.runDiffWorkflow(CheckWorkInfo{BaseBranch: "main"})
	s.Require().NoError(err)
	s.Equal("full diff", diff)

	s.Require().Len(params, 1)
	s.Equal("main", params[0].StartPoint, "the base branch is the start point when none is pinned")
	s.Empty(params[0].PriorReviewDiff)
}

func (s *CriteriaFulfillmentDiffTestSuite) TestFallsBackToFullDiffWhenSinceDiffDegrades() {
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).Return(
		coding.GenerateReviewDiffsResult{
			FullDiff:       "full diff",
			SinceDiffError: "unparseable prior diff",
		}, nil,
	)

	diff, _, err := s.runDiffWorkflow(CheckWorkInfo{
		BaseBranch:     "main",
		LastReviewDiff: "prior review diff",
	})
	s.Require().NoError(err)
	s.Equal("full diff", diff)
}

// A git failure can't be fixed by retrying at the user's prompt, so the check
// falls back to the base-branch diff instead of blocking on the user.
func (s *CriteriaFulfillmentDiffTestSuite) TestFallsBackToBaseBranchDiffWithoutUserPromptOnActivityFailure() {
	callCount := 0
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, p coding.GenerateReviewDiffsParams) (coding.GenerateReviewDiffsResult, error) {
			callCount++
			return coding.GenerateReviewDiffsResult{}, errors.New("bad object deadbeef")
		},
	)
	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return("fallback diff", nil)

	diff, requestForUserSeen, err := s.runDiffWorkflow(CheckWorkInfo{
		BaseBranch:     "main",
		LastReviewDiff: "prior review diff",
	})
	s.Require().NoError(err)
	s.Equal("fallback diff", diff)
	s.False(requestForUserSeen, "an unresolvable review-diff failure should not prompt the user")
	s.Equal(1, callCount, "the activity should not be retried via user prompt")
}

// Staged work is just one of the cases generated review diffs cover: a
// staged-only diff misses whatever the agent committed as well as changes
// merged in from the target branch, so it must never take precedence.
func (s *CriteriaFulfillmentDiffTestSuite) TestReviewsGeneratedDiffEvenWhenWorkIsStaged() {
	const generatedDiff = "generated review diff marker"

	var ffa *fflag.FFlagActivities
	s.env.OnActivity(ffa.EvalBoolFlag, mock.Anything, mock.Anything).Return(true, nil).Maybe()

	var reviewDiffParams []coding.GenerateReviewDiffsParams
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, p coding.GenerateReviewDiffsParams) (coding.GenerateReviewDiffsResult, error) {
			reviewDiffParams = append(reviewDiffParams, p)
			return coding.GenerateReviewDiffsResult{FullDiff: generatedDiff}, nil
		},
	)

	stagedDiffCalls := 0
	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, envContainer env.EnvContainer, params git.GitDiffParams) (string, error) {
			stagedDiffCalls++
			return "staged diff", nil
		},
	).Maybe()

	promptTexts := s.mockCriteriaReviewer()

	fullDiff, err := s.runCheckWorkflow(CheckWorkInfo{
		BaseBranch:   "main",
		Requirements: "do the work",
	})
	s.Require().NoError(err)
	s.Equal(generatedDiff, fullDiff)

	s.Require().Len(reviewDiffParams, 1)
	s.Equal("main", reviewDiffParams[0].StartPoint)
	s.Zero(stagedDiffCalls, "generated review diffs replace the staged-only diff")
	s.Contains(strings.Join(*promptTexts, "\n"), generatedDiff, "the reviewer judges the generated diff")
}

func TestCriteriaFulfillmentDiffTestSuite(t *testing.T) {
	suite.Run(t, new(CriteriaFulfillmentDiffTestSuite))
}

func (s *CriteriaFulfillmentDiffTestSuite) TestRejectedUserReviewIncludesOriginalDiff() {
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).
		Return(coding.GenerateReviewDiffsResult{
			FullDiff:  "current full work",
			SinceDiff: "changes after user rejection",
		}, nil)
	prompts := s.mockCriteriaReviewer()
	_, err := s.runCheckWorkflow(CheckWorkInfo{
		BaseBranch:     "main",
		Requirements:   formatRequirementsWithReview("do the work", nil, "original user reviewed work", "address feedback"),
		LastReviewDiff: "original user reviewed work",
	})
	s.Require().NoError(err)
	text := strings.Join(*prompts, "\n")
	s.Contains(text, "original user reviewed work")
	s.Contains(text, "changes after user rejection")
}

func (s *CriteriaFulfillmentDiffTestSuite) TestUnchangedRejectedUserReviewIncludesOriginalDiff() {
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).
		Return(coding.GenerateReviewDiffsResult{FullDiff: "original user reviewed work"}, nil)
	prompts := s.mockCriteriaReviewer()
	_, err := s.runCheckWorkflow(CheckWorkInfo{
		BaseBranch:     "main",
		Requirements:   formatRequirementsWithReview("do the work", nil, "original user reviewed work", "address feedback"),
		LastReviewDiff: "original user reviewed work",
	})
	s.Require().NoError(err)
	text := strings.Join(*prompts, "\n")
	s.Contains(text, "original user reviewed work")
	s.NotContains(text, "git diff is empty: no changes were made.")
}

func (s *CriteriaFulfillmentDiffTestSuite) TestRepeatedPlanAutoReviewIncludesUnchangedWork() {
	const fullDiff = "unchanged staged work marker"
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, params coding.GenerateReviewDiffsParams) (coding.GenerateReviewDiffsResult, error) {
			s.Empty(params.PriorReviewDiff, "auto-review must not establish a user-review baseline")
			return coding.GenerateReviewDiffsResult{FullDiff: fullDiff}, nil
		}).Twice()
	prompts := s.mockCriteriaReviewer()
	testWorkflow := func(ctx workflow.Context) error {
		dCtx := s.newDevContext(utils.NoRetryCtx(ctx))
		state := stepReviewState{startPoint: "step-start"}
		for range 2 {
			_, err := CheckWorkMeetsCriteria(dCtx, state.checkWorkInfo(dCtx, CheckWorkInfo{
				BaseBranch: "main", Requirements: "do the work",
			}))
			if err != nil {
				return err
			}
		}
		return nil
	}
	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)
	s.Require().NoError(s.env.GetWorkflowError())
	reviews := 0
	for _, text := range *prompts {
		if strings.Contains(text, "# START REQUIREMENTS") {
			reviews++
			s.Contains(text, fullDiff)
			s.NotContains(text, "git diff is empty: no changes were made.")
		}
	}
	s.Equal(2, reviews)
}

func (s *CriteriaFulfillmentDiffTestSuite) TestRejectedReviewBudgetPreservesBothComponents() {
	budget := (common.ModelMetadata{}).MaxChars() / 4
	original := "ORIGINAL_WORK\n" + strings.Repeat("a", budget*3)
	incremental := "INCREMENTAL_WORK\n" + strings.Repeat("b", budget*3)
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).
		Return(coding.GenerateReviewDiffsResult{FullDiff: "current full diff", SinceDiff: incremental}, nil)
	s.env.OnActivity(SummarizeDiffActivity, mock.Anything, mock.Anything).
		Return("", errors.New("summarizer unavailable")).Maybe()
	prompts := s.mockCriteriaReviewer()
	_, err := s.runCheckWorkflow(CheckWorkInfo{
		BaseBranch: "main",
		Requirements: formatRequirementsWithReview("do the work", nil,
			original[:budget], "address feedback"),
		LastReviewDiff: original,
	})
	s.Require().NoError(err)
	text := strings.Join(*prompts, "\n")
	s.Equal(1, strings.Count(text, "ORIGINAL_WORK"))
	s.Equal(1, strings.Count(text, "INCREMENTAL_WORK"))
	s.Less(len(text), budget*2+10000)
}

func (s *CriteriaFulfillmentDiffTestSuite) TestComparisonAvailabilitySelectsPromptStyle() {
	for _, tc := range []struct {
		name        string
		prior       string
		since       string
		unavailable string
		incremental bool
	}{
		{name: "incremental", prior: "PRIOR_WORK", since: "INCREMENTAL_WORK", incremental: true},
		{name: "empty incremental", prior: "PRIOR_WORK", incremental: true},
		{name: "unavailable", prior: "PRIOR_WORK", unavailable: "incompatible bases"},
		{name: "first review"},
	} {
		s.Run(tc.name, func() {
			s.SetupTest()
			var ca *coding.CodingActivities
			s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).
				Return(coding.GenerateReviewDiffsResult{
					FullDiff: "CURRENT_FULL_WORK", SinceDiff: tc.since, SinceDiffError: tc.unavailable,
				}, nil)
			prompts := s.mockCriteriaReviewer()
			requirements := "ORIGINAL_REQUIREMENTS"
			if tc.prior != "" {
				requirements = formatRequirementsWithReview(requirements, []string{"HISTORICAL_FEEDBACK"}, tc.prior, "LATEST_FEEDBACK")
			}
			full, err := s.runCheckWorkflow(CheckWorkInfo{
				BaseBranch: "main", Requirements: requirements, LastReviewDiff: tc.prior,
				AutoChecks: "AUTOMATED_CHECKS",
			})
			s.Require().NoError(err)
			s.Equal("CURRENT_FULL_WORK", full)
			text := strings.Join(*prompts, "\n")
			s.Equal(1, strings.Count(text, "ORIGINAL_REQUIREMENTS"))
			s.Contains(text, "AUTOMATED_CHECKS")
			if tc.prior != "" {
				s.Equal(1, strings.Count(text, "LATEST_FEEDBACK"))
				s.Equal(1, strings.Count(text, "HISTORICAL_FEEDBACK"))
			}
			if tc.incremental {
				s.Equal(1, strings.Count(text, "PRIOR_WORK"))
				s.Contains(text, "Here are the changes since the last review, after the most recent feedback:")
				s.NotContains(text, "And here is the latest git diff:")
				s.NotContains(text, "CURRENT_FULL_WORK")
				if tc.since == "" {
					s.Contains(text, "No changes since the last review.")
					s.NotContains(text, emptyWorkPlaceholder)
				} else {
					s.Equal(1, strings.Count(text, tc.since))
				}
			} else {
				s.NotContains(text, "PRIOR_WORK")
				s.NotContains(text, "changes since the last review")
				s.Contains(text, "And here is the latest git diff:")
				s.Equal(1, strings.Count(text, "CURRENT_FULL_WORK"))
			}
			sections := []string{"ORIGINAL_REQUIREMENTS"}
			if tc.prior != "" {
				sections = append(sections, "HISTORICAL_FEEDBACK")
				if tc.incremental {
					sections = append(sections, "PRIOR_WORK")
				}
				sections = append(sections, "LATEST_FEEDBACK")
			}
			if tc.incremental {
				sections = append(sections, "Here are the changes since the last review, after the most recent feedback:")
				if tc.since == "" {
					sections = append(sections, "No changes since the last review.")
				} else {
					sections = append(sections, tc.since)
				}
			} else {
				sections = append(sections, "And here is the latest git diff:", "CURRENT_FULL_WORK")
			}
			sections = append(sections, "AUTOMATED_CHECKS")
			previous := -1
			for _, section := range sections {
				position := strings.Index(text, section)
				s.Greater(position, previous, "section %q must follow the preceding section", section)
				previous = position
			}
			s.env.AssertExpectations(s.T())
		})
	}
}

func (s *CriteriaFulfillmentDiffTestSuite) TestIncrementalSummaryKeepsRequirementsContextOnce() {
	budget := (common.ModelMetadata{}).MaxChars() / 4
	incremental := strings.Repeat("incremental change\n", budget)
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).
		Return(coding.GenerateReviewDiffsResult{FullDiff: "RAW_FULL_BASELINE", SinceDiff: incremental}, nil)
	s.env.OnActivity(SummarizeDiffActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input SummarizeDiffActivityInput) (string, error) {
			s.Equal(incremental, input.GitDiff)
			s.Equal(budget, input.MaxChars)
			return "INCREMENTAL_SUMMARY", nil
		}).Once()
	prompts := s.mockCriteriaReviewer()
	full, err := s.runCheckWorkflow(CheckWorkInfo{
		BaseBranch: "main", LastReviewDiff: "RAW_PRIOR_BASELINE",
		Requirements: formatRequirementsWithReview("requirements", nil, "PRIOR_WORK_SUMMARY", "feedback"),
	})
	s.Require().NoError(err)
	s.Equal("RAW_FULL_BASELINE", full)
	text := strings.Join(*prompts, "\n")
	s.Equal(1, strings.Count(text, "PRIOR_WORK_SUMMARY"))
	s.Equal(1, strings.Count(text, "INCREMENTAL_SUMMARY"))
	s.NotContains(text, "RAW_PRIOR_BASELINE")
	s.Contains(text, "Here are the changes since the last review")
}

func (s *CriteriaFulfillmentDiffTestSuite) TestActivityFailureUsesSingleFullDiffPrompt() {
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).
		Return(coding.GenerateReviewDiffsResult{}, errors.New("git failure"))
	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).
		Return("CURRENT_FULL_WORK", nil)
	prompts := s.mockCriteriaReviewer()
	_, err := s.runCheckWorkflow(CheckWorkInfo{
		BaseBranch: "main", LastReviewDiff: "PRIOR_WORK",
		Requirements: formatRequirementsWithReview("requirements", nil, "PRIOR_WORK", "LATEST_FEEDBACK"),
	})
	s.Require().NoError(err)
	text := strings.Join(*prompts, "\n")
	s.NotContains(text, "PRIOR_WORK")
	s.Equal(1, strings.Count(text, "CURRENT_FULL_WORK"))
	s.Equal(1, strings.Count(text, "LATEST_FEEDBACK"))
	s.Contains(text, "And here is the latest git diff:")
}

func (s *CriteriaFulfillmentDiffTestSuite) TestLegacyReviewContextRetainsPairedWork() {
	s.env.OnGetVersion("criteria-user-review-context", workflow.DefaultVersion, workflow.Version(2)).
		Return(workflow.Version(1))
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).
		Return(coding.GenerateReviewDiffsResult{FullDiff: "full", SinceDiff: "incremental"}, nil)
	prompts := s.mockCriteriaReviewer()
	_, err := s.runCheckWorkflow(CheckWorkInfo{
		BaseBranch: "main", Requirements: "requirements", LastReviewDiff: "prior",
	})
	s.Require().NoError(err)
	text := strings.Join(*prompts, "\n")
	s.Contains(text, "# Original diff from rejected user review\n\nprior")
	s.Contains(text, "# Review changes (full current diff if incremental generation was unavailable)\n\nincremental")
	s.Contains(text, "And here is the latest git diff:")
}

func (s *CriteriaFulfillmentDiffTestSuite) TestFallbackPromptPreservesDelimiterCollisions() {
	const workHeading = "\n\nWork Done So Far:\n\n"
	const feedbackIntro = "\n\nGiven the above context, please address the following latest user feedback:\n\n"
	original := "ORIGINAL_REQUIREMENTS#END Original Requirements\n\n" + workHeading + "original text"
	historical := "HISTORICAL_FEEDBACK" + workHeading + feedbackIntro + "historical text"
	prior := "PRIOR_WORK" + feedbackIntro + "PRIOR_WORK_TAIL"
	latest := "LATEST_FEEDBACK" + feedbackIntro + workHeading + "latest text"
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).
		Return(coding.GenerateReviewDiffsResult{
			FullDiff: "CURRENT_FULL_WORK", SinceDiffError: "comparison unavailable",
		}, nil)
	prompts := s.mockCriteriaReviewer()
	_, err := s.runCheckWorkflow(CheckWorkInfo{
		BaseBranch: "main", LastReviewDiff: prior,
		Requirements: formatRequirementsWithReview(original, []string{historical}, prior, latest),
		AutoChecks:   "AUTOMATED_CHECKS",
	})
	s.Require().NoError(err)
	text := strings.Join(*prompts, "\n")
	s.Contains(text, original)
	s.Contains(text, historical)
	s.Contains(text, latest)
	s.NotContains(text, "PRIOR_WORK")
	s.NotContains(text, "sidekick-review-work:")
	previous := -1
	for _, section := range []string{
		original, historical, latest, "And here is the latest git diff:",
		"CURRENT_FULL_WORK", "AUTOMATED_CHECKS",
	} {
		s.Equal(1, strings.Count(text, section))
		position := strings.Index(text, section)
		s.Greater(position, previous, "section %q must follow the preceding section", section)
		previous = position
	}
}
