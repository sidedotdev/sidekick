package git

import (
	"context"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type GitDiffWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env          *testsuite.TestWorkflowEnvironment
	dir          string
	envContainer env.EnvContainer

	// a wrapper is required to set the ctx1 value, so that we can a method that
	// isn't a real workflow. otherwise we get errors about not having
	// StartToClose or ScheduleToCloseTimeout set
	wrapperWorkflow func(ctx workflow.Context, envContainer env.EnvContainer) (string, error)
}

func (s *GitDiffWorkflowTestSuite) SetupTest() {
	// log warnings only (default debug level is too noisy when tests fail)
	th := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{AddSource: false, Level: slog.LevelWarn})
	s.SetLogger(tlog.NewStructuredLogger(slog.New(th)))

	// setup workflow environment
	s.env = s.NewTestWorkflowEnvironment()

	// s.NewTestActivityEnvironment()
	s.wrapperWorkflow = func(ctx workflow.Context, envContainer env.EnvContainer) (string, error) {
		ctx1 := utils.NoRetryCtx(ctx)
		eCtx := flow_action.ExecContext{
			Context:      ctx1,
			EnvContainer: &envContainer,
		}
		// TODO /gen switch to testing GitDiff instead of GitDiffLegacy, with the fflag
		// activity mocked with OnActivity
		return GitDiffLegacy(eCtx)
	}
	s.env.RegisterWorkflow(s.wrapperWorkflow)
	s.env.RegisterActivity(env.EnvRunCommandActivity)

	// TODO create a helper function: CreateTestLocalEnvironment
	dir, err := os.MkdirTemp("", "git_diff_test")
	if err != nil {
		log.Fatalf("Failed to create temp dir: %v", err)
	}
	devEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{
		RepoDir: dir,
	})
	if err != nil {
		log.Fatalf("Failed to create local environment: %v", err)
	}
	s.dir = dir
	s.envContainer = env.EnvContainer{
		Env: devEnv,
	}

	// init git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	err = cmd.Run()
	if err != nil {
		log.Fatalf("Failed to init git repo: %v", err)
	}
}

func (s *GitDiffWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
	// os.RemoveAll(s.dir)
}

func TestGitDiffWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(GitDiffWorkflowTestSuite))
}

func (s *GitDiffWorkflowTestSuite) TestEmptyRepo() {
	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	var result string
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal("", strings.Trim(result, "\n"))
}

func (s *GitDiffWorkflowTestSuite) TestGitDiffExistingFile() {
	// Add and commit a file to the git repository
	filePath := filepath.Join(s.dir, "existing_file.txt")
	err := os.WriteFile(filePath, []byte("existing file content"), fs.FileMode(0644))
	s.NoError(err)

	cmd := exec.Command("git", "add", "existing_file.txt")
	cmd.Dir = s.dir
	err = cmd.Run()
	s.NoError(err)

	cmd = exec.Command("git", "commit", "-m", `"Add existing file"`)
	cmd.Dir = s.dir
	err = cmd.Run()
	s.NoError(err)

	// after changes to the existing file, should show the diff
	err = os.WriteFile(filePath, []byte("changed file content"), fs.FileMode(0644))
	s.NoError(err)

	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	var result string
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Contains(result, "existing_file.txt")
	s.Contains(result, "existing file content")
	s.Contains(result, "changed file content")
}

func (s *GitDiffWorkflowTestSuite) TestGitDiffNewFile() {
	// Create a new untracked file in the git repository
	filePath := filepath.Join(s.dir, "new_file.txt")
	file, err := os.Create(filePath)
	s.NoError(err)
	defer file.Close()

	_, err = file.WriteString("new file content")
	s.NoError(err)

	// Test GitDiff with the new file
	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	var result string
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Contains(result, "new_file.txt")
	s.Contains(result, "new file content")
}
