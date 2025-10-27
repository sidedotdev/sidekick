package dev

import (
	"encoding/json"
	"errors"
	"fmt"
	"sidekick/coding"
	"sidekick/coding/tree_sitter"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/fflag"
	"sidekick/llm"
	"sidekick/persisted_ai"
	"sidekick/utils"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	"go.temporal.io/sdk/workflow"
)

type RequiredCodeContext struct {
	Analysis string                     `json:"analysis" jsonschema:"description=Brief analysis of which code symbols (functions\\, types\\, etc) are most relevant before outputting requests."`
	Requests []coding.FileSymDefRequest `json:"requests" jsonschema:"description=Requests to retrieve full definitions of a given symbol within the given file where it is defined."`
}

func (r *RequiredCodeContext) UnmarshalJSON(data []byte) error {
	type Alias RequiredCodeContext
	aux := &struct {
		RequestsNew []coding.FileSymDefRequest `json:"requests"`
		RequestsOld []coding.FileSymDefRequest `json:"code_context_requests"`
		*Alias
	}{
		Alias: (*Alias)(r),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if len(aux.RequestsNew) > 0 {
		r.Requests = aux.RequestsNew
	} else if len(aux.RequestsOld) > 0 {
		r.Requests = aux.RequestsOld
	}

	return nil
}

var getSymbolDefinitionsTool = &llm.Tool{
	Name:        "get_symbol_definitions",
	Description: "When additional code context is required, analysis should be done first. Then the shortlist of functions and important custom types of interest. Returns the complete lines of code corresponding to that input, i.e., the full function and type defintion bodies. The go import block will also be included.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&RequiredCodeContext{}),
}

// this function doesn't do much yet, but will allow us to switch out the
// function definition for improved versions at runtime later
func currentGetSymbolDefinitionsTool() *llm.Tool {
	return getSymbolDefinitionsTool
}

// PrepareInitialCodeContextResult represents the result of preparing initial code context
type PrepareInitialCodeContextResult struct {
	CodeContext string
	Request     string
}

// TODO /gen write tests for this. use a test suite similar to the
// RunTestsTestSuite, including a wrapper workflow, setup & after
// functions. add a single test function in the suite that tests the happy
// path only.
//
// TODO /gen add tests to the test suite from above:
// - include one case where the code context is too long from step 2
// - include cases where retrieving code context fails, both in step 2 and 3, but succeeds after codeContextLoop retries
// - include a case where code context is incorrect many times and we ask for guidance, then succeed after guidance
func PrepareInitialCodeContext(dCtx DevContext, requirements string, planExec *DevPlanExecution, step *DevStep) (string, string, error) {
	result, err := RunSubflow(dCtx, "code_context", "Prepare Initial Code Context", func(subflow domain.Subflow) (PrepareInitialCodeContextResult, error) {
		return prepareInitialCodeContextSubflow(dCtx, requirements, planExec, step)
	})
	if err != nil {
		return "", "", err
	}
	return result.CodeContext, result.Request, nil
}

func prepareInitialCodeContextSubflow(dCtx DevContext, requirements string, planExec *DevPlanExecution, step *DevStep) (PrepareInitialCodeContextResult, error) {
	// Step 1: Extract high-level code outline aka repo summary
	repoSummary, needs, err := PrepareRepoSummary(dCtx, requirements)
	if err != nil {
		return PrepareInitialCodeContextResult{}, err
	}

	// Step 2: Pick out specific bits of code that are relevant
	initialPromptInfo := DetermineCodeContextInfo{
		RepoSummary:   repoSummary,
		Requirements:  requirements,
		Needs:         needs,
		PlanExecution: planExec,
		Step:          step,
	}
	codeContextRequest, codeContext, err := GetRelevantCodeContext(dCtx, initialPromptInfo)
	if err != nil {
		return PrepareInitialCodeContextResult{}, err
	}

	// Step 3: Refine and rank the code context, if needed
	if len(codeContext) > min(defaultMaxChatHistoryLength/2, refineContextLengthThreshold) {
		refinePromptInfo := RefineCodeContextInfo{
			DetermineCodeContextInfo:   initialPromptInfo,
			OriginalCodeContext:        codeContext,
			OriginalCodeContextRequest: codeContextRequest,
		}
		refinedContext, refinedRequest, err := RefineAndRankCodeContext(dCtx, *dCtx.EnvContainer, refinePromptInfo)
		if err != nil {
			return PrepareInitialCodeContextResult{}, err
		}
		return PrepareInitialCodeContextResult{CodeContext: refinedContext, Request: refinedRequest}, nil
	} else {
		return PrepareInitialCodeContextResult{CodeContext: codeContext, Request: ""}, nil
	}
}

func PrepareRepoSummary(dCtx DevContext, requirements string) (string, string, error) {
	repoSummary, err := GetRankedRepoSummary(dCtx, requirements, min(defaultMaxChatHistoryLength/2, 15000))
	if err != nil {
		return "", "", err
	}

	if !fflag.IsEnabled(dCtx, fflag.InfoNeeds) {
		return repoSummary, "", nil
	}
	// Call IdentifyInformationNeeds to get additional information needs
	chatHistory := &[]llm.ChatMessage{}
	infoNeeds, err := IdentifyInformationNeeds(dCtx, chatHistory, repoSummary, requirements)
	if err != nil {
		return "", "", fmt.Errorf("failed to identify information needs: %v", err)
	}
	needs := strings.Join(infoNeeds.Needs, "\n")

	// Append the identified needs to the requirements for second round of ranked signatures
	rankQuery := fmt.Sprintf("%s\n\n%s", requirements, strings.Join(infoNeeds.Needs, "\n"))
	repoSummary, err = GetRankedRepoSummary(dCtx, rankQuery, min(defaultMaxChatHistoryLength/2, 15000))

	return repoSummary, needs, err
}

var GetRepoSummaryForPrompt = GetRankedRepoSummary

func GetRankedRepoSummary(dCtx DevContext, rankQuery string, charLimit int) (string, error) {
	options := persisted_ai.RankedDirSignatureOutlineOptions{
		RankedViaEmbeddingOptions: persisted_ai.RankedViaEmbeddingOptions{
			WorkspaceId:  dCtx.WorkspaceId,
			EnvContainer: *dCtx.EnvContainer,
			RankQuery:    rankQuery,
			Secrets:      *dCtx.Secrets,
			ModelConfig:  dCtx.GetEmbeddingModelConfig(common.DefaultKey),
		},
		CharLimit: charLimit,
	}

	attempts := 0
	var repoSummary string
	var err error

	// TODO /gen instead of this for to reduce the char limit upon error, let's
	// use a token limit + real token counter
	for {
		actionCtx := dCtx.NewActionContext("ranked_repo_summary")
		actionCtx.ActionParams = options.ActionParams()
		repoSummary, err = Track(actionCtx, func(flowAction *domain.FlowAction) (string, error) {
			var repoSummary string
			var ra *persisted_ai.RagActivities // use a nil struct pointer to call activities that are part of a structure
			err := workflow.ExecuteActivity(utils.NoRetryCtx(dCtx), ra.RankedDirSignatureOutline, options).Get(dCtx, &repoSummary)
			if err != nil {
				return "", err
			}
			return repoSummary, nil
		})
		attempts += 1
		if err != nil {
			if attempts >= 8 {
				break
			}
			if strings.Contains(err.Error(), "maximum context length") {
				options.CharLimit = 96 * options.CharLimit / 100
			} else {
				workflow.Sleep(dCtx, 10*time.Second)
			}
			continue
		}
		break
	}

	return repoSummary, err
}

func GetRelevantCodeContext(dCtx DevContext, promptInfo DetermineCodeContextInfo) (*RequiredCodeContext, string, error) {
	// prioritize having breadth of information from many sources at this earlier stage
	longestFirst := true

	// we don't need room for other messages since we'll refine later if needed
	threshold := defaultMaxChatHistoryLength

	// TODO move up to PrepareInitialCodeContext and beyond
	return codeContextLoop(dCtx.NewActionContext("Determine Required Code Context"), promptInfo, longestFirst, threshold)
}

// TODO: make this configurable and/or dynamic by model
const refineContextLengthThreshold = 15000

func RefineAndRankCodeContext(dCtx DevContext, envContainer env.EnvContainer, promptInfo RefineCodeContextInfo) (string, string, error) {
	// shrinking code context from end to start (since we asked the LLM
	// to sort by relevance) until it's below the length threshold or it
	// can't reduce it anymore
	longestFirst := false

	// leave some room for other messages in other subflows after code context
	// is finalized
	threshold := min(defaultMaxChatHistoryLength/2, refineContextLengthThreshold)

	// TODO move up to PrepareInitialCodeContext and beyond
	requiredCodeContext, codeContext, err := codeContextLoop(dCtx.NewActionContext("Refine And Rank Code Context"), promptInfo, longestFirst, threshold)
	if err != nil {
		return "", "", err
	}
	// TODO /gen include the symbolized code context for the symbols that were
	// removed from context after refining, and add that to the end of the
	// codeContext string
	fullCodeContext, err := RetrieveCodeContext(dCtx, *requiredCodeContext, 1000*defaultMaxChatHistoryLength)
	return codeContext, fullCodeContext, err
}

// TODO /gen return a CodeContextSpec interface, which is implemented by both
// RequiredCodeContext and BulkSearchRepositoryParams. This has one method,
// GetCodeContextSpec, which returns a string. This will allow us to use the
// same function for both code context and search repository and allow
// codeContextLoop to choose between them instead of forcing RetrieveCodeContext
// as we do now. This will make refactoring tasks far easier.
func codeContextLoop(actionCtx DevActionContext, promptInfo PromptInfo, longestFirst bool, maxLength int) (*RequiredCodeContext, string, error) {
	var requiredCodeContext RequiredCodeContext
	var codeContext string
	chatHistory := &[]llm.ChatMessage{}
	addCodeContextPrompt(chatHistory, promptInfo)
	noRetryCtx := utils.NoRetryCtx(actionCtx)
	attempts := 0
	iterationsSinceLastFeedback := 0

	for {
		// Check for pause at the beginning of each iteration
		userResponse, err := UserRequestIfPaused(actionCtx.DevContext, "Code context loop paused. Would you like to provide any guidance?", nil)
		if err != nil {
			return nil, "", fmt.Errorf("failed to check for pause: %v", err)
		}
		if userResponse != nil && userResponse.Content != "" {
			addCodeContextPrompt(chatHistory, FeedbackInfo{
				Feedback: fmt.Sprintf("-- PAUSED --\n\nIMPORTANT: The user paused and provided the following guidance:\n\n%s", userResponse.Content),
			})
			iterationsSinceLastFeedback = 0
			continue
		}

		// NOTE due to most of the testing being done this way so far, we clean
		// up chat history *before* extending it. We'll look into changing this
		// later, and will tune our max history length to support that change.
		ManageChatHistory(actionCtx, chatHistory, defaultMaxChatHistoryLength)

		attempts++
		iterationsSinceLastFeedback++

		if iterationsSinceLastFeedback >= 5 {
			guidanceContext := "The system has attempted to refine and rank the code context multiple times without success. Please provide some guidance."
			userFeedback, err := GetUserFeedback(actionCtx.DevContext, SkipInfo{}, guidanceContext, chatHistory, nil)
			if err != nil {
				return nil, "", fmt.Errorf("failed to get user feedback: %v", err)
			}
			addCodeContextPrompt(chatHistory, userFeedback)
			iterationsSinceLastFeedback = 0
		} else if attempts%3 == 0 {
			chatCtx := actionCtx.DevContext.WithCancelOnPause()
			toolCall, err := ForceToolBulkSearchRepository(chatCtx, chatHistory)
			if actionCtx.GlobalState != nil && actionCtx.GlobalState.Paused {
				continue // UserRequestIfPaused will handle the pause
			}
			if err != nil {
				return nil, "", fmt.Errorf("failed to force searching repository: %v", err)
			}
			toolCallResponseInfo, err := handleToolCall(actionCtx.DevContext, toolCall)
			if err != nil {
				// retry bad tool call with feedback -- TODO move into handleToolCall
				if errors.Is(err, llm.ErrToolCallUnmarshal) {
					addCodeContextPrompt(chatHistory, toolCallResponseInfo)
					continue
				}

				return nil, "", err
			}
			addCodeContextPrompt(chatHistory, toolCallResponseInfo)
		}

		if attempts >= 17 {
			return nil, "", fmt.Errorf("failed to refine and rank code context after 17 attempts: %v", err)
		}

		// STEP 2: Decide which code to read fully
		// TODO /gen instead of forcing just get_symbol_definitions, let's force
		// one of get_symbol_definitions or bulk_search_repository. if given
		// bulk_search_repository,
		var toolCall llm.ToolCall
		chatCtx := actionCtx.WithCancelOnPause()
		toolCall, requiredCodeContext, err = ForceToolRetrieveCodeContext(chatCtx, chatHistory)
		if actionCtx.GlobalState != nil && actionCtx.GlobalState.Paused {
			continue // UserRequestIfPaused will handle the pause
		}
		if err != nil {
			if errors.Is(err, llm.ErrToolCallUnmarshal) {
				response := fmt.Sprintf("%s\n\nHint: To fix this, follow the json schema correctly. In particular, don't put json within a string.", err.Error())
				toolCallResponseInfo := ToolCallResponseInfo{Response: response, ToolCallId: toolCall.Id, FunctionName: toolCall.Name}
				addCodeContextPrompt(chatHistory, toolCallResponseInfo)
				continue
			}
			return nil, "", fmt.Errorf("failed to determine required code context: %v", err)
		}

		// STEP 3: Read the code
		var result coding.SymDefResults
		result, err = extractCodeContext(noRetryCtx, coding.DirectorySymDefRequest{
			EnvContainer:          *actionCtx.EnvContainer,
			Requests:              requiredCodeContext.Requests,
			IncludeRelatedSymbols: true,
		})

		if err == nil && result.Failures == "" {
			var didShrink bool
			codeContext, didShrink = tree_sitter.ShrinkEmbeddedCodeContext(result.SymbolDefinitions, longestFirst, maxLength)
			if didShrink && !strings.Contains(codeContext, SignaturesEditHint) {
				codeContext = strings.TrimSpace(codeContext) + "\n\n-------------\n" + SignaturesEditHint
			}

			currentMax := maxLength
			v := workflow.GetVersion(actionCtx, "code_context_max_length", workflow.DefaultVersion, 1)
			if v == 1 {
				currentMax = currentMax + len("\n\n-------------\n"+SignaturesEditHint)
			}

			// TODO use tiktoken to count exact tokens and compare with specific model being used + margin
			if len(codeContext) > currentMax {
				// TODO if this happens, we could try partially symbolizing the code context too
				feedback := "Error: the code context requested is too long to include. YOU MUST SHORTEN THE CODE CONTEXT REQUESTED. DO NOT REQUEST SO MANY FUNCTIONS AND TYPES IN SO MANY FILES. If you're not asking for too many symbols, then be more specific in your request - eg request just a few methods instead of a big class."
				promptInfo = ToolCallResponseInfo{Response: feedback, ToolCallId: toolCall.Id, FunctionName: toolCall.Name}
				addCodeContextPrompt(chatHistory, promptInfo)
				continue
			} else {
				// TODO check for empty code context too. we should use
				// alternate methods if we get empty code context repeatedly.
				break
			}
		}

		// we'll retry if we get an error, and include the error in the feedback
		hint := fmt.Sprintf("Have you followed the required formats exactly for all arguments? Look at the examples given in the %s schema descriptions for all the properties. Note that frontend components can be retrieved in full with empty symbol names array", currentGetSymbolDefinitionsTool().Name)
		feedback := fmt.Sprintf("failed to extract code context: %v\n%s\n\nHint: %s", err, result.Failures, hint)
		promptInfo = ToolCallResponseInfo{Response: feedback, ToolCallId: toolCall.Id, FunctionName: toolCall.Name}
		addCodeContextPrompt(chatHistory, promptInfo)

		// Check if the operation was paused
		if actionCtx.DevContext.GlobalState != nil && actionCtx.DevContext.GlobalState.Paused {
			return nil, "", fmt.Errorf("operation paused by user")
		}
	}

	return &requiredCodeContext, codeContext, nil
}

func extractCodeContext(ctx workflow.Context, req coding.DirectorySymDefRequest) (coding.SymDefResults, error) {
	var symDefResults coding.SymDefResults
	var ca *coding.CodingActivities // use a nil struct pointer to call activities that are part of a structure

	/*
		TODO (this TODO is stale): try to remove the 0 override by one of:

		1. detecting bad edits using the expanded context lines as the old lines
		2. detecting bad edits that are editing functions that have not been fully retrieved
		3. adding text below the code context that indicates that the code
		context is incomplete, eg the function is cut off
		4. adding a "show more" tool that will retrieve the full function (this kind of does 3 too)
		5. only adding the context if it doesn't cut off a function
	*/
	// overrideNumContextLines := 0

	/*
		With older models, we found that context was messing up python
		editing and not that helpful otherwise, so we overrode by always setting
		context lines to 0.

		But with gemini pro 2.5, the lack of context means it's guessing poorly the
		lines before/after a function in its edit blocks, and failing a lot of
		edits, which is annoying. So we're using a fixed value of 3 lines
		instead now.

		TODO: confirm the new setting works ok for claude sonnet 3.5 and gpt
		4.1, with all languages but especially with python. If not, customize
		overrides by model.
	*/
	overrideNumContextLines := 3

	req.NumContextLines = &overrideNumContextLines

	err := workflow.ExecuteActivity(ctx, ca.BulkGetSymbolDefinitions, req).Get(ctx, &symDefResults)
	return symDefResults, err
}

func RetrieveCodeContext(dCtx DevContext, requiredCodeContext RequiredCodeContext, characterLengthThreshold int) (string, error) {
	if len(requiredCodeContext.Requests) == 0 {
		return "", llm.ErrToolCallUnmarshal
	}

	dCtx.Context = utils.NoRetryCtx(dCtx)
	result, err := extractCodeContext(dCtx, coding.DirectorySymDefRequest{
		EnvContainer:          *dCtx.EnvContainer,
		Requests:              requiredCodeContext.Requests,
		IncludeRelatedSymbols: true,
	})
	if err != nil {
		return result.SymbolDefinitions, err
	}

	codeContext, didShrink := tree_sitter.ShrinkEmbeddedCodeContext(result.SymbolDefinitions, true, characterLengthThreshold)
	if didShrink && !strings.Contains(codeContext, SignaturesEditHint) {
		codeContext = strings.TrimSpace(codeContext) + "\n\n-------------\n" + SignaturesEditHint
	}

	return codeContext, nil
}

func ForceToolRetrieveCodeContext(actionCtx DevActionContext, chatHistory *[]llm.ChatMessage) (llm.ToolCall, RequiredCodeContext, error) {
	modelConfig := actionCtx.GetModelConfig(common.CodeLocalizationKey, 0, "small")
	params := llm.ToolChatParams{Messages: *chatHistory, ModelConfig: modelConfig}
	chatResponse, err := persisted_ai.ForceToolCall(actionCtx.FlowActionContext(), actionCtx.LLMConfig, &params, currentGetSymbolDefinitionsTool())
	*chatHistory = params.Messages // update chat history with the new messages
	if err != nil {
		return llm.ToolCall{}, RequiredCodeContext{}, fmt.Errorf("failed to force tool call: %v", err)
	}
	toolCall := chatResponse.ToolCalls[0]
	jsonStr := toolCall.Arguments
	var requiredCodeContext RequiredCodeContext

	// TODO move unmarshaling into the force tool call function, using reflect.Zero(tool.ParametersType)
	err = json.Unmarshal([]byte(llm.RepairJson(jsonStr)), &requiredCodeContext)
	if err != nil {
		return toolCall, RequiredCodeContext{}, fmt.Errorf("%w: %v", llm.ErrToolCallUnmarshal, err)
	} else if requiredCodeContext.Requests == nil {
		return toolCall, RequiredCodeContext{}, fmt.Errorf("%w: missing requests in tool call", llm.ErrToolCallUnmarshal)
	}

	return toolCall, requiredCodeContext, nil
}

func addCodeContextPrompt(chatHistory *[]llm.ChatMessage, promptInfo PromptInfo) {
	var content string
	role := llm.ChatMessageRoleUser
	name := ""
	toolCallId := ""
	skip := false
	isError := false
	cacheControl := ""
	switch info := promptInfo.(type) {
	case SkipInfo:
		skip = true
	case ToolCallResponseInfo:
		role = llm.ChatMessageRoleTool
		content = renderCodeContextFeedbackPrompt(info.Response)
		name = info.FunctionName
		toolCallId = info.ToolCallId
		isError = info.IsError
	case FeedbackInfo:
		content = info.Feedback
	case DetermineCodeContextInfo:
		content = renderCodeContextInitialPrompt(info)
		cacheControl = "ephemeral"
	case RefineCodeContextInfo:
		content = renderCodeContextRefineAndRankPrompt(info)
		cacheControl = "ephemeral"
	default:
		panic("Unsupported prompt type for code context: " + promptInfo.GetType())
	}

	if !skip {
		*chatHistory = append(*chatHistory, llm.ChatMessage{
			Role:         role,
			Content:      content,
			Name:         name,
			ToolCallId:   toolCallId,
			CacheControl: cacheControl,
			IsError:      isError,
		})
	}
}

func renderCodeContextFeedbackPrompt(feedback string) string {
	data := map[string]interface{}{
		"feedback":                        feedback,
		"retrieveCodeContextFunctionName": currentGetSymbolDefinitionsTool().Name,
	}
	return RenderPrompt(CodeContextFeedback, data)
}

func renderCodeContextInitialPrompt(info DetermineCodeContextInfo) string {
	var planExecution string
	if info.PlanExecution != nil {
		planExecution = info.PlanExecution.String()
	}
	var step string
	if info.Step != nil {
		step = info.Step.Definition
	}

	data := map[string]interface{}{
		"repoSummary":             info.RepoSummary,
		"requirements":            info.Requirements,
		"needs":                   info.Needs,
		"planExecution":           planExecution,
		"step":                    step,
		"startInitialCodeContext": startInitialCodeContext,
		"endInitialCodeContext":   endInitialCodeContext,
	}
	return RenderPrompt(CodeContextInitial, data)
}

func renderCodeContextRefineAndRankPrompt(info RefineCodeContextInfo) string {
	var planExecution string
	if info.PlanExecution != nil {
		planExecution = info.PlanExecution.String()
	}
	var step string
	if info.Step != nil {
		step = info.Step.Definition
	}
	data := map[string]interface{}{
		"originalCodeContext":         info.OriginalCodeContext,
		"originalCodeContextRequests": utils.PanicJSON(info.OriginalCodeContextRequest.Requests),
		"requirements":                info.Requirements,
		// don't think needs are needed (hah!) when refining, as needs are about expanding vs narrowing down
		//"needs":                       info.Needs,
		"planExecution":           planExecution,
		"step":                    step,
		"startInitialCodeContext": startInitialCodeContext,
		"endInitialCodeContext":   endInitialCodeContext,
	}
	return RenderPrompt(CodeContextRefineAndRank, data)
}
