package dev

import (
	"errors"
	"fmt"
	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"strings"

	"github.com/invopop/jsonschema"
	"go.temporal.io/sdk/workflow"
)

var commitDoneTool = llm.Tool{
	Name:        "done",
	Description: "Call this tool ONLY once every pending change has been committed and the working tree is fully clean (nothing staged, modified or untracked). Call it on its own, without any other tool calls in the same response.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&DoneArguments{}),
}

// maxCommitPromptDiffLength bounds each diff rendered into commit prompts;
// the model can always run git itself to see more.
const maxCommitPromptDiffLength = 20000

// gitWorkingState captures uncommitted work in the repository.
type gitWorkingState struct {
	Status       string
	StagedDiff   string
	UnstagedDiff string
}

// IsClean reports whether nothing is staged, modified or untracked, which is
// exactly when `git status --porcelain` prints nothing.
func (s gitWorkingState) IsClean() bool {
	return strings.TrimSpace(s.Status) == ""
}

func (s gitWorkingState) Render() string {
	if s.IsClean() {
		return "The working tree is clean: nothing is staged, modified or untracked."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## Git status (git status --porcelain)\n\n```\n%s\n```", strings.TrimRight(s.Status, "\n"))
	renderDiff := func(heading, diff string) {
		if strings.TrimSpace(diff) == "" {
			fmt.Fprintf(&b, "\n\n## %s\n\n(none)", heading)
			return
		}
		fmt.Fprintf(&b, "\n\n## %s\n\n```diff\n%s\n```", heading, strings.TrimRight(truncateCommitPromptDiff(diff), "\n"))
	}
	renderDiff("Staged diff", s.StagedDiff)
	renderDiff("Unstaged diff (including untracked files)", s.UnstagedDiff)
	return b.String()
}

func truncateCommitPromptDiff(diff string) string {
	if len(diff) <= maxCommitPromptDiffLength {
		return diff
	}
	return strings.ToValidUTF8(diff[:maxCommitPromptDiffLength], "") + "\n... (truncated: run git diff to see the rest)"
}

func collectGitWorkingState(dCtx DevContext) (gitWorkingState, error) {
	var statusOutput env.EnvRunCommandActivityOutput
	err := workflow.ExecuteActivity(dCtx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer:       *dCtx.EnvContainer,
		RelativeWorkingDir: "./",
		Command:            "git",
		Args:               []string{"status", "--porcelain", "--untracked-files=all"},
	}).Get(dCtx, &statusOutput)
	if err != nil {
		return gitWorkingState{}, fmt.Errorf("failed to get git status: %w", err)
	}
	if statusOutput.ExitStatus != 0 {
		return gitWorkingState{}, fmt.Errorf("git status failed with exit status %d: %s", statusOutput.ExitStatus, statusOutput.Stderr)
	}

	state := gitWorkingState{Status: statusOutput.Stdout}
	if state.IsClean() {
		return state, nil
	}

	err = workflow.ExecuteActivity(dCtx, git.GitDiffActivity, *dCtx.EnvContainer, git.GitDiffParams{Staged: true}).Get(dCtx, &state.StagedDiff)
	if err != nil {
		return gitWorkingState{}, fmt.Errorf("failed to get staged git diff: %w", err)
	}
	err = workflow.ExecuteActivity(dCtx, git.GitDiffActivity, *dCtx.EnvContainer, git.GitDiffParams{}).Get(dCtx, &state.UnstagedDiff)
	if err != nil {
		return gitWorkingState{}, fmt.Errorf("failed to get unstaged git diff: %w", err)
	}
	return state, nil
}

const (
	// defaultCommitRemindAfter is when committing first gets reminded to
	// wrap up, absent "commit" agent config.
	defaultCommitRemindAfter = 6
	// defaultCommitEscalateReminderEvery is how long committing may run past
	// each reminder before the next, escalated one.
	defaultCommitEscalateReminderEvery = 3
	// defaultCommitMaxIterations is when committing is aborted, absent
	// "commit" agent config.
	defaultCommitMaxIterations = 15
)

// commitLimits holds when committing is first reminded to wrap up, how often
// that reminder escalates thereafter, and when committing is aborted.
type commitLimits struct {
	remindAfter   int
	escalateEvery int
	maxIterations int
}

func commitLimitsFor(dCtx DevContext) commitLimits {
	cfg := dCtx.RepoConfig.AgentConfig[common.CommitKey]

	limits := commitLimits{
		remindAfter:   defaultCommitRemindAfter,
		escalateEvery: defaultCommitEscalateReminderEvery,
		maxIterations: defaultCommitMaxIterations,
	}
	if cfg.RemindAfter > 0 {
		limits.remindAfter = cfg.RemindAfter
	}
	if cfg.EscalateReminderEvery > 0 {
		limits.escalateEvery = cfg.EscalateReminderEvery
	}
	if cfg.MaxIterations > 0 {
		limits.maxIterations = cfg.MaxIterations
	}
	return limits
}

func (l commitLimits) exceeded(iterations int) bool {
	return iterations >= l.maxIterations
}

// reminder nudges a long-running commit loop: a warning once remindAfter
// iterations have passed and a stronger nudge every escalateEvery iterations
// after that.
func (l commitLimits) reminder(iterations int) (llm.ChatMessage, bool) {
	switch {
	case iterations < l.remindAfter:
		return llm.ChatMessage{}, false
	case iterations == l.remindAfter:
		return commitSystemMessage(fmt.Sprintf(
			"SYSTEM MESSAGE: Committing is running long. Commit all remaining changes via %s, then call %s.",
			runCommandTool.Name, commitDoneTool.Name,
		)), true
	case l.escalateEvery > 0 && (iterations-l.remindAfter)%l.escalateEvery == 0:
		return commitSystemMessage(fmt.Sprintf(
			"SYSTEM MESSAGE: ⚠️‼️ Committing has run far too long and will soon be aborted. Commit everything NOW via %s and call %s.",
			runCommandTool.Name, commitDoneTool.Name,
		)), true
	}
	return llm.ChatMessage{}, false
}

func commitSystemMessage(content string) llm.ChatMessage {
	return llm.ChatMessage{
		Role:    llm.ChatMessageRoleSystem,
		Content: content,
	}
}

func renderCommitPrompt(state gitWorkingState) string {
	return fmt.Sprintf(`The work above is finished but not yet committed. Commit ALL pending changes now, leaving the working tree fully clean: nothing staged, modified or untracked.

Rules:
- Write every commit message in the Conventional Commits format: a "type(scope): subject" line (scope optional; types such as feat, fix, refactor, test, docs, build, ci, perf, style or chore), in the imperative mood and under 72 characters, optionally followed by a blank line and a short body explaining why.
- Make a single commit, or several when the changes form distinct logical units, grouping related files together.
- Use the %s tool to run git commands, e.g. "git add <paths>" and "git commit -m <message>". Never amend existing commits, rewrite history or push.
- If a change looks like it should not be committed at all, ask using %s instead of deleting it.
- Once everything is committed, call %s on its own.

# Current Git State

%s`, runCommandTool.Name, getHelpOrInputTool.Name, commitDoneTool.Name, state.Render())
}

// commitPendingWork has an LLM commit all uncommitted work with Conventional
// Commits messages, continuing from a copy of the given chat history so the
// model knows what the work was about. The given history is never mutated.
func commitPendingWork(dCtx DevContext, chatHistory *persisted_ai.ChatHistoryContainer) error {
	if chatHistory == nil {
		return fmt.Errorf("commit chat history is required")
	}

	state, err := collectGitWorkingState(dCtx)
	if err != nil {
		return err
	}
	if state.IsClean() {
		return nil
	}

	return RunSubflowWithoutResult(dCtx, "commit", "Commit Changes", func(_ domain.Subflow) error {
		err := commitPendingWorkSubflow(dCtx, chatHistory, state)
		if checkpointErr := checkpointSubflowGit(dCtx); checkpointErr != nil {
			return errors.Join(err, checkpointErr)
		}
		return err
	})
}

func commitPendingWorkSubflow(dCtx DevContext, chatHistory *persisted_ai.ChatHistoryContainer, state gitWorkingState) error {
	history := chatHistory.Clone()
	if err := AppendChatHistory(dCtx.ExecContext, &history, llm.ChatMessage{
		Role:         llm.ChatMessageRoleUser,
		Content:      renderCommitPrompt(state),
		CacheControl: "ephemeral",
	}); err != nil {
		return fmt.Errorf("failed to append commit instructions: %w", err)
	}

	initialModelConfig := commitModelConfig(dCtx)
	resolveModelConfig := func() common.ModelConfig {
		if ModelConfigRevision(dCtx) == 0 {
			return initialModelConfig
		}
		return commitModelConfig(dCtx)
	}
	resolveOptions := func() llm2.Options {
		return llm2.Options{
			ModelConfig: resolveModelConfig(),
			Tools:       []*llm.Tool{&runCommandTool, &getHelpOrInputTool, &commitDoneTool},
			ToolChoice:  llm.ToolChoice{Type: llm.ToolChoiceTypeRequired},
		}
	}

	limits := commitLimitsFor(dCtx)
	iterations := 0
	for {
		if limits.exceeded(iterations) {
			return fmt.Errorf("changes were still not fully committed after %d commit iterations", iterations)
		}

		userResponse, err := UserRequestIfPaused(dCtx, "Committing is paused. Would you like to provide any guidance?", nil)
		if err != nil {
			return fmt.Errorf("failed to check for pause while committing: %w", err)
		}
		if userResponse != nil && userResponse.Content != "" {
			if err := AppendChatHistory(dCtx.ExecContext, &history, llm.ChatMessage{
				Role:    llm.ChatMessageRoleUser,
				Content: renderGeneralFeedbackPrompt(userResponse.Content, FeedbackTypePause),
			}); err != nil {
				return fmt.Errorf("failed to append pause guidance: %w", err)
			}
		}

		response, err := TrackedToolChat(dCtx.WithCancelOnPause(), "commit", resolveOptions(), &history, resolveOptions)
		if err != nil {
			if dCtx.GlobalState != nil && dCtx.GlobalState.Paused {
				continue
			}
			return fmt.Errorf("failed to generate commits: %w", err)
		}
		iterations++

		message := response.GetMessage()
		if err := AppendChatHistory(dCtx.ExecContext, &history, message); err != nil {
			return fmt.Errorf("failed to append commit response: %w", err)
		}

		toolCalls := message.GetToolCalls()
		committed := false
		customHandlers := map[string]func(DevContext, llm.ToolCall) (llm2.ToolResultBlock, error){
			commitDoneTool.Name: func(dCtx DevContext, toolCall llm.ToolCall) (llm2.ToolResultBlock, error) {
				result := llm2.ToolResultBlock{Name: toolCall.Name, ToolCallId: toolCall.Id}
				// running alongside other tools could check the tree before
				// their git commands finish
				if len(toolCalls) > 1 {
					result.IsError = true
					result.Content = llm2.TextContentBlocks(fmt.Sprintf("The %s tool must be called on its own, without any other tool calls in the same response.", commitDoneTool.Name))
					return result, nil
				}
				current, err := collectGitWorkingState(dCtx)
				if err != nil {
					return result, err
				}
				if !current.IsClean() {
					result.IsError = true
					result.Content = llm2.TextContentBlocks(fmt.Sprintf("The working tree is not clean yet. Keep committing until nothing is staged, modified or untracked, then call %s again.\n\n%s", commitDoneTool.Name, current.Render()))
					return result, nil
				}
				committed = true
				result.Content = llm2.TextContentBlocks("All changes are committed.")
				return result, nil
			},
		}
		if len(toolCalls) > 0 {
			if _, err := handleToolCalls(dCtx, toolCalls, &history, customHandlers); err != nil {
				return fmt.Errorf("failed to handle commit tool calls: %w", err)
			}
		}
		if committed {
			return nil
		}

		if len(toolCalls) == 0 {
			if err := AppendChatHistory(dCtx.ExecContext, &history, commitSystemMessage(fmt.Sprintf(
				"SYSTEM MESSAGE: Prose is not seen by anyone here. Use %s to commit the changes, %s to ask the user, or %s once everything is committed.",
				runCommandTool.Name, getHelpOrInputTool.Name, commitDoneTool.Name,
			))); err != nil {
				return fmt.Errorf("failed to append commit tool reminder: %w", err)
			}
		}
		if reminder, ok := limits.reminder(iterations); ok {
			if err := AppendChatHistory(dCtx.ExecContext, &history, reminder); err != nil {
				return fmt.Errorf("failed to append commit iteration reminder: %w", err)
			}
		}
	}
}
