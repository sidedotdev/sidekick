package client

import (
	"net/http"
	"sidekick/domain"
	"time"
)

// Client is a client for the Sidekick API.
type Client interface {
	CreateTask(workspaceID string, req *CreateTaskRequest) (*domain.Task, error)
	GetTask(workspaceID string, taskID string) (*GetTaskResponse, error)
	CancelTask(workspaceID string, taskID string) error
	CreateWorkspace(req *CreateWorkspaceRequest) (*domain.Workspace, error)
	GetWorkspacesByPath(repoPath string) ([]domain.Workspace, error)
	GetBaseURL() string
}

type clientImpl struct {
	BaseURL    string
	httpClient *http.Client
}

func (c *clientImpl) GetBaseURL() string {
	return c.BaseURL
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
