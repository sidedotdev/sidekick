package client

import (
	"encoding/json"
	"fmt"
	"io"
	"sidekick/domain"
)

// GetFlowResponse is the response from the GetFlow API.
type GetFlowResponse struct {
	Flow domain.Flow `json:"flow"`
}

// GetFlow fetches the details of a specific flow from the Sidekick server.
func (c *clientImpl) GetFlow(workspaceID, flowID string) (domain.Flow, error) {
	reqURL := fmt.Sprintf("%s/api/v1/workspaces/%s/flows/%s", c.BaseURL, workspaceID, flowID)

	resp, err := c.httpClient.Get(reqURL)
	if err != nil {
		return domain.Flow{}, fmt.Errorf("failed to send get flow request to API: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return domain.Flow{}, fmt.Errorf("failed to read response body from get flow request (status %s): %w", resp.Status, readErr)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return domain.Flow{}, fmt.Errorf("API request to get flow failed with status %s: %s", resp.Status, string(bodyBytes))
	}

	var responseData GetFlowResponse
	if err := json.Unmarshal(bodyBytes, &responseData); err != nil {
		return domain.Flow{}, fmt.Errorf("failed to decode API response for get flow (status %s): %w. Full response body: %s", resp.Status, err, string(bodyBytes))
	}
	return responseData.Flow, nil
}
