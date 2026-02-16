package dev

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sort"

	"github.com/invopop/jsonschema"
	"github.com/sashabaranov/go-openai"
	"go.temporal.io/sdk/workflow"
)

var recordDevRequirementsTool = llm.Tool{
	Name:        "record_dev_requirements",
	Description: "Records technical requirements for software developers to consume.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&DevRequirements{}),
}

type CriteriaUpdate struct {
	Index     int     `json:"index" jsonschema:"description=0-based index of the acceptance criterion to update. For insert\\, this is where the new criterion will be inserted."`
	Operation string  `json:"operation" jsonschema:"enum=edit,enum=delete,enum=insert,description=The operation to perform: edit updates existing criterion\\, delete removes it\\, insert adds a new criterion at the position."`
	Content   *string `json:"content,omitempty" jsonschema:"description=The criterion content (for edit/insert)"`
}

type RequirementsLearningUpdate struct {
	Index     int     `json:"index" jsonschema:"description=0-based index of the learning to update. For insert\\, this is where the new learning will be inserted."`
	Operation string  `json:"operation" jsonschema:"enum=edit,enum=delete,enum=insert,description=The operation to perform: edit updates existing learning\\, delete removes it\\, insert adds a new learning at the position."`
	Content   *string `json:"content,omitempty" jsonschema:"description=The learning content (for edit/insert)"`
}

type DevRequirementsUpdate struct {
	CriteriaUpdates       []CriteriaUpdate             `json:"criteria_updates,omitempty" jsonschema:"description=Updates to apply to acceptance criteria"`
	LearningUpdates       []RequirementsLearningUpdate `json:"learning_updates,omitempty" jsonschema:"description=Updates to apply to learnings"`
	Overview              *string                      `json:"overview,omitempty" jsonschema:"description=Update the overview"`
	RequirementsFinalized *bool                        `json:"requirements_finalized,omitempty" jsonschema:"description=Update the requirements finalized flag"`
}

var updateDevRequirementsTool = llm.Tool{
	Name:        "update_dev_requirements",
	Description: "Updates existing dev requirements incrementally without re-outputting the entire structure. Use this to edit, delete, or insert acceptance criteria and learnings.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&DevRequirementsUpdate{}),
}

func applyDevRequirementsUpdates(reqs DevRequirements, update DevRequirementsUpdate) (DevRequirements, error) {
	criteriaOps := make([]sliceUpdate, len(update.CriteriaUpdates))
	for i, u := range update.CriteriaUpdates {
		criteriaOps[i] = sliceUpdate{Index: u.Index, Operation: u.Operation, Content: u.Content}
	}
	var err error
	reqs.AcceptanceCriteria, err = applySliceUpdates(reqs.AcceptanceCriteria, criteriaOps, "criteria")
	if err != nil {
		return reqs, err
	}

	learningOps := make([]sliceUpdate, len(update.LearningUpdates))
	for i, u := range update.LearningUpdates {
		learningOps[i] = sliceUpdate{Index: u.Index, Operation: u.Operation, Content: u.Content}
	}
	reqs.Learnings, err = applySliceUpdates(reqs.Learnings, learningOps, "learning")
	if err != nil {
		return reqs, err
	}

	if update.Overview != nil {
		reqs.Overview = *update.Overview
	}

	if update.RequirementsFinalized != nil {
		reqs.Complete = *update.RequirementsFinalized
	}

	return reqs, nil
}

type sliceUpdate struct {
	Index     int
	Operation string
	Content   *string
}

// applySliceUpdates applies a batch of updates to a string slice, treating all
// indices as positions in the original slice. Operations are reordered so that
// edits apply first, then deletes (highest-index-first to preserve positions),
// then inserts (lowest-index-first, adjusted for prior deletes).
func applySliceUpdates(items []string, updates []sliceUpdate, label string) ([]string, error) {
	var edits, deletes, inserts []sliceUpdate
	for _, u := range updates {
		switch u.Operation {
		case "edit":
			edits = append(edits, u)
		case "delete":
			deletes = append(deletes, u)
		case "insert":
			inserts = append(inserts, u)
		default:
			return items, fmt.Errorf("invalid operation %q for %s update", u.Operation, label)
		}
	}

	origLen := len(items)

	// Edits first: indices refer to the original slice
	for _, u := range edits {
		if u.Index < 0 || u.Index >= origLen {
			return items, fmt.Errorf("%s index %d out of range for edit operation", label, u.Index)
		}
		if u.Content != nil {
			items[u.Index] = *u.Content
		}
	}

	// Validate all delete indices against the original length
	for _, u := range deletes {
		if u.Index < 0 || u.Index >= origLen {
			return items, fmt.Errorf("%s index %d out of range for delete operation", label, u.Index)
		}
	}
	// Sort deletes descending so removals don't shift earlier indices
	sort.Slice(deletes, func(i, j int) bool {
		return deletes[i].Index > deletes[j].Index
	})
	for _, u := range deletes {
		items = append(items[:u.Index], items[u.Index+1:]...)
	}

	// Validate all insert indices against the original length
	for _, u := range inserts {
		if u.Index < 0 || u.Index > origLen {
			return items, fmt.Errorf("%s index %d out of range for insert operation", label, u.Index)
		}
	}
	// Sort inserts ascending by original index
	sort.Slice(inserts, func(i, j int) bool {
		return inserts[i].Index < inserts[j].Index
	})
	// Adjust insert indices: each insert's effective position shifts by the
	// number of deletes that occurred before it and the number of inserts
	// already applied before it.
	for i, u := range inserts {
		content := ""
		if u.Content != nil {
			content = *u.Content
		}
		deletedBefore := 0
		for _, d := range deletes {
			if d.Index < u.Index {
				deletedBefore++
			}
		}
		adjustedIndex := u.Index - deletedBefore + i
		if adjustedIndex == len(items) {
			items = append(items, content)
		} else {
			items = append(items[:adjustedIndex], append([]string{content}, items[adjustedIndex:]...)...)
		}
	}

	return items, nil
}

/* Represents low-level requirements for a software engineer to implement a feature or fix a bug etc */
type DevRequirements struct {
	Overview string `json:"overview" jsonschema:"description=Overview of the requirements"`
	// Move UserRequirements to a higher-level ProductRequirements struct, which is expected to require multiple tasks, each of which will later have its own DevRequirements
	//UserRequirements []string `json:"user_facing_requirements" jsonschema:"description=List of all product requirements\\: A brief\\, unambiguous specification of the user-facing behavior of the product\\, with sufficient detail for a senior software engineer to start tech design and/or development\\, but no more. Does not specify implementation details or steps."`
	AcceptanceCriteria []string `json:"acceptance_criteria" jsonschema:"description=List of acceptance criteria\\, formatted with markdown including backticks for code references. Each criterium supports a single internal unordered list denoted by '    -'. Each acceptance criterium is a specific condition that must be met before the requirements are considered completely satisfied. This creates a brief\\, unambiguous specification\\, with sufficient detail for a senior software engineer to start development. Limit the length of this list to make it easier to review. Use declarative language reather than imperative."`
	Complete           bool     `json:"requirements_finalized" jsonschema:"description=Are the requirements complete\\, unambiguous and self-consistent?"`
	Learnings          []string `json:"learnings" jsonschema:"description=List of learnings about existing code that will help the software engineer fulfill the requirements. Does not specify approach to solution. It also contains learnings that will help us refine requirements in the future."`
}

// backcompat for rename of are_requirements_complete -> requirements_finalized
func (d *DevRequirements) UnmarshalJSON(data []byte) error {
	type Alias DevRequirements
	aux := &struct {
		*Alias
		CompleteOld *bool `json:"are_requirements_complete"`
		CompleteNew *bool `json:"requirements_finalized"`
	}{
		Alias: (*Alias)(d),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Priority: new field > old field
	if aux.CompleteNew != nil {
		d.Complete = *aux.CompleteNew
	} else if aux.CompleteOld != nil {
		d.Complete = *aux.CompleteOld
	}
	// If neither is present, Complete remains false (zero value)

	return nil
}

//type ProjectRequirement struct {
//	Requirement string `json:"requirement" jsonschema:"description=A single\\, specific requirement. Brief\\, unambiguous specification\\, with sufficient detail for a senior software engineer to start tech design and/or development"`
//	//Priority    string `json:"priority" jsonschema:"enum=P0,enum=P1,enum=P2,description=P0: non-negotiable i.e. must be done\\, P1: not blocking if it's hard to achieve\\, P2: nice to have"`
//}

var markdownListPattern = regexp.MustCompile(`^\s*(-|\d+\.|[a-z]\.|[iv]+\.)\s+`)

func (r DevRequirements) String() string {
	var criteria string
	var learnings string
	for _, ac := range r.AcceptanceCriteria {
		if markdownListPattern.MatchString(ac) {
			criteria += fmt.Sprintf("%s\n", ac)
		} else {
			criteria += fmt.Sprintf("- %s\n", ac)
		}
	}
	for _, learning := range r.Learnings {
		if markdownListPattern.MatchString(learning) {
			learnings += fmt.Sprintf("%s\n", learning)
		} else {
			learnings += fmt.Sprintf("- %s\n", learning)
		}
	}
	return fmt.Sprintf("#### Overview:\n%s\n\n#### Acceptance Criteria:\n%s\n\n#### Learnings:\n%s", r.Overview, criteria, learnings)
}

type buildDevRequirementsState struct {
	contextSizeExtension int
	devRequirements      DevRequirements
}

func BuildDevRequirements(dCtx DevContext, initialInfo InitialDevRequirementsInfo) (*DevRequirements, error) {
	return RunSubflow(dCtx, "dev_requirements", "Dev Requirements", func(_ domain.Subflow) (*DevRequirements, error) {
		return buildDevRequirementsSubflow(dCtx, initialInfo)
	})
}

func buildDevRequirementsSubflow(dCtx DevContext, initialInfo InitialDevRequirementsInfo) (*DevRequirements, error) {
	// Step 1: prepare code context + retrieve mission
	contextSizeExtension := 0
	if initialInfo.Context == "" {
		codeContext, fullCodeContext, err := PrepareInitialCodeContext(dCtx, initialInfo.Requirements, nil, nil)
		initialInfo.Context = codeContext
		contextSizeExtension = len(fullCodeContext) - len(codeContext)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare code context: %v", err)
		}
	}
	if initialInfo.Mission == "" {
		initialInfo.Mission = dCtx.RepoConfig.Mission
	}

	// prepend a concise repository summary to the other code context in the initial prompt
	version := workflow.GetVersion(dCtx, "initial-code-repo-summary", workflow.DefaultVersion, 2)
	if version >= 2 && fflag.IsEnabled(dCtx, fflag.InitialRepoSummary) {
		repoSummary, err := GetRepoSummaryForPrompt(dCtx, initialInfo.Requirements, 5000)
		if err != nil {
			return nil, fmt.Errorf("failed to get repo summary: %w", err)
		}
		initialInfo.Context = repoSummary + "\n\n" + initialInfo.Context
	}

	// Step 2: run the dev requirements loop
	chatHistory := NewVersionedChatHistory(dCtx, dCtx.WorkspaceId)
	addDevRequirementsPrompt(dCtx, chatHistory, initialInfo)
	initialState := &buildDevRequirementsState{
		contextSizeExtension: contextSizeExtension,
	}

	feedbackIterations := 5
	v := workflow.GetVersion(dCtx, "dev-requirements-feedback-iterations", workflow.DefaultVersion, 1)
	if v == 1 {
		// TODO when tool calls are not finding things automatically, provide
		// better hints for how to find things after Nth iteration, before going
		// to human-based support. Eg fuzzy search or embedding search etc.
		// Maybe provide that as a tool or even run that tool automatically.
		feedbackIterations = 9
	}
	if cfg, ok := dCtx.RepoConfig.AgentConfig[common.PlanningKey]; ok && cfg.AutoIterations > 0 {
		feedbackIterations = cfg.AutoIterations
	}
	return LlmLoop(dCtx, chatHistory, buildDevRequirementsIteration, WithInitialState(initialState), WithFeedbackEvery(feedbackIterations))
}

func buildDevRequirementsIteration(iteration *LlmIteration) (*DevRequirements, error) {
	state, ok := iteration.State.(*buildDevRequirementsState)
	if !ok {
		return nil, fmt.Errorf("Invalid llm iteration state type, expected *buildDevRequirementsState: %v", iteration.State)
	}

	maxLength := min(defaultMaxChatHistoryLength+state.contextSizeExtension, extendedMaxChatHistoryLength)
	ManageChatHistory(iteration.ExecCtx.Context, iteration.ChatHistory, iteration.ExecCtx.WorkspaceId, maxLength)

	hasExistingRequirements := len(state.devRequirements.AcceptanceCriteria) > 0 || state.devRequirements.Overview != ""

	var chatResponse common.MessageResponse
	var err error
	if v := workflow.GetVersion(iteration.ExecCtx, "dev-requirements-cleanup-cancel-internally", workflow.DefaultVersion, 1); v == 1 {
		chatResponse, err = generateDevRequirements(iteration.ExecCtx, iteration.ChatHistory, hasExistingRequirements)
	} else {
		// old version: new one does this in outer LlmLoop
		chatCtx := iteration.ExecCtx.WithCancelOnPause()
		chatResponse, err = generateDevRequirements(chatCtx, iteration.ChatHistory, hasExistingRequirements)
		if iteration.ExecCtx.GlobalState != nil && iteration.ExecCtx.GlobalState.Paused {
			return nil, nil // continue the loop: UserRequestIfPaused will handle the pause
		}
	}
	if err != nil {
		return nil, err
	}
	AppendChatHistory(iteration.ExecCtx, iteration.ChatHistory, chatResponse.GetMessage())

	if len(chatResponse.GetMessage().GetToolCalls()) > 0 {
		var recordedReqs *DevRequirements

		customHandlers := map[string]func(DevContext, llm.ToolCall) (ToolCallResponseInfo, error){
			recordDevRequirementsTool.Name: func(dCtx DevContext, tc llm.ToolCall) (ToolCallResponseInfo, error) {
				devReq, unmarshalErr := unmarshalDevRequirements(tc.Arguments)
				if unmarshalErr != nil {
					return ToolCallResponseInfo{ToolResultContent: llm2.TextContentBlocks(unmarshalErr.Error()), FunctionName: tc.Name, ToolCallId: tc.Id, IsError: true}, nil
				}
				state.devRequirements = devReq
				if devReq.Complete {
					userResponse, approveErr := ApproveDevRequirements(dCtx, devReq)
					if approveErr != nil {
						return ToolCallResponseInfo{}, fmt.Errorf("error approving dev requirements: %v", approveErr)
					}
					iteration.AutoIterationCount = 0
					if userResponse.Approved != nil && *userResponse.Approved {
						recordedReqs = &devReq
						return ToolCallResponseInfo{ToolResultContent: llm2.TextContentBlocks("Requirements recorded and approved."), FunctionName: tc.Name, ToolCallId: tc.Id}, nil
					} else {
						feedback := fmt.Sprintf("Requirements were not approved. Current state:\n%s\n\nPlease try again, taking this feedback into account:\n\n%s", devReq.String(), userResponse.Content)
						return ToolCallResponseInfo{ToolResultContent: llm2.TextContentBlocks(feedback), FunctionName: tc.Name, ToolCallId: tc.Id}, nil
					}
				} else {
					return ToolCallResponseInfo{ToolResultContent: llm2.TextContentBlocks("Recorded partial requirements, but requirements are not finalized yet based on the \"requirements_finalized\" boolean field value being set to false. Do some more research or thinking to finalize the requirements, as needed. If you need more details or clarification from the user, use the " + getHelpOrInputTool.Name + " tool. Then record the finalized requirements again in full, or use update_dev_requirements to make incremental changes."), FunctionName: tc.Name, ToolCallId: tc.Id}, nil
				}
			},
			updateDevRequirementsTool.Name: func(dCtx DevContext, tc llm.ToolCall) (ToolCallResponseInfo, error) {
				var update DevRequirementsUpdate
				if err := json.Unmarshal([]byte(llm.RepairJson(tc.Arguments)), &update); err != nil {
					return ToolCallResponseInfo{ToolResultContent: llm2.TextContentBlocks(fmt.Sprintf("failed to unmarshal update: %v", err)), FunctionName: tc.Name, ToolCallId: tc.Id, IsError: true}, nil
				}

				updatedReqs, err := applyDevRequirementsUpdates(state.devRequirements, update)
				if err != nil {
					return ToolCallResponseInfo{ToolResultContent: llm2.TextContentBlocks(fmt.Sprintf("failed to apply updates: %v", err)), FunctionName: tc.Name, ToolCallId: tc.Id, IsError: true}, nil
				}

				state.devRequirements = updatedReqs

				if updatedReqs.Complete {
					userResponse, approveErr := ApproveDevRequirements(dCtx, updatedReqs)
					if approveErr != nil {
						return ToolCallResponseInfo{}, fmt.Errorf("error approving dev requirements: %v", approveErr)
					}
					iteration.AutoIterationCount = 0
					if userResponse.Approved != nil && *userResponse.Approved {
						recordedReqs = &updatedReqs
						return ToolCallResponseInfo{ToolResultContent: llm2.TextContentBlocks("Requirements updated and approved."), FunctionName: tc.Name, ToolCallId: tc.Id}, nil
					} else {
						feedback := fmt.Sprintf("Requirements updated but not approved. Current state:\n%s\n\nPlease try again, taking this feedback into account:\n\n%s", updatedReqs.String(), userResponse.Content)
						return ToolCallResponseInfo{ToolResultContent: llm2.TextContentBlocks(feedback), FunctionName: tc.Name, ToolCallId: tc.Id}, nil
					}
				}

				return ToolCallResponseInfo{ToolResultContent: llm2.TextContentBlocks(fmt.Sprintf("Requirements updated successfully. Current state:\n%s", updatedReqs.String())), FunctionName: tc.Name, ToolCallId: tc.Id}, nil
			},
		}

		toolCallResults := handleToolCalls(iteration.ExecCtx, chatResponse.GetMessage().GetToolCalls(), customHandlers)

		for _, res := range toolCallResults {
			addToolCallResponse(iteration.ExecCtx, iteration.ChatHistory, res)

			if len(res.TextResponse()) > 5000 {
				state.contextSizeExtension += len(res.TextResponse()) - 5000
			}

			if res.FunctionName == getHelpOrInputTool.Name {
				iteration.AutoIterationCount = 0
			}
		}

		if recordedReqs != nil {
			return recordedReqs, nil
		}
	} else if chatResponse.GetStopReason() == string(openai.FinishReasonStop) || chatResponse.GetStopReason() == string(openai.FinishReasonToolCalls) {
		// TODO try to extract the dev requirements from the content in this case and treat it as if it was a tool call
		feedbackInfo := FeedbackInfo{Feedback: "Expected a tool call to record the dev requirements, but didn't get it. Embedding the json in the content is not sufficient. Please record the plan via the " + recordDevRequirementsTool.Name + " tool. If you need more details or clarification from the user to finalize, use the " + getHelpOrInputTool.Name + " tool."}
		addDevRequirementsPrompt(iteration.ExecCtx, iteration.ChatHistory, feedbackInfo)
	} else { // FIXME handle other stop reasons with more specific logic
		//return nil, fmt.Errorf("expected OpenAI chat completion finish reason to be stop or tool calls, got: %v", chatResponse.StopReason)
		// NOTE: we continue the loop instead of failing here so we can attempt to recover
		feedbackInfo := FeedbackInfo{Feedback: "Expected a tool call to record the dev requirements, but didn't get it. Embedding the json in the content is not sufficient. Please record the plan via the " + recordDevRequirementsTool.Name + " tool. If you need more details or clarification from the user to finalize, use the " + getHelpOrInputTool.Name + " tool."}
		addDevRequirementsPrompt(iteration.ExecCtx, iteration.ChatHistory, feedbackInfo)
	}

	if err != nil {
		return nil, err // break the loop with an error
	}

	return nil, nil // continue the loop
}

func generateDevRequirements(dCtx DevContext, chatHistory *persisted_ai.ChatHistoryContainer, hasExistingRequirements bool) (common.MessageResponse, error) {
	modelConfig := dCtx.GetModelConfig(common.PlanningKey, 0, "default")

	tools := []*llm.Tool{
		&recordDevRequirementsTool,
		currentGetSymbolDefinitionsTool(),
		&bulkSearchRepositoryTool,
		&bulkReadFileTool,
	}
	if hasExistingRequirements {
		tools = append(tools, &updateDevRequirementsTool)
	}
	if supportsImageToolResults(modelConfig) {
		tools = append(tools, &readImageTool)
	}
	if !dCtx.RepoConfig.DisableHumanInTheLoop {
		tools = append(tools, &getHelpOrInputTool)
	}

	// random order of tools to avoid bias in the LLM's use of the tools
	// NOTE: disabled shuffling as it can mess with cache hit rate
	/*
		rand.Shuffle(len(tools), func(i, j int) {
			tools[i], tools[j] = tools[j], tools[i]
		})
	*/

	options := llm2.Options{
		Params: llm2.Params{
			Tools: tools,
			ToolChoice: llm.ToolChoice{
				Type: llm.ToolChoiceTypeAuto, // TODO test with llm.ToolChoiceTypeRequired
			},
			ModelConfig: modelConfig,
		},
	}
	return TrackedToolChat(dCtx, "dev_requirements", options, chatHistory)
}

// TrackedToolChat delegates to persisted_ai.ExecuteChatStream for LLM calls,
// taking chat history as a separate parameter from options.
func TrackedToolChat(dCtx DevContext, actionType string, options llm2.Options, chatHistory *persisted_ai.ChatHistoryContainer) (common.MessageResponse, error) {
	if chatHistory == nil {
		return nil, fmt.Errorf("chatHistory is required for TrackedToolChat")
	}

	streamInput := persisted_ai.StreamInput{
		Options:     options,
		Secrets:     *dCtx.Secrets,
		ChatHistory: chatHistory,
		WorkspaceId: dCtx.WorkspaceId,
		Providers:   dCtx.Providers,
	}

	actionCtx := dCtx.NewActionContext("generate." + actionType)
	actionCtx.ActionParams = streamInput.ActionParams()
	return Track(actionCtx, func(flowAction *domain.FlowAction) (common.MessageResponse, error) {
		if options.Params.ModelConfig.Provider == "" {
			options.Params.ModelConfig = dCtx.GetModelConfig(common.DefaultKey, 0, "default")
			streamInput.Options = options
		}
		streamInput.FlowId = workflow.GetInfo(dCtx).WorkflowExecution.ID
		streamInput.FlowActionId = flowAction.Id

		response, err := persisted_ai.ExecuteChatStream(
			actionCtx.FlowActionContext(),
			streamInput,
		)
		if err != nil {
			return nil, fmt.Errorf("error during tracked tool chat action '%s': %v", actionType, err)
		}

		return response, nil
	})
}

func unmarshalDevRequirements(jsonStr string) (DevRequirements, error) {
	var devRequirements DevRequirements
	err := json.Unmarshal([]byte(llm.RepairJson(jsonStr)), &devRequirements)
	if err != nil {
		return DevRequirements{}, fmt.Errorf("failed to unmarshal dev requirements json: %v", err)
	}
	return devRequirements, nil
}

func addDevRequirementsPrompt(ctx workflow.Context, chatHistory *persisted_ai.ChatHistoryContainer, promptInfo PromptInfo) {
	var content string
	role := llm.ChatMessageRoleUser
	cacheControl := ""
	contextType := ""
	switch info := promptInfo.(type) {
	case InitialDevRequirementsInfo:
		content = getInitialDevRequirementsPrompt(info.Mission, info.Context, info.Requirements)
		cacheControl = "ephemeral"
		contextType = ContextTypeInitialInstructions
	case FeedbackInfo:
		content = renderGeneralFeedbackPrompt(info.Feedback, info.Type)
	case ToolCallResponseInfo:
		addToolCallResponse(ctx, chatHistory, info)
		return
	default:
		panic("Unsupported prompt type for dev requirements: " + promptInfo.GetType())
	}
	AppendChatHistory(ctx, chatHistory, llm.ChatMessage{
		Role:         role,
		Content:      content,
		CacheControl: cacheControl,
		ContextType:  contextType,
	})
}

func addToolCallResponse(ctx workflow.Context, chatHistory *persisted_ai.ChatHistoryContainer, info ToolCallResponseInfo) {
	hasRichContent := false
	for _, cb := range info.ToolResultContent {
		if cb.Type != llm2.ContentBlockTypeText {
			hasRichContent = true
			break
		}
	}

	if hasRichContent {
		msg := &llm2.Message{
			Role: llm2.RoleUser,
			Content: []llm2.ContentBlock{{
				Type: llm2.ContentBlockTypeToolResult,
				ToolResult: &llm2.ToolResultBlock{
					ToolCallId: info.ToolCallId,
					Name:       info.FunctionName,
					IsError:    info.IsError,
					Content:    info.ToolResultContent,
				},
			}},
		}
		AppendChatHistory(ctx, chatHistory, msg)
		return
	}

	AppendChatHistory(ctx, chatHistory, llm.ChatMessage{
		Role:       llm.ChatMessageRoleTool,
		Content:    info.TextResponse(),
		Name:       info.FunctionName,
		ToolCallId: info.ToolCallId,
		IsError:    info.IsError,
	})
}

func ApproveDevRequirements(dCtx DevContext, devReq DevRequirements) (*flow_action.UserResponse, error) {
	// Create a RequestForUser struct for approval request
	req := flow_action.RequestForUser{
		Content:       "Please approve or reject these requirements:\n\n" + devReq.String() + "\n\nDo you approve these requirements? If not, please provide feedback on what needs to be changed.",
		RequestParams: map[string]interface{}{"approveTag": "approve_plan", "rejectTag": "reject_plan"},
	}
	return GetUserApproval(dCtx, "dev_requirements", req.Content, req.RequestParams)
}

// TODO we should determine if the context is too large programmatically instead
// of depending on the LLM's notion of "large", which is bound to be
// extremely unreliable
// TODO /gen move these prompts into the prompts mustache format and create
// prompt render functions
func getInitialDevRequirementsPrompt(mission, context, requirements string) string {
	return fmt.Sprintf(`
# CONTEXT

%s

Note: this context is not guaranteed to be comprehensive.

# MISSION

%s

# ROLE

You are assisting a user transform their software development task description
into a more complete one with less ambiguous requirements, while providing
clarity around the high-level implementation approach. Your job is to understand
the user's intent through context and questions for the user, and capture it
accurately in a brief, readable manner.

# OUTPUT

Very concise acceptance criteria for the user to review, and for software
developers to consume and use to implement a feature or fix a bug. The
requirements should be complete, unambiguous and self-consistent. Includes
detailed learnings that will help the engineers who will implement the
requirements get started faster.

The final requirements must include anything from the chat history that is
required to understand it out of context, because these requirements will be
used directly, without any of the associated chat history we had. The final
artifacts, both the acceptance criteria, and the learnings, must stand on their
own and must not lose any critical details provided in the task description.

# INSTRUCTIONS

Thinking step-by-step, analyze the task description. Retrieve additional context
if needed, and then ask questions to clarify if needed. Use it to create a set
of concise, yet reasonably comprehensive acceptance criteria, keeping in mind
the mission of the product and the context gathered so far.

Follow these steps precisely:

1. Determine what information is needed to finalize requirements.
2. Try to obtain that information yourself based on existing context and tools.
3. Before recording requirements, you must first write out a list of
assumptions, potential questions and decisions to confirm with the user to
clarify their intent and any aspects of the requirements or the high-level
implementation approach.
4. For each candidate question/decision, determine whether the answer to the
question is consequential. If the end result would be substantially different
based on the answer, the decision is consequential. If the decision relates to
the recommended high-level implementation approach, but other completely
different yet valid approaches exist, it's a consequential decision as well: the
user is the architect & technical designer whose vision you must incorporate.
5. If there are any consequential questions to ask the user, do so via the
`+getHelpOrInputTool.Name+` tool. It is possible but uncommon for there to be no
such questions.
6. Record the requirements, even if not yet completely finalized, using the
`+recordDevRequirementsTool.Name+` tool.

The `+getHelpOrInputTool.Name+` tool will allow you to ask the user for more
information, when required. If the task description is already very clear and
unambiguous in regards to both requirements and approach, you can skip this
step, but usually there are important details that are not mentioned in the
initial task and that can't be inferred with confidence that you will have to
ask about before finalizing requirements. You can ask several questions in one
go and should do so if multiple areas need clarification. Make your questions
concise.

You are able to also read code via the `+currentGetSymbolDefinitionsTool().Name+`
tool to understand how to interpret technical requirements as well as understand
the status quo before suggesting changes that software engineers will need to
implement. Searching the repository for relevant text which may be strewn across
comments or READMEs, using the `+bulkSearchRepositoryTool.Name+` tool, may also
be helpful.

Consider how the different requirements interact with each other in the context
of the codebase. If the requirements clash with each other in any way, try to
resolve the conflict yourself after considering what option would be best, and
confirm your resolution with the user.

When you have enough information to produce unambiguous requirements, do so via
the `+recordDevRequirementsTool.Name+` tool, including any helpful learnings that
will help the engineers who will implement the requirements get started faster.
Existing related functionality that should be maintained as-is without changes
should be added to learnings rather than acceptance criteria.

If we already have a large amount of code context, but it's not enough for fully
comprehensive requirements, DO NOT ask for more. Instead, record a partial list
of requirements, with specific learnings from the process that will help you
continue where you left off. Include file names and names of functions etc that
are relevant so that we you can easily pick up right where you last left off.

If there are crucial details in the original task description below that need to
be kept, then copy them into either requirements or learnings. Things like code
snippets should almost always be copied verbatim into learnings for instance.
The original task description and any chat history will be wiped away with the
final new requirements you come up with, so make sure not to lose any essential
details, examples or code.

DO NOT make any consequential/top-level decisions on behalf of the user without
checking with them, especially if there are multiple potential options, unless
they tell you to choose on their behalf. When multiple good approaches exist and
the codebase does not already have a strong pattern that is obviously preferred,
definitely ask the user what they prefer.

# TASK DESCRIPTION

%s`, context, mission, requirements)
}
