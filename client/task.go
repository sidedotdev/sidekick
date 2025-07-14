package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"sidekick/dev"
	"sidekick/domain"
)

// FlowOptions defines the options that can be passed to a flow
type FlowOptions struct {
	ConfigOverrides dev.DevConfigOverrides `json:"configOverrides,omitempty"`
	// Preserve extensibility by keeping additional arbitrary options
	AdditionalOptions map[string]interface{} `json:"additionalOptions,omitempty"`
}

// CreateTaskRequest defines the structure for the task creation API request.
type CreateTaskRequest struct {
	Title       string      `json:"title"`
	Description string      `json:"description"`
	FlowType    string      `json:"flowType"`
	FlowOptions FlowOptions `json:"flowOptions"`
}

// CreateTaskResponse is the response from the CreateTask API.
type CreateTaskResponse struct {
	Task Task `json:"task"`
}

// CreateTask sends a request to the Sidekick server to create a new task.
func (c *clientImpl) CreateTask(workspaceID string, req *CreateTaskRequest) (Task, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return Task{}, fmt.Errorf("failed to marshal create task request: %w", err)
	}

	reqURL := fmt.Sprintf("%s/api/v1/workspaces/%s/tasks", c.BaseURL, workspaceID)
	resp, err := c.httpClient.Post(reqURL, "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return Task{}, fmt.Errorf("failed to send create task request to API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return Task{}, fmt.Errorf("failed to read response body from create task request (status %s): %w", resp.Status, readErr)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Task{}, fmt.Errorf("API request to create task failed with status %s: %s", resp.Status, string(bodyBytes))
	}

	var responseData CreateTaskResponse
	if err := json.Unmarshal(bodyBytes, &responseData); err != nil {
		return Task{}, fmt.Errorf("failed to decode API response for create task (status %s): %w. Full response body: %s", resp.Status, err, string(bodyBytes))
	}
	return responseData.Task, nil
}

type Task struct {
	domain.Task
	Flows []domain.Flow `json:"flows"`
}

// GetTaskResponse is the response from the GetTask API.
type GetTaskResponse struct {
	Task Task `json:"task"`
}

// GetTask fetches the details of a specific task from the Sidekick server.
func (c *clientImpl) GetTask(workspaceID string, taskID string) (Task, error) {
	reqURL := fmt.Sprintf("%s/api/v1/workspaces/%s/tasks/%s", c.BaseURL, workspaceID, taskID)

	resp, err := c.httpClient.Get(reqURL)
	if err != nil {
		return Task{}, fmt.Errorf("failed to send get task request to API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return Task{}, fmt.Errorf("failed to read response body from get task request (status %s): %w", resp.Status, readErr)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Task{}, fmt.Errorf("API request to get task failed with status %s: %s", resp.Status, string(bodyBytes))
	}

	var responseData GetTaskResponse
	if err := json.Unmarshal(bodyBytes, &responseData); err != nil {
		return Task{}, fmt.Errorf("failed to decode API response for get task (status %s): %w. Full response body: %s", resp.Status, err, string(bodyBytes))
	}
	return responseData.Task, nil
}

// CancelTask sends a request to the Sidekick server to cancel a task.
func (c *clientImpl) CancelTask(workspaceID string, taskID string) error {
	reqURL := fmt.Sprintf("%s/api/v1/workspaces/%s/tasks/%s/cancel", c.BaseURL, workspaceID, taskID)

	resp, err := c.httpClient.Post(reqURL, "application/json", nil)
	if err != nil {
		return fmt.Errorf("failed to send cancel task request to API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
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
