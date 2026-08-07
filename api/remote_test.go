package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"sidekick/domain"
	sidekickiroh "sidekick/iroh"

	"github.com/gin-gonic/gin"
	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/require"
	"github.com/tmc/go-iroh/endpointticket"
	irohlib "github.com/tmc/go-iroh/iroh"
)

func TestDeviceAuthMiddleware(t *testing.T) {
	t.Parallel()

	service := NewTestService(t)
	token, err := generateDeviceToken()
	require.NoError(t, err)

	device := domain.RemoteDevice{
		Id:        "rd_" + ksuid.New().String(),
		Name:      "test device",
		TokenHash: hashDeviceToken(token),
		Created:   time.Now(),
	}
	require.NoError(t, service.CreateRemoteDevice(context.Background(), device))

	router := gin.New()
	router.Use(DeviceAuthMiddleware(service))
	router.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{name: "missing token", authHeader: "", wantStatus: http.StatusUnauthorized},
		{name: "unknown token", authHeader: "Bearer not-a-real-token", wantStatus: http.StatusUnauthorized},
		{name: "malformed header", authHeader: token, wantStatus: http.StatusUnauthorized},
		{name: "valid token", authHeader: "Bearer " + token, wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/ping", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)
			require.Equal(t, tt.wantStatus, rr.Code)
		})
	}
}

func TestPairingCreateListDelete(t *testing.T) {
	setRemoteTicket("test-ticket")
	defer setRemoteTicket("")

	ctrl := NewMockController(t)
	router := DefineRoutes(ctrl, TestAllowedOrigins())

	// Create a pairing.
	body, _ := json.Marshal(CreatePairingRequest{Name: "My Phone"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/remote/pairings/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code)

	var created CreatePairingResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &created))
	require.Equal(t, "My Phone", created.Device.Name)
	require.NotEmpty(t, created.Token)
	require.Equal(t, "test-ticket", created.Ticket)
	require.Equal(t, hashDeviceToken(created.Token), created.Device.TokenHash)

	// List pairings.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/remote/pairings/", nil)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var listed struct {
		Devices []domain.RemoteDevice `json:"devices"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &listed))
	require.Len(t, listed.Devices, 1)
	require.Equal(t, created.Device.Id, listed.Devices[0].Id)

	// Delete the pairing.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/remote/pairings/"+created.Device.Id, nil)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)

	req = httptest.NewRequest(http.MethodGet, "/api/v1/remote/pairings/", nil)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &listed))
	require.Empty(t, listed.Devices)
}

func TestRemoteServerEndToEnd(t *testing.T) {
	setRemoteTicket("")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ctrl := NewMockController(t)

	workspaceId := "ws_" + ksuid.New().String()
	require.NoError(t, ctrl.service.PersistWorkspace(ctx, domain.Workspace{Id: workspaceId}))
	task := domain.Task{
		WorkspaceId: workspaceId,
		Id:          "task_" + ksuid.New().String(),
		Title:       "remote task",
		Status:      domain.TaskStatusToDo,
		AgentType:   domain.AgentTypeHuman,
		FlowType:    domain.FlowTypeBasicDev,
		Created:     time.Now(),
	}
	require.NoError(t, ctrl.service.PersistTask(ctx, task))

	// Bind to loopback so the endpoint advertises a directly reachable address,
	// letting the same-machine client connect without a relay or discovery.
	loopback := netip.MustParseAddrPort("127.0.0.1:0")

	rs, err := serveRemote(ctx, ctrl, TestAllowedOrigins(), irohlib.WithBindAddr(loopback))
	require.NoError(t, err)
	defer rs.Shutdown(context.Background())

	// Pair a device over the local router (which shares the same service).
	localRouter := DefineRoutes(ctrl, TestAllowedOrigins())
	body, _ := json.Marshal(CreatePairingRequest{Name: "e2e device"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/remote/pairings/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	localRouter.ServeHTTP(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code)
	var pairing CreatePairingResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &pairing))
	require.Equal(t, rs.Ticket(), pairing.Ticket)

	// Dial the remote node over iroh using the ticket.
	clientEp, err := irohlib.Bind(ctx, irohlib.WithBindAddr(loopback))
	require.NoError(t, err)
	defer clientEp.Shutdown(ctx)

	addr, err := endpointticket.Decode(rs.Ticket())
	require.NoError(t, err)
	conn, err := clientEp.Connect(ctx, addr, sidekickiroh.ALPN)
	require.NoError(t, err)
	defer conn.CloseWithError(0, "")

	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				return conn.OpenStreamConn(ctx)
			},
		},
	}

	url := fmt.Sprintf("http://sidekick/api/v1/workspaces/%s/tasks/", workspaceId)

	// Requests without a valid device token are rejected.
	unauthReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	unauthResp, err := httpClient.Do(unauthReq)
	require.NoError(t, err)
	unauthResp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, unauthResp.StatusCode)

	// With a valid device token, the task listing succeeds over iroh.
	authReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	authReq.Header.Set("Authorization", "Bearer "+pairing.Token)
	resp, err := httpClient.Do(authReq)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var payload struct {
		Tasks []struct {
			Id string `json:"id"`
		} `json:"tasks"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	require.Len(t, payload.Tasks, 1)
	require.Equal(t, task.Id, payload.Tasks[0].Id)

	// Pairing management must not be reachable over the remote connection.
	pairingReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://sidekick/api/v1/remote/pairings/", nil)
	pairingReq.Header.Set("Authorization", "Bearer "+pairing.Token)
	pairingResp, err := httpClient.Do(pairingReq)
	require.NoError(t, err)
	pairingResp.Body.Close()
	require.Equal(t, http.StatusForbidden, pairingResp.StatusCode)
}
