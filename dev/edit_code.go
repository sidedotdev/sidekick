package dev

import (
	_ "embed"
	"errors"
	"fmt"
	"regexp"
	"sidekick/coding/git"
	"sidekick/coding/tree_sitter"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/llm2"
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
var ErrExtractEditBlocks = fmt.Errorf("failed to extract edit blocks")

// edits code in the envContainer based on code context + requirements
func EditCode(dCtx DevContext, codingModelConfig common.ModelConfig, contextSizeExtension int, chatHistory *common.ChatHistoryContainer, promptInfo PromptInfo) error {
	return RunSubflowWithoutResult(dCtx, "edit_code", "Edit Code", func(_ domain.Subflow) error {
		return editCodeSubflow(dCtx, codingModelConfig, contextSizeExtension, chatHistory, promptInfo)
	})
}

type applyEditBlocksResult struct {
	ReportMessage string
	AllApplied    bool
	Reports       []ApplyEditBlockReport
}

func applyEditBlocksAndReport(dCtx DevContext, editBlocks []EditBlock) (applyEditBlocksResult, error) {
	reports, err := validateAndApplyEditBlocks(dCtx, editBlocks)
	if err != nil {
		return applyEditBlocksResult{}, err
	}

	v := workflow.GetVersion(dCtx, "edit_code_diff", workflow.DefaultVersion, 1)
	diffEnabled := false // TODO remove this later
	if v == 1 && diffEnabled {
		anyApplied := false
		for _, report := range reports {
			if report.DidApply {
				anyApplied = true
				break
			}
		}

		if anyApplied {
			diff, err := git.GitDiff(dCtx.ExecContext)
			if err != nil {
				return applyEditBlocksResult{}, fmt.Errorf("failed to get git diff after edits: %v", err)
			}

			subflow := dCtx.FlowScope.Subflow
			err = workflow.ExecuteActivity(dCtx, srv.Activities.AddFlowEvent, dCtx.WorkspaceId, subflow.FlowId, domain.CodeDiffEvent{
				EventType: domain.CodeDiffEventType,
				SubflowId: subflow.Id,
				Diff:      diff,
			}).Get(dCtx, nil)
			if err != nil {
				return applyEditBlocksResult{}, fmt.Errorf("failed to emit code diff event: %v", err)
			}
		}
	}

	reportMessage := feedbackFromApplyEditBlockReports(reports)
	allApplied := true
	for _, report := range reports {
		if !report.DidApply {
			allApplied = false
			break
		}
	}

	return applyEditBlocksResult{
		ReportMessage: reportMessage,
		AllApplied:    allApplied,
		Reports:       reports,
	}, nil
}

func editCodeSubflow(dCtx DevContext, codingModelConfig common.ModelConfig, contextSizeExtension int, chatHistory *common.ChatHistoryContainer, promptInfo PromptInfo) error {
	var err error
	var editBlocks []EditBlock

	// TODO return info that could help redefine requirements if issues are
	// discovered while editing code. It should indicate if edits
	// were made or not, and what feedback there may be for adjusting or
	// gathering requirements
	attemptCount := 0
	maxAttempts := 17
	repoConfig := dCtx.RepoConfig
	if repoConfig.MaxIterations > 0 {
		maxAttempts = repoConfig.MaxIterations
	}

editLoop:
	for {
		// Handle user request to go to the next step, if versioned feature is active.
		goNextVersion := workflow.GetVersion(dCtx, "user-action-go-next", workflow.DefaultVersion, 1)
		if goNextVersion == 1 {
			action := dCtx.ExecContext.GlobalState.GetPendingUserAction()
			if action != nil {
				return flow_action.PendingActionError
			}
		}

		// pause checkpoint
		if response, err := UserRequestIfPaused(dCtx, "Paused. Provide some guidance to continue:", nil); err != nil {
			return fmt.Errorf("failed to make user request when paused: %v", err)
		} else if response != nil {
			promptInfo = FeedbackInfo{Feedback: response.Content, Type: FeedbackTypePause}
		}

		v := workflow.GetVersion(dCtx, "no-max-unless-disabled-human", workflow.DefaultVersion, 1)
		if attemptCount >= maxAttempts && (v < 1 || dCtx.RepoConfig.DisableHumanInTheLoop) {
			return ErrMaxAttemptsReached
		}

		maxLength := min(defaultMaxChatHistoryLength+contextSizeExtension, extendedMaxChatHistoryLength)
		ManageChatHistory(dCtx, chatHistory, dCtx.WorkspaceId, maxLength)

		// Step 1: Get a list of *edit blocks* from the LLM
		editBlocks, err = authorEditBlocks(dCtx, codingModelConfig, contextSizeExtension, chatHistory, promptInfo)
		if err != nil && !errors.Is(err, flow_action.PendingActionError) {
			v := workflow.GetVersion(dCtx, "edit-code-max-attempts-bugfix", workflow.DefaultVersion, 1)
			isMaxAttempts := errors.Is(err, ErrMaxAttemptsReached)
			if v < 0 || !isMaxAttempts {
				if errors.Is(err, ErrExtractEditBlocks) {
					feedback := fmt.Sprintf("Please write out all the *edit blocks* again and ensure we follow the format, as we encountered this error when processing them: %v", err)
					promptInfo = FeedbackInfo{Feedback: feedback, Type: FeedbackTypeEditBlockError}
					attemptCount++
					continue
				}

				return err
			}
		}
		if goNextVersion == 1 {
			action := dCtx.ExecContext.GlobalState.GetPendingUserAction()
			if action != nil {
				return flow_action.PendingActionError
			}
		}

		// Step 2: Try to apply all the edit blocks, reverting on check failures
		v = workflow.GetVersion(dCtx, "apply-edit-blocks-immediately", workflow.DefaultVersion, 1)
		applyImmediately := v >= 1 && !dCtx.RepoConfig.DisableHumanInTheLoop
		if applyImmediately {
			// when applying immediately, edit blocks were already successfully
			// applied in authorEditBlocks, unless that returned
			// ErrMaxAttemptsReached. In either case we break here, which is
			// equivalent to the non-immediate case.
			break
		}

		result, err := applyEditBlocksAndReport(dCtx, editBlocks)
		if err != nil {
			feedback := fmt.Sprintf("Error while applying edit blocks: %v", err)
			promptInfo = FeedbackInfo{Feedback: feedback, Type: FeedbackTypeSystemError}
			attemptCount++
		} else if !result.AllApplied {
			// if any edit blocks failed to apply, loop back to authoring edit blocks
			// TODO if most succeeded, it might be better to continue to
			// the next step and let tests/critique guide this.
			// alternatively, we could do a special subflow to repair
			// only the broken edit blocks with more targeted prompting
			promptInfo = FeedbackInfo{Feedback: result.ReportMessage, Type: FeedbackTypeApplyError}
			attemptCount++
			continue editLoop
		} else {
			// no errors, but want to retain the system message in this case as
			// well. in the error case, we use the system message as the
			// feedback and get it into chat history that way
			chatHistory.Append(llm.ChatMessage{
				Role:        "system",
				Content:     result.ReportMessage,
				ContextType: ContextTypeEditBlockReport,
			})

			break
		}
	}

	return nil
}

func authorEditBlocks(dCtx DevContext, codingModelConfig common.ModelConfig, contextSizeExtension int, chatHistory *common.ChatHistoryContainer, promptInfo PromptInfo) ([]EditBlock, error) {
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
	if cfg, ok := dCtx.RepoConfig.AgentConfig[common.CodingKey]; ok && cfg.AutoIterations > 0 {
		feedbackIterations = cfg.AutoIterations
	}

	for {
		// Check for UserActionGoNext and version to potentially skip this step
		version := workflow.GetVersion(dCtx, "user-action-go-next", workflow.DefaultVersion, 1)
		if version == 1 {
			action := dCtx.ExecContext.GlobalState.GetPendingUserAction()
			if action != nil && *action == flow_action.UserActionGoNext {
				// If UserActionGoNext is pending and version is new, skip authoring edit blocks.
				// The action is not consumed here; it will be consumed in completeDevStepSubflow.
				return nil, flow_action.PendingActionError
			}
		}

		// pause checkpoint
		if response, err := UserRequestIfPaused(dCtx, "Paused. Provide some guidance to continue:", nil); err != nil {
			return nil, fmt.Errorf("failed to make user request when paused: %v", err)
		} else if response != nil && response.Content != "" {
			// If promptInfo is an initial type, add it to chat history first before
			// overwriting with pause feedback. This ensures the initial instructions
			// are in the history before the pause feedback.
			switch promptInfo.(type) {
			case InitialCodeInfo, InitialDevStepInfo:
				buildAuthorEditBlockInput(dCtx, codingModelConfig, chatHistory, promptInfo)
			}
			promptInfo = FeedbackInfo{Feedback: response.Content, Type: FeedbackTypePause}
			attemptsSinceLastEditBlockOrFeedback = 0
		}

		// Inject proactive system message based on tool-call thresholds
		if msg, ok := ThresholdMessageForCounter(feedbackIterations, attemptsSinceLastEditBlockOrFeedback); ok {
			//*chatHistory = append(*chatHistory, llm.ChatMessage{
			//	Role:    "system",
			//	Content: msg,
			//})
			promptInfo = FeedbackInfo{Feedback: msg, Type: FeedbackTypeSystemError}
		}

		v := workflow.GetVersion(dCtx, "apply-edit-blocks-immediately", workflow.DefaultVersion, 1)
		applyImmediately := v >= 1 && !dCtx.RepoConfig.DisableHumanInTheLoop

		maxAttemptsVersion := workflow.GetVersion(dCtx, "author-edit-no-max-unless-disabled-human", workflow.DefaultVersion, 1)
		if attemptCount >= maxAttempts && (maxAttemptsVersion < 1 || dCtx.RepoConfig.DisableHumanInTheLoop) {
			if !applyImmediately && len(extractedEditBlocks) > 0 {
				// make use of the results so far, given there are some that are
				// not yet applied: it may be sufficient
				return extractedEditBlocks, nil
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
		authorEditBlockInput, visibleChatHistory := buildAuthorEditBlockInput(dCtx, codingModelConfig, chatHistory, promptInfo)
		maxLength := min(defaultMaxChatHistoryLength+contextSizeExtension, extendedMaxChatHistoryLength)

		// NOTE this MUST be below authorEditBlockInput to ensure tool call
		// responses are retained and we keep enough history.
		// TODO when switching to the LlmLoop-style approach of adding tool
		// calls immediately, we'll need a way to support this "burst"
		// functionality (or maybe the ManageChatHistoryV2 function will
		// natively always support burst due to the markers, hmmm...)
		ManageChatHistory(dCtx, chatHistory, dCtx.WorkspaceId, maxLength)

		if !applyImmediately && len(extractedEditBlocks) > 0 {
			content := fmt.Sprintf("Note: %d edit block(s) are pending application.", len(extractedEditBlocks))
			chatHistory.Append(llm.ChatMessage{
				Role:    llm.ChatMessageRoleSystem,
				Content: content,
			})
		}

		// Increment counters before making the call
		attemptCount++
		attemptsSinceLastEditBlockOrFeedback++

		// call Open AI to get back messages that contain edit blocks
		chatCtx := dCtx.WithCancelOnPause()
		authorEditBlockInput.Params.ChatHistory = chatHistory
		chatResponse, err := TrackedToolChat(chatCtx, "code_edits", authorEditBlockInput)
		if dCtx.GlobalState.Paused {
			continue // UserRequestIfPaused will handle the pause
		}
		if err != nil {
			return []EditBlock{}, err
		}
		chatHistory.Append(chatResponse.GetMessage())

		// visibleChatHistory is captured before ManageChatHistory to reflect
		// what was actually visible to the LLM when edit blocks were generated

		chatHistory.Append(chatResponse.GetMessage())
		if v := workflow.GetVersion(dCtx, "bugfix-edit-block-visibility-orig-history", workflow.DefaultVersion, 1); v == 1 {
			// this maintains the buggy behavior on older workflows to still replay them
			visibleChatHistory = append(visibleChatHistory, chatResponse.GetMessage().(llm.ChatMessage))
		}

		tildeOnly := workflow.GetVersion(dCtx, "tilde-edit-block-fence", workflow.DefaultVersion, 1) >= 1
		currentExtractedBlocks, err := ExtractEditBlocksWithVisibility(chatResponse.GetMessage().GetContentString(), visibleChatHistory, tildeOnly)
		if err != nil {
			return []EditBlock{}, fmt.Errorf("%w: %v", ErrExtractEditBlocks, err)
		}

		// Apply edit blocks immediately if enabled
		var applyEditBlocksError error
		var applyEditBlocksResult applyEditBlocksResult
		if len(currentExtractedBlocks) > 0 {
			attemptsSinceLastEditBlockOrFeedback = 0
			if applyImmediately {
				applyEditBlocksResult, applyEditBlocksError = applyEditBlocksAndReport(dCtx, currentExtractedBlocks)
			} else {
				extractedEditBlocks = append(extractedEditBlocks, currentExtractedBlocks...)
			}
		}

		var toolCallResponses []ToolCallResponseInfo
		if len(chatResponse.GetMessage().GetToolCalls()) > 0 {
			toolCallResponses = handleToolCalls(dCtx, chatResponse.GetMessage().GetToolCalls(), nil)
		}

		// Add tool call responses to history
		for _, toolCallResponseInfo := range toolCallResponses {
			// Reset feedback counter if this was a getHelpOrInput response
			if toolCallResponseInfo.FunctionName == getHelpOrInputTool.Name {
				attemptsSinceLastEditBlockOrFeedback = 0
			}
			// dynamically adjust the context size extension based on the length of the response
			if len(toolCallResponseInfo.Response) > 5000 {
				contextSizeExtension += len(toolCallResponseInfo.Response) - 5000
			}
			addToolCallResponse(chatHistory, toolCallResponseInfo)
		}

		// keep loop going if we failed to apply edit blocks, with feedback hints
		if len(currentExtractedBlocks) > 0 && applyImmediately {
			if applyEditBlocksError != nil {
				feedback := fmt.Sprintf("Error while applying edit blocks: %v", applyEditBlocksError)

				promptInfo = FeedbackInfo{Feedback: feedback, Type: FeedbackTypeSystemError}
				attemptCount++
				continue
			} else if !applyEditBlocksResult.AllApplied {
				promptInfo = FeedbackInfo{Feedback: applyEditBlocksResult.ReportMessage, Type: FeedbackTypeApplyError}
				attemptCount++
				continue
			} else {
				chatHistory.Append(llm.ChatMessage{
					Role:        llm.ChatMessageRoleSystem,
					Content:     applyEditBlocksResult.ReportMessage,
					ContextType: ContextTypeEditBlockReport,
				})
			}
		}

		if len(chatResponse.ToolCalls) > 0 {
			promptInfo = SkipInfo{}
		} else {
			// we use the fact that no tool call happened to
			// infer that we're done with this loop
			break
		}
	}

	return extractedEditBlocks, nil
}

// buildAuthorEditBlockInput builds the LLM options for authoring edit blocks.
// Returns the options and the visible messages (for edit block extraction).
func buildAuthorEditBlockInput(dCtx DevContext, codingModelConfig common.ModelConfig, chatHistory *common.ChatHistoryContainer, promptInfo PromptInfo) (llm2.Options, []llm.ChatMessage) {
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
		v := workflow.GetVersion(dCtx, "apply-edit-blocks-immediately", workflow.DefaultVersion, 1)
		applyImmediately := v >= 1 && !dCtx.RepoConfig.DisableHumanInTheLoop
		content = renderAuthorEditBlockInitialPrompt(dCtx, info.CodeContext, info.Requirements, applyImmediately)
		cacheControl = "ephemeral"
		contextType = ContextTypeInitialInstructions
	case InitialDevStepInfo:
		v := workflow.GetVersion(dCtx, "apply-edit-blocks-immediately", workflow.DefaultVersion, 1)
		applyImmediately := v >= 1 && !dCtx.RepoConfig.DisableHumanInTheLoop
		content = renderAuthorEditBlockInitialDevStepPrompt(dCtx, info.CodeContext, info.Requirements, info.PlanExecution.String(), info.Step.Definition, applyImmediately)
		cacheControl = "ephemeral"
		contextType = ContextTypeInitialInstructions
	case SkipInfo:
		skip = true
	case FeedbackInfo:
		content = renderAuthorEditBlockFeedbackPrompt(info.Feedback, info.Type)
		if info.Type == FeedbackTypeApplyError {
			contextType = ContextTypeEditBlockReport
		}
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
		chatHistory.Append(newMessage)
	}

	var tools []*llm.Tool
	tools = append(tools, &bulkSearchRepositoryTool)
	tools = append(tools, currentGetSymbolDefinitionsTool())
	tools = append(tools, &bulkReadFileTool)
	tools = append(tools, &runCommandTool)

	if !dCtx.RepoConfig.DisableHumanInTheLoop {
		tools = append(tools, &getHelpOrInputTool)
	}

	// Convert Messages() to []llm.ChatMessage for visibility tracking
	messages := chatHistory.Messages()
	chatMessages := make([]llm.ChatMessage, len(messages))
	for i, msg := range messages {
		chatMessages[i] = msg.(llm.ChatMessage)
	}

	options := llm2.Options{
		Secrets: *dCtx.Secrets,
		Params: llm2.Params{
			Tools: tools,
			ToolChoice: llm.ToolChoice{
				Type: llm.ToolChoiceTypeAuto,
			},
			ModelConfig: codingModelConfig,
		},
	}

	return options, chatMessages
}

// we use these variable names so that code extracting edit blocks and merge conflict
// detectors don't get confused
// TODO move to a separate package's constants, eg coding/edit_block/constants.go
const search = "<<<<<<< SEARCH_EXACT"
const divider = "======="
const replace = ">>>>>>> REPLACE_EXACT"

const startInitialCodeContext = "#START INITIAL CODE CONTEXT"
const endInitialCodeContext = "#END INITIAL CODE CONTEXT"

func renderAuthorEditBlockInitialPrompt(dCtx DevContext, codeContext, requirements string, applyEditBlocksImmediately bool) string {
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
		"retrieveCodeContextFunctionName": currentGetSymbolDefinitionsTool().Name,
		"applyEditBlocksImmediately":      applyEditBlocksImmediately,
	}
	if !dCtx.RepoConfig.DisableHumanInTheLoop {
		data["getHelpOrInputFunctionName"] = getHelpOrInputTool.Name
	}
	return RenderPrompt(AuthorEditBlockInitial, data)
}

func renderAuthorEditBlockInitialDevStepPrompt(dCtx DevContext, codeContext, requirements, planContext, currentStep string, applyEditBlocksImmediately bool) string {
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
		"retrieveCodeContextFunctionName": currentGetSymbolDefinitionsTool().Name,
		"applyEditBlocksImmediately":      applyEditBlocksImmediately,
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
func renderAuthorEditBlockFeedbackPrompt(feedback, feedbackType string) string {
	if feedbackType == FeedbackTypePause || feedbackType == FeedbackTypeUserGuidance || feedbackType == FeedbackTypeSystemError {
		return renderGeneralFeedbackPrompt(feedback, feedbackType)
	}

	// Simple regex for finding line numbers like "file.go:123" or "file.go:123:45"
	hasLineNumbers, _ := regexp.MatchString(`\w+\.\w+:\d+`, feedback)

	data := map[string]interface{}{
		"feedback":                         feedback,
		"isApplyError":                     feedbackType == FeedbackTypeApplyError,
		"isEditBlockError":                 feedbackType == FeedbackTypeEditBlockError,
		"isTestFailure":                    feedbackType == FeedbackTypeTestFailure,
		"isAutoReview":                     feedbackType == FeedbackTypeAutoReview,
		"hasLineNumbers":                   hasLineNumbers,
		"retrieveCodeContextFunctionName":  currentGetSymbolDefinitionsTool().Name,
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
