package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// GetTasksResponse is the response from the GetTasks API.
type GetTasksResponse struct {
	Tasks []Task `json:"tasks"`
}

// GetTasks fetches tasks with the specified statuses from the Sidekick server.
func (c *clientImpl) GetTasks(workspaceID string, statuses []string) ([]Task, error) {
	reqURL := fmt.Sprintf("%s/api/v1/workspaces/%s/tasks", c.BaseURL, workspaceID)

	if len(statuses) > 0 {
		params := url.Values{}
		params.Add("statuses", strings.Join(statuses, ","))
		reqURL += "?" + params.Encode()
	}

	resp, err := c.httpClient.Get(reqURL)
	if err != nil {
		return nil, fmt.Errorf("failed to send get tasks request to API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read response body from get tasks request (status %s): %w", resp.Status, readErr)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("API request to get tasks failed with status %s: %s", resp.Status, string(bodyBytes))
	}

	var responseData GetTasksResponse
	if err := json.Unmarshal(bodyBytes, &responseData); err != nil {
		return nil, fmt.Errorf("failed to decode API response for get tasks (status %s): %w. Full response body: %s", resp.Status, err, string(bodyBytes))
	}
	return responseData.Tasks, nil
}
