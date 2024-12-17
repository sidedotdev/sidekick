package dev

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sidekick/agent"
	"sidekick/db"
	"sidekick/llm"
	"sidekick/models"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
	"sidekick/utils"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/invopop/jsonschema"
	"github.com/redis/go-redis/v9"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

type DevAgent struct {
	TemporalClient    client.Client
	TemporalTaskQueue string
	WorkspaceId       string
	ChatHistory       *[]llm.ChatMessage
}

type DevActionData struct {
	WorkflowId  string
	ChatHistory *[]llm.ChatMessage
}

func (ia *DevAgent) getFirstExecutionRunID(ctx context.Context, workflowID string) string {
	handle := ia.TemporalClient.GetWorkflow(ctx, workflowID, "")
	firstExecutionRunID := handle.GetRunID()
	return firstExecutionRunID
}

func (ia *DevAgent) workRequest(ctx context.Context, parentId, request, flowType string, flowOptions map[string]interface{}) (models.Flow, error) {
	devManagerWorkflowId, err := ia.findOrStartDevAgentManagerWorkflow(ctx, ia.WorkspaceId)
	if err != nil {
		return models.Flow{}, fmt.Errorf("error finding or starting dev manager workflow: %w", err)
	}

	workRequest := WorkRequest{ParentId: parentId, Input: request, FlowType: flowType, FlowOptions: flowOptions}
	//updateHandle, err := ia.TemporalClient.UpdateWorkflow(ctx, devManagerWorkflowId, "", UpdateNameWorkRequest, workRequest)
	firstRunId := ia.getFirstExecutionRunID(ctx, devManagerWorkflowId)
	updateRequest := client.UpdateWorkflowOptions{
		UpdateID:   uuid.New().String(),
		WorkflowID: devManagerWorkflowId,
		UpdateName: UpdateNameWorkRequest,
		Args:       []interface{}{workRequest},
		// FirstExecutionRunID specifies the RunID expected to identify the first
		// run in the workflow execution chain. If this expectation does not match
		// then the server will reject the update request with an error.
		FirstExecutionRunID: firstRunId,

		// How this RPC should block on the server before returning.
		WaitForStage: client.WorkflowUpdateStageAccepted,
	}
	updateHandle, err := ia.TemporalClient.UpdateWorkflow(ctx, updateRequest)
	if err != nil {
		return models.Flow{}, fmt.Errorf("error issuing Update request: %w\n%v", err, updateRequest)
	}

	var flow models.Flow
	err = updateHandle.Get(ctx, &flow)
	if err != nil {
		return models.Flow{}, fmt.Errorf("update encountered an error: %w", err)
	}
	return flow, nil
}

func (ia *DevAgent) RelayResponse(ctx context.Context, userResponse UserResponse) error {
	log.Printf("relaying response to workflow: %s\n", userResponse.TargetWorkflowId)
	devManagerWorkflowId, err := ia.findOrStartDevAgentManagerWorkflow(ctx, ia.WorkspaceId)
	if err != nil {
		return fmt.Errorf("error finding or starting dev manager workflow: %w", err)
	}

	err = ia.TemporalClient.SignalWorkflow(ctx, devManagerWorkflowId, "", SignalNameUserResponse, userResponse)
	return err
}

func (ia *DevAgent) otherResponse(ctx context.Context, topicId string, chatHistory *[]llm.ChatMessage, deltaStringChan chan<- string) (string, error) {
	deltaChan := make(chan llm.ChatMessageDelta, 10)
	input := buildOtherResponseInput(chatHistory)
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		for delta := range deltaChan {
			deltaStr := strings.Builder{}
			if delta.Content != "" {
				deltaStr.WriteString(delta.Content)
			} else if delta.ToolCalls != nil {
				for _, toolCall := range delta.ToolCalls {
					if toolCall.Name != "" {
						deltaStr.WriteString(fmt.Sprintf("functionName = %s\n", toolCall.Name))
					}
					if toolCall.Arguments != "" {
						deltaStr.WriteString(toolCall.Arguments)
					}
				}
			}
			deltaStringChan <- deltaStr.String()
		}
		wg.Done()
	}()
	oai := llm.OpenaiToolChat{}
	response, err := oai.ChatStream(ctx, input, deltaChan)
	if err != nil {
		return "", err
	}

	wg.Wait()
	return response.ChatMessage.Content, nil
}

func buildOtherResponseInput(chatHistory *[]llm.ChatMessage) llm.ToolChatOptions {
	const prompt = "The last message from the user was not recognized as a work request or a response to a question etc. Ask the user what they intended using the available context."
	newMessage := llm.ChatMessage{
		Role:    llm.ChatMessageRoleSystem,
		Content: prompt,
	}
	tempChatHistory := make([]llm.ChatMessage, len(*chatHistory))
	copy(tempChatHistory, *chatHistory)
	tempChatHistory = append(tempChatHistory, newMessage)

	return llm.ToolChatOptions{
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: secret_manager.EnvSecretManager{},
		},
		Params: llm.ToolChatParams{
			Messages: tempChatHistory,
		},
	}
}

func (ia DevAgent) findOrStartDevAgentManagerWorkflow(ctx context.Context, workspaceId string) (string, error) {
	workflowId := workspaceId + "_dev_manager"
	workflowRetryPolicy := &temporal.RetryPolicy{
		InitialInterval:        time.Second,
		BackoffCoefficient:     2.0,
		MaximumInterval:        100 * time.Second,
		MaximumAttempts:        1000,       // up to 1000 retries
		NonRetryableErrorTypes: []string{}, // TODO make out-of-bounds errors non-retryable
	}
	options := client.StartWorkflowOptions{
		ID:                    workflowId,
		TaskQueue:             ia.TemporalTaskQueue,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		RetryPolicy:           workflowRetryPolicy,
		SearchAttributes:      map[string]interface{}{"WorkspaceId": workspaceId},
	}

	we, err := ia.TemporalClient.ExecuteWorkflow(ctx, options, DevAgentManagerWorkflow, DevAgentManagerWorkflowInput{
		WorkspaceId: workspaceId,
	})
	if err != nil {
		// fmt.Printf("Failed to start dev manager workflow: %v\n", err)
		return "", err
	}
	// fmt.Printf("Started dev manager workflow: %s\n", we.GetID())
	return we.GetID(), nil
}

// PerformAction carries out the action specified by the action parameter.
// It builds up a response string from response deltas and sends an Event for each response delta.
// Depending on the action type, it either initiates a work request, relays a response,
// cancels a workflow, processes it with otherResponse method, or returns an error for an unknown action type.
func (ia DevAgent) PerformAction(ctx context.Context, action agent.AgentAction, events chan<- agent.Event) (string, error) {
	// build up full response string from response deltas
	var responseBuilder strings.Builder
	responseDeltaChan := make(chan string, 10)
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		for responseDelta := range responseDeltaChan {
			events <- agent.Event{
				Type: "message/contentDelta",
				Data: utils.PanicJSON(responseDelta),
			}
			responseBuilder.WriteString(responseDelta)
		}
		wg.Done()
	}()

	// do the action
	data := action.Data.(DevActionData)
	lastMessage := (*data.ChatHistory)[len(*data.ChatHistory)-1].Content
	var err error
	if action.Type == "new_development_request" {
		var flow models.Flow
		// TODO emit an event to the user that the workflow is starting
		flow, err = ia.workRequest(ctx, action.TopicId, lastMessage, "basic_dev", map[string]any{})
		if err == nil {
			// TODO llm-ify this response
			responseDeltaChan <- "Started workflow: " + flow.Id
		}
	} else if action.Type == "request_response" {
		userResponse := UserResponse{
			TargetWorkflowId: data.WorkflowId,
			Content:          lastMessage,
		}
		// TODO implement response / error handling
		// TODO emit an event to the user that the response is being relayed
		// TODO respond to the user with a llm-generated message that the response is being relayed
		ia.RelayResponse(ctx, userResponse)
	} else if action.Type == "cancel_workflow" {
		// TODO implement response / error handling
		// TODO emit an event to the user that the workflow is being canceled
		ia.TerminateWorkflowIfExists(ctx, data.WorkflowId)
		// TODO respond to the user with a llm-generated message that the workflow was canceled
	} else if action.Type == "other" {
		_, err = ia.otherResponse(ctx, action.TopicId, data.ChatHistory, responseDeltaChan)
	} else {
		errorMessage := fmt.Sprintf("Unknown action type: %s", action.Type)
		err = errors.New(errorMessage)
	}

	close(responseDeltaChan)
	wg.Wait()

	// NOTE: both could have values potentially. the current logic doesn't
	// result in that though, but it could in the future
	return responseBuilder.String(), err
}

type RequestResponseInfo struct {
	WorkflowId string `json:"workflow_id" jsonschema:"description=The workflow ID tied to the request that the response is for."`
}

// DevInferredIntent struct maps to the parameters for the categorize openai function
type DevInferredIntent struct {
	Analysis            string              `json:"analysis" jsonschema:"description=Brief analysis of which intent type best matches the user's latest/last message and why. This analysis must give special consideration to whether the message is a response to a previous request or not."`
	IntentType          string              `json:"intentType" jsonschema:"enum=request_response,enum=new_development_request,enum=other,description=\"new_development_request\" means that the user is making the initial request for something to be done with some initial requirements\\, unrelated to the previous request from the assistant. This is never the intent if the user is responding to someone\\, in which case it's a request_response. \"request_response\" means that the user is responding to a request from the assistant. If the assistant just asked questions or asked for help right before the last user message\\, it's a request_response if it is related to the request. If the assistant did NOT make a request\\, then the intent cannot possibly be a request_response. \"other\" means anything else or unknown/unclear."`
	RequestResponseInfo RequestResponseInfo `json:"requestResponseInfo,omitempty" jsonschema:"description=You MUST provide this if the intent type is \"request_response\"."`
}

var inferIntentTool = llm.Tool{
	Name:        "categorize",
	Description: "Used to infer the intent of the user's latest message, with older messages provided for context. The analysis MUST be provided first, before specifying the intent type",
	Parameters:  (&jsonschema.Reflector{ExpandedStruct: true}).Reflect(&DevInferredIntent{}),
}

func (ia DevAgent) InferLastMessageIntent(ctx context.Context) (intent DevInferredIntent, err error) {
	// TODO goes away when we move this to a new InceptionConversationWorkflow
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		log.Println("Missing Redis address, using default localhost:6379")
		redisAddr = "localhost:6379"
	}
	redisClient := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: "", // no password set
		DB:       0,  // use default DB
	})
	_, err = redisClient.Ping(context.Background()).Result()
	if err != nil {
		log.Fatal("Failed to connect to Redis", err)
	}
	la := persisted_ai.LlmActivities{
		FlowEventAccessor: &db.RedisFlowEventAccessor{Client: redisClient},
	}

	chatHistoryCopy := make([]llm.ChatMessage, len(*ia.ChatHistory))
	copy(chatHistoryCopy, *ia.ChatHistory)
	attempts := 0
	prompt := ""
	for {
		input := llm.BuildFuncCallInput(&chatHistoryCopy, true, inferIntentTool, prompt)
		if err = persisted_ai.GetOpenaiFuncArgs(ctx, la, input, &intent); err != nil {
			return DevInferredIntent{}, fmt.Errorf("failed to infer intent: %v", err)
		}
		err = validateIntent(intent)
		if err == nil {
			break
		} else {
			prompt = fmt.Sprintf("Please try again, there was a validation failure: %s", err.Error())
		}

		attempts++
		if attempts >= 5 {
			return DevInferredIntent{}, fmt.Errorf("failed to infer intent after 5 attempts: %v", err)
		}
	}
	return intent, err
}

type ActionData struct {
	ID         string `json:"id"`
	ActionType string `json:"action_type"`
	Status     string `json:"status"`
	Title      string `json:"title"`
	Details    string `json:"details,omitempty"`
}

func validateIntent(intent DevInferredIntent) error {
	if intent.IntentType == "request_response" && intent.RequestResponseInfo.WorkflowId == "" {
		return fmt.Errorf("request_response_info is missing and required when the intent is request_response")
	}

	// TODO validate that the workflow id appears in the chat history
	// TODO validate that the workflow id is one with a known open request using a query to the dev manager workflow
	// TODO validate that the workflow id is not completed already
	return nil
}

func (ia DevAgent) HandleNewTask(ctx context.Context, task *models.Task) error {
	// perform a work request where the parentId is the taskId and the task description is the request
	_, err := ia.workRequest(ctx, task.Id, task.Description, task.FlowType, task.FlowOptions)
	if err != nil {
		return err
	}
	return nil
}

func (ia DevAgent) HandleNewMessage(ctx context.Context, topicId string, events chan<- agent.Event) string {
	defer close(events)

	inferActionData := ActionData{
		ID:         "infer_intent",
		ActionType: "infer_intent",
		Status:     "in_progress",
		Title:      "Thinking...",
	}
	events <- agent.Event{Type: agent.EventTypeAction, Data: utils.PanicJSON(inferActionData)}
	var err error
	inferredIntent, err := ia.InferLastMessageIntent(ctx)
	if err != nil {
		log.Printf("Failed to infer intent: %v\n", err)
		events <- agent.Event{
			Type: agent.EventTypeError,
			Data: fmt.Sprintf("Failed to infer intent: %v", err), // TODO hide the error from the user later
		}
		inferActionData.Status = "failed"
		inferActionData.Title = "Failure"
		events <- agent.Event{Type: agent.EventTypeAction, Data: utils.PanicJSON(inferActionData)}
		return ""
	}

	log.Printf("Message intent inferred: %s\n", inferredIntent)
	inferActionData.Status = "complete"
	inferActionData.Title = "Inferred intent: " + inferredIntent.IntentType
	inferActionData.Details = utils.PanicJSON(inferredIntent)
	events <- agent.Event{Type: agent.EventTypeAction, Data: utils.PanicJSON(inferActionData)}

	actionData := DevActionData{
		ChatHistory: ia.ChatHistory,
	}
	if inferredIntent.IntentType == "request_response" && inferredIntent.RequestResponseInfo.WorkflowId != "" {
		actionData.WorkflowId = inferredIntent.RequestResponseInfo.WorkflowId
	}
	action := agent.AgentAction{
		Type:    inferredIntent.IntentType, // maybe map it instead of using it directly
		TopicId: topicId,
		Data:    actionData,
	}

	response, err := ia.PerformAction(ctx, action, events)
	if err != nil {
		// the user gets the error message via SSE through events
		log.Println(err)
		// TODO make this a user-friendly error message
		events <- agent.Event{
			Type: agent.EventTypeError,
			Data: err.Error(),
		}
	}
	return response

	// TODO when the action type is not other, let's also check for any
	// outstanding requests for information from workflows that the workflow
	// manager knows about so we can follow up and add more agent events /
	// assistant messages to request this information
}

const temporalLiteNotFoundError1 = "no rows in result set"
const temporalLiteAlreadyCompletedError = "workflow execution already completed"

// TerminateWorkflowIfExists terminates a workflow execution if there is one running
func (ia *DevAgent) TerminateWorkflowIfExists(ctx context.Context, workflowId string) error {
	reason := "DevAgent TerminateWorkflowIfExists"
	err := ia.TemporalClient.TerminateWorkflow(ctx, workflowId, "", reason)
	if err != nil && !strings.Contains(err.Error(), temporalLiteNotFoundError1) && !strings.Contains(err.Error(), temporalLiteAlreadyCompletedError) {
		fmt.Printf("failed to terminate workflow: %v\n", err)
		return fmt.Errorf("failed to terminate workflow: %w", err)
	}
	return nil
}
