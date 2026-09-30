package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func setRemoteTicket(ticket string) {
	setRemoteTicketProvider(func() string { return ticket })
}

func TestPairingUsesCurrentRemoteTicket(t *testing.T) {
	ticket := "bootstrap-ticket"
	setRemoteTicketProvider(func() string { return ticket })
	t.Cleanup(func() { setRemoteTicketProvider(nil) })

	ctrl := NewMockController(t)
	router := DefineRoutes(ctrl, TestAllowedOrigins())
	pair := func() *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/remote/pairings/",
			bytes.NewBufferString(`{"name":"roaming phone"}`))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}

	for _, current := range []string{"bootstrap-ticket", "selected-relay-ticket", "new-network-ticket"} {
		ticket = current
		response := pair()
		require.Equal(t, http.StatusCreated, response.Code)
		var pairing CreatePairingResponse
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &pairing))
		require.Equal(t, current, pairing.Ticket)
		require.NotEmpty(t, pairing.Token)
	}

	setRemoteTicketProvider(nil)
	require.Equal(t, http.StatusServiceUnavailable, pair().Code)
}
