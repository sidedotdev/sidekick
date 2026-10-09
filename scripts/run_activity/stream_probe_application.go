package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"sidekick/llm2"
)

type probeResponseExchange struct {
	RequestBody  []byte
	ResponseBody bytes.Buffer
	StatusCode   int
}

func probeHostedRequest(captured *probeCapturedRequest) (*probeCapturedRequest, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(captured.Body, &fields); err != nil {
		return nil, err
	}
	var tools []json.RawMessage
	if err := json.Unmarshal(fields["tools"], &tools); err != nil {
		return nil, err
	}
	searches := 0
	for i, raw := range tools {
		var tool struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &tool); err != nil {
			return nil, err
		}
		if tool.Type == "web_search" {
			searches++
		} else if name, ok := llm2.BuiltinToolNameFromClientName(tool.Name); tool.Type == "function" && ok && name == "web_search" {
			tools[i] = json.RawMessage(`{"type":"web_search"}`)
			searches++
		}
	}
	if searches != 1 {
		return nil, fmt.Errorf("hosted control requires exactly one search tool, got %d", searches)
	}
	rawTools, err := json.Marshal(tools)
	if err != nil {
		return nil, err
	}
	return probeRequestVariant(captured, map[string]json.RawMessage{"tools": rawTools})
}

func analyzeProbeApplication(transport *probeCaptureTransport, response *llm2.MessageResponse, functionName string) (probeTraceReport, error) {
	report := probeTraceReport{Outcome: "invalid"}
	if len(transport.exchanges) == 0 {
		return report, fmt.Errorf("provider made no Responses requests")
	}
	var expected struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(transport.exchanges[0].RequestBody, &expected); err != nil {
		return report, err
	}
	for i, exchange := range transport.exchanges {
		trace, err := analyzeProbeLiteSSE(bytes.NewReader(exchange.ResponseBody.Bytes()))
		report.ResponseID, report.Model = trace.ResponseID, trace.Model
		report.EventCount += trace.EventCount
		report.LastEventType = trace.LastEventType
		report.SearchesCompleted += trace.SearchesCompleted
		report.ErrorCode, report.ErrorMessage = trace.ErrorCode, trace.ErrorMessage
		if trace.Outcome == "providerError" {
			report.Outcome = "providerError"
		}
		if err != nil {
			return report, fmt.Errorf("provider round %d: %w", i+1, err)
		}
		if exchange.StatusCode != http.StatusOK || trace.Model != expected.Model {
			return report, fmt.Errorf("provider round %d: status %d, model %q; expected HTTP 200 and model %q",
				i+1, exchange.StatusCode, trace.Model, expected.Model)
		}
	}
	if response == nil {
		return report, fmt.Errorf("provider returned no response")
	}
	if response.Model != expected.Model {
		return report, fmt.Errorf("provider returned model %q, expected %q", response.Model, expected.Model)
	}

	searchCalls := make(map[string]bool)
	completedSearches := make(map[string]bool)
	hasText, functionDone := false, false
	for _, block := range response.Output.Content {
		if call := block.BuiltinToolUse; call != nil && call.Name == "web_search" {
			searchCalls[call.Id] = true
		}
		if result := block.BuiltinToolResult; result != nil && result.Name == "web_search" {
			if result.IsError {
				return report, fmt.Errorf("provider search failed: %s", result.Content)
			}
			if !searchCalls[result.ToolCallId] || completedSearches[result.ToolCallId] {
				return report, fmt.Errorf("unmatched or duplicate search result %q", result.ToolCallId)
			}
			if strings.TrimSpace(result.Content) == "" && len(result.SearchResults) == 0 {
				return report, fmt.Errorf("empty search result %q", result.ToolCallId)
			}
			completedSearches[result.ToolCallId] = true
		}
		if call := block.ToolUse; call != nil {
			if functionName == "" {
				return report, fmt.Errorf("unexpected function in search-only response")
			}
			if call.Name == functionName {
				if !validProbeArguments(call.Arguments) {
					return report, fmt.Errorf("invalid completed function arguments")
				}
				functionDone = true
				report.FunctionItemID = block.Id
				report.TransitionObserved = len(completedSearches) > 0 || report.SearchesCompleted > 0
			}
		}
		hasText = hasText || (block.Type == llm2.ContentBlockTypeText && strings.TrimSpace(block.Text) != "")
	}
	if transport.expected.Header.Get("ChatGPT-Account-Id") != "" {
		report.SearchesCompleted = len(completedSearches)
		if len(searchCalls) != len(completedSearches) {
			return report, fmt.Errorf("provider returned unresolved search calls")
		}
	}
	if report.SearchesCompleted == 0 {
		report.Outcome = "inconclusive"
		return report, fmt.Errorf("no completed web search")
	}
	if functionName != "" && (!functionDone || !report.TransitionObserved) {
		report.Outcome = "inconclusive"
		return report, fmt.Errorf("no completed web search followed by %s", functionName)
	}
	if functionName == "" && !hasText {
		report.Outcome = "inconclusive"
		return report, fmt.Errorf("search completed without a text answer")
	}
	report.Outcome = "success"
	return report, nil
}

func validateProbeContinuation(expected, actual []byte) error {
	var want, got map[string]json.RawMessage
	if err := json.Unmarshal(expected, &want); err != nil {
		return fmt.Errorf("decode expected continuation: %w", err)
	}
	if err := json.Unmarshal(actual, &got); err != nil {
		return fmt.Errorf("decode continuation: %w", err)
	}
	var input []json.RawMessage
	if err := json.Unmarshal(got["input"], &input); err != nil {
		return fmt.Errorf("decode continuation input: %w", err)
	}
	if len(input) == 0 {
		return fmt.Errorf("continuation requires nonempty input")
	}
	want["input"] = got["input"]
	withInput, err := json.Marshal(want)
	if err != nil {
		return err
	}
	if err := validateProbeRequest(withInput, actual); err == nil {
		return nil
	}

	// The provider withdraws client search at its round limit.
	var tools []json.RawMessage
	if err := json.Unmarshal(want["tools"], &tools); err != nil {
		return fmt.Errorf("decode expected continuation tools: %w", err)
	}
	remaining := make([]json.RawMessage, 0, len(tools))
	removed := false
	for _, raw := range tools {
		var tool struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &tool); err != nil {
			return err
		}
		if name, ok := llm2.BuiltinToolNameFromClientName(tool.Name); tool.Type == "function" && ok && name == "web_search" {
			removed = true
			continue
		}
		remaining = append(remaining, raw)
	}
	if !removed {
		return validateProbeRequest(withInput, actual)
	}
	if len(remaining) == 0 {
		delete(want, "tools")
		delete(want, "tool_choice")
	} else {
		want["tools"], err = json.Marshal(remaining)
		if err != nil {
			return err
		}
	}
	withoutSearch, err := json.Marshal(want)
	if err != nil {
		return err
	}
	return validateProbeRequest(withoutSearch, actual)
}
