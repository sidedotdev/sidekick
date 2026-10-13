package dev

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/coding"
	"sidekick/coding/git"
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

// AutoMergeApprovalTestSuite verifies the AutoMerge option causes getMergeApproval
// to approve and target the configured branch without blocking for human input,
// while still recording a completed flow action.
type AutoMergeApprovalTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment

	// uncommitted is the `git status --porcelain` output of the worktree
	// being merged, cleared once the commit subflow commits it.
	uncommitted string
	// events records, in order, the commit subflow, squash message and merge
	// steps of a merge.
	events []string
	// prompts are the user messages appended to chat histories.
	prompts []string
	// llmCommitMessagesOff turns the LLM commit messages flag off.
	llmCommitMessagesOff bool
}

const generatedSquashMessage = "feat: publish under the new npm package name\n\nRenames the package in the manifest."

func (s *AutoMergeApprovalTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetWorkerOptions(utils.TestWorkerOptions())
	s.uncommitted = ""
	s.events = nil
	s.prompts = nil
	s.llmCommitMessagesOff = false
}

func (s *AutoMergeApprovalTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func (s *AutoMergeApprovalTestSuite) setupCommonMocks() {
	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return("diff content", nil).Maybe()
	s.env.OnActivity(git.WriteTreeActivity, mock.Anything, mock.Anything).Return("tree-hash", nil).Maybe()
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).
		Return(coding.GenerateReviewDiffsResult{FullDiff: "diff content"}, nil).Maybe()
	var meta *temporalmeta.TemporalMetaActivities
	s.env.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).
		Return([]domain.TemporalActivityRef{}, nil).Maybe()
}

func (s *AutoMergeApprovalTestSuite) approvalWorkflow(target string, autoMerge bool) func(ctx workflow.Context) (MergeApprovalResponse, error) {
	return func(ctx workflow.Context) (MergeApprovalResponse, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				WorkspaceId: "test-workspace",
				Context:     ctx,
				FlowScope: &flow_action.FlowScope{
					SubflowName: "test-subflow",
				},
				GlobalState: &flow_action.GlobalState{},
				EnvContainer: &env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
				},
			},
			Worktree: &domain.Worktree{
				Name: "side/sub-task",
			},
			RepoConfig: common.RepoConfig{},
		}
		response, _, _, err := getMergeApproval(dCtx, target, true, "", "", autoMerge)
		return response, err
	}
}

// TestAutoMergeApprovesWithoutUserRequest ensures that when autoMerge is true,
// the merge is approved automatically against the given target branch. The
// workflow completes without blocking, which would not happen if a human merge
// approval request were issued.
func (s *AutoMergeApprovalTestSuite) TestAutoMergeApprovesWithoutUserRequest() {
	s.setupCommonMocks()

	var persistedActions []domain.FlowAction
	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			persistedActions = append(persistedActions, args.Get(1).(domain.FlowAction))
		}).
		Return(nil)

	testWorkflow := s.approvalWorkflow("side/idd-worktree", true)
	s.env.RegisterWorkflow(testWorkflow)

	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result MergeApprovalResponse
	s.NoError(s.env.GetWorkflowResult(&result))
	s.True(result.Approved)
	s.Equal("side/idd-worktree", result.TargetBranch)
	s.Equal(MergeStrategySquash, result.MergeStrategy)

	// The auto-approval must still be recorded as a completed, non-human flow action
	s.Require().NotEmpty(persistedActions)
	last := persistedActions[len(persistedActions)-1]
	s.Equal("user_request.approve.merge", last.ActionType)
	s.Equal(domain.ActionStatusComplete, last.ActionStatus)
	s.False(last.IsHumanAction)
	s.Contains(last.ActionResult, `"Approved":true`)
}

func (s *AutoMergeApprovalTestSuite) setupMergeMocks() {
	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()

	var srvActivities srv.Activities
	s.env.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
	s.env.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()

	s.env.OnActivity(git.GitAddActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return("diff content", nil).Maybe()
	s.env.OnActivity(git.WriteTreeActivity, mock.Anything, mock.Anything).Return("tree-hash", nil).Maybe()
	var ca *coding.CodingActivities
	s.env.OnActivity(ca.GenerateReviewDiffsActivity, mock.Anything, mock.Anything).
		Return(coding.GenerateReviewDiffsResult{FullDiff: "diff content"}, nil).Maybe()
	s.env.OnActivity(git.GitCommitActivity, mock.Anything, mock.Anything, mock.Anything).Return("commit-sha", nil).Maybe()
	s.env.OnActivity(git.CleanupWorktreeActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.setupLlmCommitMocks()
}

// setupLlmCommitMocks stands in for the worktree's git state and for the model
// that commits leftover work and writes squash commit messages.
func (s *AutoMergeApprovalTestSuite) setupLlmCommitMocks() {
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
			if input.Command == "git" && len(input.Args) > 0 {
				switch input.Args[0] {
				case "status":
					return env.EnvRunCommandActivityOutput{Stdout: s.uncommitted}, nil
				case "log":
					return env.EnvRunCommandActivityOutput{Stdout: "chore: rename package in manifest\n"}, nil
				}
			}
			return env.EnvRunCommandActivityOutput{}, fmt.Errorf("unexpected command: %s %v", input.Command, input.Args)
		}).Maybe()

	s.env.RegisterActivity(persisted_ai.RepairToolCallArgumentsActivity)
	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.GetModelMetadata, mock.Anything, mock.Anything, mock.Anything).
		Return(common.ModelMetadata{}, nil).Maybe()
	var flagActivities *fflag.FFlagActivities
	s.env.OnActivity(flagActivities.EvalBoolFlag, mock.Anything, mock.Anything).Return(false, nil).Maybe()
	s.env.OnActivity(flagActivities.EvaluateFlags, mock.Anything, mock.Anything).Return(
		func(_ context.Context, _ fflag.EvaluateFlagsInput) (fflag.EvaluateFlagsOutput, error) {
			return fflag.EvaluateFlagsOutput{BoolValues: map[string]bool{
				fflag.LlmCommitMessages: !s.llmCommitMessagesOff,
			}}, nil
		}).Maybe()
	var chatHistoryActivities *persisted_ai.ChatHistoryActivities
	s.env.OnActivity(chatHistoryActivities.AppendMessage, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input persisted_ai.AppendMessageInput) (*persisted_ai.MessageRef, error) {
			if input.Message.Role == llm2.RoleUser {
				s.prompts = append(s.prompts, input.Message.GetContentString())
			}
			return &persisted_ai.MessageRef{BlockKeys: []string{"mock-block"}, Role: string(input.Message.Role)}, nil
		}).Maybe()

	var llmActivities *persisted_ai.Llm2Activities
	s.env.OnActivity(llmActivities.Stream, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input persisted_ai.StreamInput) (*llm2.MessageResponse, error) {
			if input.Options.ToolChoice.Type == common.ToolChoiceTypeTool && input.Options.ToolChoice.Name == commitMessageTool.Name {
				s.events = append(s.events, "squash_message")
				return verifierWorkflowResponse("message-call", commitMessageTool.Name,
					`{"subject":"feat: publish under the new npm package name","body":"Renames the package in the manifest."}`), nil
			}
			if offersCommitDone(input.Options.Tools) {
				// stands in for the model committing everything before calling done
				s.events = append(s.events, "commit_subflow")
				s.uncommitted = ""
				return verifierWorkflowResponse("done-call", commitDoneTool.Name, `{"summary":"committed"}`), nil
			}
			return nil, fmt.Errorf("unexpected stream call with tool choice %+v", input.Options.ToolChoice)
		}).Maybe()
}

func (s *AutoMergeApprovalTestSuite) mergeDevContext(ctx workflow.Context) (DevContext, error) {
	gs := &flow_action.GlobalState{}
	gs.InitValues()
	dCtx := DevContext{
		ExecContext: flow_action.ExecContext{
			WorkspaceId: "test-workspace",
			Context:     utils.NoRetryCtx(ctx),
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
		},
		Worktree: &domain.Worktree{
			Name: "side/sub-task",
		},
		RepoConfig: common.RepoConfig{},
	}
	dCtx.SetLLMConfig(common.LLMConfig{
		Defaults: []common.ModelConfig{{Provider: "test", Model: "default-model"}},
	})
	return dCtx, SetupModelConfigHandlers(dCtx)
}

func (s *AutoMergeApprovalTestSuite) mergeWorkflow(autoMerge bool, startBranch *string) func(ctx workflow.Context) (MergeApprovalResponse, error) {
	return func(ctx workflow.Context) (MergeApprovalResponse, error) {
		dCtx, err := s.mergeDevContext(ctx)
		if err != nil {
			return MergeApprovalResponse{}, err
		}
		_, mergeInfo, _, err := mergeWorktreeIfApproved(dCtx, MergeWithReviewParams{
			CommitRequired: true,
			Requirements:   "Implement the thing",
			StartBranch:    startBranch,
			AutoMerge:      autoMerge,
		}, "", "")
		return mergeInfo, err
	}
}

// TestAutoMergeMergesIntoConfiguredBranch verifies that when AutoMerge is set,
// mergeWorktreeIfApproved performs the merge against the start branch without
// requesting human approval.
func (s *AutoMergeApprovalTestSuite) TestAutoMergeMergesIntoConfiguredBranch() {
	s.setupMergeMocks()

	var mergedTarget string
	var mergedSource string
	s.env.OnActivity(git.GitMergeActivity, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			params := args.Get(2).(git.GitMergeParams)
			mergedTarget = params.TargetBranch
			mergedSource = params.SourceBranch
		}).
		Return(git.MergeActivityResult{HasConflicts: false}, nil)

	startBranch := "side/idd-worktree"
	testWorkflow := s.mergeWorkflow(true, &startBranch)
	s.env.RegisterWorkflow(testWorkflow)

	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result MergeApprovalResponse
	s.NoError(s.env.GetWorkflowResult(&result))
	s.True(result.Approved)
	s.Equal("side/idd-worktree", result.TargetBranch)
	s.Equal("side/idd-worktree", mergedTarget)
	s.Equal("side/sub-task", mergedSource)
}

// TestMergeCommitUsesTaskTitleGeneratedAfterFlowStart covers flows started
// before their task title was generated: in executions predating LLM-written
// commit messages, the commit must still use the title.
func (s *AutoMergeApprovalTestSuite) TestMergeCommitUsesTaskTitleGeneratedAfterFlowStart() {
	s.setupMergeMocks()
	s.env.OnGetVersion(fflag.VersionChangeId(fflag.LlmCommitMessages), workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
	var ima *DevAgentManagerActivities
	s.env.OnActivity(ima.GetFlowTaskTitle, mock.Anything, mock.Anything).
		Return(GetFlowTaskTitleOutput{Title: "Rename npm package"}, nil)

	var commitMessage string
	s.env.OnActivity(git.GitMergeActivity, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			commitMessage = args.Get(2).(git.GitMergeParams).CommitMessage
		}).
		Return(git.MergeActivityResult{HasConflicts: false}, nil)

	startBranch := "main"
	testWorkflow := s.mergeWorkflow(true, &startBranch)
	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.Equal("Rename npm package", commitMessage)
}

// With the LLM commit messages flag off, a squash merge commits pending work
// and merges with the title-based message, without asking the model.
func (s *AutoMergeApprovalTestSuite) TestSquashMergeUsesTitleWhenLlmCommitMessagesOff() {
	s.setupMergeMocks()
	s.llmCommitMessagesOff = true
	s.uncommitted = " M package.json\n"
	var ima *DevAgentManagerActivities
	s.env.OnActivity(ima.GetFlowTaskTitle, mock.Anything, mock.Anything).
		Return(GetFlowTaskTitleOutput{Title: "Rename npm package"}, nil)

	var mergeParams git.GitMergeParams
	s.env.OnActivity(git.GitMergeActivity, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			mergeParams = args.Get(2).(git.GitMergeParams)
			s.events = append(s.events, "merge")
		}).
		Return(git.MergeActivityResult{HasConflicts: false}, nil)

	startBranch := "main"
	testWorkflow := s.mergeWorkflow(true, &startBranch)
	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.Equal(git.MergeStrategySquash, mergeParams.MergeStrategy)
	s.Equal("Rename npm package", mergeParams.CommitMessage)
	s.Equal([]string{"merge"}, s.events)
	s.env.AssertActivityNumberOfCalls(s.T(), "GitCommitActivity", 1)
	s.Empty(s.prompts)
}

// A squash merge is described by an LLM-written message informed by the task
// title, and work already committed by coding is not committed again.
func (s *AutoMergeApprovalTestSuite) TestSquashMergeUsesGeneratedCommitMessage() {
	s.setupMergeMocks()
	var ima *DevAgentManagerActivities
	s.env.OnActivity(ima.GetFlowTaskTitle, mock.Anything, mock.Anything).
		Return(GetFlowTaskTitleOutput{Title: "Rename npm package"}, nil)

	var mergeParams git.GitMergeParams
	s.env.OnActivity(git.GitMergeActivity, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			mergeParams = args.Get(2).(git.GitMergeParams)
			s.events = append(s.events, "merge")
		}).
		Return(git.MergeActivityResult{HasConflicts: false}, nil)

	startBranch := "main"
	testWorkflow := s.mergeWorkflow(true, &startBranch)
	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.Equal(git.MergeStrategySquash, mergeParams.MergeStrategy)
	s.Equal(generatedSquashMessage, mergeParams.CommitMessage)
	s.Equal([]string{"squash_message", "merge"}, s.events)
	s.env.AssertActivityNumberOfCalls(s.T(), "GitCommitActivity", 0)
	s.Require().Len(s.prompts, 1)
	s.Contains(s.prompts[0], "Rename npm package")
	s.Contains(s.prompts[0], "Implement the thing")
	s.Contains(s.prompts[0], "chore: rename package in manifest")
}

// Changes still uncommitted when the merge is approved are committed by the
// commit subflow before merging, so they are never left behind.
func (s *AutoMergeApprovalTestSuite) TestUncommittedChangesAreCommittedBeforeMerge() {
	s.setupMergeMocks()
	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistSubflow, mock.Anything, mock.Anything).Return(nil)
	s.uncommitted = " M package.json\n"

	var mergeParams git.GitMergeParams
	s.env.OnActivity(git.GitMergeActivity, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			mergeParams = args.Get(2).(git.GitMergeParams)
			s.events = append(s.events, "merge")
		}).
		Return(git.MergeActivityResult{HasConflicts: false}, nil)

	startBranch := "main"
	testWorkflow := s.mergeWorkflow(true, &startBranch)
	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.Equal([]string{"commit_subflow", "squash_message", "merge"}, s.events)
	s.Equal(generatedSquashMessage, mergeParams.CommitMessage)
	s.env.AssertActivityNumberOfCalls(s.T(), "GitCommitActivity", 0)
}

// The merge strategy decides between an LLM-written squash message and the
// title-based message that a merge commit keeps.
func (s *AutoMergeApprovalTestSuite) TestMergeCommitMessageDependsOnStrategy() {
	s.setupMergeMocks()

	type messages struct {
		Squash string
		Merge  string
	}
	testWorkflow := func(ctx workflow.Context) (messages, error) {
		dCtx, err := s.mergeDevContext(ctx)
		if err != nil {
			return messages{}, err
		}
		params := MergeWithReviewParams{Title: "Rename npm package", Requirements: "Implement the thing"}
		var result messages
		result.Merge, err = commitAndComposeMergeMessage(dCtx, params, MergeApprovalResponse{
			Approved: true, TargetBranch: "main", MergeStrategy: MergeStrategyMerge,
		})
		if err != nil {
			return messages{}, err
		}
		result.Squash, err = commitAndComposeMergeMessage(dCtx, params, MergeApprovalResponse{
			Approved: true, TargetBranch: "main", MergeStrategy: MergeStrategySquash,
		})
		return result, err
	}
	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	var result messages
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal("Rename npm package", result.Merge)
	s.Equal(generatedSquashMessage, result.Squash)
	s.Equal([]string{"squash_message"}, s.events, "only the squash merge asks the model for a message")
}

// setupStashConflictMocks makes the merge report a base-worktree stash
// conflict relocated onto the flow's own worktree, with a trivially
// "resolved" resolution subflow (nothing conflicted, no merge in progress).
func (s *AutoMergeApprovalTestSuite) setupStashConflictMocks() {
	s.setupMergeMocks()
	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistSubflow, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(git.GitMergeActivity, mock.Anything, mock.Anything, mock.Anything).Return(git.MergeActivityResult{
		HasConflicts:          true,
		ConflictDirPath:       "/tmp/test-repo",
		BaseStashWorktreePath: "/tmp/base-repo",
		BaseStashSha:          "base-stash-sha",
	}, nil)
	s.env.OnActivity(git.GitSnapshotConflictMarkersActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(git.ConflictMarkerSnapshot{}, nil)
	s.env.OnActivity(git.GitMergeInProgressActivity, mock.Anything, mock.Anything, mock.Anything).Return(false, nil)
	s.env.OnActivity(AssessResolutionSubstantialityActivity, mock.Anything, mock.Anything).
		Return(AssessResolutionSubstantialityResult{Substantial: false}, nil)
}

// A resolution that no longer applies to the base worktree is relocated and
// resolved again until the transfer succeeds.
func (s *AutoMergeApprovalTestSuite) TestStashConflictTransferLoopsUntilDelivered() {
	s.setupStashConflictMocks()
	var transfers []git.GitTransferWorktreeChangesParams
	s.env.OnActivity(git.GitTransferWorktreeChangesActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ context.Context, _ env.EnvContainer, params git.GitTransferWorktreeChangesParams) (git.GitTransferWorktreeChangesResult, error) {
			transfers = append(transfers, params)
			if len(transfers) == 1 {
				return git.GitTransferWorktreeChangesResult{HasConflicts: true, ConflictDirPath: "/tmp/test-repo"}, nil
			}
			return git.GitTransferWorktreeChangesResult{}, nil
		})

	startBranch := "side/idd-worktree"
	testWorkflow := s.mergeWorkflow(true, &startBranch)
	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.Require().Len(transfers, 2)
	for _, params := range transfers {
		s.Equal("/tmp/test-repo", params.SourceWorktreePath)
		s.Equal("/tmp/base-repo", params.TargetWorktreePath)
		s.Equal("base-stash-sha", params.BaseStashSha)
	}
}

// Delivering a stash-conflict resolution to the base worktree is what lands the
// merge, so each delivery attempt must show up as a merge flow action after the
// initial conflicted merge, ending in a successful one.
func (s *AutoMergeApprovalTestSuite) TestStashConflictTransferRecordedAsMergeFlowActions() {
	var actionOrder []string
	latestById := map[string]domain.FlowAction{}
	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			action := args.Get(1).(domain.FlowAction)
			if _, seen := latestById[action.Id]; !seen {
				actionOrder = append(actionOrder, action.Id)
			}
			latestById[action.Id] = action
		}).
		Return(nil)
	s.setupStashConflictMocks()
	transfers := 0
	s.env.OnActivity(git.GitTransferWorktreeChangesActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(func(context.Context, env.EnvContainer, git.GitTransferWorktreeChangesParams) (git.GitTransferWorktreeChangesResult, error) {
			transfers++
			if transfers == 1 {
				return git.GitTransferWorktreeChangesResult{HasConflicts: true, ConflictDirPath: "/tmp/test-repo"}, nil
			}
			return git.GitTransferWorktreeChangesResult{}, nil
		})

	startBranch := "side/idd-worktree"
	testWorkflow := s.mergeWorkflow(true, &startBranch)
	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var merges []domain.FlowAction
	for _, id := range actionOrder {
		if action := latestById[id]; action.ActionType == "merge" {
			merges = append(merges, action)
		}
	}
	s.Require().Len(merges, 3, "the initial merge plus one merge action per delivery attempt")
	s.Contains(merges[0].ActionResult, `"hasConflicts":true`)
	s.Contains(merges[1].ActionResult, `"hasConflicts":true`)
	final := merges[2]
	s.Equal(domain.ActionStatusComplete, final.ActionStatus)
	s.Contains(final.ActionResult, `"hasConflicts":false`)
	s.Equal("side/sub-task", final.ActionParams["sourceBranch"])
	s.Equal(startBranch, final.ActionParams["targetBranch"])
}

// Executions that predate the transfer loop must not mistake an undelivered
// conflict result for a successful delivery.
func (s *AutoMergeApprovalTestSuite) TestStashConflictTransferConflictFailsOldExecutions() {
	s.setupStashConflictMocks()
	s.env.OnGetVersion("transfer-resolution-conflicts", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
	transfers := 0
	s.env.OnActivity(git.GitTransferWorktreeChangesActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(func(context.Context, env.EnvContainer, git.GitTransferWorktreeChangesParams) (git.GitTransferWorktreeChangesResult, error) {
			transfers++
			return git.GitTransferWorktreeChangesResult{HasConflicts: true, ConflictDirPath: "/tmp/test-repo"}, nil
		})

	startBranch := "side/idd-worktree"
	testWorkflow := s.mergeWorkflow(true, &startBranch)
	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Require().Error(err)
	s.Contains(err.Error(), "resolution conflicts with the base worktree")
	s.Equal(1, transfers)
}

func TestAutoMergeApprovalTestSuite(t *testing.T) {
	suite.Run(t, new(AutoMergeApprovalTestSuite))
}

func TestCommitMessageForMerge(t *testing.T) {
	t.Parallel()

	longTitle := strings.Repeat("a", 105)

	cases := []struct {
		name     string
		params   MergeWithReviewParams
		expected string
	}{
		{
			name:     "title preferred over requirements",
			params:   MergeWithReviewParams{Title: "Add retry to merge", Requirements: "Some very long requirements\nwith details"},
			expected: "Add retry to merge",
		},
		{
			name:     "blank title falls back to requirements",
			params:   MergeWithReviewParams{Title: "   ", Requirements: "Some very long requirements\nwith details"},
			expected: "Some very long requirements",
		},
		{
			name:     "empty title falls back to requirements overview",
			params:   MergeWithReviewParams{Requirements: "Preamble\n\nOverview:\nImplement the thing\nmore detail"},
			expected: "Implement the thing",
		},
		{
			name:     "multi-line title keeps only the subject line",
			params:   MergeWithReviewParams{Title: "Subject line\nbody line", Requirements: "requirements"},
			expected: "Subject line",
		},
		{
			name:     "over-long title overflows into the commit body",
			params:   MergeWithReviewParams{Title: longTitle},
			expected: longTitle[:100] + "...\n\n..." + longTitle[100:],
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, commitMessageForMerge(tc.params))
		})
	}
}

func TestCommitTitleForTask(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		title       string
		description string
		expected    string
	}{
		{name: "distinct title is used", title: "Fix flaky test", description: "The test fails sometimes", expected: "Fix flaky test"},
		{name: "title copied from description is dropped", title: "The test fails sometimes", description: "The test fails sometimes ", expected: ""},
		{name: "missing title", title: "", description: "The test fails sometimes", expected: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, commitTitleForTask(tc.title, tc.description))
		})
	}
}
func (s *AutoMergeApprovalTestSuite) TestFinalMergeConflictIsResolvedBeforeCompletion() {
	s.setupMergeMocks()
	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistSubflow, mock.Anything, mock.Anything).Return(nil)
	merges := 0
	var mergeMessages []string
	s.env.OnActivity(git.GitMergeActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ context.Context, _ env.EnvContainer, params git.GitMergeParams) (git.MergeActivityResult, error) {
			merges++
			mergeMessages = append(mergeMessages, params.CommitMessage)
			if merges < 3 {
				return git.MergeActivityResult{
					HasConflicts: true, ConflictDirPath: "/tmp/test-repo",
				}, nil
			}
			return git.MergeActivityResult{}, nil
		})
	s.env.OnActivity(git.GitSnapshotConflictMarkersActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(git.ConflictMarkerSnapshot{}, nil)
	s.env.OnActivity(git.GitMergeInProgressActivity, mock.Anything, mock.Anything, mock.Anything).Return(false, nil)
	s.env.OnActivity(AssessResolutionSubstantialityActivity, mock.Anything, mock.Anything).
		Return(AssessResolutionSubstantialityResult{Substantial: false}, nil)

	startBranch := "side/idd-worktree"
	testWorkflow := s.mergeWorkflow(true, &startBranch)
	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)

	s.NoError(s.env.GetWorkflowError())
	s.Equal(3, merges)
	s.Equal([]string{generatedSquashMessage, generatedSquashMessage, generatedSquashMessage}, mergeMessages,
		"re-merges after conflict resolution reuse the generated message")
	s.Equal(1, strings.Count(strings.Join(s.events, ","), "squash_message"))
	s.env.AssertActivityNumberOfCalls(s.T(), "GitSnapshotConflictMarkersActivity", 2)
	s.env.AssertActivityNumberOfCalls(s.T(), "CleanupWorktreeActivity", 1)
}
