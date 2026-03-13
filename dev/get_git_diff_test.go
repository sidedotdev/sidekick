package dev

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/coding/git"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/utils"
)

type GetGitDiffTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env          *testsuite.TestWorkflowEnvironment
	dir          string
	envContainer env.EnvContainer
}

func (s *GetGitDiffTestSuite) SetupTest() {
	s.T().Helper()
	th := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{AddSource: false, Level: slog.LevelWarn})
	s.SetLogger(tlog.NewStructuredLogger(slog.New(th)))

	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetTestTimeout(30 * time.Second)
	s.env.RegisterActivity(git.GitDiffActivity)
	s.env.RegisterActivity(env.EnvRunCommandActivity)
	s.env.RegisterActivity(git.DiffUntrackedFilesActivity)

	s.dir = s.T().TempDir()
	devEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{RepoDir: s.dir})
	require.NoError(s.T(), err)
	s.envContainer = env.EnvContainer{Env: devEnv}

	runCmd(s.T(), s.dir, "git", "init", "-b", "main")
	runCmd(s.T(), s.dir, "git", "config", "user.email", "test@test.com")
	runCmd(s.T(), s.dir, "git", "config", "user.name", "Test")
}

func (s *GetGitDiffTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestGetGitDiffTestSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(GetGitDiffTestSuite))
}

// TestThreeDotInflatedByMergedUpstream verifies that GetGitDiff picks the
// smaller of three-dot and direct diffs when upstream merges inflate the
// three-dot result.
func (s *GetGitDiffTestSuite) TestThreeDotInflatedByMergedUpstream() {
	t := s.T()

	writeAndCommit(t, s.dir, "base.txt", "base content", "initial commit")

	runCmd(t, s.dir, "git", "checkout", "-b", "feature1")
	feature1Content := strings.Repeat("feature1 work line\n", 100)
	writeAndCommit(t, s.dir, "feature1.txt", feature1Content, "feature1 commit")

	runCmd(t, s.dir, "git", "checkout", "-b", "feature2")
	writeAndCommit(t, s.dir, "feature2.txt", "feature2 work", "feature2 commit")

	runCmd(t, s.dir, "git", "checkout", "main")
	writeAndCommit(t, s.dir, "upstream.txt", "upstream change", "upstream changes")

	runCmd(t, s.dir, "git", "checkout", "feature1")
	runCmd(t, s.dir, "git", "merge", "main", "-m", "merge main into feature1")

	runCmd(t, s.dir, "git", "checkout", "feature2")
	runCmd(t, s.dir, "git", "merge", "main", "-m", "merge main into feature2")

	// Stage all changes so the staged three-dot diff sees them
	runCmd(t, s.dir, "git", "add", "-A")

	wrapperWorkflow := func(ctx workflow.Context, ec env.EnvContainer) (string, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context:      ctx,
				EnvContainer: &ec,
			},
		}
		return GetGitDiff(dCtx, "feature1", false)
	}
	s.env.RegisterWorkflow(wrapperWorkflow)

	s.env.ExecuteWorkflow(wrapperWorkflow, s.envContainer)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result string
	s.NoError(s.env.GetWorkflowResult(&result))

	s.Contains(result, "feature2.txt", "real changes should be in the diff")
	s.NotContains(result, "feature1.txt",
		"GetGitDiff should pick the smaller direct diff, excluding inflated three-dot content")
}

func runCmd(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s %v failed: %s", name, args, string(out))
	return string(out)
}

func writeAndCommit(t *testing.T, dir, filename, content, msg string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, filename), []byte(content), 0644))
	runCmd(t, dir, "git", "add", filename)
	runCmd(t, dir, "git", "commit", "-m", msg)
}
