package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sidekick/domain"

	"github.com/gin-gonic/gin"
	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newProjectTestContext(t *testing.T, method, path string, body any, params gin.Params) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)

	reqBody := bytes.NewBuffer(nil)
	switch b := body.(type) {
	case nil:
	case string:
		reqBody = bytes.NewBufferString(b)
	default:
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reqBody = bytes.NewBuffer(data)
	}

	ginCtx.Request = httptest.NewRequest(method, path, reqBody)
	ginCtx.Params = params
	return ginCtx, recorder
}

func decodeProjectResponse(t *testing.T, recorder *httptest.ResponseRecorder) domain.Project {
	t.Helper()
	var resp struct {
		Project domain.Project `json:"project"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	return resp.Project
}

func TestCreateProjectHandler(t *testing.T) {
	t.Parallel()
	ctrl := NewMockController(t)
	workspaceId := "ws_" + ksuid.New().String()

	ginCtx, recorder := newProjectTestContext(t, http.MethodPost, "/v1/workspaces/"+workspaceId+"/projects/",
		ProjectRequest{Title: "  My Project  ", Description: "some description", Priority: "high", Rank: "aa"},
		gin.Params{{Key: "workspaceId", Value: workspaceId}})

	ctrl.CreateProjectHandler(ginCtx)

	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	project := decodeProjectResponse(t, recorder)
	assert.True(t, strings.HasPrefix(project.Id, "project_"))
	assert.Equal(t, workspaceId, project.WorkspaceId)
	assert.Equal(t, "My Project", project.Title)
	assert.Equal(t, "some description", project.Description)
	assert.Equal(t, domain.ProjectPriorityHigh, project.Priority)
	assert.Equal(t, "aa", project.Rank)
	assert.False(t, project.Created.IsZero())
	assert.False(t, project.Updated.IsZero())

	persisted, err := ctrl.service.GetProject(context.Background(), workspaceId, project.Id)
	require.NoError(t, err)
	assert.Equal(t, "My Project", persisted.Title)
	assert.Equal(t, domain.ProjectPriorityHigh, persisted.Priority)
}

func TestCreateProjectHandler_DefaultPriority(t *testing.T) {
	t.Parallel()
	ctrl := NewMockController(t)
	workspaceId := "ws_" + ksuid.New().String()

	ginCtx, recorder := newProjectTestContext(t, http.MethodPost, "/v1/workspaces/"+workspaceId+"/projects/",
		ProjectRequest{Title: "No Priority Project"},
		gin.Params{{Key: "workspaceId", Value: workspaceId}})

	ctrl.CreateProjectHandler(ginCtx)

	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	project := decodeProjectResponse(t, recorder)
	assert.Equal(t, domain.ProjectPriorityNone, project.Priority)
}

func TestCreateProjectHandler_ValidationErrors(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		body any
	}{
		{name: "missing title", body: ProjectRequest{Priority: "low"}},
		{name: "whitespace-only title", body: ProjectRequest{Title: "   "}},
		{name: "invalid priority", body: ProjectRequest{Title: "Something", Priority: "bogus"}},
		{name: "malformed json", body: "{not json"},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := NewMockController(t)
			workspaceId := "ws_" + ksuid.New().String()

			ginCtx, recorder := newProjectTestContext(t, http.MethodPost, "/v1/workspaces/"+workspaceId+"/projects/",
				tc.body, gin.Params{{Key: "workspaceId", Value: workspaceId}})

			ctrl.CreateProjectHandler(ginCtx)

			assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
		})
	}
}

func TestUpdateProjectHandler(t *testing.T) {
	t.Parallel()
	ctrl := NewMockController(t)
	workspaceId := "ws_" + ksuid.New().String()

	created := time.Now().UTC().Add(-time.Hour)
	existing := domain.Project{
		WorkspaceId: workspaceId,
		Id:          "project_" + ksuid.New().String(),
		Title:       "Old Title",
		Description: "old description",
		Priority:    domain.ProjectPriorityLow,
		Rank:        "aa",
		Created:     created,
		Updated:     created,
	}
	require.NoError(t, ctrl.service.PersistProject(context.Background(), existing))

	ginCtx, recorder := newProjectTestContext(t, http.MethodPut, "/v1/workspaces/"+workspaceId+"/projects/"+existing.Id,
		ProjectRequest{Title: "New Title", Description: "new description", Priority: "urgent", Rank: "ab"},
		gin.Params{{Key: "workspaceId", Value: workspaceId}, {Key: "id", Value: existing.Id}})

	ctrl.UpdateProjectHandler(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	project := decodeProjectResponse(t, recorder)
	assert.Equal(t, existing.Id, project.Id)
	assert.Equal(t, "New Title", project.Title)
	assert.Equal(t, "new description", project.Description)
	assert.Equal(t, domain.ProjectPriorityUrgent, project.Priority)
	assert.Equal(t, "ab", project.Rank)
	assert.True(t, project.Updated.After(project.Created))

	persisted, err := ctrl.service.GetProject(context.Background(), workspaceId, existing.Id)
	require.NoError(t, err)
	assert.Equal(t, "New Title", persisted.Title)
	assert.Equal(t, domain.ProjectPriorityUrgent, persisted.Priority)
}

func TestUpdateProjectHandler_NotFound(t *testing.T) {
	t.Parallel()
	ctrl := NewMockController(t)
	workspaceId := "ws_" + ksuid.New().String()

	ginCtx, recorder := newProjectTestContext(t, http.MethodPut, "/v1/workspaces/"+workspaceId+"/projects/project_missing",
		ProjectRequest{Title: "Whatever"},
		gin.Params{{Key: "workspaceId", Value: workspaceId}, {Key: "id", Value: "project_missing"}})

	ctrl.UpdateProjectHandler(ginCtx)

	assert.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
}

func TestUpdateProjectHandler_ValidationErrors(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		body any
	}{
		{name: "missing title", body: ProjectRequest{Priority: "low"}},
		{name: "invalid priority", body: ProjectRequest{Title: "Something", Priority: "bogus"}},
		{name: "malformed json", body: "{not json"},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := NewMockController(t)
			workspaceId := "ws_" + ksuid.New().String()

			ginCtx, recorder := newProjectTestContext(t, http.MethodPut, "/v1/workspaces/"+workspaceId+"/projects/project_x",
				tc.body, gin.Params{{Key: "workspaceId", Value: workspaceId}, {Key: "id", Value: "project_x"}})

			ctrl.UpdateProjectHandler(ginCtx)

			assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
		})
	}
}

func TestDeleteProjectHandler(t *testing.T) {
	t.Parallel()
	ctrl := NewMockController(t)
	workspaceId := "ws_" + ksuid.New().String()

	project := domain.Project{
		WorkspaceId: workspaceId,
		Id:          "project_" + ksuid.New().String(),
		Title:       "To Delete",
		Priority:    domain.ProjectPriorityNone,
		Created:     time.Now().UTC(),
		Updated:     time.Now().UTC(),
	}
	require.NoError(t, ctrl.service.PersistProject(context.Background(), project))

	ginCtx, recorder := newProjectTestContext(t, http.MethodDelete, "/v1/workspaces/"+workspaceId+"/projects/"+project.Id,
		nil, gin.Params{{Key: "workspaceId", Value: workspaceId}, {Key: "id", Value: project.Id}})

	ctrl.DeleteProjectHandler(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	_, err := ctrl.service.GetProject(context.Background(), workspaceId, project.Id)
	assert.Error(t, err)
}

func TestDeleteProjectHandler_NotFound(t *testing.T) {
	t.Parallel()
	ctrl := NewMockController(t)
	workspaceId := "ws_" + ksuid.New().String()

	ginCtx, recorder := newProjectTestContext(t, http.MethodDelete, "/v1/workspaces/"+workspaceId+"/projects/project_missing",
		nil, gin.Params{{Key: "workspaceId", Value: workspaceId}, {Key: "id", Value: "project_missing"}})

	ctrl.DeleteProjectHandler(ginCtx)

	assert.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
}

func TestGetProjectsHandler(t *testing.T) {
	t.Parallel()
	ctrl := NewMockController(t)
	workspaceId := "ws_" + ksuid.New().String()

	now := time.Now().UTC()
	projects := []domain.Project{
		{WorkspaceId: workspaceId, Id: "project_none", Title: "None", Priority: domain.ProjectPriorityNone, Rank: "aa", Created: now, Updated: now},
		{WorkspaceId: workspaceId, Id: "project_urgent_b", Title: "Urgent B", Priority: domain.ProjectPriorityUrgent, Rank: "ab", Created: now, Updated: now},
		{WorkspaceId: workspaceId, Id: "project_urgent_a", Title: "Urgent A", Priority: domain.ProjectPriorityUrgent, Rank: "aa", Created: now, Updated: now},
		{WorkspaceId: workspaceId, Id: "project_low", Title: "Low", Priority: domain.ProjectPriorityLow, Rank: "aa", Created: now, Updated: now},
	}
	for _, p := range projects {
		require.NoError(t, ctrl.service.PersistProject(context.Background(), p))
	}

	ginCtx, recorder := newProjectTestContext(t, http.MethodGet, "/v1/workspaces/"+workspaceId+"/projects/",
		nil, gin.Params{{Key: "workspaceId", Value: workspaceId}})

	ctrl.GetProjectsHandler(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var resp struct {
		Projects []domain.Project `json:"projects"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Len(t, resp.Projects, 4)

	gotIds := make([]string, len(resp.Projects))
	for i, p := range resp.Projects {
		gotIds[i] = p.Id
	}
	assert.Equal(t, []string{"project_urgent_a", "project_urgent_b", "project_low", "project_none"}, gotIds)
}

func TestGetProjectsHandler_Empty(t *testing.T) {
	t.Parallel()
	ctrl := NewMockController(t)
	workspaceId := "ws_" + ksuid.New().String()

	ginCtx, recorder := newProjectTestContext(t, http.MethodGet, "/v1/workspaces/"+workspaceId+"/projects/",
		nil, gin.Params{{Key: "workspaceId", Value: workspaceId}})

	ctrl.GetProjectsHandler(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.JSONEq(t, `{"projects": []}`, recorder.Body.String())
}

func TestProjectHandlers_RequireWorkspaceId(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		method  string
		body    any
		handler func(ctrl *Controller) gin.HandlerFunc
	}{
		{
			name:   "create",
			method: http.MethodPost,
			body:   ProjectRequest{Title: "Something"},
			handler: func(ctrl *Controller) gin.HandlerFunc {
				return ctrl.CreateProjectHandler
			},
		},
		{
			name:   "list",
			method: http.MethodGet,
			handler: func(ctrl *Controller) gin.HandlerFunc {
				return ctrl.GetProjectsHandler
			},
		},
		{
			name:   "update",
			method: http.MethodPut,
			body:   ProjectRequest{Title: "Something"},
			handler: func(ctrl *Controller) gin.HandlerFunc {
				return ctrl.UpdateProjectHandler
			},
		},
		{
			name:   "delete",
			method: http.MethodDelete,
			handler: func(ctrl *Controller) gin.HandlerFunc {
				return ctrl.DeleteProjectHandler
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := NewMockController(t)

			ginCtx, recorder := newProjectTestContext(t, tc.method, "/v1/workspaces//projects/project_x",
				tc.body, gin.Params{{Key: "workspaceId", Value: " "}, {Key: "id", Value: "project_x"}})

			tc.handler(&ctrl)(ginCtx)

			assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
		})
	}
}

// Route-level coverage: the projects collection must be served directly at
// /projects (no trailing slash), not via a redirect, since fetch callers and
// non-browser clients may not follow redirects.
func TestProjectRoutes_CollectionWithoutTrailingSlash(t *testing.T) {
	t.Parallel()
	ctrl := NewMockController(t)
	router := DefineRoutes(ctrl, TestAllowedOrigins())
	workspaceId := "ws_" + ksuid.New().String()
	baseUrl := "/api/v1/workspaces/" + workspaceId + "/projects"

	body, err := json.Marshal(ProjectRequest{Title: "My Project"})
	require.NoError(t, err)
	postRecorder := httptest.NewRecorder()
	router.ServeHTTP(postRecorder, httptest.NewRequest(http.MethodPost, baseUrl, bytes.NewBuffer(body)))
	require.Equal(t, http.StatusCreated, postRecorder.Code, postRecorder.Body.String())

	getRecorder := httptest.NewRecorder()
	router.ServeHTTP(getRecorder, httptest.NewRequest(http.MethodGet, baseUrl, nil))
	require.Equal(t, http.StatusOK, getRecorder.Code, getRecorder.Body.String())

	var resp struct {
		Projects []domain.Project `json:"projects"`
	}
	require.NoError(t, json.Unmarshal(getRecorder.Body.Bytes(), &resp))
	require.Len(t, resp.Projects, 1)
	assert.Equal(t, "My Project", resp.Projects[0].Title)
}