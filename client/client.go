package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sidekick/domain"
	"time"
)

// Client is a client for the Sidekick API.
type Client interface {
	CreateTask(workspaceID string, req *CreateTaskRequest) (Task, error)
	GetTask(workspaceID string, taskID string) (Task, error)
	CancelTask(workspaceID string, taskID string) error
	CreateWorkspace(req *CreateWorkspaceRequest) (*domain.Workspace, error)
	GetAllWorkspaces(ctx context.Context) ([]domain.Workspace, error)
	GetBaseURL() string
}

type clientImpl struct {
	BaseURL    string
	httpClient *http.Client
}

func (c *clientImpl) GetBaseURL() string {
	return c.BaseURL
}

// get performs a GET request to the specified path and unmarshals the response into v.
func (c *clientImpl) get(ctx context.Context, path string, v interface{}) error {
	reqURL := c.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	return nil
}

// NewClient creates a new Sidekick API client.
func NewClient(baseURL string) Client {
	return &clientImpl{
		BaseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}
