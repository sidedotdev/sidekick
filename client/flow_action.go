package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sidekick/domain"
	"strconv"
)

// GetFlowActionsResponse is the response from the GetFlowActions API.
type GetFlowActionsResponse struct {
	FlowActions []domain.FlowAction `json:"flowActions"`
}

// GetFlowActionResponse is the response from the GetFlowAction API.
type GetFlowActionResponse struct {
	FlowAction domain.FlowAction `json:"flowAction"`
}

// CompleteFlowActionRequest defines the structure for completing a flow action.
type CompleteFlowActionRequest struct {
	UserResponse map[string]interface{} `json:"userResponse"`
}

// GetFlowActions fetches flow actions for a specific flow with optional pagination.
func (c *clientImpl) GetFlowActions(workspaceID, flowID, after string, limit int) ([]domain.FlowAction, error) {
	reqURL := fmt.Sprintf("%s/api/v1/workspaces/%s/flows/%s/flow_actions", c.BaseURL, workspaceID, flowID)

	params := url.Values{}
	if after != "" {
		params.Add("after", after)
	}
	if limit > 0 {
		params.Add("limit", strconv.Itoa(limit))
	}
	if len(params) > 0 {
		reqURL += "?" + params.Encode()
	}

	resp, err := c.httpClient.Get(reqURL)
	if err != nil {
		return nil, fmt.Errorf("failed to send get flow actions request to API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read response body from get flow actions request (status %s): %w", resp.Status, readErr)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("API request to get flow actions failed with status %s: %s", resp.Status, string(bodyBytes))
	}

	var responseData GetFlowActionsResponse
	if err := json.Unmarshal(bodyBytes, &responseData); err != nil {
		return nil, fmt.Errorf("failed to decode API response for get flow actions (status %s): %w. Full response body: %s", resp.Status, err, string(bodyBytes))
	}
	return responseData.FlowActions, nil
}

// GetFlowAction fetches the details of a specific flow action from the Sidekick server.
func (c *clientImpl) GetFlowAction(workspaceID, actionID string) (domain.FlowAction, error) {
	reqURL := fmt.Sprintf("%s/api/v1/workspaces/%s/flow_actions/%s", c.BaseURL, workspaceID, actionID)

	resp, err := c.httpClient.Get(reqURL)
	if err != nil {
		return domain.FlowAction{}, fmt.Errorf("failed to send get flow action request to API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return domain.FlowAction{}, fmt.Errorf("failed to read response body from get flow action request (status %s): %w", resp.Status, readErr)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return domain.FlowAction{}, fmt.Errorf("API request to get flow action failed with status %s: %s", resp.Status, string(bodyBytes))
	}

	var responseData GetFlowActionResponse
	if err := json.Unmarshal(bodyBytes, &responseData); err != nil {
		return domain.FlowAction{}, fmt.Errorf("failed to decode API response for get flow action (status %s): %w. Full response body: %s", resp.Status, err, string(bodyBytes))
	}
	return responseData.FlowAction, nil
}

// CompleteFlowAction sends a request to complete a flow action.
func (c *clientImpl) CompleteFlowAction(workspaceID, actionID string, req *CompleteFlowActionRequest) (domain.FlowAction, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return domain.FlowAction{}, fmt.Errorf("failed to marshal complete flow action request: %w", err)
	}

	reqURL := fmt.Sprintf("%s/api/v1/workspaces/%s/flow_actions/%s/complete", c.BaseURL, workspaceID, actionID)
	resp, err := c.httpClient.Post(reqURL, "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return domain.FlowAction{}, fmt.Errorf("failed to send complete flow action request to API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return domain.FlowAction{}, fmt.Errorf("failed to read response body from complete flow action request (status %s): %w", resp.Status, readErr)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return domain.FlowAction{}, fmt.Errorf("API request to complete flow action failed with status %s: %s", resp.Status, string(bodyBytes))
	}

	var responseData GetFlowActionResponse
	if err := json.Unmarshal(bodyBytes, &responseData); err != nil {
		return domain.FlowAction{}, fmt.Errorf("failed to decode API response for complete flow action (status %s): %w. Full response body: %s", resp.Status, err, string(bodyBytes))
	}
	return responseData.FlowAction, nil
}
