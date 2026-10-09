package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"sidekick/common"
	"sidekick/llm2"
	"sidekick/persisted_ai"

	"github.com/segmentio/ksuid"
)

const probeSearchOnlyPrompt = "Use the hosted web_search tool now to find the current Go release on go.dev. After the search completes, answer briefly in plain text with your findings. Do not answer from memory."

const probeAlphaSearchQuery = "current Go release site:go.dev"

type probeAlphaSearchReport struct {
	Name        string `json:"name"`
	Endpoint    string `json:"endpoint"`
	Model       string `json:"model"`
	HTTPStatus  int    `json:"httpStatus"`
	Outcome     string `json:"outcome"`
	OutputBytes int    `json:"outputBytes"`
	ResultCount int    `json:"resultCount"`
	Error       string `json:"error,omitempty"`
}

type probeSearchTransportReport struct {
	HostedSearch []probeMatrixRun        `json:"hostedSearch"`
	AlphaSearch  *probeAlphaSearchReport `json:"alphaSearch,omitempty"`
}

func probeAlphaSearchURL(responsesURL string) (string, error) {
	base, found := strings.CutSuffix(responsesURL, "/responses")
	if !found {
		return "", fmt.Errorf("endpoint %q is not a responses endpoint", responsesURL)
	}
	return base + "/alpha/search", nil
}

type probeHostedSearchToolVariant struct {
	Name string
	Tool json.RawMessage
}

// The codex variant mirrors the hosted tool Codex sends for models that are
// not on Responses Lite.
var probeHostedSearchToolVariants = []probeHostedSearchToolVariant{
	{Name: "bare", Tool: json.RawMessage(`{"type":"web_search"}`)},
	{Name: "codex", Tool: json.RawMessage(`{"type":"web_search","external_web_access":true,"search_content_types":["text","image"]}`)},
}

// buildProbeHostedSearchBody omits every optional field so that only the model
// and web search tool definition vary between hosted search controls.
func buildProbeHostedSearchBody(model string, input json.RawMessage, tool json.RawMessage) ([]byte, error) {
	if model == "" {
		return nil, fmt.Errorf("hosted search control requires a model")
	}
	if len(input) == 0 {
		return nil, fmt.Errorf("hosted search control requires input")
	}
	var toolType struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(tool, &toolType); err != nil || toolType.Type != "web_search" {
		return nil, fmt.Errorf("hosted search control requires a web_search tool: %w", err)
	}
	return json.Marshal(map[string]any{
		"model":  model,
		"input":  input,
		"store":  false,
		"stream": true,
		"tools":  []json.RawMessage{tool},
	})
}

func validateProbeAlphaSearchResponse(status int, body []byte) (probeAlphaSearchReport, error) {
	report := probeAlphaSearchReport{HTTPStatus: status, Outcome: "invalid"}
	if status != http.StatusOK {
		return report, fmt.Errorf("HTTP %d: %s", status, truncateProbeText(body, 500))
	}
	var response struct {
		Output  *string           `json:"output"`
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return report, fmt.Errorf("decode alpha search response: %w", err)
	}
	if response.Output != nil {
		report.OutputBytes = len(*response.Output)
	}
	report.ResultCount = len(response.Results)
	if report.OutputBytes == 0 && report.ResultCount == 0 {
		return report, fmt.Errorf("alpha search returned neither output nor results")
	}
	report.Outcome = "success"
	return report, nil
}

func truncateProbeText(body []byte, limit int) string {
	if len(body) <= limit {
		return string(body)
	}
	return string(body[:limit]) + "..."
}

// probeMinimalHeaders keeps only authentication and content negotiation so
// SDK- and application-specific headers cannot influence the result.
func probeMinimalHeaders(captured http.Header, accept string) http.Header {
	headers := make(http.Header)
	for _, name := range []string{"Authorization", "ChatGPT-Account-Id", "OpenAI-Organization", "OpenAI-Project"} {
		for _, value := range captured.Values(name) {
			headers.Add(name, value)
		}
	}
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", accept)
	return headers
}

// probeCodexClientVersion only needs to be a plausible Codex CLI version; the
// probe tests whether Codex client identification changes backend behavior.
const probeCodexClientVersion = "0.130.0"

// probeCodexHeaders adds the client identification and session headers Codex
// sends on Responses HTTP requests to the minimal header set.
func probeCodexHeaders(captured http.Header, accept string) http.Header {
	headers := probeMinimalHeaders(captured, accept)
	sessionID := ksuid.New().String()
	headers.Set("originator", "codex_cli_rs")
	headers.Set("User-Agent", "codex_cli_rs/"+probeCodexClientVersion+" (Mac OS 15.0.0; arm64) sidekick-probe")
	headers.Set("session-id", sessionID)
	headers.Set("thread-id", sessionID)
	headers.Set("x-client-request-id", sessionID)
	return headers
}

type probeHeaderVariant struct {
	Name  string
	Build func(captured http.Header, accept string) http.Header
}

var probeHeaderVariants = []probeHeaderVariant{
	{Name: "minimal", Build: probeMinimalHeaders},
	{Name: "codex", Build: probeCodexHeaders},
}

// executeSearchTransportProbe compares hosted web search across models on the
// captured endpoint and authentication, and exercises the standalone
// alpha/search endpoint that Codex uses for Responses Lite models.
func executeSearchTransportProbe(ctx context.Context, activities *persisted_ai.Llm2Activities, input persisted_ai.StreamInput, dir string, models []string) (json.RawMessage, error) {
	if len(models) == 0 {
		return nil, fmt.Errorf("search transport probe requires at least one model")
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create new private probe directory: %w", err)
	}
	base := http.DefaultTransport

	captured, err := captureProbeSearchOnly(ctx, activities, input, base)
	if err != nil {
		return nil, err
	}
	var capturedFields map[string]json.RawMessage
	if err := json.Unmarshal(captured.Body, &capturedFields); err != nil {
		return nil, err
	}
	var storedModel string
	if err := json.Unmarshal(capturedFields["model"], &storedModel); err != nil || storedModel == "" {
		return nil, fmt.Errorf("captured request has no model: %w", err)
	}

	var report probeSearchTransportReport
	var failures []error
	writeReport := func() error {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, string(data))
		return os.WriteFile(filepath.Join(dir, "report.json"), data, 0o600)
	}

	type hostedControl struct {
		model   string
		variant probeHostedSearchToolVariant
		headers probeHeaderVariant
	}
	var controls []hostedControl
	for _, model := range models {
		for _, variant := range probeHostedSearchToolVariants {
			for _, headers := range probeHeaderVariants {
				controls = append(controls, hostedControl{model: model, variant: variant, headers: headers})
			}
		}
	}
	for _, control := range controls {
		model := control.model
		body, err := buildProbeHostedSearchBody(model, capturedFields["input"], control.variant.Tool)
		if err != nil {
			return nil, err
		}
		request := &probeCapturedRequest{
			URL: captured.URL, Header: control.headers.Build(captured.Header, "text/event-stream"), Body: body,
		}
		name := "hosted-search-" + control.variant.Name + "-" + control.headers.Name + "-headers-" + model
		if err := os.WriteFile(filepath.Join(dir, name+".request.json"), body, 0o600); err != nil {
			return nil, err
		}
		transport := &probeCaptureTransport{base: base, expected: request}
		runErr := runRawProbe(ctx, request, transport)
		if err := os.WriteFile(filepath.Join(dir, name+".sse"), transport.responseBody.Bytes(), 0o600); err != nil {
			return nil, err
		}
		trace, traceErr := analyzeProbeSSE(bytes.NewReader(transport.responseBody.Bytes()), "")
		if !strings.HasPrefix(trace.Model, model) {
			traceErr = errors.Join(traceErr, fmt.Errorf("response model %q does not match requested %q", trace.Model, model))
		}
		if transport.calls != 1 || transport.statusCode != http.StatusOK {
			traceErr = errors.Join(traceErr, fmt.Errorf("expected one HTTP 200 response, got %d calls and status %d", transport.calls, transport.statusCode))
		}
		run := probeMatrixRun{Name: name, Endpoint: request.URL, AuthMode: "api", HTTPStatus: transport.statusCode, Trace: trace}
		if request.Header.Get("ChatGPT-Account-Id") != "" {
			run.AuthMode = "subscription"
		}
		if combined := errors.Join(runErr, traceErr); combined != nil {
			run.Error = combined.Error()
			failures = append(failures, fmt.Errorf("%s: %w", name, combined))
		}
		report.HostedSearch = append(report.HostedSearch, run)
		if err := writeReport(); err != nil {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, errors.Join(ctx.Err(), errors.Join(failures...))
		}
	}

	alpha, _, err := runProbeAlphaSearch(ctx, base, captured, storedModel, probeAlphaSearchQuery, "alpha-search-"+storedModel, dir)
	if err != nil {
		return nil, err
	}
	if alpha.Error != "" {
		failures = append(failures, fmt.Errorf("%s: %s", alpha.Name, alpha.Error))
	}
	report.AlphaSearch = &alpha
	if err := writeReport(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(report)
	return data, errors.Join(err, errors.Join(failures...))
}

func captureProbeSearchOnly(ctx context.Context, activities *persisted_ai.Llm2Activities, input persisted_ai.StreamInput, base http.RoundTripper) (*probeCapturedRequest, error) {
	searchInput := input
	searchInput.Options.Tools = []*common.Tool{{Type: common.ToolTypeWebSearch}}
	searchInput.Options.ToolChoice = common.ToolChoice{Type: common.ToolChoiceTypeAuto}
	searchInput.ToolNameMapping = nil
	history := persisted_ai.NewLlm2ChatHistory(input.FlowId, input.WorkspaceId)
	history.Append(&llm2.Message{Role: llm2.RoleUser, Content: llm2.TextContentBlocks(probeSearchOnlyPrompt)})
	searchInput.ChatHistory = &persisted_ai.ChatHistoryContainer{History: history}
	return captureProbeApplication(ctx, activities, searchInput, base)
}

// runProbeAlphaSearch reports endpoint failures in the returned report; the
// error is reserved for local failures that should abort the probe.
func runProbeAlphaSearch(ctx context.Context, base http.RoundTripper, captured *probeCapturedRequest, model, query, name, dir string) (probeAlphaSearchReport, []byte, error) {
	endpoint, err := probeAlphaSearchURL(captured.URL)
	if err != nil {
		return probeAlphaSearchReport{}, nil, err
	}
	body, err := json.Marshal(map[string]any{
		"id":    ksuid.New().String(),
		"model": model,
		"commands": map[string]any{
			"search_query": []map[string]string{{"q": query}},
		},
		"settings":          map[string]any{"external_web_access": true},
		"max_output_tokens": 2000,
	})
	if err != nil {
		return probeAlphaSearchReport{}, nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, name+".request.json"), body, 0o600); err != nil {
		return probeAlphaSearchReport{}, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return probeAlphaSearchReport{}, nil, err
	}
	request.Header = probeMinimalHeaders(captured.Header, "application/json")

	report := probeAlphaSearchReport{Outcome: "invalid"}
	var responseBody []byte
	response, err := (&http.Client{Transport: base}).Do(request)
	if err != nil {
		report.Error = err.Error()
	} else {
		var readErr error
		responseBody, readErr = io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if writeErr := os.WriteFile(filepath.Join(dir, name+".response.json"), responseBody, 0o600); writeErr != nil {
			return probeAlphaSearchReport{}, nil, writeErr
		}
		var validateErr error
		report, validateErr = validateProbeAlphaSearchResponse(response.StatusCode, responseBody)
		if combined := errors.Join(readErr, closeErr, validateErr); combined != nil {
			report.Error = combined.Error()
		}
	}
	report.Name, report.Endpoint, report.Model = name, endpoint, model
	return report, responseBody, nil
}
