package git

import (
	"context"
	"fmt"
	"sidekick/env"
	"sidekick/flow_action"
	"strings"

	"go.temporal.io/sdk/workflow"
)

type GitCommitParams struct {
	CommitMessage  string
	CommitAll      bool
	CommitterName  string
	CommitterEmail string
}

func GitCommitActivity(ctx context.Context, envContainer env.EnvContainer, params GitCommitParams) (string, error) {
	committerName, committerEmail := params.CommitterName, params.CommitterEmail
	if committerName == "" || committerEmail == "" {
		envType := envContainer.Env.GetType()
		if envType == env.EnvTypeLocal || envType == env.EnvTypeLocalGitWorktree {
			name, email, err := getGitUserConfig(ctx, envContainer)
			if err == nil {
				if committerName == "" {
					committerName = name
				}
				if committerEmail == "" {
					committerEmail = email
				}
			}
		}
	}

	envVars := buildGitEnvVars(committerName, committerEmail)
	args := []string{"commit", "-m", params.CommitMessage}
	if params.CommitAll {
		args = append(args, "-a")
	}
	gitCommitOutput, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               args,
		EnvVars:            envVars,
	})
	if err != nil {
		return "", fmt.Errorf("failed to git commit: %v", err)
	}
	if gitCommitOutput.ExitStatus != 0 {
		return "", fmt.Errorf("git commit failed: %s", gitCommitOutput.Stdout+"\n"+gitCommitOutput.Stderr)
	}
	return gitCommitOutput.Stdout, nil
}

// GitUserConfig holds the git user configuration
type GitUserConfig struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// GetGitUserConfigActivity retrieves the git user.name and user.email configuration
func GetGitUserConfigActivity(ctx context.Context, envContainer env.EnvContainer) (GitUserConfig, error) {
	nameOutput, nameErr := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               []string{"config", "user.name"},
	})
	if nameErr != nil || nameOutput.ExitStatus != 0 {
		return GitUserConfig{}, fmt.Errorf("failed to get git user.name")
	}

	emailOutput, emailErr := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               []string{"config", "user.email"},
	})
	if emailErr != nil || emailOutput.ExitStatus != 0 {
		return GitUserConfig{}, fmt.Errorf("failed to get git user.email")
	}

	return GitUserConfig{
		Name:  strings.TrimSpace(nameOutput.Stdout),
		Email: strings.TrimSpace(emailOutput.Stdout),
	}, nil
}

func getGitUserConfig(ctx context.Context, envContainer env.EnvContainer) (name string, email string, err error) {
	config, err := GetGitUserConfigActivity(ctx, envContainer)
	if err != nil {
		return "", "", err
	}
	return config.Name, config.Email, nil
}

func buildGitEnvVars(committerName, committerEmail string) []string {
	envVars := []string{
		"GIT_AUTHOR_NAME=Sidekick",
		"GIT_AUTHOR_EMAIL=sidekick@side.dev",
	}
	if committerName != "" {
		envVars = append(envVars, "GIT_COMMITTER_NAME="+committerName)
	}
	if committerEmail != "" {
		envVars = append(envVars, "GIT_COMMITTER_EMAIL="+committerEmail)
	}
	return envVars
}

func GitCommit(eCtx flow_action.ExecContext, commitMessage string) error {
	diff, err := gitDiffStaged(eCtx)
	if diff == "" && err == nil {
		// can't commit what ain't staged (points at head)
		return nil
	}

	commitParams := GitCommitParams{
		CommitMessage: commitMessage,
	}
	if eCtx.GlobalState != nil {
		commitParams.CommitterName = eCtx.GlobalState.GetStringValue("committerName")
		commitParams.CommitterEmail = eCtx.GlobalState.GetStringValue("committerEmail")
	}
	commitErr := workflow.ExecuteActivity(eCtx, GitCommitActivity, eCtx.EnvContainer, commitParams).Get(eCtx, nil)
	if commitErr != nil {
		if !strings.Contains(commitErr.Error(), "nothing to commit") {
			return fmt.Errorf("failed to commit changes: %v", commitErr)
		}
	}
	return nil
}

func gitDiffStaged(eCtx flow_action.ExecContext) (string, error) {
	var diff string
	err := workflow.ExecuteActivity(eCtx, GitDiffActivity, eCtx.EnvContainer, GitDiffParams{
		Staged: true,
	}).Get(eCtx, &diff)
	return diff, err
}
