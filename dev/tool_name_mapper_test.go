package dev

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAnthropicToolNameMapping_MapToolName(t *testing.T) {
	t.Parallel()

	config := anthropicToolNameMapping

	testCases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "native read tool alias",
			in:   bulkReadFileTool.Name,
			want: "Read",
		},
		{
			name: "native grep tool alias",
			in:   bulkSearchRepositoryTool.Name,
			want: "Grep",
		},
		{
			name: "native bash tool alias",
			in:   runCommandTool.Name,
			want: "Bash",
		},
		{
			name: "native ask user tool alias",
			in:   getHelpOrInputTool.Name,
			want: "AskUserQuestion",
		},
		{
			name: "prefixed done tool",
			in:   doneTool.Name,
			want: "mcp__tu__done",
		},
		{
			name: "prefixed symbol definitions tool",
			in:   getSymbolDefinitionsTool.Name,
			want: "mcp__tu__get_symbol_definitions",
		},
		{
			name: "prefixed custom flow tool",
			in:   setBaseBranchTool.Name,
			want: "mcp__tu__set_base_branch",
		},
		{
			name: "non-native tool is prefixed generically",
			in:   "unmapped_tool",
			want: "mcp__tu__unmapped_tool",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, config.MapToolName(tc.in))
		})
	}
}

func TestAnthropicToolNameMapping_ReverseMapToolName(t *testing.T) {
	t.Parallel()

	config := anthropicToolNameMapping

	testCases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "native read tool alias",
			in:   "Read",
			want: bulkReadFileTool.Name,
		},
		{
			name: "native ask user tool alias",
			in:   "AskUserQuestion",
			want: getHelpOrInputTool.Name,
		},
		{
			name: "prefixed done tool",
			in:   "mcp__tu__done",
			want: doneTool.Name,
		},
		{
			name: "prefixed symbol definitions tool",
			in:   "mcp__tu__get_symbol_definitions",
			want: getSymbolDefinitionsTool.Name,
		},
		{
			name: "prefixed custom flow tool",
			in:   "mcp__tu__record_dev_plan",
			want: recordDevPlanTool.Name,
		},
		{
			name: "generic prefixed tool strips prefix",
			in:   "mcp__tu__unmapped_tool",
			want: "unmapped_tool",
		},
		{
			name: "unknown unprefixed tool stays unchanged",
			in:   "unmapped_tool",
			want: "unmapped_tool",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, config.ReverseMapToolName(tc.in))
		})
	}
}
