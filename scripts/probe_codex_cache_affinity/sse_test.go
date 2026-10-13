package main

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sse(events ...string) string {
	var sb strings.Builder
	for _, e := range events {
		sb.WriteString("data: " + e + "\n\n")
	}
	return sb.String()
}

func TestParseResponsesStream(t *testing.T) {
	t.Parallel()
	completed := `{"type":"response.completed","response":{"model":"gpt-x","usage":{"input_tokens":5000,"input_tokens_details":{"cached_tokens":4096}}}}`
	tests := []struct {
		name       string
		body       string
		wantErr    string
		wantInput  int
		wantCached int
		wantModel  string
		wantTTFT   bool
	}{
		{name: "completed with done", body: sse(`{"type":"response.created"}`, completed, "[DONE]"), wantInput: 5000, wantCached: 4096, wantModel: "gpt-x"},
		{name: "zero cached is legitimate", body: sse(`{"type":"response.completed","response":{"usage":{"input_tokens":5000,"input_tokens_details":{"cached_tokens":0}}}}`), wantInput: 5000},
		{name: "missing cached field", body: sse(`{"type":"response.completed","response":{"usage":{"input_tokens":5000}}}`), wantErr: "without usage"},
		{name: "missing usage", body: sse(`{"type":"response.completed","response":{}}`), wantErr: "without usage"},
		{name: "cached exceeds input", body: sse(`{"type":"response.completed","response":{"usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":20}}}}`), wantErr: "implausible"},
		{name: "truncated stream", body: sse(`{"type":"response.created"}`, `{"type":"response.output_text.delta","delta":"O"}`), wantErr: "before response.completed"},
		{name: "failed event", body: sse(`{"type":"response.failed","message":"boom"}`), wantErr: "response.failed: boom"},
		{name: "error event with nested message", body: sse(`{"type":"error","error":{"message":"rate limited"}}`), wantErr: "rate limited"},
		{name: "malformed json", body: sse(`{not json`), wantErr: "bad SSE JSON"},
		{name: "ttft from first delta", body: sse(`{"type":"response.created"}`, `{"type":"response.output_text.delta","delta":"OK"}`, completed), wantInput: 5000, wantCached: 4096, wantModel: "gpt-x", wantTTFT: true},
		{name: "multiline data joined", body: "data: {\"type\":\"response.completed\",\ndata: \"response\":{\"usage\":{\"input_tokens\":7,\"input_tokens_details\":{\"cached_tokens\":0}}}}\n\n", wantInput: 7},
		{name: "final event without trailing blank line", body: "data: " + completed, wantInput: 5000, wantCached: 4096, wantModel: "gpt-x"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseResponsesStream(strings.NewReader(tt.body), time.Now().Add(-time.Millisecond))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.InputTokens != tt.wantInput || got.CachedTokens != tt.wantCached || got.Model != tt.wantModel {
				t.Errorf("got %+v", got)
			}
			if tt.wantTTFT != (got.FirstOutputAfter > 0) {
				t.Errorf("FirstOutputAfter = %v, wantTTFT=%v", got.FirstOutputAfter, tt.wantTTFT)
			}
		})
	}
}

// poisonAfterReader fails any read past the provided prefix, proving the parser
// stops consuming once it has the terminal event.
type poisonAfterReader struct {
	r    *strings.Reader
	done bool
}

func (p *poisonAfterReader) Read(b []byte) (int, error) {
	if p.done {
		return 0, errors.New("read past terminal event")
	}
	n, err := p.r.Read(b)
	if err == io.EOF {
		p.done = true
		err = nil
	}
	return n, err
}

// The raw usage object is the evidence for which billing counters a route
// returns, so it must survive verbatim: unknown fields, absent vs zero vs
// positive cache_write_tokens all have to be distinguishable afterwards.
func TestParseResponsesStream_PreservesRawUsage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		usage     string
		wantWrite string
	}{
		{name: "write field absent", usage: `{"input_tokens":5000,"input_tokens_details":{"cached_tokens":4096}}`, wantWrite: "absent"},
		{name: "write field zero", usage: `{"input_tokens":5000,"input_tokens_details":{"cached_tokens":4096,"cache_write_tokens":0}}`, wantWrite: "0"},
		{name: "write field positive with unknown siblings", usage: `{"input_tokens":5000,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":4990},"attribution":{"items":{}},"total_tokens":5005}`, wantWrite: "4990"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := sse(`{"type":"response.completed","response":{"model":"gpt-x","usage":` + tt.usage + `}}`)
			got, err := parseResponsesStream(strings.NewReader(body), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			var canonWant, canonGot any
			if err := json.Unmarshal([]byte(tt.usage), &canonWant); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(got.RawUsage, &canonGot); err != nil {
				t.Fatalf("RawUsage not JSON: %v (%s)", err, got.RawUsage)
			}
			if !reflect.DeepEqual(canonWant, canonGot) {
				t.Errorf("RawUsage = %s, want %s", got.RawUsage, tt.usage)
			}
			var details struct {
				InputTokensDetails map[string]json.RawMessage `json:"input_tokens_details"`
			}
			if err := json.Unmarshal(got.RawUsage, &details); err != nil {
				t.Fatal(err)
			}
			write, present := details.InputTokensDetails["cache_write_tokens"]
			gotWrite := "absent"
			if present {
				gotWrite = string(write)
			}
			if gotWrite != tt.wantWrite {
				t.Errorf("cache_write_tokens = %s, want %s", gotWrite, tt.wantWrite)
			}
		})
	}
}

func TestParseResponsesStream_StopsAtCompleted(t *testing.T) {
	t.Parallel()
	body := sse(`{"type":"response.completed","response":{"usage":{"input_tokens":5,"input_tokens_details":{"cached_tokens":5}}}}`)
	got, err := parseResponsesStream(&poisonAfterReader{r: strings.NewReader(body)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.InputTokens != 5 {
		t.Fatalf("got %+v", got)
	}
}

func TestRotatedArms(t *testing.T) {
	t.Parallel()
	arms := []arm{"a", "b", "c"}
	got := [][]arm{rotatedArms(arms, 0), rotatedArms(arms, 1), rotatedArms(arms, 2), rotatedArms(arms, 3)}
	want := [][]arm{{"a", "b", "c"}, {"b", "c", "a"}, {"c", "a", "b"}, {"a", "b", "c"}}
	for i := range want {
		if strings.Join(toStrings(got[i]), ",") != strings.Join(toStrings(want[i]), ",") {
			t.Errorf("round %d: got %v want %v", i, got[i], want[i])
		}
	}
}

func toStrings(arms []arm) []string {
	out := make([]string, len(arms))
	for i, a := range arms {
		out[i] = string(a)
	}
	return out
}
