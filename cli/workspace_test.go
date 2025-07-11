package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sidekick/domain"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type mockProgram struct {
}

func (m mockProgram) Send(msg tea.Msg) {
}

func TestEnsureWorkspace(t *testing.T) {
	// Save current working directory
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalWd)

	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "workspace-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Change to the temporary directory
	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	absPath, err := filepath.Abs(tmpDir)
	require.NoError(t, err)

	tests := []struct {
		name               string
		workspaces         []domain.Workspace
		disableHumanInLoop bool
		getWorkspacesErr   error
		createWorkspaceErr error
		expectedWorkspace  *domain.Workspace
		expectedError      string
	}{
		{
			name:       "no workspaces found - creates new one",
			workspaces: []domain.Workspace{},
			expectedWorkspace: &domain.Workspace{
				Id:      "test-id",
				Name:    fmt.Sprintf("%s-workspace", filepath.Base(tmpDir)),
				Updated: time.Now(),
			},
		},
		{
			name: "single workspace found",
			workspaces: []domain.Workspace{
				{Id: "existing-id", Name: "existing", Updated: time.Now()},
			},
			expectedWorkspace: &domain.Workspace{Id: "existing-id", Name: "existing"},
		},
		{
			name: "multiple workspaces with human disabled - uses most recent",
			workspaces: []domain.Workspace{
				{Id: "old-id", Name: "old", Updated: time.Now().Add(-1 * time.Hour)},
				{Id: "new-id", Name: "new", Updated: time.Now()},
			},
			disableHumanInLoop: true,
			expectedWorkspace:  &domain.Workspace{Id: "new-id", Name: "new"},
		},
		{
			name:             "get workspaces error",
			getWorkspacesErr: fmt.Errorf("failed to get workspaces"),
			expectedError:    "failed to retrieve workspaces for path",
		},
		{
			name:               "create workspace error",
			workspaces:         []domain.Workspace{},
			createWorkspaceErr: fmt.Errorf("failed to create workspace"),
			expectedError:      "failed to create workspace for path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup client
			c := &mockClient{
				Mock:    mock.Mock{},
				baseURL: "http://test",
			}

			// Setup expectations
			// Using mock.AnythingOfType for path comparison to handle symlinks in OSX (/private/var/folders/... -> /var/folders/...)
			c.On("GetWorkspacesByPath", mock.AnythingOfType("string")).Return(tt.workspaces, tt.getWorkspacesErr).Run(func(args mock.Arguments) {
				path := args.Get(0).(string)
				// Verify path contains the expected path or vice versa (to handle symlinks)
				if !strings.Contains(path, absPath) && !strings.Contains(absPath, path) {
					t.Errorf("Path %s does not match expected path %s", path, absPath)
				}
			})

			if len(tt.workspaces) == 0 && tt.getWorkspacesErr == nil {
				if tt.createWorkspaceErr != nil {
					c.On("CreateWorkspace", mock.AnythingOfType("*client.CreateWorkspaceRequest")).Return(nil, tt.createWorkspaceErr)
				} else {
					c.On("CreateWorkspace", mock.AnythingOfType("*client.CreateWorkspaceRequest")).Return(tt.expectedWorkspace, nil)
				}
			}

			// Call the function
			workspace, err := ensureWorkspace(context.Background(), mockProgram{}, c, tt.disableHumanInLoop)

			// Verify results
			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, workspace)

			if tt.expectedWorkspace != nil {
				assert.Equal(t, tt.expectedWorkspace.Id, workspace.Id)
				assert.Equal(t, tt.expectedWorkspace.Name, workspace.Name)
			}

			// Verify all expectations were met
			c.AssertExpectations(t)
		})
	}
}
