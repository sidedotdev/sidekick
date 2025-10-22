package env

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sidekick/coding/unix"
	"sidekick/common"
	"sidekick/domain"
)

type EnvType string

const (
	EnvTypeLocal            EnvType = "local"
	EnvTypeLocalGitWorktree EnvType = "local_git_worktree"
)

func (e EnvType) IsValid() bool {
	return e == EnvTypeLocal || e == EnvTypeLocalGitWorktree
}

type Env interface {
	GetType() EnvType
	GetWorkingDirectory() string
	RunCommand(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error)
}

type EnvRunCommandInput struct {
	// the directory relative to the environment's working directory. must not contain ".."
	RelativeWorkingDir string
	Command            string
	Args               []string
	EnvVars            []string
}

type EnvRunCommandOutput = unix.RunCommandActivityOutput

type LocalEnv struct {
	WorkingDirectory string
}

type LocalGitWorktreeEnv struct {
	WorkingDirectory string
}

type LocalEnvParams struct {
	RepoDir     string
	StartBranch *string
}

func NewLocalEnv(ctx context.Context, params LocalEnvParams) (Env, error) {
	if params.StartBranch != nil {
		return nil, fmt.Errorf("start branch is not supported for local environment")
	}
	dir, err := filepath.Abs(params.RepoDir)
	return &LocalEnv{WorkingDirectory: dir}, err
}

func NewLocalGitWorktreeActivity(ctx context.Context, params LocalEnvParams, worktree domain.Worktree) (EnvContainer, error) {
	env, err := NewLocalGitWorktreeEnv(ctx, params, worktree)
	return EnvContainer{Env: env}, err
}

func NewLocalGitWorktreeEnv(ctx context.Context, params LocalEnvParams, worktree domain.Worktree) (Env, error) {
	sidekickDataHome, err := common.GetSidekickDataHome()
	if err != nil {
		return nil, fmt.Errorf("failed to get Sidekick data home: %w", err)
	}

	// Create worktree directory
	// dirName combines original repo name and suffix of branch name, for better DX
	repoName := filepath.Base(params.RepoDir)
	branchSuffix := strings.TrimPrefix(worktree.Name, "side/")
	dirName := repoName + "-" + branchSuffix
	workingDir := filepath.Join(sidekickDataHome, "worktrees", worktree.WorkspaceId, dirName)
	if err := os.MkdirAll(workingDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create worktree directory: %w", err)
	}

	// a worktree's name refers to its branch name
	newBranchName := worktree.Name
	worktreeBaseRef := "HEAD"
	if params.StartBranch != nil && *params.StartBranch != "" {
		worktreeBaseRef = *params.StartBranch
	}
	// Add the worktree, creating a new branch based on the target branch
	addWorktreeInput := unix.RunCommandActivityInput{
		WorkingDir: params.RepoDir,
		Command:    "git",
		Args:       []string{"worktree", "add", "-b", newBranchName, workingDir, worktreeBaseRef},
	}
	addWorktreeOutput, err := unix.RunCommandActivity(ctx, addWorktreeInput)
	if err != nil {
		return nil, fmt.Errorf("failed to run git worktree add command: %w", err)
	}

	if addWorktreeOutput.ExitStatus != 0 {
		return nil, fmt.Errorf("git worktree add command failed with exit status %d: %s", addWorktreeOutput.ExitStatus, addWorktreeOutput.Stderr)
	}

	return &LocalGitWorktreeEnv{WorkingDirectory: workingDir}, nil
}

func (e *LocalEnv) GetType() EnvType {
	return EnvTypeLocal
}

func (e *LocalEnv) GetWorkingDirectory() string {
	return e.WorkingDirectory
}

func (e *LocalEnv) RunCommand(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
	runCommandInput := unix.RunCommandActivityInput{
		WorkingDir: filepath.Join(e.WorkingDirectory, input.RelativeWorkingDir),
		Command:    input.Command,
		Args:       input.Args,
		EnvVars:    input.EnvVars,
	}
	return unix.RunCommandActivity(ctx, runCommandInput)
}

func (e *LocalGitWorktreeEnv) GetType() EnvType {
	return EnvTypeLocalGitWorktree
}

func (e *LocalGitWorktreeEnv) GetWorkingDirectory() string {
	return e.WorkingDirectory
}

func (e *LocalGitWorktreeEnv) RunCommand(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
	runCommandInput := unix.RunCommandActivityInput{
		WorkingDir: filepath.Join(e.WorkingDirectory, input.RelativeWorkingDir),
		Command:    input.Command,
		Args:       input.Args,
		EnvVars:    input.EnvVars,
	}
	return unix.RunCommandActivity(ctx, runCommandInput)
}

// EnvContainer is a wrapper for the Env interface that provides custom
// JSON marshaling and unmarshaling.
type EnvContainer struct {
	Env Env
}

// MarshalJSON returns the JSON encoding of the EnvContainer.
func (ec EnvContainer) MarshalJSON() ([]byte, error) {
	// Marshal to type and actual data to handle unmarshaling to specific interface type
	return json.Marshal(struct {
		Type string
		Env  Env
	}{
		Type: string(ec.Env.GetType()),
		Env:  ec.Env,
	})
}

// UnmarshalJSON parses the JSON-encoded data and stores the result in the EnvContainer.
func (ec *EnvContainer) UnmarshalJSON(data []byte) error {
	var v struct {
		Type string
		Env  json.RawMessage
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}

	switch v.Type {
	case string(EnvTypeLocal):
		var le *LocalEnv
		if err := json.Unmarshal(v.Env, &le); err != nil {
			return err
		}
		ec.Env = le
	case string(EnvTypeLocalGitWorktree):
		var lgwe *LocalGitWorktreeEnv
		if err := json.Unmarshal(v.Env, &lgwe); err != nil {
			return err
		}
		ec.Env = lgwe
	default:
		return fmt.Errorf("unknown Env type: %s", v.Type)
	}

	return nil
}

type EnvRunCommandActivityInput struct {
	EnvContainer EnvContainer
	/* the following fields should always match EnvRunCommandInput */
	RelativeWorkingDir string   `json:"relativeWorkingDir"`
	Command            string   `json:"command"`
	Args               []string `json:"args"`
}

type EnvRunCommandActivityOutput = EnvRunCommandOutput

// EnvRunCommandActivity runs a command in the environment contained in the provided EnvContainer.
func EnvRunCommandActivity(ctx context.Context, input EnvRunCommandActivityInput) (EnvRunCommandActivityOutput, error) {
	return input.EnvContainer.Env.RunCommand(ctx, EnvRunCommandInput{
		RelativeWorkingDir: input.RelativeWorkingDir,
		Command:            input.Command,
		Args:               input.Args,
	})
}
