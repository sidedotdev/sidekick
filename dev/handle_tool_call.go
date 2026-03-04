package dev

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sidekick/domain"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/utils"
	"strings"

	"go.temporal.io/sdk/workflow"
)

// TODO figure out how to make this more dynamic based on when
// we need to go past this hard-coded threshold, eg for single
// large functions that exceed this limit
// TODO /gen/planned/req move this to RepoConfig
const maxRetrieveCodeContextLength = 15000

func handleToolCalls(dCtx DevContext, toolCalls []llm.ToolCall, chatHistory *persisted_ai.ChatHistoryContainer, customHandlers map[string]func(DevContext, llm.ToolCall) (llm2.ToolResultBlock, error)) []llm2.ToolResultBlock {
	// backward compatibility: handle-parallel-tool-calls
	// if old version, only process the first tool call
	version := workflow.GetVersion(dCtx, "handle-parallel-tool-calls", workflow.DefaultVersion, 1)
	if version == workflow.DefaultVersion {
		if len(toolCalls) == 0 {
			return []llm2.ToolResultBlock{}
		}
		// Process only the first tool call sequentially
		tc := toolCalls[0]
		var output ToolCallOutput
		var err error

		if handler, ok := customHandlers[tc.Name]; ok {
			output.ToolResultBlock, err = handler(dCtx, tc)
		} else {
			output, err = handleToolCall(dCtx, tc)
		}

		if err != nil {
			output.IsError = true
			output.Name = tc.Name
			output.ToolCallId = tc.Id
			if len(output.Content) == 0 {
				output.Content = llm2.TextContentBlocks(err.Error())
			}
			output.Ref = nil
		}

		results := cleanupWorkingDirFromResults(dCtx, []llm2.ToolResultBlock{output.ToolResultBlock})
		appendToolCallResult(dCtx, chatHistory, results[0], output.Ref)
		return results
	}

	responseChannel := workflow.NewChannel(dCtx)
	for i, tc := range toolCalls {
		// capture loop variables
		tc := tc
		index := i
		workflow.Go(dCtx, func(ctx workflow.Context) {
			localDCtx := dCtx
			localDCtx.Context = ctx

			var output ToolCallOutput
			var err error

			if handler, ok := customHandlers[tc.Name]; ok {
				output.ToolResultBlock, err = handler(localDCtx, tc)
			} else {
				output, err = handleToolCall(localDCtx, tc)
			}

			responseChannel.Send(ctx, struct {
				Index  int
				Output ToolCallOutput
				Err    error
			}{index, output, err})
		})
	}

	outputs := make([]ToolCallOutput, len(toolCalls))

	for i := 0; i < len(toolCalls); i++ {
		var resp struct {
			Index  int
			Output ToolCallOutput
			Err    error
		}
		responseChannel.Receive(dCtx, &resp)
		outputs[resp.Index] = resp.Output
		if resp.Err != nil {
			outputs[resp.Index].IsError = true
			outputs[resp.Index].ToolCallId = toolCalls[resp.Index].Id
			outputs[resp.Index].Name = toolCalls[resp.Index].Name
			if len(outputs[resp.Index].Content) == 0 {
				outputs[resp.Index].Content = llm2.TextContentBlocks(resp.Err.Error())
			}
			outputs[resp.Index].Ref = nil
		}
	}

	results := make([]llm2.ToolResultBlock, len(outputs))
	for i, o := range outputs {
		results[i] = o.ToolResultBlock
	}
	results = cleanupWorkingDirFromResults(dCtx, results)
	for i, result := range results {
		appendToolCallResult(dCtx, chatHistory, result, outputs[i].Ref)
	}
	return results
}

// appendToolCallResult appends a tool call result to chat history. For pre-persisted
// results (with a non-nil ref), it appends the ref directly. Otherwise it wraps
// and persists via addToolCallResponse.
func appendToolCallResult(ctx workflow.Context, chatHistory *persisted_ai.ChatHistoryContainer, trb llm2.ToolResultBlock, ref *persisted_ai.MessageRef) {
	if ref != nil {
		if llm2History, ok := chatHistory.History.(*persisted_ai.Llm2ChatHistory); ok {
			llm2History.AppendRef(*ref)
			return
		}
	}
	addToolCallResponse(ctx, chatHistory, trb)
}

func cleanupWorkingDirFromResults(dCtx DevContext, results []llm2.ToolResultBlock) []llm2.ToolResultBlock {
	if dCtx.EnvContainer != nil && dCtx.EnvContainer.Env != nil {
		workingDir := dCtx.EnvContainer.Env.GetWorkingDirectory()
		for i := range results {
			for j := range results[i].Content {
				if results[i].Content[j].Type == llm2.ContentBlockTypeText {
					results[i].Content[j].Text = removeWorkingDirFromPaths(results[i].Content[j].Text, workingDir)
				}
			}
		}
	}
	return results
}

// ToolCallOutput wraps a tool result with an optional persisted ref for
// content that shouldn't be inlined in the tracked flow action (e.g. images).
type ToolCallOutput struct {
	llm2.ToolResultBlock
	Ref *persisted_ai.MessageRef `json:"ref,omitempty"`
}

// TODO /gen/planned/req add a test for this function using WorkflowTestSuite
func handleToolCall(dCtx DevContext, toolCall llm.ToolCall) (ToolCallOutput, error) {
	var toolCallResult llm2.ToolResultBlock
	dCtx.Context = utils.NoRetryCtx(dCtx)
	toolCallResult.Name = toolCall.Name
	toolCallResult.ToolCallId = toolCall.Id

	// we need to use the TrackHuman function when the tool call is for a human
	// to respond, which happens inside the GetHelpOrInput tool call itself
	if toolCall.Name == getHelpOrInputTool.Name {
		var wrapper GetHelpOrInputArguments
		response, err := unmarshalAndInvoke(toolCall, &wrapper, func() (string, error) {
			return GetHelpOrInput(dCtx, wrapper.Requests)
		})
		toolCallResult.Content = llm2.TextContentBlocks(response)
		return ToolCallOutput{ToolResultBlock: toolCallResult}, err
	}

	actionParams := make(map[string]interface{})
	err := json.Unmarshal([]byte(llm.RepairJson(toolCall.Arguments)), &actionParams)
	if err != nil {
		result, unmarshalErr := handleErrToolCallUnmarshal(toolCallResult, fmt.Errorf("%w: %v", llm.ErrToolCallUnmarshal, err))
		return ToolCallOutput{ToolResultBlock: result}, unmarshalErr
	}

	actionCtx := dCtx.NewActionContext("tool_call." + toolCall.Name)
	actionCtx.ActionParams = actionParams

	// The tracked flow action result includes both the tool result content
	// and an optional ref for persisted content (e.g. images).
	var ref *persisted_ai.MessageRef
	result, trackErr := Track(actionCtx, func(trackedCtx DevActionContext, flowAction *domain.FlowAction) (ToolCallOutput, error) {
		var response string
		trackedDCtx := trackedCtx.DevContext
		switch toolCall.Name {
		case "retrieve_code_context", currentGetSymbolDefinitionsTool().Name:
			var requiredCodeContext RequiredCodeContext
			response, err = unmarshalAndInvoke(toolCall, &requiredCodeContext, func() (string, error) {
				// we want to leave room for the rest of the chat history, hence this lengthThreshold

				// TODO ideally we'd just keep all the code context at this
				// point, but return the entire SourceBlock + request for code
				// context, then later on, when rendering a promp, we can decide
				// to shrink it or truncate it etc if it's too long, and use the
				// detailed metadata + other chat history and current context to
				// make a better decision here. We'd need to change the format
				// of ToolResultBlock here to add an map[string]{interface}
				// field for detailed info, and also change how we pass the
				// variables to render the prompts later based on this more
				// detailed metadata with context of max history limits.
				lengthThreshold := min(defaultMaxChatHistoryLength/2, maxRetrieveCodeContextLength)
				return RetrieveCodeContext(trackedDCtx, requiredCodeContext, lengthThreshold)
			})
		case bulkReadFileTool.Name:
			var bulkReadFileParams BulkReadFileParams
			response, err = unmarshalAndInvoke(toolCall, &bulkReadFileParams, func() (string, error) {
				return BulkReadFile(trackedDCtx, bulkReadFileParams)
			})
		case bulkSearchRepositoryTool.Name:
			var bulkSearchRepositoryParams BulkSearchRepositoryParams
			response, err = unmarshalAndInvoke(toolCall, &bulkSearchRepositoryParams, func() (string, error) {
				return BulkSearchRepository(trackedDCtx, *trackedDCtx.EnvContainer, bulkSearchRepositoryParams)
			})
		case recordDevPlanTool.Name:
			response, err = "recorded", nil
		case runCommandTool.Name:
			var runCommandParams RunCommandParams
			response, err = unmarshalAndInvoke(toolCall, &runCommandParams, func() (string, error) {
				return RunCommand(trackedDCtx, runCommandParams)
			})
		case readImageTool.Name:
			var params ReadImageParams
			response, err = unmarshalAndInvoke(toolCall, &params, func() (string, error) {
				flowId := workflow.GetInfo(trackedDCtx).WorkflowExecution.ID
				var ria *ReadImageActivities
				var output ReadImageOutput
				actErr := workflow.ExecuteActivity(trackedDCtx, ria.ReadImageActivity, ReadImageInput{
					EnvContainer: *trackedDCtx.EnvContainer,
					FilePath:     params.FilePath,
					URL:          params.URL,
					FlowId:       flowId,
					ToolCall:     &toolCall,
					WorkspaceId:  trackedDCtx.WorkspaceId,
				}).Get(trackedDCtx, &output)
				if actErr != nil {
					return "", actErr
				}
				ref = &output.Ref
				return "", nil
			})
		case setBaseBranchTool.Name:
			var setBaseBranchParams SetBaseBranchParams
			response, err = unmarshalAndInvoke(toolCall, &setBaseBranchParams, func() (string, error) {
				return SetBaseBranch(trackedDCtx, setBaseBranchParams)
			})
		default:
			// FIXME this should be non-retryable but is being retried now (openai can rarely use a function name that we don't support)
			response, err = "", fmt.Errorf("unknown function name: %s", toolCall.Name)
		}

		if response != "" && len(toolCallResult.Content) == 0 {
			toolCallResult.Content = llm2.TextContentBlocks(response)
		}
		// ensure tracked flow action gets the state after handling this type of error
		trb, unmarshalErr := handleErrToolCallUnmarshal(toolCallResult, err)
		return ToolCallOutput{ToolResultBlock: trb, Ref: ref}, unmarshalErr
	})
	return result, trackErr
}

func handleErrToolCallUnmarshal(toolCallResult llm2.ToolResultBlock, err error) (llm2.ToolResultBlock, error) {
	if err != nil {
		toolCallResult.IsError = true
		if errors.Is(err, llm.ErrToolCallUnmarshal) {
			// NOTE: this error happens when the tool call arguments didn't
			// follow schema. by providing the error as the tool call response,
			// we give the llm a chance to self-correct via feedback.
			toolCallResult.Content = llm2.TextContentBlocks(fmt.Sprintf("%s\n\nHint: To fix this, follow the json schema correctly. In particular, don't put json within a string.", err.Error()))
			err = nil
		}
	}
	return toolCallResult, err
}

func unmarshalAndInvoke(toolCall llm.ToolCall, target interface{}, fn func() (string, error)) (string, error) {
	jsonStr := toolCall.Arguments
	err := json.Unmarshal([]byte(llm.RepairJson(jsonStr)), target)
	if err != nil {
		return "", fmt.Errorf("%w: %v", llm.ErrToolCallUnmarshal, err)
	}

	response, err := fn()
	if err != nil {
		return "", err
	}

	return response, nil
}

// removeWorkingDirFromPaths removes the working directory prefix from paths in
// the given string. It matches patterns like /path/to/workdir/some/file and
// replaces them with some/file. It only removes the prefix when followed by
// path-like content (non-whitespace after the trailing slash).
func removeWorkingDirFromPaths(s, workingDir string) string {
	if workingDir == "" {
		return s
	}

	// Ensure workingDir doesn't have a trailing slash for consistent matching
	workingDir = strings.TrimSuffix(workingDir, "/")

	// Match workingDir/ followed by non-whitespace, non-quote characters (a path)
	// This avoids replacing when it's just the directory alone or followed by whitespace/quotes
	pattern := regexp.MustCompile(regexp.QuoteMeta(workingDir) + `/(\S+)`)

	return pattern.ReplaceAllStringFunc(s, func(match string) string {
		// Extract what comes after workingDir/
		suffix := strings.TrimPrefix(match, workingDir+"/")

		// Check if the suffix starts with a quote or is empty, if so leave it alone
		if suffix == "" || suffix[0] == '"' || suffix[0] == '\'' {
			return match
		}

		return suffix
	})
}
