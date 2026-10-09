package dev

import (
	"encoding/json"
	"fmt"
	"strings"

	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/persisted_ai"

	"github.com/invopop/jsonschema"
	"go.temporal.io/sdk/workflow"
)

var commitMessageTool = llm.Tool{
	Name:        "record_commit_message",
	Description: "Records the commit message.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&commitMessageArguments{}),
}

type commitMessageArguments struct {
	Subject string `json:"subject" jsonschema:"description=The single-line commit subject."`
	Body    string `json:"body,omitempty" jsonschema:"description=The optional commit body. Leave empty when not requested or not needed."`
}

func (a commitMessageArguments) Message() string {
	subject := strings.TrimSpace(a.Subject)
	body := strings.TrimSpace(a.Body)
	if body == "" {
		return subject
	}
	return subject + "\n\n" + body
}

// commitModelConfig resolves the commit model, falling back to the code
// localization model and then the defaults when commit isn't configured.
func commitModelConfig(dCtx DevContext) common.ModelConfig {
	return dCtx.GetModelConfig(common.CommitKey, 0, common.CodeLocalizationKey)
}

// generateCommitMessage asks the commit model for a commit message via a
// single forced tool call on a fresh history, since the instructions and
// context given fully describe what the message needs to cover.
func generateCommitMessage(dCtx DevContext, actionType, instructions, promptContext string) (commitMessageArguments, error) {
	return forceCommitMessageToolCall(dCtx, actionType, instructions, promptContext, false)
}

// forceCommitMessageToolCall implements generateCommitMessage. Callers with
// their own fallback set disableUserRetry so failures surface as errors after
// a few quick retries instead of waiting on the user.
func forceCommitMessageToolCall(dCtx DevContext, actionType, instructions, promptContext string, disableUserRetry bool) (commitMessageArguments, error) {
	chatHistory := NewVersionedChatHistory(dCtx, dCtx.WorkspaceId)
	if err := AppendChatHistory(dCtx.ExecContext, chatHistory, llm.ChatMessage{
		Role:    llm.ChatMessageRoleUser,
		Content: strings.TrimSpace(instructions) + "\n\n" + strings.TrimSpace(promptContext),
	}); err != nil {
		return commitMessageArguments{}, fmt.Errorf("failed to build commit message prompt: %w", err)
	}

	modelConfig := commitModelConfig(dCtx)
	actionCtx := dCtx.ExecContext.NewActionContext("generate." + actionType)
	toolNameMapping, err := resolveStreamToolNameMapping(actionCtx.ExecContext, modelConfig, *actionCtx.Secrets)
	if err != nil {
		return commitMessageArguments{}, fmt.Errorf("failed to resolve tool name mapping: %w", err)
	}
	response, err := persisted_ai.ForceToolCallWithFallbackV2(actionCtx, flow_action.TrackOptions{}, disableUserRetry, modelConfig, chatHistory, toolNameMapping, &commitMessageTool)
	if err != nil {
		return commitMessageArguments{}, fmt.Errorf("failed to generate commit message: %w", err)
	}

	toolCalls := response.GetMessage().GetToolCalls()
	if len(toolCalls) == 0 {
		return commitMessageArguments{}, fmt.Errorf("commit message generation returned no tool call")
	}
	var args commitMessageArguments
	if err := json.Unmarshal([]byte(llm.RepairJson(toolCalls[0].Arguments)), &args); err != nil {
		return commitMessageArguments{}, fmt.Errorf("%w: %v", llm.ErrToolCallUnmarshal, err)
	}
	args.Subject = strings.TrimSpace(args.Subject)
	args.Body = strings.TrimSpace(args.Body)
	if args.Subject == "" {
		return commitMessageArguments{}, fmt.Errorf("generated commit message has an empty subject")
	}
	return args, nil
}

const squashCommitMessageInstructions = `Write the commit message for squash-merging the work described below into a single commit.

Follow Conventional Commits:
- The subject is "type(scope): description", where the scope is optional and type is one of feat, fix, refactor, perf, test, docs, build, ci, chore or style.
- The description is in the imperative mood, lowercase, without a trailing period, and the whole subject is at most 72 characters.
- The body is a short summary of the notable changes and why they were made: a few lines or bullet points, wrapped at 72 characters. Leave it empty when the subject already says everything worth saying.

Describe the resulting change, not the process: don't mention review rounds, plan steps, or individual branch commits.

Record the message with the ` + "`record_commit_message`" + ` tool.`

// squashMergeCommitMessage generates the message for squash-merging the
// current branch into targetBranch. The branch's commit log stands in for the
// diff: it already summarizes the changes at a fraction of the tokens.
func squashMergeCommitMessage(dCtx DevContext, params MergeWithReviewParams, targetBranch string) (string, error) {
	var logOutput env.EnvRunCommandActivityOutput
	err := workflow.ExecuteActivity(dCtx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer:       *dCtx.EnvContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               []string{"log", "--no-merges", "--format=%s%n%n%b", targetBranch + "..HEAD"},
	}).Get(dCtx, &logOutput)
	if err != nil {
		return "", fmt.Errorf("failed to get branch commit log: %w", err)
	}
	if logOutput.ExitStatus != 0 {
		return "", fmt.Errorf("git log failed with exit status %d: %s", logOutput.ExitStatus, logOutput.Stderr)
	}

	args, err := generateCommitMessage(dCtx, "squash_commit_message", squashCommitMessageInstructions, renderSquashCommitContext(params, logOutput.Stdout))
	if err != nil {
		return "", err
	}
	return args.Message(), nil
}

func renderSquashCommitContext(params MergeWithReviewParams, commitLog string) string {
	var b strings.Builder
	writeSection := func(heading, content string) {
		content = strings.TrimSpace(content)
		if content == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "# %s\n\n%s", heading, content)
	}
	writeSection("Task Title", params.Title)
	writeSection("Requirements", params.Requirements)
	writeSection("Work Summary", params.WorkSummary)
	if strings.TrimSpace(commitLog) == "" {
		commitLog = "(no commits)"
	}
	writeSection("Branch Commit Log", commitLog)
	return b.String()
}
