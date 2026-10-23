package dev

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"sidekick/common"
	"sidekick/env"
	"sidekick/env/localio"
	"sidekick/repotools"

	"go.temporal.io/sdk/workflow"
)

type SearchRepositoryInput = repotools.SearchRepositoryInput
type SingleSearchParams = repotools.SingleSearchParams

const coreIgnoreFileContent = repotools.CoreIgnoreFileContent
const coreIgnoreRelPath = repotools.CoreIgnoreRelPath
const maxSearchOutputLength = repotools.MaxSearchOutputLength
const refuseAtSearchOutputLength = repotools.RefuseAtSearchOutputLength

type EnsureCoreIgnoreFileActivityInput struct {
	EnvContainer env.EnvContainer
}

type EnsureCoreIgnoreFileActivityOutput struct {
	Path string
}

func EnsureCoreIgnoreFileActivity(ctx context.Context, input EnsureCoreIgnoreFileActivityInput) (EnsureCoreIgnoreFileActivityOutput, error) {
	if err := input.EnvContainer.Env.MkdirAll(ctx, filepath.Dir(coreIgnoreRelPath), 0755); err != nil {
		return EnsureCoreIgnoreFileActivityOutput{}, fmt.Errorf("failed to create core ignore dir: %w", err)
	}
	if err := input.EnvContainer.Env.WriteFile(ctx, coreIgnoreRelPath, []byte(coreIgnoreFileContent), 0644); err != nil {
		return EnsureCoreIgnoreFileActivityOutput{}, fmt.Errorf("failed to write core ignore file: %w", err)
	}
	return EnsureCoreIgnoreFileActivityOutput{
		Path: filepath.Join(input.EnvContainer.Env.GetWorkingDirectory(), coreIgnoreRelPath),
	}, nil
}

func getOrCreateCoreIgnoreFile() (string, error) {
	configDir := common.GetSidekickConfigDir()
	coreIgnorePath := filepath.Join(configDir, "core_ignore")
	if _, err := os.Stat(coreIgnorePath); os.IsNotExist(err) {
		if err := os.MkdirAll(configDir, 0755); err != nil {
			return "", fmt.Errorf("failed to create config directory: %v", err)
		}
		if err := os.WriteFile(coreIgnorePath, []byte(coreIgnoreFileContent), 0644); err != nil {
			return "", fmt.Errorf("failed to create core_ignore file: %v", err)
		}
	} else if err != nil {
		return "", fmt.Errorf("failed to check core_ignore file: %v", err)
	}
	return coreIgnorePath, nil
}

type workflowSearchRunner struct {
	ctx workflow.Context
}

type activitySearchRunner struct {
	ctx context.Context
}

type searchRunner interface {
	runCommand(env.EnvRunCommandActivityInput) (env.EnvRunCommandOutput, error)
	ensureCoreIgnoreFile(EnsureCoreIgnoreFileActivityInput) (EnsureCoreIgnoreFileActivityOutput, error)
	getVersion(string, workflow.Version, workflow.Version) workflow.Version
}

func (r workflowSearchRunner) runCommand(input env.EnvRunCommandActivityInput) (env.EnvRunCommandOutput, error) {
	var output env.EnvRunCommandOutput
	err := workflow.ExecuteActivity(r.ctx, env.EnvRunCommandActivity, input).Get(r.ctx, &output)
	return output, err
}

func (r workflowSearchRunner) ensureCoreIgnoreFile(input EnsureCoreIgnoreFileActivityInput) (EnsureCoreIgnoreFileActivityOutput, error) {
	var output EnsureCoreIgnoreFileActivityOutput
	err := workflow.ExecuteActivity(r.ctx, EnsureCoreIgnoreFileActivity, input).Get(r.ctx, &output)
	return output, err
}

func (r workflowSearchRunner) getVersion(changeID string, min, max workflow.Version) workflow.Version {
	return workflow.GetVersion(r.ctx, changeID, min, max)
}

func (r activitySearchRunner) runCommand(input env.EnvRunCommandActivityInput) (env.EnvRunCommandOutput, error) {
	return env.EnvRunCommandActivity(r.ctx, input)
}

func (r activitySearchRunner) ensureCoreIgnoreFile(input EnsureCoreIgnoreFileActivityInput) (EnsureCoreIgnoreFileActivityOutput, error) {
	return EnsureCoreIgnoreFileActivity(r.ctx, input)
}

func (r activitySearchRunner) getVersion(_ string, _, max workflow.Version) workflow.Version {
	return max
}

type repositorySearchRunner struct {
	searchRunner
	container env.EnvContainer
}

func (r repositorySearchRunner) RunCommand(command string, args ...string) (localio.RunCommandOutput, error) {
	// Historical workflows scheduled an empty command in the length check.
	if command == "" {
		return r.runCommand(env.EnvRunCommandActivityInput{})
	}
	return r.runCommand(env.EnvRunCommandActivityInput{
		EnvContainer: r.container, RelativeWorkingDir: "./", Command: command, Args: args,
	})
}

func (r repositorySearchRunner) EnsureCoreIgnoreFile() (string, error) {
	if r.Legacy("env-side-core-ignore") {
		return getOrCreateCoreIgnoreFile()
	}
	output, err := r.ensureCoreIgnoreFile(EnsureCoreIgnoreFileActivityInput{EnvContainer: r.container})
	return output.Path, err
}

func (r repositorySearchRunner) Legacy(changeID string) bool {
	return r.getVersion(changeID, workflow.DefaultVersion, 1) < 1
}

func SearchRepository(ctx workflow.Context, container env.EnvContainer, input SearchRepositoryInput) (string, error) {
	return repotools.SearchRepository(repositorySearchRunner{workflowSearchRunner{ctx}, container}, input)
}
