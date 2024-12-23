package dev

import (
	"fmt"
	"sidekick/domain"
	"sidekick/llm"
	"slices"
	"strings"

	"github.com/invopop/jsonschema"
	"go.temporal.io/sdk/workflow"
)

// GetHelpOrInputArguments  struct maps to the parameters for the get_help_or_input openai function
type GetHelpOrInputArguments struct {
	// TODO incorporate roles of who could be best suited to answer, or reason codes for why the request is being made, and/or request types.
	Requests []HelpOrInputRequest `json:"requests" jsonschema:"description=A list of requests for help and/or questions to ask."`
}
type HelpOrInputRequest struct {
	// TODO escape commas and double quotes automatically from descriptions
	// TODO format enums automatically from basic list of string constants
	//Reason string `json:"reason" jsonschema:"description=A sentence or two explaining why the request is being made. The reason MUST precede the request type and content in the json object."`
	//RequestType string `json:"requestType" jsonschema:"enum=question,enum=clarify_requirements,enum=debug_issue,enum=run_commands,enum=fix_environment,enum=improve_tooling,enum=other,description=The type of request being made."`
	Content string `json:"content" jsonschema:"description=The substance of the request/question\\, which the requestee/responder will read and respond to. Provide just enough context for them to understand your request easily. Follow best practices for questions\\, similar to the policy for high-quality StackOverflow questions. Don't add multiple questions inside one request\\, use the list to separate them and don't write out the question number."`
	//Target      string `json:"target"  jsonschema:"enum=requester,enum=product_owner,enum=software_engineer,enum=devops,enum=other,description=Who is likely to be able to service the request."`
	SelfHelp SelfHelp `json:"selfHelp"  jsonschema:"description=Describes how the requester can help themselves, eg by using the tools available."`
}

type SelfHelp struct {
	Analysis              string   `json:"analysis" jsonschema:"description=Must precede tools list. Brief anaylsis of whether any tools provided are very likely to be able to satisfy the request."`
	Tools                 []string `json:"functions" jsonschema:"description=List of tool names that are extremely likely be able to satisfy the request. MUST be empty if only a human can satisfy the request."`
	AlreadyAttemptedTools []string `json:"alreadyAttemptedTools" jsonschema:"description=List of tools that are have already been attempted to be used to resolve this issue\\, based on chat message history."`
}

var getHelpOrInputTool = llm.Tool{
	Name:        "get_help_or_input",
	Description: "Used to ask a human for help, feedback, information or input. This is a catch-all function for when you are stuck or confused and need to ask a human for something to unblock you and move forward with confidence. Do NOT use this function to ask for help finding info about repository files/code, except as as a last resort, since there are several other tools designed for that purpose to retrieve code context, read files or search the repo.",
	Parameters:  (&jsonschema.Reflector{ExpandedStruct: true}).Reflect(&GetHelpOrInputArguments{}),
}

// GetHelpOrInput signals request for help or input to the agent workflow and
// awaits for the 'userResponse' signal. it does this by signalling its parent
// workflow, which is going to be the agent manager workflow for now. in the
// future, if we have longer ancestry of workflows, the parent workflow would
// have to pass on the signal its own parent workflow, unless it can handle the
// signal itself by looking at its own context
func GetHelpOrInput(dCtx DevContext, requests []HelpOrInputRequest) (string, error) {
	messageBuilder := strings.Builder{}
	allSelfHelp := true
	selfHelpFunctions := make([]string, 0)
	numRequests := len(requests)
	for i, r := range requests {
		if numRequests > 1 {
			messageBuilder.WriteString(fmt.Sprintf("%d. %s\n", i+1, r.Content))
		} else {
			messageBuilder.WriteString(fmt.Sprintf("%s\n", r.Content))
		}

		if r.SelfHelp.Tools != nil && len(r.SelfHelp.Tools) > 0 {
			for _, f := range r.SelfHelp.Tools {
				if f == getHelpOrInputTool.Name {
					continue
				}
				if !slices.Contains(r.SelfHelp.AlreadyAttemptedTools, f) {
					selfHelpFunctions = append(selfHelpFunctions, f)
				}
			}
		} else {
			allSelfHelp = false
		}
	}

	if allSelfHelp && len(selfHelpFunctions) > 0 {
		// NOTE this won't be tracked as a separate flow action, but it will be
		// visible in the chat history for the next one.
		return fmt.Sprintf("Try using the following function(s) to unblock yourself before asking for help again: %s", strings.Join(selfHelpFunctions, ", ")), nil
	}

	message := messageBuilder.String()
	req := RequestForUser{
		OriginWorkflowId: workflow.GetInfo(dCtx).WorkflowExecution.ID,
		Subflow:          dCtx.FlowScope.SubflowName,
		Content:          message,
		RequestParams:    nil,         // no options when using get_help_or_input
		RequestKind:      "free_form", // free-form request is the only kind supported in get_help_or_input
	}

	actionCtx := dCtx.NewActionContext("user_request")
	actionCtx.ActionParams = req.ActionParams()
	userResponse, err := TrackHuman(actionCtx, func(flowAction domain.FlowAction) (*UserResponse, error) {
		req.FlowActionId = flowAction.Id
		return GetUserResponse(dCtx, req)
	})
	return userResponse.Content, err
}
