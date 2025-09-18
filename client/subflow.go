package client

import (
	"encoding/json"
	"fmt"
	"io"
	"sidekick/domain"
)

// GetSubflowsResponse is the response from the GetSubflows API.
type GetSubflowsResponse struct {
	Subflows []domain.Subflow `json:"subflows"`
}

// GetSubflows fetches subflows for a specific flow from the Sidekick server.
func (c *clientImpl) GetSubflows(workspaceID, flowID string) ([]domain.Subflow, error) {
	reqURL := fmt.Sprintf("%s/api/v1/workspaces/%s/flows/%s/subflows", c.BaseURL, workspaceID, flowID)

	resp, err := c.httpClient.Get(reqURL)
	if err != nil {
		return nil, fmt.Errorf("failed to send get subflows request to API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read response body from get subflows request (status %s): %w", resp.Status, readErr)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("API request to get subflows failed with status %s: %s", resp.Status, string(bodyBytes))
	}

	var responseData GetSubflowsResponse
	if err := json.Unmarshal(bodyBytes, &responseData); err != nil {
		return nil, fmt.Errorf("failed to decode API response for get subflows (status %s): %w. Full response body: %s", resp.Status, err, string(bodyBytes))
	}
	return responseData.Subflows, nil
}
