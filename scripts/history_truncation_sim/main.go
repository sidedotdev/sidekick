// Command history_truncation_sim replays recorded LLM call histories through
// alternative chat-history management policies and estimates what each would
// have cost under an idealized prefix cache.
//
// Input is the DuckDB snapshot produced by scripts/llm_usage_stats plus the
// sidekick KV store the message blocks live in. For each (model, role) the
// tool:
//
//  1. hydrates every call's prompt and derives the ordered changes between
//     consecutive calls, separating genuine content changes from the
//     recorded policy's own truncation decisions;
//
//  2. fits billed input tokens against measured prompt chars;
//
//  3. reports how far the idealized cache model is from the recorded usage
//     (baseline error), so savings are read with that error in mind;
//
//  4. replays each policy and prints read/write tokens, truncation counts,
//     and cost relative to the recorded policy.
//
//     go run ./scripts/history_truncation_sim -db .side/tmp/llm_usage.duckdb -model claude-fable-5-1
//     go run ./scripts/history_truncation_sim -db .side/tmp/llm_usage.duckdb -model claude-fable-5-1 \
//     -price-as anthropic/claude-haiku-5-5,openai/gpt-6-astra -cache-modes 5m,1h,30m,implicit
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"sidekick/common"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/srv/sqlite"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	_ "modernc.org/sqlite"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	if err := run(context.Background(), os.Stdout); err != nil {
		log.Fatal().Err(err).Msg("history_truncation_sim failed")
	}
}

type options struct {
	duckdbBin, snapshot, kvDB          string
	provider, model, actionType, since string
	maxFlows                           int
	minCacheable, granularity          int
	strictOverlap                      bool
	policies                           string
	priceAs, cacheModes, hitProbs      string
	priceInput, priceRead, priceWrite  float64
}

// cacheMode is a named cache-billing assumption: the retention bought, how
// long entries live, how uncached tokens are charged, and the conditional
// probability that an eligible prefix actually hits.
type cacheMode struct {
	name      string
	retention string
	cm        cacheModel
	hitProb   float64
}

func (m cacheMode) withHitProbability(p float64) cacheMode {
	m.hitProb = p
	m.name = fmt.Sprintf("%s, p(hit)=%.2f", m.name, p)
	return m
}

func cacheModeByName(retention string, minCacheable, granularity int) (cacheMode, error) {
	base := cacheModel{MinCacheable: minCacheable, Granularity: granularity}
	switch retention {
	case retention5m:
		base.TTL, base.ExplicitWrites = 5*time.Minute, true
		return cacheMode{name: "explicit 5m", retention: retention, cm: base, hitProb: 1}, nil
	case retention1h:
		base.TTL, base.ExplicitWrites = time.Hour, true
		return cacheMode{name: "explicit 1h", retention: retention, cm: base, hitProb: 1}, nil
	case retention30m:
		base.TTL, base.ExplicitWrites = 30*time.Minute, true
		return cacheMode{name: "explicit 30m", retention: retention, cm: base, hitProb: 1}, nil
	case retentionImplicit:
		// The 5m TTL is an assumption (OpenAI documents 5–10 min, longer
		// off-peak); it has not been measured here. Residual misses are
		// modeled via hitProb rather than by shortening the TTL.
		base.TTL, base.ExplicitWrites = 5*time.Minute, false
		if base.Granularity < 128 {
			base.Granularity = 128
		}
		return cacheMode{name: "implicit (TTL 5m)", retention: retention, cm: base, hitProb: 1}, nil
	}
	return cacheMode{}, fmt.Errorf("unknown cache mode %q (want 5m, 1h, 30m or implicit)", retention)
}

// recordedRetention is the cache product the recorded calls were billed
// under, which the validity check must reproduce.
func recordedRetention(provider string) string {
	if strings.Contains(provider, "openai") {
		return retentionImplicit
	}
	return retention5m
}

func run(ctx context.Context, w io.Writer) error {
	stateHome, err := common.GetSidekickDataHome()
	if err != nil {
		return err
	}
	var o options
	flag.StringVar(&o.duckdbBin, "duckdb", "duckdb", "DuckDB CLI binary")
	flag.StringVar(&o.snapshot, "db", filepath.Join(stateHome, "llm_usage.duckdb"), "llm_usage_stats snapshot")
	flag.StringVar(&o.kvDB, "kv-db", filepath.Join(stateHome, sqlite.KVDatabaseFileName), "KV sqlite DB holding message blocks (opened read-only)")
	flag.StringVar(&o.provider, "provider", "", "provider name as recorded (required when the model was used via more than one)")
	flag.StringVar(&o.model, "model", "", "model id to replay (required)")
	flag.StringVar(&o.actionType, "action-type", "generate.code_edits", "flow action type (role) to replay")
	flag.StringVar(&o.since, "since", "", "only calls created at or after this UTC date (YYYY-MM-DD)")
	flag.IntVar(&o.maxFlows, "max-flows", 0, "cap on flows to replay (0 = all)")
	flag.IntVar(&o.minCacheable, "min-cacheable", 1024, "smallest cacheable prefix in tokens")
	flag.IntVar(&o.granularity, "granularity", 1, "cache hit granularity in tokens (implicit mode forces ≥128)")
	flag.BoolVar(&o.strictOverlap, "strict-overlap", false, "exclude every call whose creation precedes its predecessor's UpdatedAt (sensitivity check)")
	flag.StringVar(&o.policies, "policies", "", "comma-separated keep:trigger[:minTurns] char policies; default sweeps a grid")
	flag.StringVar(&o.cacheModes, "cache-modes", "5m,1h", "comma-separated cache billing modes: 5m, 1h, 30m, implicit")
	flag.StringVar(&o.hitProbs, "hit-prob", "1", "comma-separated conditional hit probabilities for eligible prefixes; each cache mode is reported at each (1 = ideal). Applies to policy reports only, not the validity baseline")
	flag.StringVar(&o.priceAs, "price-as", "", "comma-separated price schedules to cost the same replay at; built-in names: "+strings.Join(builtinScheduleNames(), ", ")+"; or provider/model from model_prices; default: the replayed model")
	flag.Float64Var(&o.priceInput, "price-input", 0, "override input $/Mtok (single ad-hoc schedule)")
	flag.Float64Var(&o.priceRead, "price-cache-read", 0, "override cache read $/Mtok")
	flag.Float64Var(&o.priceWrite, "price-cache-write", 0, "override cache write $/Mtok (defaults to input when overriding)")
	flag.Parse()
	if o.model == "" {
		return fmt.Errorf("-model is required")
	}

	calls, err := loadCalls(ctx, o)
	if err != nil {
		return err
	}
	if len(calls) == 0 {
		return fmt.Errorf("no calls match provider=%q model=%s action-type=%s since=%q", o.provider, o.model, o.actionType, o.since)
	}
	provider := calls[0].Provider
	recordedSchedule, err := scheduleFor(ctx, o, provider, o.model)
	if err != nil {
		return err
	}
	schedules, err := resolveSchedules(ctx, o, recordedSchedule)
	if err != nil {
		return err
	}
	hitProbs, err := parseHitProbabilities(o.hitProbs)
	if err != nil {
		return err
	}
	var modes []cacheMode
	for _, name := range strings.Split(o.cacheModes, ",") {
		m, err := cacheModeByName(strings.TrimSpace(name), o.minCacheable, o.granularity)
		if err != nil {
			return err
		}
		for _, p := range hitProbs {
			modes = append(modes, m.withHitProbability(p))
		}
	}

	kv, err := openKV(o.kvDB)
	if err != nil {
		return err
	}
	defer kv.db.Close()

	lineages, stats, err := buildLineages(ctx, kv, calls, o.maxFlows, o.strictOverlap)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "## Input (provider=%s model=%s role=%s)\n", provider, o.model, o.actionType)
	fmt.Fprintf(w, "| matched calls | flows replayed | lineages | calls included | flows skipped (blocks gone) | overlaps | identical-prompt retries | excluded (ambiguous) |\n|--:|--:|--:|--:|--:|--:|--:|--:|\n")
	fmt.Fprintf(w, "| %d | %d | %d | %d | %d | %d | %d | %d |\n\n", len(calls), stats.flows, len(lineages), stats.included,
		stats.skippedFlows, stats.diag.Overlaps, stats.diag.IdenticalPrompts, stats.diag.Excluded)
	if stats.included == 0 {
		return fmt.Errorf("no calls could be included")
	}
	fmt.Fprintf(w, "Prices ($/Mtok, output excluded):\n")
	for _, s := range schedules {
		fmt.Fprintf(w, "- %s — %s\n", s.Name, s)
	}
	fmt.Fprintln(w)

	tm := fitTokens(lineages, provider)
	fmt.Fprintf(w, "## Token model (tokens = %.0f + %.4f·chars)\nR²=%.3f median |err|=%.1f%% n=%d\n\n", tm.Overhead, tm.PerChar, tm.R2, 100*tm.MedianAbsPctErr, tm.N)

	modelConfig := common.ModelConfig{Provider: provider, Model: o.model}
	policies, err := parsePolicies(o.policies, modelConfig)
	if err != nil {
		return err
	}
	replays, err := replayAll(lineages, provider, policies)
	if err != nil {
		return err
	}

	recordedMode, err := cacheModeByName(recordedRetention(provider), o.minCacheable, o.granularity)
	if err != nil {
		return err
	}
	reportBaseline(w, replays, tm, recordedSchedule, recordedMode)
	for _, s := range schedules {
		reportSchedule(w, replays, tm, s, modes, policies)
	}
	return nil
}

// lineageReplay holds every policy's replay of one lineage so that costing
// under many price/cache assumptions does not repeat the expensive replay.
type lineageReplay struct {
	calls   []callRecord
	actual  []replayStep
	byPolic [][]replayStep
}

func replayAll(lineages [][]traceStep, provider string, policies []namedPolicy) ([]lineageReplay, error) {
	out := make([]lineageReplay, 0, len(lineages))
	for _, l := range lineages {
		r := lineageReplay{calls: make([]callRecord, len(l))}
		for i, s := range l {
			r.calls[i] = s.Call
		}
		trace := &historyTrace{Steps: l}
		r.actual = actualSteps(trace, provider)
		for _, np := range policies {
			steps, err := replay(trace, provider, np.policy)
			if err != nil {
				return nil, fmt.Errorf("policy %s: %w", np.name, err)
			}
			r.byPolic = append(r.byPolic, steps)
		}
		out = append(out, r)
	}
	return out, nil
}

type rawCall struct {
	ID          string   `json:"id"`
	WorkspaceID string   `json:"workspace_id"`
	FlowID      string   `json:"flow_id"`
	SubflowID   string   `json:"subflow_id"`
	Provider    string   `json:"provider"`
	Model       string   `json:"model"`
	ToolsHash   string   `json:"tools_hash"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	MsgRefs     []string `json:"msg_refs"`
	MsgRoles    []string `json:"msg_roles"`
	Input       int      `json:"input_tokens"`
	Output      int      `json:"output_tokens"`
	CacheRead   int      `json:"cache_read_tokens"`
	CacheWrite  int      `json:"cache_write_tokens"`
}

func loadCalls(ctx context.Context, o options) ([]callRecord, error) {
	where := fmt.Sprintf("model = %s AND action_type = %s AND has_usage AND NOT (usage_unparseable OR negative_usage OR cache_exceeds_input OR params_invalid_json) AND len(msg_refs) > 0",
		sqlString(o.model), sqlString(o.actionType))
	if o.since != "" {
		where += " AND created_at >= TIMESTAMP " + sqlString(o.since)
	}
	if o.provider != "" {
		where += " AND provider = " + sqlString(o.provider)
	} else {
		providers, err := distinctProviders(ctx, o, where)
		if err != nil {
			return nil, err
		}
		if len(providers) > 1 {
			return nil, fmt.Errorf("model %s was used via several providers (%s); pass -provider", o.model, strings.Join(providers, ", "))
		}
	}
	query := `SELECT id, workspace_id, flow_id, coalesce(subflow_id, '') AS subflow_id, provider, model, tools_hash,
		strftime(created_at, '%Y-%m-%d %H:%M:%S.%f') AS created_at, strftime(updated_at, '%Y-%m-%d %H:%M:%S.%f') AS updated_at,
		msg_refs, msg_roles, input_tokens, coalesce(output_tokens, 0) AS output_tokens,
		coalesce(cache_read_tokens, 0) AS cache_read_tokens, coalesce(cache_write_tokens, 0) AS cache_write_tokens
		FROM llm_calls WHERE ` + where + ` ORDER BY flow_id, created_at;`
	out, err := runDuckDB(ctx, o.duckdbBin, o.snapshot, query, "-readonly", "-json")
	if err != nil {
		return nil, err
	}
	var raws []rawCall
	if len(bytes.TrimSpace(out)) > 0 {
		if err := json.Unmarshal(out, &raws); err != nil {
			return nil, fmt.Errorf("decoding llm_calls: %w", err)
		}
	}
	calls := make([]callRecord, 0, len(raws))
	for _, r := range raws {
		created, err := parseDuckTime(r.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("call %s: %w", r.ID, err)
		}
		updated, err := parseDuckTime(r.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("call %s: %w", r.ID, err)
		}
		if len(r.MsgRefs) != len(r.MsgRoles) {
			return nil, fmt.Errorf("call %s: %d refs but %d roles", r.ID, len(r.MsgRefs), len(r.MsgRoles))
		}
		c := callRecord{
			ID: r.ID, WorkspaceID: r.WorkspaceID, FlowID: r.FlowID, SubflowID: r.SubflowID,
			Provider: r.Provider, Model: r.Model, ToolsHash: r.ToolsHash, CreatedAt: created, UpdatedAt: updated,
			InputTokens: r.Input, OutputTokens: r.Output, CacheReadTokens: r.CacheRead, CacheWriteTokens: r.CacheWrite,
		}
		for i, keys := range r.MsgRefs {
			c.Refs = append(c.Refs, persisted_ai.MessageRef{Role: r.MsgRoles[i], BlockKeys: strings.Split(keys, ",")})
		}
		calls = append(calls, c)
	}
	return calls, nil
}

func distinctProviders(ctx context.Context, o options, where string) ([]string, error) {
	out, err := runDuckDB(ctx, o.duckdbBin, o.snapshot, "SELECT DISTINCT provider FROM llm_calls WHERE "+where+" ORDER BY provider;", "-readonly", "-json")
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Provider string `json:"provider"`
	}
	if len(bytes.TrimSpace(out)) > 0 {
		if err := json.Unmarshal(out, &rows); err != nil {
			return nil, fmt.Errorf("decoding providers: %w", err)
		}
	}
	providers := make([]string, 0, len(rows))
	for _, r := range rows {
		providers = append(providers, r.Provider)
	}
	return providers, nil
}

// resolveSchedules picks the price schedules to cost the replay at: an
// ad-hoc override wins, then each -price-as entry, else the replayed
// model's own listing.
func resolveSchedules(ctx context.Context, o options, recorded priceSchedule) ([]priceSchedule, error) {
	if o.priceInput > 0 || o.priceRead > 0 {
		if o.priceInput <= 0 || o.priceRead <= 0 {
			return nil, fmt.Errorf("-price-input and -price-cache-read must be given together")
		}
		write := o.priceWrite
		if write <= 0 {
			write = o.priceInput
		}
		return []priceSchedule{flatSchedule("ad-hoc (5m only)", o.priceInput, o.priceRead, write)}, nil
	}
	if o.priceAs == "" {
		return []priceSchedule{recorded}, nil
	}
	var out []priceSchedule
	for _, name := range strings.Split(o.priceAs, ",") {
		asProvider, asModel, ok := strings.Cut(strings.TrimSpace(name), "/")
		if !ok {
			return nil, fmt.Errorf("price schedule %q: want provider/model", name)
		}
		s, err := scheduleFor(ctx, o, asProvider, asModel)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// scheduleFor prefers the built-in list prices (which know the provider's
// retention options) and falls back to the snapshot's models.dev table.
func scheduleFor(ctx context.Context, o options, provider, model string) (priceSchedule, error) {
	if s, ok := builtinSchedules[provider+"/"+model]; ok {
		return s, nil
	}
	// Gateway providers are billed as the upstream they front.
	for _, upstream := range []string{"anthropic", "openai"} {
		if strings.Contains(provider, upstream) {
			if s, ok := builtinSchedules[upstream+"/"+model]; ok {
				return s, nil
			}
		}
	}
	return loadPrice(ctx, o, provider, model)
}

func loadPrice(ctx context.Context, o options, provider, model string) (priceSchedule, error) {
	query := fmt.Sprintf(`SELECT input_per_mtok, cache_read_per_mtok, cache_write_per_mtok, provider_matched
		FROM model_prices WHERE provider = %s AND model = %s;`, sqlString(provider), sqlString(model))
	out, err := runDuckDB(ctx, o.duckdbBin, o.snapshot, query, "-readonly", "-json")
	if err != nil {
		return priceSchedule{}, err
	}
	var rows []struct {
		Input           *float64 `json:"input_per_mtok"`
		CacheRead       *float64 `json:"cache_read_per_mtok"`
		CacheWrite      *float64 `json:"cache_write_per_mtok"`
		ProviderMatched bool     `json:"provider_matched"`
	}
	if len(bytes.TrimSpace(out)) > 0 {
		if err := json.Unmarshal(out, &rows); err != nil {
			return priceSchedule{}, fmt.Errorf("decoding model_prices: %w", err)
		}
	}
	if len(rows) != 1 || rows[0].Input == nil || rows[0].CacheRead == nil {
		return priceSchedule{}, fmt.Errorf("no usable price for %s/%s in model_prices (rows=%d)", provider, model, len(rows))
	}
	if !rows[0].ProviderMatched {
		log.Warn().Str("provider", provider).Str("model", model).Msg("price matched a different provider's listing")
	}
	write := *rows[0].Input
	if rows[0].CacheWrite != nil {
		write = *rows[0].CacheWrite
	}
	return flatSchedule(provider+"/"+model+" (models.dev, 5m only)", *rows[0].Input, *rows[0].CacheRead, write), nil
}

type kvSource struct{ db *sql.DB }

func openKV(path string) (*kvSource, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	return &kvSource{db: db}, nil
}

func (k *kvSource) Blocks(ctx context.Context, workspaceID string, storageKeys []string) (map[string][]byte, error) {
	if len(storageKeys) == 0 {
		return map[string][]byte{}, nil
	}
	placeholders := make([]string, len(storageKeys))
	args := make([]any, 0, len(storageKeys)+1)
	args = append(args, workspaceID)
	for i, key := range storageKeys {
		placeholders[i] = "?"
		args = append(args, key)
	}
	rows, err := k.db.QueryContext(ctx, "SELECT key, value FROM kv WHERE workspace_id = ? AND key IN ("+strings.Join(placeholders, ",")+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]byte{}
	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

type lineageStats struct {
	flows, skippedFlows, included int
	diag                          lineageDiagnostics
}

// buildLineages hydrates calls flow by flow and splits them into
// unambiguous lineages. Flows whose blocks are gone (expired KV data) are
// counted and skipped rather than failing the whole run.
func buildLineages(ctx context.Context, src blockSource, calls []callRecord, maxFlows int, strictOverlap bool) ([][]traceStep, lineageStats, error) {
	var byFlow [][]callRecord
	for i := 0; i < len(calls); {
		j := i
		for j < len(calls) && calls[j].FlowID == calls[i].FlowID && calls[j].WorkspaceID == calls[i].WorkspaceID {
			j++
		}
		byFlow = append(byFlow, calls[i:j])
		i = j
	}
	if maxFlows > 0 && len(byFlow) > maxFlows {
		byFlow = byFlow[len(byFlow)-maxFlows:]
	}

	var lineages [][]traceStep
	stats := lineageStats{flows: len(byFlow)}
	for _, flowCalls := range byFlow {
		trace, err := reconstructHistory(ctx, src, flowCalls)
		if err != nil {
			if strings.Contains(err.Error(), "missing from storage") {
				stats.skippedFlows++
				log.Debug().Err(err).Msg("skipping flow with missing blocks")
				continue
			}
			return nil, stats, err
		}
		ls, diag := splitLineages(trace.Steps, strictOverlap)
		stats.diag.Overlaps += diag.Overlaps
		stats.diag.IdenticalPrompts += diag.IdenticalPrompts
		stats.diag.Excluded += diag.Excluded
		for _, l := range ls {
			if len(l) > 0 {
				lineages = append(lineages, l)
				stats.included += len(l)
			}
		}
	}
	if stats.skippedFlows > 0 {
		log.Warn().Int("flows", stats.skippedFlows).Int("total", len(byFlow)).Msg("flows skipped: message blocks missing from KV store")
	}
	return lineages, stats, nil
}

func fitTokens(lineages [][]traceStep, provider string) tokenModel {
	var chars, tokens []float64
	for _, l := range lineages {
		for _, step := range l {
			chars = append(chars, float64(persisted_ai.MeasureLlm2Messages(provider, step.Actual)))
			tokens = append(tokens, float64(step.Call.InputTokens))
		}
	}
	return fitTokenModel(chars, tokens)
}

type totals struct {
	calls, truncations, skippedOps int
	usage                          usage
	cost                           float64
	promptTokens                   float64
}

func (t *totals) add(step replayStep, u usage, s priceSchedule, mode cacheMode) {
	t.calls++
	if step.Truncated {
		t.truncations++
	}
	t.skippedOps += step.SkippedOps
	t.usage.Read += u.Read
	t.usage.Write += u.Write
	t.usage.Uncached += u.Uncached
	t.cost += expectedCost(u, s, mode.retention, mode.hitProb, mode.cm.ExplicitWrites)
	t.promptTokens += float64(u.total())
}

func sumSteps(replays []lineageReplay, pick func(lineageReplay) []replayStep, tm tokenModel, mode cacheMode, s priceSchedule) totals {
	var t totals
	for _, r := range replays {
		steps := pick(r)
		sim := simulateUsage(steps, r.calls, tm, mode.cm)
		for i := range steps {
			t.add(steps[i], sim[i], s, mode)
		}
	}
	return t
}

// reportBaseline is the validity check: the idealized cache under the
// retention the recorded calls were actually billed with, applied to the
// prompts actually sent, versus the provider's recorded usage buckets. Both
// are priced at the recorded model's own schedule.
func reportBaseline(w io.Writer, replays []lineageReplay, tm tokenModel, s priceSchedule, mode cacheMode) {
	var recorded, modeled totals
	var readErrs []float64
	for _, r := range replays {
		sim := simulateUsage(r.actual, r.calls, tm, mode.cm)
		for i := range r.actual {
			rec := recordedUsage(r.calls[i])
			recorded.add(r.actual[i], rec, s, mode)
			modeled.add(r.actual[i], sim[i], s, mode)
			if r.calls[i].InputTokens > 0 {
				readErrs = append(readErrs, float64(sim[i].Read-rec.Read)/float64(r.calls[i].InputTokens))
			}
		}
	}
	sort.Float64s(readErrs)
	fmt.Fprintf(w, "## Validity: idealized %s cache vs provider-recorded buckets, both on the prompts actually sent, priced as %s\n", mode.name, s.Name)
	fmt.Fprintf(w, "| source | calls | truncations | read | write | uncached | cost $ |\n|---|--:|--:|--:|--:|--:|--:|\n")
	fmt.Fprintf(w, "| recorded by provider | %d | %d | %d | %d | %d | %.2f |\n", recorded.calls, recorded.truncations, recorded.usage.Read, recorded.usage.Write, recorded.usage.Uncached, recorded.cost)
	fmt.Fprintf(w, "| idealized cache model | %d | %d | %d | %d | %d | %.2f |\n", modeled.calls, modeled.truncations, modeled.usage.Read, modeled.usage.Write, modeled.usage.Uncached, modeled.cost)
	fmt.Fprintf(w, "\nmodel cost error %+.1f%%; per-call (modeled − recorded cache read)/input: p10 %+.2f, median %+.2f, p90 %+.2f\n",
		100*(modeled.cost-recorded.cost)/math.Max(recorded.cost, 1e-9),
		percentileSorted(readErrs, 0.1), percentileSorted(readErrs, 0.5), percentileSorted(readErrs, 0.9))
	fmt.Fprintf(w, "Everything below is modeled (replayed prompts, cache TTL and hit probability as labeled per column); deltas are against the modeled cost of the recorded prompts under the same mode. Output tokens excluded.\n\n")
}

// reportSchedule prints, for one price schedule, every policy's cost under
// every cache mode, with deltas against the recorded prompts in that mode.
// Modes the schedule does not sell are shown as n/a.
func reportSchedule(w io.Writer, replays []lineageReplay, tm tokenModel, s priceSchedule, modes []cacheMode, policies []namedPolicy) {
	fmt.Fprintf(w, "## %s (input-side $ only)\n", s.Name)
	fmt.Fprintf(w, "| policy | truncations | skipped ops | avg prompt tok |")
	for _, m := range modes {
		fmt.Fprintf(w, " %s $ | Δ vs recorded |", m.name)
	}
	fmt.Fprintf(w, "\n|---|--:|--:|--:|")
	for range modes {
		fmt.Fprintf(w, "--:|--:|")
	}
	fmt.Fprintln(w)

	refs := make([]totals, len(modes))
	for j, m := range modes {
		refs[j] = sumSteps(replays, func(r lineageReplay) []replayStep { return r.actual }, tm, m, s)
	}
	printRow := func(name string, pick func(lineageReplay) []replayStep, isRef bool) {
		var cells []string
		var first totals
		for j, m := range modes {
			t := sumSteps(replays, pick, tm, m, s)
			if j == 0 {
				first = t
			}
			if !s.supports(m.retention) {
				cells = append(cells, " n/a | n/a |")
				continue
			}
			delta := "—"
			if !isRef && refs[j].cost > 0 {
				delta = fmt.Sprintf("%+.1f%%", 100*(t.cost-refs[j].cost)/refs[j].cost)
			}
			cells = append(cells, fmt.Sprintf(" %.2f | %s |", t.cost, delta))
		}
		fmt.Fprintf(w, "| %s | %d | %d | %.0f |%s\n", name, first.truncations, first.skippedOps, first.promptTokens/math.Max(float64(first.calls), 1), strings.Join(cells, ""))
	}
	printRow("recorded prompts (reference)", func(r lineageReplay) []replayStep { return r.actual }, true)
	for i, np := range policies {
		i := i
		printRow(np.name, func(r lineageReplay) []replayStep { return r.byPolic[i] }, false)
	}
	fmt.Fprintln(w)
}

type namedPolicy struct {
	name   string
	policy policyFunc
}

// parsePolicies builds the policy set. Production's requested keep length
// is min(100k + per-flow context extension, 200k) and the extension is not
// recorded, so the production resolver is replayed at both bounds as
// labeled hypotheticals rather than as "the recorded policy".
func parsePolicies(spec string, modelConfig common.ModelConfig) ([]namedPolicy, error) {
	var out []namedPolicy
	for _, requested := range []int{100000, 200000} {
		requested := requested
		out = append(out, namedPolicy{name: fmt.Sprintf("hypothetical: production resolve, requested %dk", requested/1000), policy: func(m []llm2.Message) ([]llm2.Message, error) {
			return (&persisted_ai.ChatHistoryActivities{}).ManageLlm2ChatHistory(m, requested, modelConfig)
		}})
	}
	var specs []string
	if spec == "" {
		for _, keep := range []int{100000, 150000, 200000} {
			for _, buf := range []int{50000, 100000, 150000, 200000, 300000} {
				for _, turns := range []int{0, 2} {
					specs = append(specs, fmt.Sprintf("%d:%d:%d", keep, keep+buf, turns))
				}
			}
		}
	} else {
		specs = strings.Split(spec, ",")
	}
	maxChars := common.MaxCharsForModel(modelConfig.Provider, modelConfig.Model)
	for _, s := range specs {
		var keep, trigger, turns int
		n, err := fmt.Sscanf(strings.TrimSpace(s), "%d:%d:%d", &keep, &trigger, &turns)
		if err != nil && n < 2 {
			return nil, fmt.Errorf("policy %q: want keep:trigger[:minTurns]", s)
		}
		if trigger > maxChars {
			continue
		}
		pol := persisted_ai.ManagePolicy{KeepLength: keep, TruncationTrigger: trigger, MinRecentTurns: turns}
		out = append(out, namedPolicy{name: fmt.Sprintf("keep %dk trig %dk turns %d", keep/1000, trigger/1000, turns), policy: func(m []llm2.Message) ([]llm2.Message, error) {
			return persisted_ai.ManageLlm2ChatHistoryWithPolicy(m, pol, modelConfig)
		}})
	}
	return out, nil
}

func runDuckDB(ctx context.Context, duckdbBin, dbPath, script string, args ...string) ([]byte, error) {
	cmdArgs := append(append([]string{"-bail"}, args...), dbPath)
	cmd := exec.CommandContext(ctx, duckdbBin, cmdArgs...)
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil || stderr.Len() > 0 {
		return nil, fmt.Errorf("duckdb %s failed: %v: %s", dbPath, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func sqlString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
