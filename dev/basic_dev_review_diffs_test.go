package dev

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/workflow"

	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/srv"
	"sidekick/workspace"
)

// repoMutator changes a repository the way a flow does: through the real git
// activities, so the review diffs under test are produced from changes made the
// same way production makes them. It records how many of each it made, so tests
// can assert the changes they asked for really happened.
type repoMutator struct {
	t            *testing.T
	envContainer env.EnvContainer
	branch       string
	baseBranch   string

	adds    int
	commits int
	merges  int
}

func (m *repoMutator) dir() string {
	return m.envContainer.Env.GetWorkingDirectory()
}

func (m *repoMutator) stage(filename, content string) {
	m.t.Helper()
	require.NoError(m.t, os.WriteFile(filepath.Join(m.dir(), filename), []byte(content), 0644))
	require.NoError(m.t, git.GitAddActivity(context.Background(), git.GitAddActivityInput{
		EnvContainer: m.envContainer,
		Path:         filename,
	}))
	m.adds++
}

func (m *repoMutator) commit(filename, content, message string) {
	m.t.Helper()
	m.stage(filename, content)
	_, err := git.GitCommitActivity(context.Background(), m.envContainer, git.GitCommitParams{
		CommitMessage: message,
	})
	require.NoError(m.t, err)
	m.commits++
}

// mergeBase merges the base branch into the branch being worked on, which is
// how upstream work reaches a flow's working copy mid-review.
func (m *repoMutator) mergeBase() git.MergeActivityResult {
	m.t.Helper()
	m.branch = m.currentBranch()
	result, err := git.GitMergeActivity(context.Background(), m.envContainer, git.GitMergeParams{
		SourceBranch: m.baseBranch,
		TargetBranch: m.branch,
	})
	require.NoError(m.t, err)
	m.merges++
	return result
}

// runGit covers the few repository operations, all of them branch switching,
// that no production activity exposes. Everything a flow itself does goes
// through the corresponding git activity instead.
func (m *repoMutator) runGit(args ...string) env.EnvRunCommandActivityOutput {
	m.t.Helper()
	output, err := env.EnvRunCommandActivity(context.Background(), env.EnvRunCommandActivityInput{
		EnvContainer:       m.envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               args,
	})
	require.NoError(m.t, err)
	require.Zero(m.t, output.ExitStatus, "git %s failed: %s", strings.Join(args, " "), output.Stderr)
	return output
}

func (m *repoMutator) checkout(branch string) {
	m.t.Helper()
	m.runGit("checkout", branch)
	m.branch = branch
}

func (m *repoMutator) checkoutNew(branch string) {
	m.t.Helper()
	m.runGit("checkout", "-b", branch)
	m.branch = branch
}

func (m *repoMutator) currentBranch() string {
	m.t.Helper()
	return strings.TrimSpace(m.runGit("rev-parse", "--abbrev-ref", "HEAD").Stdout)
}

// flowRepos are the repositories a review flow spans: the flow's own working
// copy and the base repository where other people's commits land.
type flowRepos struct {
	work       *repoMutator
	base       *repoMutator
	baseBranch string
}

// commitUpstream adds a commit to the base branch, standing in for work others
// do while ours is in review.
func (r *flowRepos) commitUpstream(filename, content, message string) {
	r.base.t.Helper()
	previous := r.base.currentBranch()
	r.base.checkout(r.baseBranch)
	defer r.base.checkout(previous)
	r.base.commit(filename, content, message)
}

// runBasicDevWorkflow runs the exported basic dev workflow end to end, which
// unlike the review/resolve entry point does its first coding round before any
// review, so the work reviewed in the first round is authored in round one and
// the response to that review in round two.
func (h *reviewDiffsFlowHarness) runBasicDevWorkflow(requirements string) reviewRoundsOutcome {
	h.t.Helper()
	h.registerBasicDevMocks()
	h.roundHooks = map[int]func(t *testing.T, repos *flowRepos){
		1: h.prepare,
		2: h.respond,
	}

	target := "main"
	input := BasicDevWorkflowInput{
		WorkspaceId:  "test-workspace",
		RepoDir:      h.dir,
		Requirements: requirements,
		BasicDevOptions: BasicDevOptions{
			EnvType:     env.EnvTypeLocal,
			RepoMode:    env.RepoModeWorktree,
			StartBranch: &target,
		},
	}
	h.runFlowWithUserResponses(func(ctx workflow.Context) error {
		_, err := BasicDevWorkflow(ctx, input)
		return err
	}, &realFlowUserResponder{rejectionMessage: "please address the feedback", approveAfter: 1})

	return h.reviewRoundsOutcome(2)
}

// registerBasicDevMocks covers what the basic dev workflow needs on top of the
// review loop itself: configuration lookups and worktree setup. The flow's
// worktree is the harness repository, checked out on the branch the flow
// generated, so every git operation the flow runs stays visible to the test.
func (h *reviewDiffsFlowHarness) registerBasicDevMocks() {
	h.env.RegisterActivity(GetRepoConfigActivity)
	h.env.RegisterActivity(GetRepoConfigActivityV2)

	h.env.OnActivity(common.GetLocalConfig).Return(common.LocalPublicConfig{}, nil).Maybe()
	h.env.OnActivity(common.BaseCommandPermissionsActivity, mock.Anything, mock.Anything).
		Return(common.CommandPermissionConfig{}, nil).Maybe()

	var workspaceActivities *workspace.Activities
	h.env.OnActivity(workspaceActivities.GetWorkspace, mock.Anything).
		Return(domain.Workspace{ConfigMode: "merge"}, nil).Maybe()
	h.env.OnActivity(workspaceActivities.GetWorkspaceConfig, mock.Anything).Return(domain.WorkspaceConfig{
		LLM:       common.LLMConfig{Defaults: []common.ModelConfig{{Provider: "test", Model: "judging-model"}}},
		Embedding: common.EmbeddingConfig{Defaults: []common.ModelConfig{{Provider: "test", Model: "embedding-model"}}},
	}, nil).Maybe()

	var srvActivities srv.Activities
	h.env.OnActivity(srvActivities.PersistWorktree, mock.Anything, mock.Anything).Return(nil).Maybe()

	h.env.OnActivity(env.NewLocalGitWorktreeActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ context.Context, _ env.LocalEnvParams, worktree domain.Worktree) (env.EnvContainer, error) {
			h.repos.work.checkout(h.repos.baseBranch)
			h.repos.work.checkoutNew(worktree.Name)
			return h.envContainer, nil
		}).Maybe()
}
