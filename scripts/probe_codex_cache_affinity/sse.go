package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// streamOutcome is what a single Responses SSE stream told us about caching.
type streamOutcome struct {
	Model        string
	InputTokens  int
	CachedTokens int
	// FirstOutputAfter is the delay until the first output or reasoning delta,
	// i.e. time-to-first-token rather than time-to-first-SSE-line.
	FirstOutputAfter time.Duration
	Completed        bool
}

var errNoUsage = errors.New("stream completed without usage")

// parseResponsesStream consumes a Responses API SSE body until the terminal
// event or EOF and extracts usage. It fails loudly when the stream ends without
// a completed response carrying a usable input/cached token pair.
func parseResponsesStream(body io.Reader, start time.Time) (streamOutcome, error) {
	var out streamOutcome
	scanner := bufio.NewScanner(body)
	scanner.Buffer(nil, bufio.MaxScanTokenSize<<8)
	var data []byte
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		event := data
		data = nil
		if bytes.Equal(bytes.TrimSpace(event), []byte("[DONE]")) {
			return nil
		}
		var envelope struct {
			Type     string `json:"type"`
			Message  string `json:"message"`
			Error    *struct{ Message string }
			Delta    json.RawMessage `json:"delta"`
			Response struct {
				Model string `json:"model"`
				Usage *struct {
					InputTokens        int `json:"input_tokens"`
					InputTokensDetails *struct {
						CachedTokens *int `json:"cached_tokens"`
					} `json:"input_tokens_details"`
				} `json:"usage"`
			} `json:"response"`
		}
		if err := json.Unmarshal(event, &envelope); err != nil {
			return fmt.Errorf("bad SSE JSON: %w", err)
		}
		switch envelope.Type {
		case "response.output_text.delta", "response.reasoning_summary_text.delta", "response.function_call_arguments.delta":
			if out.FirstOutputAfter == 0 && len(envelope.Delta) > 2 {
				out.FirstOutputAfter = time.Since(start)
			}
		case "response.completed":
			out.Completed = true
			out.Model = envelope.Response.Model
			u := envelope.Response.Usage
			if u == nil || u.InputTokens <= 0 || u.InputTokensDetails == nil || u.InputTokensDetails.CachedTokens == nil {
				return errNoUsage
			}
			out.InputTokens = u.InputTokens
			out.CachedTokens = *u.InputTokensDetails.CachedTokens
			if out.CachedTokens < 0 || out.CachedTokens > out.InputTokens {
				return fmt.Errorf("implausible usage: cached=%d input=%d", out.CachedTokens, out.InputTokens)
			}
		case "error", "response.failed", "response.incomplete":
			msg := envelope.Message
			if msg == "" && envelope.Error != nil {
				msg = envelope.Error.Message
			}
			return fmt.Errorf("%s: %s", envelope.Type, msg)
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Bytes()
		if value, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			// Multi-line data fields are joined with newlines per the SSE spec.
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, bytes.TrimPrefix(value, []byte(" "))...)
			continue
		}
		if len(line) == 0 {
			if err := flush(); err != nil {
				return out, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return out, err
	}
	if err := flush(); err != nil {
		return out, err
	}
	if !out.Completed {
		return out, errors.New("stream ended before response.completed")
	}
	return out, nil
}
