package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sidekick/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

const fixtureSchema = `CREATE TABLE flow_actions (
    id TEXT PRIMARY KEY,
    subflow_name TEXT NOT NULL,
    subflow_description TEXT NOT NULL,
    subflow_id TEXT,
    flow_id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    created DATETIME NOT NULL,
    updated DATETIME NOT NULL,
    action_type TEXT NOT NULL,
    action_params TEXT NOT NULL,
    action_status TEXT NOT NULL,
    action_result TEXT,
    is_human_action BOOLEAN NOT NULL,
    is_callback_action BOOLEAN NOT NULL,
    temporal_activities TEXT
)`

var fixtureCutoff = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

type fixtureAction struct {
	id, flowId, actionType, created, updated, params string
	result                                           *string
}

func requireDuckDB(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("duckdb")
	if err != nil {
		t.Skip("duckdb CLI not installed")
	}
	return bin
}

func refsParams(t *testing.T, blockKeys ...string) string {
	t.Helper()
	refs := make([]map[string]any, len(blockKeys))
	for i, key := range blockKeys {
		refs[i] = map[string]any{"blockKeys": []string{key}, "role": "user"}
	}
	params, err := json.Marshal(map[string]any{
		"messages":          map[string]any{"type": "llm2", "refs": refs},
		"tools":             []map[string]any{{"name": "run_command"}},
		"secretManagerType": "keyring",
	})
	require.NoError(t, err)
	return string(params)
}

func result(provider, model, usage string) *string {
	r := `{"id":"msg","model":"` + model + `","provider":"` + provider + `","usage":` + usage + `}`
	return &r
}

func utc(clock string) string {
	return "2026-10-02 " + clock + " +0000 UTC"
}

func fixtureActions(t *testing.T) []fixtureAction {
	const opus = "claude-opus-5-5"
	malformed := `{not json`
	notLlm := `"ok"`
	return []fixtureAction{
		{"c1", "f1", "generate.code_edits", utc("10:00:00.000001"), utc("10:00:30"),
			refsParams(t, "b_init", "b_u1m"),
			result("anthropic", opus, `{"inputTokens":1000,"outputTokens":100,"cacheReadInputTokens":0,"cacheWriteInputTokens":1000}`)},
		{"c2", "f1", "generate.code_edits", utc("10:01:00"), utc("10:01:30"),
			refsParams(t, "b_init", "b_u1", "b_a1", "b_u2m"),
			result("anthropic", opus, `{"inputTokens":1600,"outputTokens":100,"cacheReadInputTokens":1000,"cacheWriteInputTokens":600}`)},
		{"c3", "f1", "generate.code_edits", utc("10:02:00"), utc("10:02:30"),
			refsParams(t, "b_init", "b_u1", "b_a1", "b_u2m", "b_a2", "b_u3m"),
			result("anthropic", opus, `{"inputTokens":2200,"outputTokens":100,"cacheReadInputTokens":1600,"cacheWriteInputTokens":600}`)},
		{"c4", "f1", "generate.code_edits", utc("10:20:00"), utc("10:20:30"),
			refsParams(t, "b_init", "b_u3", "b_a3", "b_u4m"),
			result("anthropic", opus, `{"inputTokens":900,"outputTokens":100,"cacheReadInputTokens":300,"cacheWriteInputTokens":600}`)},
		{"c5", "f2", "generate.advise", "2026-10-02 03:00:00 -0700 PDT", "2026-10-02 03:00:10 -0700 PDT",
			refsParams(t, "b_adv"),
			result("openai", "gpt-6-astra", `{"inputTokens":500,"outputTokens":50}`)},
		{"c6", "f2", "generate.advise", utc("10:00:00"), utc("10:00:00"), refsParams(t, "b_adv2"), &malformed},
		{"c7", "f3", "generate.code_edits", utc("10:00:00"), utc("10:00:00"), refsParams(t, "b_f3"),
			result("anthropic", opus, `{"inputTokens":100,"outputTokens":10,"cacheReadInputTokens":80,"cacheWriteInputTokens":50}`)},
		{"c8", "f1", "generate.code_edits", utc("13:00:00"), utc("13:00:30"), refsParams(t, "b_init", "b_late"),
			result("anthropic", opus, `{"inputTokens":3000,"outputTokens":100}`)},
		{"c9", "f3", "generate.code_edits", utc("10:00:00"), utc("10:00:00"), refsParams(t, "b_f3b"),
			result("anthropic", opus, `{"inputTokens":"abc","outputTokens":10}`)},
		{"c10", "f3", "generate.code_edits", utc("10:00:00"), utc("10:00:00"), refsParams(t, "b_f3c"),
			result("anthropic", opus, `{"inputTokens":100,"outputTokens":-5}`)},
		{"c11", "f3", "generate.code_edits", "garbage", "garbage", refsParams(t, "b_f3d"),
			result("anthropic", opus, `{"inputTokens":100,"outputTokens":5}`)},
		{"c12", "f1", "generate.code_edits", utc("10:05:00"), utc("10:05:30"), refsParams(t, "b_other"),
			result("anthropic", opus, `{"inputTokens":400,"outputTokens":40,"cacheReadInputTokens":0,"cacheWriteInputTokens":400}`)},
		{"c13", "f1", "generate.code_edits", utc("10:21:00"), utc("10:21:30"),
			refsParams(t, "b_init", "b_a3", "b_u3", "b_u4m", "b_a4"),
			result("anthropic", opus, `{"inputTokens":1000,"outputTokens":100,"cacheReadInputTokens":900,"cacheWriteInputTokens":100}`)},
		{"c14", "f1", "generate.code_edits", utc("10:22:00"), utc("10:22:30"),
			refsParams(t, "b_init", "b_a3", "b_u3", "b_u4m", "b_a4", "b_u5"),
			result("anthropic", opus, `{"inputTokens":800,"outputTokens":100,"cacheReadInputTokens":700,"cacheWriteInputTokens":100}`)},
		{"n1", "f1", "tool_call.run_command", utc("10:00:00"), utc("10:00:00"), `{}`, &notLlm},
	}
}

func writeFixtureCoreDB(t *testing.T, path string, actions []fixtureAction) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(fixtureSchema)
	require.NoError(t, err)
	for _, a := range actions {
		_, err = db.Exec(`INSERT INTO flow_actions (id, subflow_name, subflow_description, subflow_id, flow_id, workspace_id,
			created, updated, action_type, action_params, action_status, action_result, is_human_action, is_callback_action)
			VALUES (?, '', '', '', ?, 'ws1', ?, ?, ?, ?, 'complete', ?, false, false)`,
			a.id, a.flowId, a.created, a.updated, a.actionType, a.params, a.result)
		require.NoError(t, err)
	}
}

// fakeLookupModel mirrors common.GetModel: a priced model under its own
// provider, and a model found only under an unrelated provider (matched=false).
func fakeLookupModel(provider, model string) (*common.ModelInfo, bool) {
	switch model {
	case "claude-opus-5-5":
		return &common.ModelInfo{ID: model, Cost: common.Cost{Input: 15, Output: 75, CacheRead: 1.5, CacheWrite: 18.75}}, true
	case "gpt-6-astra":
		return &common.ModelInfo{ID: model, Cost: common.Cost{Input: 10, Output: 60, CacheRead: 1}}, false
	}
	return nil, false
}

func buildSnapshot(t *testing.T) (bin, out string) {
	t.Helper()
	bin = requireDuckDB(t)
	dir := t.TempDir()
	coreDB := filepath.Join(dir, "core.db")
	writeFixtureCoreDB(t, coreDB, fixtureActions(t))
	out = filepath.Join(dir, "usage.duckdb")
	err := extract(context.Background(), extractConfig{
		DuckDB:      bin,
		CoreDB:      coreDB,
		Out:         out,
		Cutoff:      fixtureCutoff,
		ExtractedAt: fixtureCutoff.Add(time.Minute),
		LookupModel: fakeLookupModel,
	})
	require.NoError(t, err)
	return bin, out
}

// queryById runs a query whose first column is "id" and indexes rows by it.
func queryById(t *testing.T, bin, db, query string) map[string]map[string]any {
	t.Helper()
	output, err := runDuckDB(context.Background(), bin, db, query, "-readonly", "-json")
	require.NoError(t, err)
	var rows []map[string]any
	if len(bytes.TrimSpace(output)) > 0 {
		require.NoError(t, json.Unmarshal(output, &rows))
	}
	byId := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		byId[row["id"].(string)] = row
	}
	return byId
}

type rowExpectation struct {
	name   string
	id     string
	expect map[string]any
}

func assertRows(t *testing.T, rows map[string]map[string]any, tests []rowExpectation) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			row, ok := rows[tt.id]
			require.True(t, ok, "missing row %s", tt.id)
			for column, want := range tt.expect {
				assert.Equal(t, want, row[column], "column %s", column)
			}
		})
	}
}

func TestExtract_CallsSnapshot(t *testing.T) {
	t.Parallel()
	bin, out := buildSnapshot(t)
	calls := queryById(t, bin, out, `SELECT id, CAST(created_at AS VARCHAR) AS created_at, provider, model, has_usage,
		input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, len(msg_refs) AS n_refs, msg_refs[1] AS first_ref,
		created_unparsed, result_invalid_json, usage_unparseable, negative_usage, cache_exceeds_input
		FROM llm_calls`)

	assert.NotContains(t, calls, "c8", "calls created at or after the cutoff are excluded")
	assert.NotContains(t, calls, "n1", "only generate.* actions are LLM calls")

	assertRows(t, calls, []rowExpectation{
		{"complete usage", "c2", map[string]any{
			"created_at": "2026-10-02 10:01:00", "provider": "anthropic", "model": "claude-opus-5-5", "has_usage": true,
			"input_tokens": 1600.0, "output_tokens": 100.0, "cache_read_tokens": 1000.0, "cache_write_tokens": 600.0,
			"n_refs": 4.0, "first_ref": "b_init",
			"created_unparsed": false, "result_invalid_json": false, "usage_unparseable": false,
			"negative_usage": false, "cache_exceeds_input": false,
		}},
		{"offset normalized to UTC and missing cache fields stay NULL", "c5", map[string]any{
			"created_at": "2026-10-02 10:00:00", "provider": "openai", "input_tokens": 500.0,
			"cache_read_tokens": nil, "cache_write_tokens": nil, "cache_exceeds_input": false,
		}},
		{"malformed result JSON", "c6", map[string]any{"result_invalid_json": true, "has_usage": false, "input_tokens": nil}},
		{"cache read plus write above input", "c7", map[string]any{"cache_exceeds_input": true}},
		{"non-numeric usage", "c9", map[string]any{"usage_unparseable": true, "input_tokens": nil}},
		{"negative usage", "c10", map[string]any{"negative_usage": true}},
		{"unparseable timestamp", "c11", map[string]any{"created_unparsed": true, "created_at": nil}},
	})
}

func TestExtract_HistoryLinks(t *testing.T) {
	t.Parallel()
	bin, out := buildSnapshot(t)
	links := queryById(t, bin, out, `SELECT id, prev_id, link_class, common_prefix, dropped_messages, input_delta, gap_seconds, model_changed
		FROM llm_call_links`)

	assert.NotContains(t, links, "c11", "calls without a parseable timestamp can't be ordered into a history")
	assertRows(t, links, []rowExpectation{
		{"first call of a history", "c1", map[string]any{"prev_id": nil, "link_class": "first", "model_changed": false}},
		{"only the previous last message's ref changed", "c2", map[string]any{
			"prev_id": "c1", "link_class": "extend_tail_change", "common_prefix": 1.0, "dropped_messages": 1.0,
			"input_delta": 600.0, "gap_seconds": 30.0,
		}},
		{"pure append", "c3", map[string]any{
			"prev_id": "c2", "link_class": "extend", "common_prefix": 4.0, "dropped_messages": 0.0, "input_delta": 600.0,
		}},
		{"earlier messages vanished and input shrank", "c4", map[string]any{
			"prev_id": "c3", "link_class": "truncate", "common_prefix": 1.0, "dropped_messages": 5.0,
			"input_delta": -1300.0, "gap_seconds": 1050.0,
		}},
		{"same ref set in a different order is not continuity, but no shrink means no truncation", "c13", map[string]any{
			"prev_id": "c4", "link_class": "mutate", "common_prefix": 1.0, "dropped_messages": 0.0,
		}},
		{"shrinking input on an append is kept as a negative delta", "c14", map[string]any{
			"prev_id": "c13", "link_class": "extend", "input_delta": -200.0,
		}},
		{"different first message in the same flow and role is a separate history", "c12", map[string]any{
			"prev_id": nil, "link_class": "first",
		}},
	})
}

func TestExtract_ModelPricesAndMeta(t *testing.T) {
	t.Parallel()
	bin, out := buildSnapshot(t)
	prices := queryById(t, bin, out, `SELECT model AS id, provider_matched, input_per_mtok, output_per_mtok, cache_read_per_mtok, cache_write_per_mtok
		FROM model_prices`)
	assertRows(t, prices, []rowExpectation{
		{"priced model", "claude-opus-5-5", map[string]any{
			"provider_matched": true, "input_per_mtok": 15.0, "output_per_mtok": 75.0, "cache_read_per_mtok": 1.5, "cache_write_per_mtok": 18.75,
		}},
		{"price from an unrelated provider is kept but marked unmatched", "gpt-6-astra", map[string]any{
			"provider_matched": false, "input_per_mtok": 10.0, "cache_write_per_mtok": nil,
		}},
	})

	meta := queryById(t, bin, out, `SELECT 'meta' AS id, CAST(cutoff_utc AS VARCHAR) AS cutoff_utc,
		CAST(extracted_at_utc AS VARCHAR) AS extracted_at_utc FROM extraction_meta`)
	assert.Equal(t, "2026-10-02 12:00:00", meta["meta"]["cutoff_utc"])
	assert.Equal(t, "2026-10-02 12:01:00", meta["meta"]["extracted_at_utc"])
}

// markdownRow finds the first table row in section whose cells start with the
// given prefix cells, returning all its trimmed cells.
func markdownRow(t *testing.T, output, section string, prefix ...string) []string {
	t.Helper()
	_, rest, found := strings.Cut(output, "## "+section)
	require.True(t, found, "section %q missing", section)
	body, _, _ := strings.Cut(rest, "\n## ")
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if len(cells) < len(prefix) {
			continue
		}
		matches := true
		for i, want := range prefix {
			if cells[i] != want {
				matches = false
				break
			}
		}
		if matches {
			return cells
		}
	}
	require.Failf(t, "row not found", "section %q has no row starting with %v:\n%s", section, prefix, body)
	return nil
}

func TestReport_Numbers(t *testing.T) {
	t.Parallel()
	bin, out := buildSnapshot(t)
	var buf bytes.Buffer
	require.NoError(t, report(context.Background(), bin, out, sinceDefault, &buf))
	output := buf.String()

	// Spend columns: role, provider, model, calls, histories, input_mtok, read_share, write_share, output_mtok, cost_usd, cost_pct, unpriced_calls.
	// Priced opus calls are c1-c4, c12-c14 (c7 is excluded for cache > input):
	// uncached 0 + reads 4500*1.5 + writes 3400*18.75 + output 640*75 = $0.1185.
	opus := markdownRow(t, output, "Spend", "generate.code_edits", "anthropic", "claude-opus-5-5")
	assert.Equal(t, "7", opus[3], "calls")
	assert.Equal(t, "0.12", opus[9], "cost_usd")
	assert.Equal(t, "0", opus[11], "unpriced_calls")

	astra := markdownRow(t, output, "Spend", "generate.advise", "openai", "gpt-6-astra")
	assert.Equal(t, "1", astra[3], "calls")
	assert.Equal(t, "NULL", astra[9], "cost must be NULL when the only price came from an unrelated provider")
	assert.Equal(t, "1", astra[11], "unpriced_calls")

	// Growth columns: role, model, histories, growth_links, negative_growth_links, g_median, g_mean, g_p90,
	// tail_changes, mutations, drops, growth_links_per_drop, pre_drop_median, post_drop_median, drop_size_median, ...
	growth := markdownRow(t, output, "History growth", "generate.code_edits", "claude-opus-5-5")
	assert.Equal(t, "2", growth[2], "histories (b_init and b_other)")
	assert.Equal(t, "3", growth[3], "growth links: c2, c3, c14")
	assert.Equal(t, "1", growth[4], "negative growth links: c14")
	assert.Equal(t, "333.0", growth[6], "g_mean keeps the negative delta: (600+600-200)/3")
	assert.Equal(t, "1", growth[8], "tail changes: c2")
	assert.Equal(t, "1", growth[9], "mutations: reordered c13")
	assert.Equal(t, "1", growth[10], "drops: c4")
	assert.Equal(t, "1300.0", growth[14], "drop_size_median: 2200-900")

	// Buffer columns: role, model, rho, g_mean, k_post_drop, drop_size_median, b_star, inapplicable_reason.
	// rho = 18.75/1.5 = 12.5; K = 900; B* = sqrt(2*333.33*900*11.5) = 2626.8.
	buffer := markdownRow(t, output, "Buffer model", "generate.code_edits", "claude-opus-5-5")
	assert.Equal(t, "12.5", buffer[2], "rho")
	assert.Equal(t, "900.0", buffer[4], "k_post_drop")
	assert.Equal(t, "2627.0", buffer[6], "b_star")
	assert.Equal(t, "NULL", buffer[7], "no inapplicable reason")

	advise := markdownRow(t, output, "Buffer model", "generate.advise", "gpt-6-astra")
	assert.Equal(t, "NULL", advise[6], "b_star")
	assert.Equal(t, "no trusted price", advise[7])
}