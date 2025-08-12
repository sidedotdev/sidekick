package dev

import (
	_ "embed"
	"errors"
	"fmt"
	"sidekick/coding/git"
	"sidekick/coding/tree_sitter"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/llm"
	"sidekick/srv"
	"sidekick/utils"
	"sort"
	"strings"

	"go.temporal.io/sdk/workflow"
)

const SignaturesEditHint = `!! Important note about shrunk context:
In order to edit code for which we only show extracted code signatures, you must
retrieve code context using a tool, to see the actual code you're editing first.
Note that the reason the code was shrunk is because the code that was requested
was too long. When you retrieve code context, only request the specific symbols
you really need to see/edit based on the signatures listed. If you request just
one symbol and it's still too long, you can try utilizing search or reading
specific lines directly instead.`

var ErrMaxAttemptsReached = fmt.Errorf("reached max attempts")

// edits code in the envContainer based on code context + requirements
func EditCode(dCtx DevContext, codingModelConfig common.ModelConfig, contextSizeExtension int, chatHistory *[]llm.ChatMessage, promptInfo PromptInfo) error {
	return RunSubflowWithoutResult(dCtx, "edit_code", "Edit Code", func(_ domain.Subflow) error {
		return editCodeSubflow(dCtx, codingModelConfig, contextSizeExtension, chatHistory, promptInfo)
	})
}

func editCodeSubflow(dCtx DevContext, codingModelConfig common.ModelConfig, contextSizeExtension int, chatHistory *[]llm.ChatMessage, promptInfo PromptInfo) error {
	var err error
	var editBlocks []EditBlock
	var reports []ApplyEditBlockReport

	// TODO return info that could help redefine requirements if issues are
	// discovered while editing code. It should indicate if edits
	// were made or not, and what feedback there may be for adjusting or
	// gathering requirements
	attemptCount := 0
	attemptsSinceLastFeedback := 0
	maxAttempts := 17
	repoConfig := dCtx.RepoConfig
	if repoConfig.MaxIterations > 0 {
		maxAttempts = repoConfig.MaxIterations
	}
	maxIterationsBeforeFeedback := 3

editLoop:
	for {
		// Handle user request to go to the next step, if versioned feature is active.
		version := workflow.GetVersion(dCtx, "user-action-go-next", workflow.DefaultVersion, 1)
		if version == 1 {
			action := dCtx.GlobalState.GetPendingUserAction()
			if action != nil {
				return PendingActionError
			}
		}

		// pause checkpoint
		if response, err := UserRequestIfPaused(dCtx, "Paused. Provide some guidance to continue:", nil); err != nil {
			return fmt.Errorf("failed to make user request when paused: %v", err)
		} else if response != nil {
			promptInfo = FeedbackInfo{Feedback: fmt.Sprintf("-- PAUSED --\n\nIMPORTANT: The user paused and provided the following guidance:\n\n%s", response.Content)}
			attemptsSinceLastFeedback = 0
		}

		v := workflow.GetVersion(dCtx, "no-max-unless-disabled-human", workflow.DefaultVersion, 1)
		if attemptCount >= maxAttempts && (v == 0 || dCtx.RepoConfig.DisableHumanInTheLoop) {
			return ErrMaxAttemptsReached
		}

		// Only request feedback if we haven't received any recently from any source
		if attemptCount > 0 && attemptsSinceLastFeedback >= maxIterationsBeforeFeedback {
			guidanceContext := "The system has attempted to edit the code multiple times without success. Please provide some guidance."
			requestParams := map[string]any{
				"editBlockReports": reports,
			}
			promptInfo, err = GetUserFeedback(dCtx, promptInfo, guidanceContext, chatHistory, requestParams)
			if err != nil {
				return fmt.Errorf("failed to get user feedback: %v", err)
			}
			attemptsSinceLastFeedback = 0
		}

		maxLength := min(defaultMaxChatHistoryLength+contextSizeExtension, extendedMaxChatHistoryLength)
		ManageChatHistory(dCtx, chatHistory, maxLength)

		// Step 1: Get a list of *edit blocks* from the LLM
		editBlocks, err = authorEditBlocks(dCtx, codingModelConfig, contextSizeExtension, chatHistory, promptInfo)
		if err != nil && !errors.Is(err, PendingActionError) {
			// The err is likely when extracting edit blocks
			// TODO if the failure was something else, eg openai rate limit, then don't feedback like this
			feedback := fmt.Sprintf("Please write out all the *edit blocks* again and ensure we follow the format, as we encountered this error when processing them: %v", err)
			promptInfo = FeedbackInfo{Feedback: feedback}
			attemptCount++
			continue
		}
		if version == 1 {
			action := dCtx.GlobalState.GetPendingUserAction()
			if action != nil {
				return PendingActionError
			}
		}

		// Step 2: Try to apply all the edit blocks, reverting on check failures
		reports, err = validateAndApplyEditBlocks(dCtx, editBlocks)
		if err != nil {
			feedback := fmt.Sprintf("Error encountered during ApplyEditBlockActivity. Error: %v", err)
			promptInfo = FeedbackInfo{Feedback: feedback}
			attemptCount++
		} else {
			v := workflow.GetVersion(dCtx, "edit_code_diff", workflow.DefaultVersion, 1)
			diff_enabled := false // TODO remove this later
			if v == 1 && diff_enabled {
				anyApplied := false
				for _, report := range reports {
					if report.DidApply {
						anyApplied = true
					}
				}

				if anyApplied {
					// emit event with the git diff after any successful edits
					diff, err := git.GitDiff(dCtx.ExecContext)
					if err != nil {
						return fmt.Errorf("failed to get git diff after edits: %v", err)
					}

					subflow := dCtx.FlowScope.Subflow
					err = workflow.ExecuteActivity(dCtx, srv.Activities.AddFlowEvent, dCtx.WorkspaceId, subflow.FlowId, domain.CodeDiffEvent{
						EventType: domain.CodeDiffEventType,
						SubflowId: subflow.Id,
						Diff:      diff,
					}).Get(dCtx, nil)
					if err != nil {
						return fmt.Errorf("failed to emit code diff event: %v", err)
					}
				}
			}

			// Construct a message from the reports and add it to the chat
			// history, so that the LLM can see what happened
			reportMessage := feedbackFromApplyEditBlockReports(reports)
			for _, report := range reports {
				if !report.DidApply {
					// if any edit blocks failed to apply, loop back to authoring edit blocks
					// TODO if most succeeded, it might be better to continue to
					// the next step and let tests/critique guide this.
					// alternatively, we could do a special subflow to repair
					// only the broken edit blocks with more targeted prompting
					promptInfo = FeedbackInfo{Feedback: reportMessage}
					attemptCount++
					continue editLoop
				}
			}

			// no errors, but want to retain the system message in this case as
			// well. in the error case, we use the system message as the
			// feedback and get it into chat history that way
			*chatHistory = append(*chatHistory, llm.ChatMessage{
				Role:    "system",
				Content: reportMessage,
			})

			break
		}
	}

	return nil
}

func authorEditBlocks(dCtx DevContext, codingModelConfig common.ModelConfig, contextSizeExtension int, chatHistory *[]llm.ChatMessage, promptInfo PromptInfo) ([]EditBlock, error) {
	var extractedEditBlocks []EditBlock

	attemptCount := 0
	attemptsSinceLastEditBlockOrFeedback := 0
	maxAttempts := 7 // Default value

	repoConfig := dCtx.RepoConfig
	if repoConfig.MaxIterations > 0 {
		maxAttempts = repoConfig.MaxIterations
	}

	feedbackIterations := 3
	v := workflow.GetVersion(dCtx, "author-edit-feedback-iterations", workflow.DefaultVersion, 1)
	if v == 1 {
		// TODO when tool calls are not finding things automatically, provide
		// better hints for how to find things after Nth iteration, before going
		// to human-based support. Eg fuzzy search or embedding search etc.
		// Maybe provide that as a tool or even run that tool automatically.
		feedbackIterations = 6
	}

	for {
		// Check for UserActionGoNext and version to potentially skip this step
		version := workflow.GetVersion(dCtx, "user-action-go-next", workflow.DefaultVersion, 1)
		if version == 1 {
			action := dCtx.GlobalState.GetPendingUserAction()
			if action != nil && *action == UserActionGoNext {
				// If UserActionGoNext is pending and version is new, skip authoring edit blocks.
				// The action is not consumed here; it will be consumed in completeDevStepSubflow.
				return nil, nil
			}
		}

		// pause checkpoint
		if response, err := UserRequestIfPaused(dCtx, "Paused. Provide some guidance to continue:", nil); err != nil {
			return nil, fmt.Errorf("failed to make user request when paused: %v", err)
		} else if response != nil && response.Content != "" {
			promptInfo = FeedbackInfo{Feedback: fmt.Sprintf("-- PAUSED --\n\nIMPORTANT: The user paused and provided the following guidance:\n\n%s", response.Content)}
			attemptsSinceLastEditBlockOrFeedback = 0
		}

		if attemptCount >= maxAttempts {
			if len(extractedEditBlocks) > 0 {
				// make use of the results so far, given there are some that are
				// not yet applied: it may be sufficient
				return extractedEditBlocks, nil
			}

			// HACK: since we don't add tool call responses right away (TODO),
			// we make sure we don't end up with a tool call missing a result
			// here.
			switch info := promptInfo.(type) {
			case ToolCallResponseInfo:
				addToolCallResponse(chatHistory, info)
			}

			return nil, ErrMaxAttemptsReached
		} else if attemptsSinceLastEditBlockOrFeedback > 0 && attemptsSinceLastEditBlockOrFeedback%feedbackIterations == 0 {
			guidanceContext := "The system has attempted to generate edits multiple times without success. Please provide some guidance."
			requestParams := map[string]any{
				// TODO include the latest failure if any
			}
			var err error
			promptInfo, err = GetUserFeedback(dCtx, promptInfo, guidanceContext, chatHistory, requestParams)
			if err != nil {
				return nil, fmt.Errorf("failed to get user feedback: %v", err)
			}
			attemptsSinceLastEditBlockOrFeedback = 0
		}

		// NOTE: this also ensures the tool call response is added to chat history
		authorEditBlockInput := buildAuthorEditBlockInput(dCtx, codingModelConfig, chatHistory, promptInfo)
		maxLength := min(defaultMaxChatHistoryLength+contextSizeExtension, extendedMaxChatHistoryLength)

		// NOTE this MUST be below authorEditBlockInput to ensure tool call
		// responses are retained and we keep enough history.
		// TODO when switching to the LlmLoop-style approach of adding tool
		// calls immediately, we'll need a way to support this "burst"
		// functionality (or maybe the ManageChatHistoryV2 function will
		// natively always support burst due to the markers, hmmm...)
		ManageChatHistory(dCtx, chatHistory, maxLength)

		if len(extractedEditBlocks) > 0 {
			content := fmt.Sprintf("Note: %d edit block(s) are pending application.", len(extractedEditBlocks))
			*chatHistory = append(*chatHistory, llm.ChatMessage{
				Role:    llm.ChatMessageRoleSystem,
				Content: content,
			})
		}

		// Increment counters before making the call
		attemptCount++
		attemptsSinceLastEditBlockOrFeedback++

		// call Open AI to get back messages that contain edit blocks
		chatCtx := dCtx.WithCancelOnPause()
		chatResponse, err := TrackedToolChat(chatCtx, "code_edits", authorEditBlockInput)
		if dCtx.GlobalState.Paused {
			continue // UserRequestIfPaused will handle the pause
		}
		if err != nil {
			return []EditBlock{}, err
		}
		*chatHistory = append(*chatHistory, chatResponse.ChatMessage)

		visibleChatHistory := *chatHistory
		if v := workflow.GetVersion(dCtx, "bugfix-edit-block-visibility-orig-history", workflow.DefaultVersion, 1); v == 1 {
			// use history that is actually passed to the LLM, before
			// ManageChatHistory was called, since that corresponds to what was
			// actually visible to the LLM at the time edit blocks were
			// generated
			visibleChatHistory = authorEditBlockInput.Params.Messages
		}
		currentExtractedBlocks, err := ExtractEditBlocksWithVisibility(chatResponse.ChatMessage.Content, visibleChatHistory)
		if err != nil {
			return []EditBlock{}, fmt.Errorf("failed to extract edit blocks: %v", err)
		}
		if len(currentExtractedBlocks) > 0 {
			attemptsSinceLastEditBlockOrFeedback = 0
		}
		extractedEditBlocks = append(extractedEditBlocks, currentExtractedBlocks...)

		if len(chatResponse.ToolCalls) > 0 && chatResponse.ToolCalls[0].Name != "" {
			toolCallResponseInfo, err := handleToolCall(dCtx, chatResponse.ToolCalls[0])
			// Reset feedback counter if this was a getHelpOrInput response
			if chatResponse.ToolCalls[0].Name == getHelpOrInputTool.Name {
				attemptsSinceLastEditBlockOrFeedback = 0
			}
			// dynamically adjust the context size extension based on the length of the response
			if len(toolCallResponseInfo.Response) > 5000 {
				contextSizeExtension += len(toolCallResponseInfo.Response) - 5000
			}
			// TODO: addToolCallResponse(chatHistory, toolCallResponseInfo)
			// 		promptInfo = SkipInfo{} // TODO remove after using addX functions everywhere
			promptInfo = toolCallResponseInfo
			if err != nil {
				// need a tool call response always after a tool call, so we append it here before returning
				*chatHistory = append(*chatHistory, llm.ChatMessage{
					Role:       llm.ChatMessageRoleTool,
					ToolCallId: chatResponse.ToolCalls[0].Id,
					Name:       chatResponse.ToolCalls[0].Name,
					IsError:    true,
					Content:    err.Error(),
				})
				return nil, err
			}
		} else {
			// we use the fact that no tool call happened to infer that we're
			// done with this loop
			break
		}
	}

	return extractedEditBlocks, nil
}

// TODO move to a coding-related package, eg coding/edit_block
func buildAuthorEditBlockInput(dCtx DevContext, codingModelConfig common.ModelConfig, chatHistory *[]llm.ChatMessage, promptInfo PromptInfo) llm.ToolChatOptions {
	// TODO extract chat message building into a separate function
	var content string
	role := llm.ChatMessageRoleUser
	name := ""
	toolCallId := ""
	skip := false
	isError := false
	cacheControl := ""
	contextType := ""
	switch info := promptInfo.(type) {
	case InitialCodeInfo:
		content = renderAuthorEditBlockInitialPrompt(dCtx, info.CodeContext, info.Requirements)
		cacheControl = "ephemeral"
		contextType = ContextTypeInitialInstructions
	case InitialDevStepInfo:
		content = renderAuthorEditBlockInitialDevStepPrompt(dCtx, info.CodeContext, info.Requirements, info.PlanExecution.String(), info.Step.Definition)
		cacheControl = "ephemeral"
		contextType = ContextTypeInitialInstructions
	case SkipInfo:
		skip = true
	case FeedbackInfo:
		content = renderAuthorEditBlockFeedbackPrompt(info.Feedback)
	case ToolCallResponseInfo:
		role = llm.ChatMessageRoleTool
		content = info.Response
		name = info.FunctionName
		toolCallId = info.ToolCallId
		isError = info.IsError
	default:
		panic("Unsupported prompt type for authoring edit blocks: " + promptInfo.GetType())
	}

	if !skip {
		newMessage := llm.ChatMessage{
			Role:         role,
			Content:      content,
			Name:         name,
			ToolCallId:   toolCallId,
			CacheControl: cacheControl,
			IsError:      isError,
			ContextType:  contextType,
		}
		// FIXME don't mutate chatHistory here, let the caller do it if they want it
		*chatHistory = append(*chatHistory, newMessage)
	}

	var tools []*llm.Tool
	tools = append(tools, &bulkSearchRepositoryTool)
	tools = append(tools, getRetrieveCodeContextTool())
	tools = append(tools, &bulkReadFileTool)
	tools = append(tools, &runCommandTool)

	if !dCtx.RepoConfig.DisableHumanInTheLoop {
		tools = append(tools, &getHelpOrInputTool)
	}

	var temperature float32 = 0.0
	return llm.ToolChatOptions{
		Secrets: *dCtx.Secrets,
		Params: llm.ToolChatParams{
			Messages: *chatHistory,
			Tools:    tools,
			ToolChoice: llm.ToolChoice{
				Type: llm.ToolChoiceTypeAuto,
			},
			Temperature: &temperature,
			ModelConfig: codingModelConfig,
		},
	}
}

// we use these variable names so that code extracting edit blocks and merge conflict
// detectors don't get confused
// TODO move to a separate package's constants, eg coding/edit_block/constants.go
const search = "<<<<<<< SEARCH_EXACT"
const divider = "======="
const replace = ">>>>>>> REPLACE_EXACT"

const startInitialCodeContext = "#START INITIAL CODE CONTEXT"
const endInitialCodeContext = "#END INITIAL CODE CONTEXT"

func renderAuthorEditBlockInitialPrompt(dCtx DevContext, codeContext, requirements string) string {
	data := map[string]interface{}{
		"codeContext":                     codeContext,
		"requirements":                    requirements,
		"startInitialCodeContext":         startInitialCodeContext,
		"endInitialCodeContext":           endInitialCodeContext,
		"summaryStart":                    summaryStart,
		"summaryEnd":                      summaryEnd,
		"search":                          search,
		"divider":                         divider,
		"replace":                         replace,
		"editCodeHints":                   dCtx.RepoConfig.EditCode.Hints,
		"retrieveCodeContextFunctionName": getRetrieveCodeContextTool().Name,
	}
	if !dCtx.RepoConfig.DisableHumanInTheLoop {
		data["getHelpOrInputFunctionName"] = getHelpOrInputTool.Name
	}
	return RenderPrompt(AuthorEditBlockInitial, data)
}

func renderAuthorEditBlockInitialDevStepPrompt(dCtx DevContext, codeContext, requirements, planContext, currentStep string) string {
	data := map[string]interface{}{
		"codeContext":                     codeContext,
		"requirements":                    requirements,
		"planContext":                     planContext,
		"currentStep":                     currentStep,
		"startInitialCodeContext":         startInitialCodeContext,
		"endInitialCodeContext":           endInitialCodeContext,
		"summaryStart":                    summaryStart,
		"summaryEnd":                      summaryEnd,
		"search":                          search,
		"divider":                         divider,
		"replace":                         replace,
		"editCodeHints":                   dCtx.RepoConfig.EditCode.Hints,
		"retrieveCodeContextFunctionName": getRetrieveCodeContextTool().Name,
	}
	if !dCtx.RepoConfig.DisableHumanInTheLoop {
		data["getHelpOrInputFunctionName"] = getHelpOrInputTool.Name
	}

	return RenderPrompt(AuthorEditBlockInitialWithPlan, data)
}

// renderAuthorEditBlockFeedbackPrompt formats the author edit block feedback
// into a prompt specialized for fixing issues in applying the edit block
// TODO only provide the first hint if the feedback is about applying edit
// blocks and if we know that this case occurred (partial edit block failure).
// TODO only provide the second hint if the feedback is about tests failing.
// TODO only provide hint 3 if feedback includes test results
// TODO only provide hint 4 if we see that pattern like path/to/file.extension:10:5
func renderAuthorEditBlockFeedbackPrompt(feedback string) string {
	data := map[string]interface{}{
		"feedback":                         feedback,
		"hasUserGuidance":                  strings.Contains(feedback, guidanceStart),
		"retrieveCodeContextFunctionName":  getRetrieveCodeContextTool().Name,
		"bulkSearchRepositoryFunctionName": bulkSearchRepositoryTool.Name,
		"bulkReadFileFunctionName":         bulkReadFileTool.Name,
	}
	return RenderPrompt(AuthorEditBlockFeedback, data)
}

// feedbackFromApplyEditBlockReports creates a system message summarizing the results from the reports
// TODO include information about any new structs or functions that were created (maybe deleted too?)
// TODO maybe force a summary of the edit when the edit block is created by the
// LLM, and include that summary here so we know what the edit was
func feedbackFromApplyEditBlockReports(reports []ApplyEditBlockReport) string {
	var messages []string
	messages = append(messages, "Edit block application results:\n")
	for _, report := range reports {
		seqNum := report.OriginalEditBlock.SequenceNumber
		if report.Error != "" {
			messages = append(messages, fmt.Sprintf("- edit_block:%d application failed: %s", seqNum, report.Error))
		} else if !report.DidApply {
			messages = append(messages, fmt.Sprintf("- edit_block:%d application failed due to unknown reasons", seqNum))
		} else {
			messages = append(messages, fmt.Sprintf("- edit_block:%d application succeeded", seqNum))
		}
	}
	return strings.Join(messages, "\n")
}

func codeBlocksToMergedFileRanges(filePath string, visibleCodeBlocks []tree_sitter.CodeBlock) []FileRange {
	visibleFileRanges := utils.Map(visibleCodeBlocks, func(cb tree_sitter.CodeBlock) FileRange {
		return FileRange{FilePath: cb.FilePath, StartLine: cb.StartLine, EndLine: cb.EndLine}
	})

	return mergedRangesForFile(filePath, visibleFileRanges)
}

func mergedRangesForFile(filePath string, visibleFileRanges []FileRange) []FileRange {
	relevantRanges := utils.Filter(visibleFileRanges, func(r FileRange) bool {
		return r.FilePath == filePath
	})

	// merge overlapping or adjacent ranges
	sort.Slice(relevantRanges, func(i, j int) bool {
		return relevantRanges[i].StartLine < relevantRanges[j].StartLine
	})
	mergedRanges := []FileRange{}
	// TODO /gen if the file content is empty between nearly-adjacent ranges (eg
	// one or more lines in between the ranges that are all whitespace or
	// comments only), also merge those. This requires reading the original
	// file, and thus requires returning an error or panicking.
	for _, r := range relevantRanges {
		if len(mergedRanges) == 0 {
			mergedRanges = append(mergedRanges, r)
			continue
		}
		lastRange := &mergedRanges[len(mergedRanges)-1]
		if lastRange.EndLine >= r.StartLine-1 {
			lastRange.EndLine = max(lastRange.EndLine, r.EndLine)
		} else {
			mergedRanges = append(mergedRanges, r)
		}
	}
	return mergedRanges
}
