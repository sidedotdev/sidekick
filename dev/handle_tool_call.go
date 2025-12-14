package dev

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sidekick/domain"
	"sidekick/llm"
	"sidekick/utils"
	"strings"

	"go.temporal.io/sdk/workflow"
)

// TODO figure out how to make this more dynamic based on when
// we need to go past this hard-coded threshold, eg for single
// large functions that exceed this limit
// TODO /gen/planned/req move this to RepoConfig
const maxRetrieveCodeContextLength = 15000

func handleToolCalls(dCtx DevContext, toolCalls []llm.ToolCall, customHandlers map[string]func(DevContext, llm.ToolCall) (ToolCallResponseInfo, error)) []ToolCallResponseInfo {
	// backward compatibility: handle-parallel-tool-calls
	// if old version, only process the first tool call
	version := workflow.GetVersion(dCtx, "handle-parallel-tool-calls", workflow.DefaultVersion, 1)
	if version == workflow.DefaultVersion {
		if len(toolCalls) == 0 {
			return []ToolCallResponseInfo{}
		}
		// Process only the first tool call sequentially
		tc := toolCalls[0]
		var result ToolCallResponseInfo
		var err error

		if handler, ok := customHandlers[tc.Name]; ok {
			result, err = handler(dCtx, tc)
		} else {
			result, err = handleToolCall(dCtx, tc)
		}

		if err != nil {
			result.IsError = true
			result.Response = err.Error()
			result.FunctionName = tc.Name
			result.ToolCallId = tc.Id
		}

		return cleanupWorkingDirFromResults(dCtx, []ToolCallResponseInfo{result})
	}

	responseChannel := workflow.NewChannel(dCtx)
	for i, tc := range toolCalls {
		// capture loop variables
		tc := tc
		index := i
		workflow.Go(dCtx, func(ctx workflow.Context) {
			localDCtx := dCtx
			localDCtx.Context = ctx

			var result ToolCallResponseInfo
			var err error

			if handler, ok := customHandlers[tc.Name]; ok {
				result, err = handler(localDCtx, tc)
			} else {
				result, err = handleToolCall(localDCtx, tc)
			}

			responseChannel.Send(ctx, struct {
				Index  int
				Result ToolCallResponseInfo
				Err    error
			}{index, result, err})
		})
	}

	results := make([]ToolCallResponseInfo, len(toolCalls))

	for i := 0; i < len(toolCalls); i++ {
		var resp struct {
			Index  int
			Result ToolCallResponseInfo
			Err    error
		}
		responseChannel.Receive(dCtx, &resp)
		results[resp.Index] = resp.Result
		if resp.Err != nil {
			results[resp.Index].IsError = true
			results[resp.Index].ToolCallId = toolCalls[resp.Index].Id
			results[resp.Index].FunctionName = toolCalls[resp.Index].Name
			if results[resp.Index].Response == "" {
				results[resp.Index].Response = resp.Err.Error()
			}
		}
	}

	return cleanupWorkingDirFromResults(dCtx, results)
}

func cleanupWorkingDirFromResults(dCtx DevContext, results []ToolCallResponseInfo) []ToolCallResponseInfo {
	if dCtx.EnvContainer != nil && dCtx.EnvContainer.Env != nil {
		workingDir := dCtx.EnvContainer.Env.GetWorkingDirectory()
		for i := range results {
			results[i].Response = removeWorkingDirFromPaths(results[i].Response, workingDir)
		}
	}
	return results
}

// TODO /gen/planned/req add a test for this function using WorkflowTestSuite
func handleToolCall(dCtx DevContext, toolCall llm.ToolCall) (toolCallResult ToolCallResponseInfo, err error) {
	dCtx.Context = utils.NoRetryCtx(dCtx)
	toolCallResult.FunctionName = toolCall.Name
	toolCallResult.ToolCallId = toolCall.Id

	// we need to use the TrackHuman function when the tool call is for a human
	// to respond, which happens inside the GetHelpOrInput tool call itself
	if toolCall.Name == getHelpOrInputTool.Name {
		var wrapper GetHelpOrInputArguments
		response, err := unmarshalAndInvoke(toolCall, &wrapper, func() (string, error) {
			return GetHelpOrInput(dCtx, wrapper.Requests)
		})
		toolCallResult.Response = response
		return toolCallResult, err
	}

	actionParams := make(map[string]interface{})
	err = json.Unmarshal([]byte(llm.RepairJson(toolCall.Arguments)), &actionParams)
	if err != nil {
		return handleErrToolCallUnmarshal(toolCallResult, fmt.Errorf("%w: %v", llm.ErrToolCallUnmarshal, err))
	}

	actionCtx := dCtx.NewActionContext("tool_call." + toolCall.Name)
	actionCtx.ActionParams = actionParams

	// NOTE: the function passed in very deliberately returns
	// ToolCallResponseInfo since what's returned is what's tracked, and we want
	// to the entire tool call response, not just the response string
	return Track(actionCtx, func(flowAction *domain.FlowAction) (ToolCallResponseInfo, error) {
		var response string
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
				// of ToolCallResponseInfo here to add an map[string]{interface}
				// field for detailed info, and also change how we pass the
				// variables to render the prompts later based on this more
				// detailed metadata with context of max history limits.
				lengthThreshold := min(defaultMaxChatHistoryLength/2, maxRetrieveCodeContextLength)
				return RetrieveCodeContext(dCtx, requiredCodeContext, lengthThreshold)
			})
		case bulkReadFileTool.Name:
			var bulkReadFileParams BulkReadFileParams
			response, err = unmarshalAndInvoke(toolCall, &bulkReadFileParams, func() (string, error) {
				return BulkReadFile(dCtx, bulkReadFileParams)
			})
		case bulkSearchRepositoryTool.Name:
			var bulkSearchRepositoryParams BulkSearchRepositoryParams
			response, err = unmarshalAndInvoke(toolCall, &bulkSearchRepositoryParams, func() (string, error) {
				return BulkSearchRepository(dCtx, *dCtx.EnvContainer, bulkSearchRepositoryParams)
			})
		case recordDevPlanTool.Name:
			response, err = "recorded", nil
		case runCommandTool.Name:
			var runCommandParams RunCommandParams
			response, err = unmarshalAndInvoke(toolCall, &runCommandParams, func() (string, error) {
				return RunCommand(dCtx, runCommandParams)
			})
		default:
			// FIXME this should be non-retryable but is being retried now (openai can rarely use a function name that we don't support)
			response, err = "", fmt.Errorf("unknown function name: %s", toolCall.Name)
		}

		toolCallResult.Response = response
		// ensure tracked flow action gets the state after handling this type of error
		return handleErrToolCallUnmarshal(toolCallResult, err)
	})
}

func handleErrToolCallUnmarshal(toolCallResult ToolCallResponseInfo, err error) (ToolCallResponseInfo, error) {
	if err != nil {
		toolCallResult.IsError = true
		if errors.Is(err, llm.ErrToolCallUnmarshal) {
			// NOTE: this error happens when the tool call arguments didn't
			// follow schema. by providing the error as the tool call response,
			// we give the llm a chance to self-correct via feedback.
			toolCallResult.Response = fmt.Sprintf("%s\n\nHint: To fix this, follow the json schema correctly. In particular, don't put json within a string.", err.Error())
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
