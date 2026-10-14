package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"sidekick/domain"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFlowHistoryPagination(t *testing.T) {
	t.Parallel()
	for _, collection := range []string{"actions", "subflows"} {
		t.Run(collection, func(t *testing.T) {
			t.Parallel()
			ctrl := NewMockController(t)
			ctx := context.Background()
			created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			for i := 0; i < 5; i++ {
				id := fmt.Sprintf("item_%02d", i)
				if collection == "actions" {
					require.NoError(t, ctrl.service.PersistFlowAction(ctx, domain.FlowAction{
						Id: id, WorkspaceId: "ws_page", FlowId: "flow_page",
						Created: created, Updated: created,
						ActionParams: map[string]interface{}{"command": id}, ActionResult: id,
					}))
				} else {
					require.NoError(t, ctrl.service.PersistSubflow(ctx, domain.Subflow{
						Id: id, WorkspaceId: "ws_page", FlowId: "flow_page", Name: id,
					}))
				}
			}
			get := func(query string) (int, map[string]json.RawMessage) {
				t.Helper()
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodGet, "/?"+query, nil)
				c.Params = gin.Params{{Key: "workspaceId", Value: "ws_page"}, {Key: "id", Value: "flow_page"}}
				if collection == "actions" {
					ctrl.GetFlowActionsHandler(c)
				} else {
					ctrl.GetFlowSubflowsHandler(c)
				}
				var body map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
				return recorder.Code, body
			}
			key := "subflows"
			if collection == "actions" {
				key = "flowActions"
			}
			var ids []string
			cursor := ""
			for page := 0; page < 3; page++ {
				code, body := get("limit=2&before=" + url.QueryEscape(cursor))
				require.Equal(t, http.StatusOK, code)
				var items []struct {
					ID string `json:"id"`
				}
				require.NoError(t, json.Unmarshal(body[key], &items))
				if page < 2 {
					require.Len(t, items, 2)
				} else {
					require.Len(t, items, 1)
				}
				for _, item := range items {
					ids = append(ids, item.ID)
				}
				require.NoError(t, json.Unmarshal(body["nextCursor"], &cursor))
				if page < 2 {
					require.NotEmpty(t, cursor)
				} else {
					require.Empty(t, cursor)
				}
			}
			require.Equal(t, []string{"item_04", "item_03", "item_02", "item_01", "item_00"}, ids)
			for _, query := range []string{"limit=0", "limit=-1", "limit=501", "limit=abc", "limit=2&before=invalid", "before=invalid"} {
				code, _ := get(query)
				require.Equal(t, http.StatusBadRequest, code, query)
			}
		})
	}
}
