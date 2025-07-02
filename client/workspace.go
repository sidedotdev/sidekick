package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"sidekick/common"
	"sidekick/domain"
)

// workspaceResponse is the internal representation of a workspace returned by the API, including configuration details.
type workspaceResponse struct {
	Id              string                 `json:"id"`
	Created         time.Time              `json:"created"`
	Updated         time.Time              `json:"updated"`
	Name            string                 `json:"name"`
	LocalRepoDir    string                 `json:"localRepoDir"`
	LLMConfig       common.LLMConfig       `json:"llmConfig,omitempty"`
	EmbeddingConfig common.EmbeddingConfig `json:"embeddingConfig,omitempty"`
}

// CreateWorkspaceRequest defines the structure for the workspace creation request.
type CreateWorkspaceRequest struct {
	Name         string `json:"name"`
	LocalRepoDir string `json:"localRepoDir"`
}

type createWorkspaceResponseWrapper struct {
	Workspace workspaceResponse `json:"workspace"`
}

// CreateWorkspace sends a request to the Sidekick server to create a new workspace.
func (c *Client) CreateWorkspace(req *CreateWorkspaceRequest) (*domain.Workspace, error) {
	requestBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/workspaces", c.baseURL), bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to create workspace: status %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	var response createWorkspaceResponseWrapper
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	domainWorkspace := &domain.Workspace{
		Id:           response.Workspace.Id,
		Name:         response.Workspace.Name,
		LocalRepoDir: response.Workspace.LocalRepoDir,
		Created:      response.Workspace.Created,
		Updated:      response.Workspace.Updated,
	}

	return domainWorkspace, nil
}

type getWorkspacesResponseWrapper struct {
	Workspaces []domain.Workspace `json:"workspaces"`
}

// GetWorkspacesByPath fetches all workspaces and filters them by the repository path.
func (c *Client) GetWorkspacesByPath(repoPath string) ([]domain.Workspace, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/workspaces", c.baseURL), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspaces: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get workspaces: status %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	var response getWorkspacesResponseWrapper
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	var filteredWorkspaces []domain.Workspace
	for _, ws := range response.Workspaces {
		if ws.LocalRepoDir == repoPath {
			filteredWorkspaces = append(filteredWorkspaces, ws)
		}
	}

	return filteredWorkspaces, nil
}
