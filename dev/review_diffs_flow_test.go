package dev

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/coding"
	"sidekick/coding/git"
	"sidekick/coding/lsp"
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

// emptyWorkPlaceholder is what the fulfillment prompt shows instead of an empty
// diff, so comparing review diffs across flows has to treat it as empty.
const emptyWorkPlaceholder = "git diff is empty: no changes were made."

// reviewDiffsFlowHarness runs review rounds against a real git repository with
// only git activities running for real: LLM, persistence and user interaction
// activities are mocked.
type reviewDiffsFlowHarness struct {
	t            *testing.T
	ts           testsuite.WorkflowTestSuite
	env          *testsuite.TestWorkflowEnvironment
	dir          string
	envContainer env.EnvContainer

	// repos are the repositories a review flow spans: the flow's own working
	// copy and the base repository upstream commits land in.
	repos *flowRepos

	// prepare stands in for the work reviewed in the first review round, and
	// respond for the work done in response to that round's feedback. Where
	// each runs is up to the flow runner, since flows differ in how many
	// coding rounds precede the first review.
	prepare func(t *testing.T, repos *flowRepos)
	respond func(t *testing.T, repos *flowRepos)

	// roundHooks maps a coding round, counted from one, to the repository
	// changes a real model would have made during that round.
	roundHooks   map[int]func(t *testing.T, repos *flowRepos)
	codingRounds int

	// codingResponse is the assistant message, containing edit blocks, that
	// stands in for the code a model would author during a coding subflow. It
	// is returned once, so the coding loop terminates like a real one would
	// after its edits apply cleanly.
	codingResponse     string
	codingResponseSent bool

	// unfulfilledRounds is how many auto-review rounds report the work as not
	// yet meeting its criteria, which is what drives a flow into another coding
	// round and hence another review round.
	unfulfilledRounds int
	fulfillmentRounds int

	fulfillmentDiffs   []string
	fulfillmentIndexes map[string]int
	mergeApprovals     []MergeApprovalParams
	mergeIndexes       map[string]int
	promptTexts        []string
}

// upsertByAction keeps one entry per flow action, since each action is persisted
// repeatedly as it progresses and different rounds can produce equal content.
func upsertByAction[T any](values []T, indexes map[string]int, actionId string, value T) []T {
	if actionId == "" {
		return append(values, value)
	}
	if i, ok := indexes[actionId]; ok {
		values[i] = value
		return values
	}
	indexes[actionId] = len(values)
	return append(values, value)
}

func newReviewDiffsFlowHarness(t *testing.T) *reviewDiffsFlowHarness {
	t.Helper()
	h := &reviewDiffsFlowHarness{
		t:                  t,
		fulfillmentIndexes: map[string]int{},
		mergeIndexes:       map[string]int{},
	}
	h.env = h.ts.NewTestWorkflowEnvironment()
	h.env.SetWorkerOptions(utils.TestWorkerOptions())
	h.env.SetTestTimeout(2 * time.Minute)

	h.dir = t.TempDir()
	runCmd(t, h.dir, "git", "init", "-b", "main")
	runCmd(t, h.dir, "git", "config", "user.email", "test@test.com")
	runCmd(t, h.dir, "git", "config", "user.name", "Test")
	writeAndCommit(t, h.dir, "shared.txt", "line one\nline two\nline three\n", "initial commit")
	runCmd(t, h.dir, "git", "checkout", "-b", "side/task")

	devEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{RepoDir: h.dir})
	require.NoError(t, err)
	h.envContainer = env.EnvContainer{Env: devEnv}
	h.repos = &flowRepos{
		work:       &repoMutator{t: t, envContainer: h.envContainer, branch: "side/task", baseBranch: "main"},
		base:       &repoMutator{t: t, envContainer: h.envContainer, branch: "side/task", baseBranch: "main"},
		baseBranch: "main",
	}

	h.registerGitActivities()
	h.registerMocks()
	return h
}

// registerGitActivities registers every activity that really runs git, so the
// diffs under test come from git itself rather than canned mock output.
func (h *reviewDiffsFlowHarness) registerGitActivities() {
	devActivities := &DevActivities{
		LSPActivities: &lsp.LSPActivities{
			LSPClientProvider: func(languageName string) lsp.LSPClient {
				return &lsp.Jsonrpc2LSPClient{LanguageName: languageName}
			},
			InitializedClients: map[string]lsp.LSPClient{},
		},
	}
	h.env.RegisterActivity(devActivities.ApplyEditBlocks)
	h.env.RegisterActivity(git.GitDiffActivity)
	h.env.RegisterActivity(git.DiffUntrackedFilesActivity)
	h.env.RegisterActivity(git.GitAddActivity)
	h.env.RegisterActivity(git.GitCommitActivity)
	h.env.RegisterActivity(git.GitCommitMergeActivity)
	h.env.RegisterActivity(git.GitMergeActivity)
	h.env.RegisterActivity(git.GitMergeAbortActivity)
	h.env.RegisterActivity(git.GitMergeInProgressActivity)
	h.env.RegisterActivity(git.GitListUnmergedActivity)
	h.env.RegisterActivity(git.GitCheckoutActivity)
	h.env.RegisterActivity(git.GitRestoreActivity)
	h.env.RegisterActivity(git.GitRevParseActivity)
	h.env.RegisterActivity(git.GitSnapshotConflictMarkersActivity)
	h.env.RegisterActivity(git.GitConflictResolutionDiffActivity)
	h.env.RegisterActivity(git.GitTransferWorktreeChangesActivity)
	h.env.RegisterActivity(git.GetGitUserConfigActivity)
	h.env.RegisterActivity(git.WriteTreeActivity)
	h.env.RegisterActivity(env.EnvRunCommandActivity)
	var ca *coding.CodingActivities
	h.env.RegisterActivity(ca.GenerateReviewDiffsActivity)
	// small test diffs pass through unchanged, so no summarization happens
	h.env.RegisterActivity(SummarizeDiffActivity)
}

func (h *reviewDiffsFlowHarness) registerMocks() {
	var fa *flow_action.FlowActivities
	h.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).
		Return(func(_ context.Context, action domain.FlowAction) error {
			h.recordFlowAction(action)
			return nil
		}).Maybe()
	h.env.OnActivity(fa.PersistSubflow, mock.Anything, mock.Anything).Return(nil).Maybe()
	h.env.OnActivity(fa.GetModelMetadata, mock.Anything, mock.Anything, mock.Anything).
		Return(common.ModelMetadata{}, nil).Maybe()

	var srvActivities srv.Activities
	h.env.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
	h.env.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()

	var meta *temporalmeta.TemporalMetaActivities
	h.env.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).
		Return([]domain.TemporalActivityRef{}, nil).Maybe()

	var flags *fflag.FFlagActivities
	h.env.OnActivity(flags.EvalBoolFlag, mock.Anything, mock.Anything).Return(false, nil).Maybe()

	// the test repository is the flow's working copy, so it must outlive the flow
	h.env.OnActivity(git.CleanupWorktreeActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	var ragActivities *persisted_ai.RagActivities
	h.env.OnActivity(ragActivities.RankedDirSignatureOutline, mock.Anything, mock.Anything).
		Return("repo summary", nil).Maybe()

	h.env.OnActivity(env.GetEnvironmentInfoActivity, mock.Anything, mock.Anything).
		Return(env.GetEnvironmentInfoOutput{}, nil).Maybe()

	h.env.RegisterActivity(persisted_ai.RepairToolCallArgumentsActivity)

	var historyActivities *persisted_ai.ChatHistoryActivities
	h.env.OnActivity(historyActivities.AppendMessage, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input persisted_ai.AppendMessageInput) (*persisted_ai.MessageRef, error) {
			for _, block := range input.Message.Content {
				if block.Type == llm2.ContentBlockTypeText {
					h.promptTexts = append(h.promptTexts, block.Text)
				}
			}
			return &persisted_ai.MessageRef{
				BlockKeys: []string{"fulfillment-message"},
				Role:      string(input.Message.Role),
			}, nil
		}).Maybe()
	h.env.OnActivity(historyActivities.ManageV4, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input persisted_ai.ManageInput) (*persisted_ai.ManageOutput, error) {
			return &persisted_ai.ManageOutput{ChatHistory: input.ChatHistory}, nil
		}).Maybe()
	h.env.OnActivity(historyActivities.ExtractVisibleCodeBlocks, mock.Anything, mock.Anything).
		Return([]tree_sitter.CodeBlock{}, nil).Maybe()

	var llmActivities *persisted_ai.Llm2Activities
	h.env.OnActivity(llmActivities.Stream, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input persisted_ai.StreamInput) (*llm2.MessageResponse, error) {
			return h.streamResponse(input), nil
		}).Maybe()
}

// streamResponse stands in for the model: it answers whichever tool the caller
// offered, and treats the coding tool loop as the point where the repository
// changes that a real model would have authored.
func (h *reviewDiffsFlowHarness) streamResponse(input persisted_ai.StreamInput) *llm2.MessageResponse {
	offered := map[string]bool{}
	for _, tool := range input.Options.Tools {
		if tool != nil {
			offered[tool.Name] = true
		}
	}

	toolUse := func(name, arguments string) *llm2.MessageResponse {
		return &llm2.MessageResponse{
			StopReason: "tool_use",
			Output: llm2.Message{
				Role: llm2.RoleAssistant,
				Content: []llm2.ContentBlock{{
					Type: llm2.ContentBlockTypeToolUse,
					ToolUse: &llm2.ToolUseBlock{
						Id:        name + "-call",
						Name:      name,
						Arguments: arguments,
					},
				}},
			},
		}
	}

	forced := ""
	if input.Options.ToolChoice.Type == common.ToolChoiceTypeTool {
		forced = input.Options.ToolChoice.Name
	}

	switch {
	case forced == determineCriteriaFulfillmentTool.Name || (forced == "" && offered[determineCriteriaFulfillmentTool.Name]):
		return toolUse(determineCriteriaFulfillmentTool.Name, h.fulfillmentArguments())
	case forced == generateBranchNamesTool.Name || offered[generateBranchNamesTool.Name]:
		return toolUse(generateBranchNamesTool.Name, `{"candidates":["review-round-work"]}`)
	case forced == getSymbolDefinitionsTool.Name:
		// the repo under test is not the subject of the review diffs, so no
		// code context is needed to proceed
		return toolUse(getSymbolDefinitionsTool.Name, `{"requests":[]}`)
	case h.codingResponse != "" && !h.codingResponseSent && offered[doneTool.Name]:
		h.codingResponseSent = true
		return &llm2.MessageResponse{
			StopReason: "end_turn",
			Output: llm2.Message{
				Role:    llm2.RoleAssistant,
				Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: h.codingResponse}},
			},
		}
	case offered[doneTool.Name]:
		h.endCodingRound()
		return toolUse(doneTool.Name, `{"summary":"the work"}`)
	}

	return &llm2.MessageResponse{
		StopReason: "end_turn",
		Output: llm2.Message{
			Role:    llm2.RoleAssistant,
			Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "ok"}},
		},
	}
}

// fulfillmentArguments stands in for the auto-reviewer's judgement, rejecting
// the rounds a test asks to be rejected so that a later round reviews only the
// work done since the rejected one.
func (h *reviewDiffsFlowHarness) fulfillmentArguments() string {
	h.fulfillmentRounds++
	if h.fulfillmentRounds <= h.unfulfilledRounds {
		return `{"whatWasActuallyDone":"part of the work","analysis":"more is needed","isFulfilled":false}`
	}
	return `{"whatWasActuallyDone":"the work","analysis":"looks complete","isFulfilled":true}`
}

// endCodingRound stands in for the repository changes a real model would have
// authored during the coding round that is now reporting itself done.
func (h *reviewDiffsFlowHarness) endCodingRound() {
	h.codingRounds++
	if hook := h.roundHooks[h.codingRounds]; hook != nil {
		hook(h.t, h.repos)
	}
}

// recordFlowAction captures the diffs each flow saw: the auto-reviewer's work
// diff and the diffs offered for merge approval, one entry per review round.
func (h *reviewDiffsFlowHarness) recordFlowAction(action domain.FlowAction) {
	if action.ActionType == "check_criteria_fulfillment" {
		if diff, ok := action.ActionParams["diffString"].(string); ok {
			h.fulfillmentDiffs = upsertByAction(h.fulfillmentDiffs, h.fulfillmentIndexes, action.Id, diff)
		}
		return
	}
	raw, ok := action.ActionParams["mergeApprovalInfo"]
	if !ok {
		return
	}
	info, err := decodeMergeApprovalInfo(raw)
	require.NoError(h.t, err)
	h.mergeApprovals = upsertByAction(h.mergeApprovals, h.mergeIndexes, action.Id, info)
}

func (h *reviewDiffsFlowHarness) devContext(ctx workflow.Context) DevContext {
	globalState := &flow_action.GlobalState{}
	globalState.InitValues()
	dCtx := DevContext{
		ExecContext: flow_action.ExecContext{
			WorkspaceId:  "test-workspace",
			Context:      ctx,
			FlowScope:    &flow_action.FlowScope{SubflowName: "review-diffs-flow"},
			GlobalState:  globalState,
			EnvContainer: &h.envContainer,
			Secrets: &secret_manager.SecretManagerContainer{
				SecretManager: secret_manager.MockSecretManager{},
			},
			EmbeddingConfig: common.EmbeddingConfig{
				Defaults: []common.ModelConfig{{Provider: "test", Model: "embedding-model"}},
			},
		},
		Worktree:   &domain.Worktree{Name: "side/task"},
		RepoConfig: common.RepoConfig{},
	}
	dCtx.SetLLMConfig(common.LLMConfig{
		Defaults: []common.ModelConfig{{Provider: "test", Model: "judging-model"}},
	})
	return dCtx
}

// reviewRoundsOutcome is what the production review loop showed across its two
// rounds: the full diff each round offered for merge approval, the second
// round's since-review diff, and the diff its auto-reviewer judged.
type reviewRoundsOutcome struct {
	fullDiffs []string
	sinceDiff string
	workDiff  string
}

// runReviewRounds drives the production review/resolve loop, entered with the
// human's previous review in hand, through two rounds: the work under review
// already exists, the first review is rejected, the respond hook stands in for
// the coding round that follows, and the second review is approved.
func (h *reviewDiffsFlowHarness) runReviewRounds(params MergeWithReviewParams) reviewRoundsOutcome {
	h.t.Helper()

	if h.prepare != nil {
		h.prepare(h.t, h.repos)
	}
	h.roundHooks = map[int]func(t *testing.T, repos *flowRepos){1: h.respond}

	target := "main"
	params.StartBranch = &target
	params.CommitRequired = true
	h.runFlowWithUserResponses(func(ctx workflow.Context) error {
		dCtx := h.devContext(ctx)
		dCtx.ExecContext.GlobalState.SetValue(common.KeyCurrentTargetBranch, target)
		return reviewAndResolve(dCtx, params)
	}, &realFlowUserResponder{rejectionMessage: "please address the feedback", approveAfter: 1})

	return h.reviewRoundsOutcome(1)
}

// reviewRoundsOutcome reports what the flow's review rounds showed, once every
// expected coding round has run.
func (h *reviewDiffsFlowHarness) reviewRoundsOutcome(expectedCodingRounds int) reviewRoundsOutcome {
	h.t.Helper()
	require.GreaterOrEqual(h.t, h.codingRounds, expectedCodingRounds, "every coding round must have run")
	require.GreaterOrEqual(h.t, len(h.mergeApprovals), 2, "the loop must review twice: once rejected, once approved")
	require.NotEmpty(h.t, h.fulfillmentDiffs, "the coding round must have been auto-reviewed")

	lastApproval := h.mergeApprovals[len(h.mergeApprovals)-1]
	return reviewRoundsOutcome{
		fullDiffs: []string{h.mergeApprovals[0].Diff, lastApproval.Diff},
		sinceDiff: lastApproval.DiffSinceLastReview,
		workDiff:  h.fulfillmentDiffs[len(h.fulfillmentDiffs)-1],
	}
}

// normalizeReviewDiff maps the fulfillment prompt's empty-diff placeholder back
// to an empty diff so review diffs from different flows can be compared.
func normalizeReviewDiff(diff string) string {
	if strings.TrimSpace(diff) == emptyWorkPlaceholder {
		return ""
	}
	return strings.TrimSpace(diff)
}

// TestReviewRoundDiffsRealFlow covers what each review round shows, driving the
// production review loop that basic_dev runs directly and that review/resolve
// runs with the human's previous review in hand: staged work and new commits
// always show up, work reviewed in an earlier round drops out of the since
// diff, and changes merged in from the base branch never show up in either
// diff.
func TestReviewRoundDiffsRealFlow(t *testing.T) {
	t.Parallel()

	flows := []struct {
		name string
		run  func(h *reviewDiffsFlowHarness) reviewRoundsOutcome
	}{
		{
			name: "basic_dev",
			run: func(h *reviewDiffsFlowHarness) reviewRoundsOutcome {
				return h.runBasicDevWorkflow("do the work")
			},
		},
		{
			name: "review_resolve",
			run: func(h *reviewDiffsFlowHarness) reviewRoundsOutcome {
				return h.runReviewRounds(MergeWithReviewParams{
					Requirements:   "do the work",
					PreviousReview: "please also handle the edge case",
				})
			},
		},
	}

	tests := []struct {
		name    string
		prepare func(t *testing.T, repos *flowRepos)
		respond func(t *testing.T, repos *flowRepos)
		assert  func(t *testing.T, fullDiffs []string, sinceDiff string, work *repoMutator)
	}{
		{
			name: "reported version check reversion survives a disappeared file section",
			prepare: func(t *testing.T, repos *flowRepos) {
				repos.work.stage("shared.txt", "line one\n"+reportedReviewVersionCheck+"\nline two\nline three\n")
				repos.work.stage("retained.txt", "RETAINED_REVIEWED_WORK\n")
			},
			respond: func(t *testing.T, repos *flowRepos) {
				repos.work.stage("shared.txt", "line one\nline two\nline three\n")
				repos.work.stage("response.txt", "FEEDBACK_RESPONSE\n")
			},
			assert: func(t *testing.T, fullDiffs []string, sinceDiff string, work *repoMutator) {
				assert.Contains(t, fullDiffs[0], "+"+reportedReviewVersionCheck)
				assert.NotContains(t, fullDiffs[1], "shared.txt")
				assert.Contains(t, fullDiffs[1], "RETAINED_REVIEWED_WORK")
				assert.Contains(t, sinceDiff, "\n-"+reportedReviewVersionCheck+"\n")
				assert.Contains(t, sinceDiff, "FEEDBACK_RESPONSE")
				assert.NotContains(t, sinceDiff, "RETAINED_REVIEWED_WORK")
				assert.NotContains(t, sinceDiff, "Reverted since last review")
				assert.NotContains(t, sinceDiff, "previously added")
				assert.NotContains(t, sinceDiff, "previously removed")
			},
		},
		{
			name: "staged changes are always included",
			prepare: func(t *testing.T, repos *flowRepos) {
				repos.work.stage("staged_first.txt", "FIRST_ROUND_STAGED\n")
			},
			respond: func(t *testing.T, repos *flowRepos) {
				repos.work.stage("staged_second.txt", "SECOND_ROUND_STAGED\n")
			},
			assert: func(t *testing.T, fullDiffs []string, sinceDiff string, work *repoMutator) {
				assert.Equal(t, 2, work.adds, "both rounds must have staged their work through git add")
				assert.Contains(t, fullDiffs[0], "FIRST_ROUND_STAGED")
				assert.Contains(t, fullDiffs[1], "FIRST_ROUND_STAGED")
				assert.Contains(t, fullDiffs[1], "SECOND_ROUND_STAGED")
				assert.Contains(t, sinceDiff, "SECOND_ROUND_STAGED")
				assert.NotContains(t, sinceDiff, "FIRST_ROUND_STAGED",
					"work already reviewed must not reappear in the since diff")
			},
		},
		{
			name: "new commits are included and reviewed commits are not",
			prepare: func(t *testing.T, repos *flowRepos) {
				repos.work.commit("committed_first.txt", "FIRST_ROUND_COMMIT\n", "first round work")
			},
			respond: func(t *testing.T, repos *flowRepos) {
				repos.work.commit("committed_second.txt", "SECOND_ROUND_COMMIT\n", "second round work")
			},
			assert: func(t *testing.T, fullDiffs []string, sinceDiff string, work *repoMutator) {
				assert.Equal(t, 2, work.commits, "both rounds must have committed their work")
				assert.Contains(t, fullDiffs[0], "FIRST_ROUND_COMMIT")
				assert.Contains(t, fullDiffs[1], "FIRST_ROUND_COMMIT")
				assert.Contains(t, fullDiffs[1], "SECOND_ROUND_COMMIT")
				assert.Contains(t, sinceDiff, "SECOND_ROUND_COMMIT")
				assert.NotContains(t, sinceDiff, "FIRST_ROUND_COMMIT")
			},
		},
		{
			name: "clean base branch merge affects neither diff",
			prepare: func(t *testing.T, repos *flowRepos) {
				repos.work.commit("ours.txt", "OUR_WORK\n", "our work")
				repos.commitUpstream("upstream.txt", "UNRELATED_BASE_CHANGE\n", "upstream work")
			},
			respond: func(t *testing.T, repos *flowRepos) {
				result := repos.work.mergeBase()
				require.False(t, result.HasConflicts, "the upstream change must merge cleanly")
			},
			assert: func(t *testing.T, fullDiffs []string, sinceDiff string, work *repoMutator) {
				assert.Equal(t, 1, work.merges, "the base branch must have been merged in")
				assert.Contains(t, fullDiffs[1], "OUR_WORK")
				assert.Equal(t, normalizeReviewDiff(fullDiffs[0]), normalizeReviewDiff(fullDiffs[1]),
					"a clean base merge leaves the full diff exactly as it was before the merge")
				assert.Empty(t, normalizeReviewDiff(sinceDiff),
					"a clean base merge changes nothing on our end")
			},
		},
		{
			name: "conflict resolution shows our changes only",
			prepare: func(t *testing.T, repos *flowRepos) {
				repos.work.commit("shared.txt", "line one\nOUR_CHANGE\nline three\n", "our change")
				repos.commitUpstream("shared.txt", "line one\nTHEIR_CHANGE\nline three\n", "their change")
				repos.commitUpstream("other.txt", "UNRELATED_BASE_CHANGE\n", "unrelated upstream work")
			},
			respond: func(t *testing.T, repos *flowRepos) {
				result := repos.work.mergeBase()
				require.True(t, result.HasConflicts, "the upstream change must conflict with ours")
				repos.work.commit("shared.txt", "line one\nRESOLVED_CHANGE\nline three\n", "resolve conflict")
			},
			assert: func(t *testing.T, fullDiffs []string, sinceDiff string, work *repoMutator) {
				assert.Equal(t, 1, work.merges, "the base branch must have been merged in")
				assert.Equal(t, 2, work.commits, "our change and its conflict resolution are both committed")
				assert.Contains(t, fullDiffs[0], "OUR_CHANGE")
				assert.Contains(t, fullDiffs[1], "RESOLVED_CHANGE")
				assert.NotContains(t, fullDiffs[1], "UNRELATED_BASE_CHANGE")
				assert.Contains(t, sinceDiff, "Failed to generate diff since last review:",
					"incompatible bases must not produce a successful approximate comparison")
				assert.NotContains(t, sinceDiff, "UNRELATED_BASE_CHANGE",
					"non-conflicting base changes are not ours")
			},
		},
	}

	for _, flow := range flows {
		flow := flow
		for _, tc := range tests {
			tc := tc
			t.Run(flow.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				h := newReviewDiffsFlowHarness(t)
				h.prepare = tc.prepare
				h.respond = tc.respond

				outcome := flow.run(h)

				assert.Empty(t, h.mergeApprovals[0].DiffSinceLastReview,
					"the first review has no prior review to diff against")
				prompt := h.fulfillmentPromptContaining(outcome.workDiff)
				if strings.HasPrefix(outcome.sinceDiff, "Failed to generate diff since last review:") {
					assert.NotContains(t, prompt, "Work Done So Far:")
					assert.NotContains(t, prompt, normalizeReviewDiff(outcome.fullDiffs[0]))
				} else {
					assert.Equal(t, 1, strings.Count(prompt, normalizeReviewDiff(outcome.fullDiffs[0])),
						"prior work belongs once in the requirements context")
				}
				if strings.Contains(outcome.fullDiffs[0], reportedReviewVersionCheck) {
					assert.Contains(t, outcome.workDiff, "\n-"+reportedReviewVersionCheck+"\n",
						"criteria fulfillment must receive the unified removal")
				}
				if normalizeReviewDiff(outcome.sinceDiff) == "" {
					assert.Contains(t, outcome.workDiff, "No changes since the last review.")
				} else if strings.HasPrefix(outcome.sinceDiff, "Failed to generate diff since last review:") {
					assert.Contains(t, outcome.workDiff, normalizeReviewDiff(outcome.fullDiffs[1]),
						"an unavailable comparison must leave the current full diff reviewable")
				} else {
					assert.Contains(t, outcome.workDiff, normalizeReviewDiff(outcome.sinceDiff),
						"auto-review also needs the changes since user rejection")
				}

				tc.assert(t, outcome.fullDiffs, outcome.sinceDiff, h.repos.work)
			})
		}
	}
}

const reportedReviewVersionCheck = `workflow.GetVersion(dCtx, "human-feedback-provenance", workflow.DefaultVersion, 1) >= 1`

func (h *reviewDiffsFlowHarness) fulfillmentPromptContaining(work string) string {
	h.t.Helper()
	require.NotEmpty(h.t, strings.TrimSpace(work))
	for i := len(h.promptTexts) - 1; i >= 0; i-- {
		if strings.Contains(h.promptTexts[i], strings.TrimSpace(work)) {
			return h.promptTexts[i]
		}
	}
	h.t.Fatal("no captured fulfillment message contains the reviewed work")
	return ""
}
