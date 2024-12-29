package env

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"sidekick/coding/unix"
	"sidekick/common"
	"sidekick/domain"
)

type EnvType string

const (
	EnvTypeLocal            EnvType = "local"
	EnvTypeLocalGitWorktree EnvType = "local_git_worktree"
)

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
	WorkspaceId string
	RepoDir     string
	Branch      string
	FlowId      string
}

func NewLocalEnv(ctx context.Context, params LocalEnvParams) (Env, error) {
	if params.Branch != "" {
		return nil, fmt.Errorf("branch is not supported for local environment")
	}
	dir, err := filepath.Abs(params.RepoDir)
	return &LocalEnv{WorkingDirectory: dir}, err
}

func NewLocalGitWorktreeEnv(ctx context.Context, params LocalEnvParams, worktree domain.Worktree) (Env, error) {
	sidekickDataHome, err := common.GetSidekickDataHome()
	if err != nil {
		return nil, fmt.Errorf("failed to get Sidekick data home: %w", err)
	}

	workingDir := filepath.Join(sidekickDataHome, "worktrees", worktree.WorkspaceId, worktree.Name)
	if err := os.MkdirAll(workingDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create worktree directory: %w", err)
	}

	runCommandInput := unix.RunCommandActivityInput{
		WorkingDir: params.RepoDir,
		Command:    "git",
		Args:       []string{"worktree", "add", workingDir},
	}
	runCommandOutput, err := unix.RunCommandActivity(ctx, runCommandInput)
	if err != nil {
		return nil, fmt.Errorf("failed to run git worktree add command: %w", err)
	}

	if runCommandOutput.ExitStatus != 0 {
		return nil, fmt.Errorf("git worktree add command failed with exit status %d: %s", runCommandOutput.ExitStatus, runCommandOutput.Stderr)
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
