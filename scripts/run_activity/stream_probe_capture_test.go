package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbeCaptureDoesNotSendRequest(t *testing.T) {
	t.Parallel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	transport := &probeCaptureTransport{base: server.Client().Transport, captureOnly: true}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/responses", strings.NewReader(`{"model":"test"}`))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer secret")
	_, err = transport.RoundTrip(request)
	require.ErrorIs(t, err, errProbeCaptured)
	require.NotNil(t, transport.request)
	assert.Equal(t, "Bearer secret", transport.request.Header.Get("Authorization"))
	assert.JSONEq(t, `{"model":"test"}`, string(transport.requestBody))
	assert.Zero(t, calls)
	assert.Equal(t, 1, transport.calls)
}

func TestProbeCaptureValidatesBeforeSending(t *testing.T) {
	t.Parallel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: test\n\n")
	}))
	defer server.Close()

	transport := &probeCaptureTransport{
		base: server.Client().Transport,
		expected: &probeCapturedRequest{
			URL:    server.URL + "/responses",
			Header: http.Header{"Authorization": {"Bearer secret"}},
			Body:   []byte(`{"model":"expected"}`),
		},
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/responses", strings.NewReader(`{"model":"different"}`))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer secret")
	_, err = transport.RoundTrip(request)
	require.ErrorContains(t, err, "wire request")
	assert.Zero(t, calls)

	request, err = http.NewRequest(http.MethodPost, server.URL+"/responses", strings.NewReader(`{"model":"expected"}`))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer other")
	_, err = transport.RoundTrip(request)
	require.ErrorContains(t, err, "authentication")
	assert.Zero(t, calls)
}

func TestProbeCaptureRecordsStream(t *testing.T) {
	t.Parallel()
	body := "data: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"test\"}\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()

	transport := &probeCaptureTransport{base: server.Client().Transport}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/responses", strings.NewReader(`{"model":"test"}`))
	require.NoError(t, err)
	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, body, transport.responseBody.String())
	assert.Equal(t, http.StatusOK, transport.statusCode)
	assert.False(t, errors.Is(err, errProbeCaptured))
}

func TestProbeRecordingBodyDrainsOnClose(t *testing.T) {
	t.Parallel()
	var captured strings.Builder
	source := io.NopCloser(strings.NewReader("first frame\nremaining frames\n"))
	body := &probeRecordingBody{
		ReadCloser: source,
		reader:     io.TeeReader(source, &captured),
	}
	prefix := make([]byte, len("first frame\n"))
	_, err := io.ReadFull(body, prefix)
	require.NoError(t, err)
	require.NoError(t, body.Close())
	assert.Equal(t, "first frame\nremaining frames\n", captured.String())
}
