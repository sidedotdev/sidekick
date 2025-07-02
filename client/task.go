package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"sidekick/domain"
)

// CreateTaskRequest defines the structure for the task creation API request.
type CreateTaskRequest struct {
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	FlowType    string                 `json:"flowType"`
	FlowOptions map[string]interface{} `json:"flowOptions"`
}

// CreateTask sends a request to the Sidekick server to create a new task.
func (c *Client) CreateTask(workspaceID string, req *CreateTaskRequest) (*domain.Task, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal create task request: %w", err)
	}

	reqURL := fmt.Sprintf("%s/api/v1/workspaces/%s/tasks", c.baseURL, workspaceID)
	resp, err := c.httpClient.Post(reqURL, "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to send create task request to API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read response body from create task request (status %s): %w", resp.Status, readErr)
	}

	if resp.StatusCode != http.StatusCreated { // Expect 201 Created
		return nil, fmt.Errorf("API request to create task failed with status %s: %s", resp.Status, string(bodyBytes))
	}

	var responseData domain.Task
	if err := json.Unmarshal(bodyBytes, &responseData); err != nil {
		return nil, fmt.Errorf("failed to decode API response for create task (status %s): %w. Full response body: %s", resp.Status, err, string(bodyBytes))
	}
	return &responseData, nil
}

// TaskWithFlows is a task with its associated flows.
type TaskWithFlows struct {
	domain.Task
	Flows []domain.Flow `json:"flows"`
}

// GetTaskResponse is the response from the GetTask API.
type GetTaskResponse struct {
	Task TaskWithFlows `json:"task"`
}

// GetTask fetches the details of a specific task from the Sidekick server.
func (c *Client) GetTask(workspaceID string, taskID string) (*GetTaskResponse, error) {
	reqURL := fmt.Sprintf("%s/api/v1/workspaces/%s/tasks/%s", c.baseURL, workspaceID, taskID)

	resp, err := c.httpClient.Get(reqURL)
	if err != nil {
		return nil, fmt.Errorf("failed to send get task request to API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read response body from get task request (status %s): %w", resp.Status, readErr)
	}

	if resp.StatusCode != http.StatusOK { // Expect 200 OK
		return nil, fmt.Errorf("API request to get task failed with status %s: %s", resp.Status, string(bodyBytes))
	}

	var responseData GetTaskResponse
	if err := json.Unmarshal(bodyBytes, &responseData); err != nil {
		return nil, fmt.Errorf("failed to decode API response for get task (status %s): %w. Full response body: %s", resp.Status, err, string(bodyBytes))
	}
	return &responseData, nil
}

// CancelTask sends a request to the Sidekick server to cancel a task.
func (c *Client) CancelTask(workspaceID string, taskID string) error {
	reqURL := fmt.Sprintf("%s/api/v1/workspaces/%s/tasks/%s/cancel", c.baseURL, workspaceID, taskID)

	resp, err := c.httpClient.Post(reqURL, "application/json", nil)
	if err != nil {
		return fmt.Errorf("failed to send cancel task request to API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK { // Expect 200 OK for cancellation
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("API request to cancel task failed with status %s and could not read response body: %w", resp.Status, readErr)
		}
		var errorResponse struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(bodyBytes, &errorResponse) == nil && errorResponse.Error != "" {
			return fmt.Errorf("API request to cancel task failed with status %s: %s", resp.Status, errorResponse.Error)
		}
		return fmt.Errorf("API request to cancel task failed with status %s: %s", resp.Status, string(bodyBytes))
	}
	return nil
}
