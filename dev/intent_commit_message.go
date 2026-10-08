package dev

import (
	"fmt"
	"strings"

	"sidekick/coding/git"
	"sidekick/env"

	"go.temporal.io/sdk/workflow"
)

// intentCommitPrefix marks commits of intent documents with a custom
// Conventional Commits type.
const intentCommitPrefix = "intent:"

const intentCommitMessageInstructions = `Write the subject of a commit that records an edit to the project's intent documents, which describe how the system should behave. The staged change is shown below as a word diff, where [-text-] was removed and {+text+} was added.

- The subject is "intent: description", using the custom Conventional Commits type ` + "`intent`" + ` without a scope.
- The description minimally states, in the present tense, what the intent now says, e.g. "intent: require profile re-resolution when a flow resumes". Describe the intent itself, not the edit: never use words like "update", "change" or "implement".
- Lowercase, without a trailing period, and the whole subject is at most 72 characters.
- Leave the body empty.

Record the message with the ` + "`record_commit_message`" + ` tool.`

const iddFinishMessageInstructions = `Write a one-line commit subject for merging an intent-driven development branch. On that branch the intent documents, which describe how the system should behave, were edited as shown below (a word diff, where [-text-] was removed and {+text+} was added), and the code was changed to realize them.

- Follow Conventional Commits: "type(scope): description", where the scope is optional and type is one of feat, fix, refactor, perf, docs or chore, whichever best fits what the intent asks of the code.
- The description says which intent was realized, in the imperative mood, lowercase, without a trailing period, and the whole subject is at most 72 characters.
- Leave the body empty and don't list sub-tasks.

Record the message with the ` + "`record_commit_message`" + ` tool.`

// intentCommitParams prepares committing the staged intent. The human wrote the
// intent, so they are its author while Sidekick is only the committer: the
// reverse of code commits.
func intentCommitParams(dCtx DevContext) (git.GitCommitParams, error) {
	log := workflow.GetLogger(dCtx)

	nameOutput, err := gitCommandOutput(dCtx, "diff", "--cached", "--name-only")
	if err != nil {
		return git.GitCommitParams{}, fmt.Errorf("failed to list staged intent changes: %w", err)
	}
	var paths []string
	for _, line := range strings.Split(nameOutput, "\n") {
		if path := strings.TrimSpace(line); path != "" {
			paths = append(paths, path)
		}
	}

	message := intentCommitFallbackMessage(paths)
	if len(paths) > 0 {
		stagedDiff, err := gitCommandOutput(dCtx, "diff", "--cached", "--word-diff=plain", "--ignore-all-space")
		if err != nil {
			return git.GitCommitParams{}, fmt.Errorf("failed to get staged intent diff: %w", err)
		}
		if strings.TrimSpace(stagedDiff) != "" {
			generated, err := generateIntentCommitMessage(dCtx, stagedDiff)
			if err != nil {
				log.Warn("Failed to generate intent commit message, using fallback", "Error", err)
			} else {
				message = generated
			}
		}
	}

	author := intentAuthor(dCtx)
	return git.GitCommitParams{
		CommitMessage:         message,
		CommitAll:             true,
		AuthorName:            author.Name,
		AuthorEmail:           author.Email,
		CommitterName:         git.SidekickGitName,
		CommitterEmail:        git.SidekickGitEmail,
		IgnoreNothingToCommit: true,
	}, nil
}

// intentAuthor identifies the human who wrote the intent. Their identity is
// captured from their own machine when the flow starts, since remote envs
// generally lack it, so the flow's env is only consulted when it is local. An
// empty result leaves Sidekick as the author rather than mixing identities.
func intentAuthor(dCtx DevContext) git.GitUserConfig {
	log := workflow.GetLogger(dCtx)

	if dCtx.GlobalState != nil {
		author := git.GitUserConfig{
			Name:  dCtx.GlobalState.GetStringValue("committerName"),
			Email: dCtx.GlobalState.GetStringValue("committerEmail"),
		}
		if author.Name != "" && author.Email != "" {
			return author
		}
	}

	envType := dCtx.EnvContainer.Env.GetType()
	if envType != env.EnvTypeLocal && envType != env.EnvTypeLocalGitWorktree {
		log.Warn("Human git identity unknown, intent commit will be authored by Sidekick")
		return git.GitUserConfig{}
	}

	var userConfig git.GitUserConfig
	if err := workflow.ExecuteActivity(dCtx, git.GetGitUserConfigActivity, *dCtx.EnvContainer).Get(dCtx, &userConfig); err != nil {
		log.Warn("Failed to get git user config, intent commit will be authored by Sidekick", "Error", err)
		return git.GitUserConfig{}
	}
	if userConfig.Name == "" || userConfig.Email == "" {
		return git.GitUserConfig{}
	}
	return userConfig
}

// generateIntentCommitMessage asks the commit model for an intent commit
// subject. User retry is disabled since callers fall back to a deterministic
// message, which suits the intent canvas better than a blocked commit.
func generateIntentCommitMessage(dCtx DevContext, stagedDiff string) (string, error) {
	args, err := forceCommitMessageToolCall(dCtx, "intent_commit_message", intentCommitMessageInstructions, "# Staged Intent Diff\n\n"+truncateCommitPromptDiff(stagedDiff), true)
	if err != nil {
		return "", err
	}
	return normalizeIntentCommitSubject(args.Subject)
}

// normalizeIntentCommitSubject keeps only the first line and ensures exactly one
// intent prefix, however many the model wrote.
func normalizeIntentCommitSubject(subject string) (string, error) {
	subject, _, _ = strings.Cut(strings.TrimSpace(subject), "\n")
	for {
		subject = strings.TrimSpace(subject)
		if len(subject) < len(intentCommitPrefix) || !strings.EqualFold(subject[:len(intentCommitPrefix)], intentCommitPrefix) {
			break
		}
		subject = subject[len(intentCommitPrefix):]
	}
	if subject == "" {
		return "", fmt.Errorf("generated intent commit subject is empty")
	}
	return intentCommitPrefix + " " + subject, nil
}

// intentCommitFallbackMessage names the changed intent files, relative to the
// intent directory.
func intentCommitFallbackMessage(paths []string) string {
	if len(paths) == 0 {
		return intentCommitPrefix + " update"
	}
	relative := make([]string, len(paths))
	for i, path := range paths {
		relative[i] = strings.TrimPrefix(path, "intent/")
	}
	return intentCommitPrefix + " update " + strings.Join(relative, ", ")
}

// generateIddFinishMessage writes the subject for merging an idd branch, from
// the intent changes between baseRef and the fully committed intent at HEAD.
func generateIddFinishMessage(dCtx DevContext, baseRef string) (string, error) {
	diff, err := gitCommandOutput(dCtx, "diff", "--word-diff=plain", "--ignore-all-space", baseRef, "HEAD", "--", "intent")
	if err != nil {
		return "", fmt.Errorf("failed to get the intent diff: %w", err)
	}
	if strings.TrimSpace(diff) == "" {
		diff = "(no intent changes)"
	}
	args, err := generateCommitMessage(dCtx, "idd_finish_message", iddFinishMessageInstructions, "# Intent Diff\n\n"+truncateCommitPromptDiff(diff))
	if err != nil {
		return "", err
	}
	subject, _, _ := strings.Cut(args.Subject, "\n")
	return strings.TrimSpace(subject), nil
}

func gitCommandOutput(dCtx DevContext, args ...string) (string, error) {
	var output env.EnvRunCommandActivityOutput
	err := workflow.ExecuteActivity(dCtx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer:       *dCtx.EnvContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               args,
	}).Get(dCtx, &output)
	if err != nil {
		return "", err
	}
	if output.ExitStatus != 0 {
		return "", fmt.Errorf("git %s failed with exit status %d: %s", strings.Join(args, " "), output.ExitStatus, strings.TrimSpace(output.Stderr))
	}
	return output.Stdout, nil
}
