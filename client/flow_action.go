package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

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
