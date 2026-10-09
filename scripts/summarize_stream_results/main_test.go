package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const webSearchInput = `{"Options":{"tools":[{"name":"read_file"},{"type":"web_search"}]},"FlowActionId":"fa_1"}`
const plainInput = `{"Options":{"tools":[{"name":"read_file"}]},"FlowActionId":"fa_2"}`

func dump(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

func TestSummarize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		input         string
		wantContains  []string
		wantErrSubstr string
	}{
		{
			name: "completed response with builtin blocks",
			input: dump(
				"10 2026-01-01T00:00:00Z ActivityTaskScheduled Stream id=10 scheduleToStart=0s startToClose=45m0s",
				"    Input[0]: "+webSearchInput,
				"11 2026-01-01T00:00:01Z ActivityTaskStarted Stream scheduled=10 identity=\"w\" attempt=1",
				"12 2026-01-01T00:00:02Z ActivityTaskCompleted Stream scheduled=10 identity=\"w\"",
				`    Result[0]: {"model":"m","provider":"openai","output":{"role":"assistant","content":[{"type":"reasoning","reasoning":{"text":""}},{"type":"builtin_tool_use","builtinToolUse":{"id":"ws_1","name":"web_search","arguments":"{\"query\":\"go\"}","status":"completed"}},{"type":"text","text":"done"}]},"stopReason":"stop"}`,
			),
			wantContains: []string{
				"scheduled=10 action=fa_1 openai/m webSearchOffered=true stop=stop blocks=[reasoning, builtin_tool_use(web_search completed), text]",
				"1 scheduled, 1 completed, 0 failed",
				"builtin_tool_use:1",
			},
		},
		{
			name: "failed attempt then retry without web search",
			input: dump(
				"10 2026-01-01T00:00:00Z ActivityTaskScheduled Stream id=10 scheduleToStart=0s startToClose=45m0s",
				"    Input[0]: "+plainInput,
				"13 2026-01-01T00:00:02Z ActivityTaskFailed Stream scheduled=10 started=11 identity=\"w\"",
				"    Failure: boom",
				"20 2026-01-01T00:00:03Z ActivityTaskScheduled Stream id=20 scheduleToStart=0s startToClose=45m0s",
				"    Input[0]: "+plainInput,
				"22 2026-01-01T00:00:04Z ActivityTaskCompleted Stream scheduled=20 identity=\"w\"",
				`    Result[0]: {"model":"m","provider":"anthropic","output":{"role":"assistant","content":[{"type":"text","text":"hi"}]},"stopReason":"end_turn"}`,
			),
			wantContains: []string{
				"scheduled=10 action=fa_2 FAILED",
				"scheduled=20 action=fa_2 anthropic/m webSearchOffered=false stop=end_turn blocks=[text]",
				"2 scheduled, 1 completed, 1 failed",
			},
		},
		{
			name: "malformed result payload is an error, not a silent omission",
			input: dump(
				"10 2026-01-01T00:00:00Z ActivityTaskScheduled Stream id=10 scheduleToStart=0s startToClose=45m0s",
				"    Input[0]: "+plainInput,
				"12 2026-01-01T00:00:02Z ActivityTaskCompleted Stream scheduled=10 identity=\"w\"",
				`    Result[0]: {"model":"m","output":{"role":"assistant","content":[{"type":"text"`,
			),
			wantErrSubstr: "cannot parse result",
		},
		{
			name: "non-verbose summarized input lacks FlowActionId",
			input: dump(
				"10 2026-01-01T00:00:00Z ActivityTaskScheduled Stream id=10 scheduleToStart=0s startToClose=45m0s",
				`    Input[0]: {"Options":{}}`,
			),
			wantErrSubstr: "no FlowActionId",
		},
		{
			name: "completion without a scheduled input",
			input: dump(
				"12 2026-01-01T00:00:02Z ActivityTaskCompleted Stream scheduled=10 identity=\"w\"",
				`    Result[0]: {"output":{"role":"assistant","content":[]}}`,
			),
			wantErrSubstr: "no matching scheduled input",
		},
		{
			name: "completion missing its result payload",
			input: dump(
				"10 2026-01-01T00:00:00Z ActivityTaskScheduled Stream id=10 scheduleToStart=0s startToClose=45m0s",
				"    Input[0]: "+plainInput,
				"12 2026-01-01T00:00:02Z ActivityTaskCompleted Stream scheduled=10 identity=\"w\"",
			),
			wantErrSubstr: "no Result payload",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out strings.Builder
			err := summarize(strings.NewReader(tt.input), &out)
			if tt.wantErrSubstr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrSubstr)
				return
			}
			require.NoError(t, err)
			for _, want := range tt.wantContains {
				assert.Contains(t, out.String(), want)
			}
		})
	}
}