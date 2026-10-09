package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

var errProbeCaptured = errors.New("probe captured request without sending")

type probeCapturedRequest struct {
	URL    string
	Header http.Header
	Body   []byte
}

type probeCaptureTransport struct {
	base              http.RoundTripper
	captureOnly       bool
	expected          *probeCapturedRequest
	request           *http.Request
	requestBody       []byte
	responseBody      bytes.Buffer
	statusCode        int
	calls             int
	allowContinuation bool
	exchanges         []*probeResponseExchange
}

func (t *probeCaptureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !strings.HasSuffix(request.URL.Path, "/responses") {
		return t.base.RoundTrip(request)
	}
	t.calls++
	if request.Method != http.MethodPost || request.Body == nil {
		return nil, fmt.Errorf("probe expected POST with request body")
	}
	body, err := io.ReadAll(request.Body)
	closeErr := request.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("capture request: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close captured request: %w", closeErr)
	}
	t.request = request.Clone(request.Context())
	t.requestBody = body
	if t.expected != nil {
		if request.URL.String() != t.expected.URL {
			return nil, fmt.Errorf("probe endpoint differs from expected endpoint")
		}
		for _, header := range []string{"Authorization", "ChatGPT-Account-Id", "OpenAI-Organization", "OpenAI-Project"} {
			if request.Header.Get(header) != t.expected.Header.Get(header) {
				return nil, fmt.Errorf("probe authentication differs: %s", header)
			}
		}
		if !t.allowContinuation || t.calls == 1 {
			if err := validateProbeRequest(t.expected.Body, body); err != nil {
				return nil, err
			}
		} else if err := validateProbeContinuation(t.expected.Body, body); err != nil {
			return nil, fmt.Errorf("validate provider continuation: %w", err)
		}
	}
	if t.captureOnly {
		return nil, errProbeCaptured
	}
	outgoing := request.Clone(request.Context())
	outgoing.Body = io.NopCloser(bytes.NewReader(body))
	outgoing.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	response, err := t.base.RoundTrip(outgoing)
	if err != nil {
		return nil, err
	}
	t.statusCode = response.StatusCode
	if response.Body == nil {
		return nil, fmt.Errorf("probe response missing body")
	}
	exchange := &probeResponseExchange{RequestBody: body, StatusCode: response.StatusCode}
	t.exchanges = append(t.exchanges, exchange)
	response.Body = &probeRecordingBody{
		ReadCloser: response.Body,
		reader:     io.TeeReader(response.Body, io.MultiWriter(&t.responseBody, &exchange.ResponseBody)),
	}
	return response, nil
}

type probeRecordingBody struct {
	io.ReadCloser
	reader io.Reader
}

func (b *probeRecordingBody) Read(p []byte) (int, error) {
	return b.reader.Read(p)
}

func (b *probeRecordingBody) Close() error {
	_, readErr := io.Copy(io.Discard, b.reader)
	return errors.Join(readErr, b.ReadCloser.Close())
}
