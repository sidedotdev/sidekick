package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sidekick/domain"
	"time"
)

// FlowAction represents a flow action in the client package, mirroring domain.FlowAction
// but omitting deprecated fields.
type FlowAction struct {
	Id               string                 `json:"id"`
	SubflowId        string                 `json:"subflowId,omitempty"`
	FlowId           string                 `json:"flowId"`
	WorkspaceId      string                 `json:"workspaceId"`
	Created          time.Time              `json:"created"`
	Updated          time.Time              `json:"updated"`
	ActionType       string                 `json:"actionType"`
	ActionParams     map[string]interface{} `json:"actionParams"`
	ActionStatus     domain.ActionStatus    `json:"actionStatus"`
	ActionResult     string                 `json:"actionResult"`
	IsHumanAction    bool                   `json:"isHumanAction"`
	IsCallbackAction bool                   `json:"isCallbackAction"`
}

// UserResponse represents the user's response to a flow action that requires human input.
type UserResponse struct {
	Content  string                 `json:"content"`
	Approved *bool                  `json:"approved,omitempty"`
	Choice   string                 `json:"choice,omitempty"`
	Params   map[string]interface{} `json:"params,omitempty"`
}

type completeFlowActionRequest struct {
	UserResponse UserResponse `json:"userResponse"`
}

// CompleteFlowAction sends a user response to complete a pending flow action.
func (c *clientImpl) CompleteFlowAction(workspaceID, flowActionID string, response UserResponse) error {
	reqBody := completeFlowActionRequest{UserResponse: response}
	requestBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/workspaces/%s/flow_actions/%s/complete", c.BaseURL, workspaceID, flowActionID)
	httpReq, err := http.NewRequest("POST", url, bytes.NewBuffer(requestBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to complete flow action: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to complete flow action: status %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

type userActionRequest struct {
	ActionType string `json:"actionType"`
}

// SendUserAction sends a user action to a flow (e.g., dev_run_start, dev_run_stop).
func (c *clientImpl) SendUserAction(workspaceID, flowID, actionType string) error {
	reqBody := userActionRequest{ActionType: actionType}
	requestBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/workspaces/%s/flows/%s/user_action", c.BaseURL, workspaceID, flowID)
	httpReq, err := http.NewRequest("POST", url, bytes.NewBuffer(requestBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send user action: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to send user action: status %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

type flowQueryRequest struct {
	Query string `json:"query"`
	Args  any    `json:"args,omitempty"`
}

type flowQueryResponse struct {
	Result any `json:"result"`
}

// QueryFlow queries a workflow with the given query name and optional arguments.
func (c *clientImpl) QueryFlow(workspaceID, flowID, query string, args any) (any, error) {
	reqBody := flowQueryRequest{Query: query, Args: args}
	requestBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/workspaces/%s/flows/%s/query", c.BaseURL, workspaceID, flowID)
	httpReq, err := http.NewRequest("POST", url, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to query flow: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to query flow: status %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	var response flowQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return response.Result, nil
}
