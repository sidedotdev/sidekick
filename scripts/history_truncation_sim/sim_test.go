package main

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"sidekick/llm2"
	"sidekick/persisted_ai"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memBlocks map[string][]byte

func (m memBlocks) Blocks(_ context.Context, workspaceID string, storageKeys []string) (map[string][]byte, error) {
	out := make(map[string][]byte)
	for _, k := range storageKeys {
		if v, ok := m[workspaceID+"|"+k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

type fixture struct {
	t      *testing.T
	blocks memBlocks
	msgs   map[string]llm2.Message
}

func newFixture(t *testing.T) *fixture {
	return &fixture{t: t, blocks: memBlocks{}, msgs: map[string]llm2.Message{}}
}

// add stores a single-block message under key and returns the ref to use in calls.
func (f *fixture) add(key string, msg llm2.Message) {
	require.Len(f.t, msg.Content, 1)
	raw, err := json.Marshal(msg.Content[0])
	require.NoError(f.t, err)
	f.blocks["ws|"+persisted_ai.StorageKey("flow", key)] = raw
	f.msgs[key] = msg
}

func (f *fixture) call(id string, at time.Time, keys ...string) callRecord {
	c := callRecord{ID: id, WorkspaceID: "ws", FlowID: "flow", CreatedAt: at, UpdatedAt: at.Add(5 * time.Second)}
	for _, k := range keys {
		c.Refs = append(c.Refs, persisted_ai.MessageRef{BlockKeys: []string{k}, Role: string(f.msgs[k].Role)})
	}
	return c
}

// want returns the hydrated form of the given stored messages: JSON
// round-tripped (so metadata becomes maps) with block keys attached.
func (f *fixture) want(keys ...string) []llm2.Message {
	out := []llm2.Message{}
	for _, k := range keys {
		raw, err := json.Marshal(f.msgs[k])
		require.NoError(f.t, err)
		var m llm2.Message
		require.NoError(f.t, json.Unmarshal(raw, &m))
		persisted_ai.SetBlockKey(&m.Content[0], k)
		out = append(out, m)
	}
	return out
}

func opsOfKind(step traceStep, kind opKind) []llm2.Message {
	var out []llm2.Message
	for _, op := range step.Ops {
		if op.Kind == kind {
			out = append(out, op.Msg)
		}
	}
	return out
}

func dropKinds(step traceStep) (head, tail int) {
	for _, op := range step.Ops {
		if op.Kind != opDrop {
			continue
		}
		if op.Tail {
			tail++
		} else {
			head++
		}
	}
	return head, tail
}

func text(role llm2.Role, s string) llm2.Message {
	return llm2.Message{Role: role, Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: s}}}
}

func toolUse(id, args string) llm2.Message {
	return llm2.Message{Role: llm2.RoleAssistant, Content: []llm2.ContentBlock{{
		Type: llm2.ContentBlockTypeToolUse, ToolUse: &llm2.ToolUseBlock{Id: id, Name: "read_file", Arguments: args},
	}}}
}

func toolResult(id, s string) llm2.Message {
	return llm2.Message{Role: llm2.RoleUser, Content: []llm2.ContentBlock{{
		Type:       llm2.ContentBlockTypeToolResult,
		ToolResult: &llm2.ToolResultBlock{ToolCallId: id, Name: "read_file", Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: s}}},
	}}}
}

// traceFixture models a history whose stored refs rotate cache markers, get a
// tool result shortened by the truncation policy, lose a head message to a
// real truncation, gain a message inserted before existing ones, carry a
// genuine edit to an earlier message, and finally rewind to a branch.
func traceFixture(t *testing.T) (*fixture, []callRecord) {
	f := newFixture(t)
	a := text(llm2.RoleUser, "instructions")
	persisted_ai.SetContextType(&a.Content[0], persisted_ai.ContextTypeInitialInstructions)
	a.Content[0].CacheControl = "ephemeral"
	f.add("a1", a)
	aRotated := a
	aRotated.Content = []llm2.ContentBlock{a.Content[0]}
	aRotated.Content[0].CacheControl = ""
	f.add("a2", aRotated)
	f.add("b1", toolUse("t1", `{"path":"x"}`))
	f.add("c1", toolResult("t1", strings.Repeat("result", 200)))
	f.add("c2", toolResult("t1", "res\n\n[... truncated 1000 characters from the middle ...]\n\nult\nNOTE: 1000 characters were truncated from this tool response."))
	f.add("d1", text(llm2.RoleAssistant, "done reading"))
	f.add("e1", text(llm2.RoleUser, "next instruction"))
	f.add("x1", text(llm2.RoleAssistant, "inserted"))
	f.add("y1", toolUse("t2", `{"path":"y"}`))
	f.add("z1", toolResult("t2", "original answer"))
	f.add("z2", toolResult("t2", "corrected answer"))
	f.add("w1", text(llm2.RoleAssistant, "branch reply"))

	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	calls := []callRecord{
		f.call("c1", t0, "a1"),
		f.call("c2", t0.Add(1*time.Minute), "a1", "b1", "c1"),
		f.call("c3", t0.Add(2*time.Minute), "a2", "b1", "c1", "d1"),
		f.call("c4", t0.Add(3*time.Minute), "a1", "c2", "d1", "e1"),
		f.call("c5", t0.Add(4*time.Minute), "a1", "x1", "d1", "e1"),
		f.call("c6", t0.Add(5*time.Minute), "a1", "x1", "d1", "e1", "y1", "z1"),
		f.call("c7", t0.Add(6*time.Minute), "a1", "x1", "d1", "e1", "y1", "z2", "d1"),
		f.call("c8", t0.Add(7*time.Minute), "a1", "x1", "d1", "w1"),
	}
	return f, calls
}

func TestReconstructHistory_DeltaOps(t *testing.T) {
	t.Parallel()
	f, calls := traceFixture(t)
	trace, err := reconstructHistory(context.Background(), f.blocks, calls)
	require.NoError(t, err)
	require.Len(t, trace.Steps, 8)
	s := trace.Steps

	assert.Equal(t, f.want("a1"), opsOfKind(s[0], opAppend))
	assert.Equal(t, f.want("b1", "c1"), opsOfKind(s[1], opAppend))

	assert.Equal(t, f.want("d1"), opsOfKind(s[2], opAppend), "marker rotation is not an addition")
	assert.Empty(t, opsOfKind(s[2], opDrop))
	assert.Equal(t, f.want("a2", "b1", "c1", "d1"), s[2].Actual)

	assert.Equal(t, f.want("e1"), opsOfKind(s[3], opAppend))
	head, tail := dropKinds(s[3])
	assert.Equal(t, 1, head, "b1 dropped from the head is a policy truncation")
	assert.Equal(t, 0, tail)
	assert.Equal(t, []string{identity(f.want("b1")[0])}, droppedIds(s[3]), "the tool call is dropped, not its result")
	require.Len(t, editOps(s[3]), 1)
	assert.Equal(t, f.want("c2")[0], editOps(s[3])[0].Msg)
	assert.Equal(t, identity(f.want("c1")[0]), editOps(s[3])[0].Replaces, "the shortened result replaces the original result")
	assert.Equal(t, editToolTruncation, editOps(s[3])[0].EditKind)
	assert.False(t, s[3].Branch)

	head, tail = dropKinds(s[4])
	assert.Equal(t, 1, head, "c2 is dropped from the head")
	assert.Equal(t, 0, tail)
	inserts := insertOps(s[4])
	require.Len(t, inserts, 1)
	assert.Equal(t, f.want("x1")[0], inserts[0].Msg)
	assert.Equal(t, identity(f.want("d1")[0]), inserts[0].Anchor, "x1 is inserted before existing d1")
	assert.Empty(t, opsOfKind(s[4], opAppend))

	assert.Equal(t, f.want("y1", "z1"), opsOfKind(s[5], opAppend))

	require.Len(t, editOps(s[6]), 1, "z2 replaces z1 for the same tool call")
	assert.Equal(t, editContent, editOps(s[6])[0].EditKind)
	assert.Equal(t, identity(f.want("z1")[0]), editOps(s[6])[0].Replaces)
	assert.Equal(t, f.want("d1"), opsOfKind(s[6], opAppend), "a repeated identical message is a separate occurrence")
	assert.Empty(t, opsOfKind(s[6], opDrop))

	head, tail = dropKinds(s[7])
	assert.Equal(t, 0, head)
	assert.Equal(t, 4, tail, "e1 y1 z2 d1 dropped after the last retained message")
	assert.True(t, s[7].Branch)
	assert.Equal(t, f.want("w1"), opsOfKind(s[7], opAppend))
}

func editOps(step traceStep) []deltaOp {
	var out []deltaOp
	for _, op := range step.Ops {
		if op.Kind == opEdit {
			out = append(out, op)
		}
	}
	return out
}

func insertOps(step traceStep) []deltaOp {
	var out []deltaOp
	for _, op := range step.Ops {
		if op.Kind == opInsert {
			out = append(out, op)
		}
	}
	return out
}

func droppedIds(step traceStep) []string {
	var out []string
	for _, op := range step.Ops {
		if op.Kind == opDrop {
			out = append(out, op.Dropped)
		}
	}
	return out
}

func TestReconstructHistory_MissingBlockFails(t *testing.T) {
	t.Parallel()
	f, calls := traceFixture(t)
	calls[1].Refs = append(calls[1].Refs, persisted_ai.MessageRef{BlockKeys: []string{"gone"}, Role: "user"})
	_, err := reconstructHistory(context.Background(), f.blocks, calls)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gone")
}

func TestReplay_CarriesHistoryAcrossActualTruncation(t *testing.T) {
	t.Parallel()
	f, calls := traceFixture(t)
	trace, err := reconstructHistory(context.Background(), f.blocks, calls)
	require.NoError(t, err)
	chars := func(keys ...string) int { return persisted_ai.MeasureLlm2Messages("", f.want(keys...)) }

	never := func(m []llm2.Message) ([]llm2.Message, error) { return m, nil }
	steps, err := replay(trace, "", never)
	require.NoError(t, err)
	require.Len(t, steps, 8)
	assert.Equal(t, chars("a1", "b1", "c1", "d1", "e1"), steps[3].PromptChars,
		"a never-truncating policy keeps b1 and the full c1 even though the real run dropped/shortened them")
	assert.Equal(t, chars("a1", "b1", "c1", "d1"), steps[3].PrefixChars)
	assert.False(t, steps[3].Truncated)
	assert.Equal(t, chars("a1", "b1", "c1", "x1", "d1", "e1"), steps[4].PromptChars, "insert lands before its anchor")
	assert.Equal(t, chars("a1", "b1", "c1", "x1", "d1", "e1", "y1", "z2", "d1"), steps[6].PromptChars, "genuine edit is applied")
	assert.Equal(t, chars("a1", "b1", "c1", "x1", "d1", "e1", "y1"), steps[6].PrefixChars)
	assert.Equal(t, chars("a1", "b1", "c1", "x1", "d1", "w1"), steps[7].PromptChars, "branch rewind drops the tail")
	assert.Equal(t, chars("a1", "b1", "c1", "x1", "d1"), steps[7].PrefixChars)
	assert.Equal(t, 0, steps[7].SkippedOps)

	firstAndLast := func(m []llm2.Message) ([]llm2.Message, error) {
		if len(m) <= 2 {
			return m, nil
		}
		return []llm2.Message{m[0], m[len(m)-1]}, nil
	}
	steps, err = replay(trace, "", firstAndLast)
	require.NoError(t, err)
	assert.True(t, steps[1].Truncated)
	assert.Equal(t, chars("a1", "c1"), steps[1].PromptChars)
	assert.Equal(t, chars("a1"), steps[1].PrefixChars, "only the untouched head is a cache prefix after truncation")
	// Policy-caused changes in the recording are ignored rather than applied,
	// so they are never "skipped"; but d1 was dropped by this policy before
	// x1 could be inserted ahead of it.
	assert.Equal(t, 0, steps[3].SkippedOps)
	assert.Equal(t, chars("a1", "x1"), steps[4].PromptChars)
	assert.Equal(t, 1, steps[4].SkippedOps)
}

func TestActualSteps_DetectsTruncation(t *testing.T) {
	t.Parallel()
	f, calls := traceFixture(t)
	trace, err := reconstructHistory(context.Background(), f.blocks, calls)
	require.NoError(t, err)
	steps := actualSteps(trace, "")
	require.Len(t, steps, 8)
	chars := func(keys ...string) int { return persisted_ai.MeasureLlm2Messages("", f.want(keys...)) }

	assert.False(t, steps[2].Truncated, "marker rotation keeps the prefix")
	assert.Equal(t, chars("a1", "b1", "c1"), steps[2].PrefixChars, "newly appended d1 was not in the previous prompt")
	assert.True(t, steps[3].Truncated, "c4 lost b1")
	assert.Equal(t, chars("a1"), steps[3].PrefixChars)
	assert.True(t, steps[4].Truncated, "c5 also dropped c2 from the head")
	assert.Equal(t, chars("a1"), steps[4].PrefixChars)
	assert.False(t, steps[7].Truncated, "a branch rewind is not a truncation")
	assert.Equal(t, chars("a1", "x1", "d1"), steps[7].PrefixChars)
}

func TestFitTokenModel_RecoversLinearFit(t *testing.T) {
	t.Parallel()
	var xs, ys []float64
	for i := 1; i <= 20; i++ {
		x := float64(i * 1000)
		xs = append(xs, x)
		ys = append(ys, 500+0.4*x)
	}
	tm := fitTokenModel(xs, ys)
	assert.InDelta(t, 500, tm.Overhead, 1e-6)
	assert.InDelta(t, 0.4, tm.PerChar, 1e-9)
	assert.InDelta(t, 1, tm.R2, 1e-9)
	assert.InDelta(t, 0, tm.MedianAbsPctErr, 1e-9)
}

func TestSimulateUsage_IdealizedCache(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	// UpdatedAt is deliberately far in the future: it is an async
	// bookkeeping time and must not influence the TTL gap.
	far := t0.Add(24 * time.Hour)
	calls := []callRecord{
		{ID: "1", CreatedAt: t0, UpdatedAt: far},
		{ID: "2", CreatedAt: t0.Add(time.Minute), UpdatedAt: far},
		{ID: "3", CreatedAt: t0.Add(time.Hour), UpdatedAt: far},
		{ID: "4", CreatedAt: t0.Add(time.Hour + time.Minute), UpdatedAt: far},
	}
	steps := []replayStep{
		{PromptChars: 10000},
		{PromptChars: 15000, PrefixChars: 10000},
		{PromptChars: 20000, PrefixChars: 15000},
		{PromptChars: 21000, PrefixChars: 1000},
	}
	tm := tokenModel{Overhead: 100, PerChar: 0.5}

	explicit := cacheModel{TTL: 5 * time.Minute, MinCacheable: 1024, Granularity: 1, ExplicitWrites: true}
	got := simulateUsage(steps, calls, tm, explicit)
	require.Len(t, got, 4)
	assert.Equal(t, usage{Write: 5100}, got[0], "first call has nothing cached")
	assert.Equal(t, usage{Read: 5100, Write: 2500}, got[1], "shared prefix and overhead are read")
	assert.Equal(t, usage{Write: 10100}, got[2], "beyond the TTL everything is written")
	assert.Equal(t, usage{Write: 10600}, got[3], "prefix below the cacheable minimum is not read")

	implicit := cacheModel{TTL: 5 * time.Minute, MinCacheable: 1024, Granularity: 128, ExplicitWrites: false}
	got = simulateUsage(steps, calls, tm, implicit)
	assert.Equal(t, usage{Uncached: 5100}, got[0])
	assert.Equal(t, usage{Read: 4992, Uncached: 2608}, got[1], "implicit reads hit in whole granularity increments: floor(5100/128)*128")

	fable := builtinSchedules["anthropic/claude-fable-5-1"]
	assert.InDelta(t, (5100*0.25+2500*12.5)/1e6, cost(usage{Read: 5100, Write: 2500}, fable, retention5m), 1e-12)
	astra := builtinSchedules["openai/gpt-6-astra"]
	assert.InDelta(t, (4992*1+2608*10)/1e6, cost(usage{Read: 4992, Uncached: 2608}, astra, retentionImplicit), 1e-12)
}

func TestSimulateUsage_LongRetentionTurnsRewriteIntoRead(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	calls := []callRecord{
		{ID: "1", CreatedAt: t0},
		{ID: "2", CreatedAt: t0.Add(10 * time.Minute)},
	}
	steps := []replayStep{
		{PromptChars: 100000},
		{PromptChars: 104000, PrefixChars: 100000},
	}
	tm := tokenModel{PerChar: 0.5}
	fable := builtinSchedules["anthropic/claude-fable-5-1"]

	fiveMin := cacheModel{TTL: 5 * time.Minute, MinCacheable: 1024, Granularity: 1, ExplicitWrites: true}
	got5m := simulateUsage(steps, calls, tm, fiveMin)
	assert.Equal(t, usage{Write: 52000}, got5m[1], "a 10 minute gap misses a 5m cache: full rewrite")

	oneHour := cacheModel{TTL: time.Hour, MinCacheable: 1024, Granularity: 1, ExplicitWrites: true}
	got1h := simulateUsage(steps, calls, tm, oneHour)
	assert.Equal(t, usage{Read: 50000, Write: 2000}, got1h[1], "the same gap hits a 1h cache")

	// The 1h surcharge applies to every write, including the cold first call.
	assert.InDelta(t, 50000*12.5/1e6, cost(got5m[0], fable, retention5m), 1e-12)
	assert.InDelta(t, 50000*20/1e6, cost(got1h[0], fable, retention1h), 1e-12)
	assert.InDelta(t, (50000*0.25+2000*20)/1e6, cost(got1h[1], fable, retention1h), 1e-12)
	assert.Less(t, cost(got1h[0], fable, retention1h)+cost(got1h[1], fable, retention1h),
		cost(got5m[0], fable, retention5m)+cost(got5m[1], fable, retention5m),
		"for this gap the dearer 1h write beats the 5m rewrite")
}

func TestPriceSchedule_OpenAIContextBoundary(t *testing.T) {
	t.Parallel()
	astra := builtinSchedules["openai/gpt-6-astra"]
	assert.InDelta(t, 271999*1.0/1e6, cost(usage{Read: 271999}, astra, retention30m), 1e-12, "271,999 is short context")
	assert.InDelta(t, 272000*2.0/1e6, cost(usage{Read: 272000}, astra, retention30m), 1e-12, "272,000 is long context")
}

func TestPriceSchedule_UnsupportedRetentionIsNotPriced(t *testing.T) {
	t.Parallel()
	astra := builtinSchedules["openai/gpt-6-astra"]
	fable := builtinSchedules["anthropic/claude-fable-5-1"]
	assert.True(t, math.IsNaN(cost(usage{Write: 1000}, astra, retention1h)), "OpenAI sells no 1h retention")
	assert.True(t, math.IsNaN(cost(usage{Write: 1000}, astra, retention5m)), "OpenAI's explicit cache is 30m, not 5m")
	assert.True(t, math.IsNaN(cost(usage{Write: 1000}, fable, retention30m)), "Anthropic sells no 30m retention")
	assert.True(t, math.IsNaN(cost(usage{Uncached: 1000}, fable, retentionImplicit)), "Anthropic has no implicit cache")
	assert.False(t, math.IsNaN(cost(usage{Write: 1000}, astra, retention30m)))
	assert.False(t, math.IsNaN(cost(usage{Write: 1000}, fable, retention1h)))
}

func TestParseDuckTime(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"2026-10-02 07:28:54.722166", "2026-10-02 07:28:54"} {
		got, err := parseDuckTime(s)
		require.NoError(t, err, s)
		assert.Equal(t, 2026, got.Year())
		assert.Equal(t, time.UTC, got.Location())
	}
	_, err := parseDuckTime("nope")
	require.Error(t, err)
}

func lineageIds(l []traceStep) []string {
	var out []string
	for _, s := range l {
		out = append(out, s.Call.ID)
	}
	return out
}

func TestSplitLineages_SeparatesHistories(t *testing.T) {
	t.Parallel()
	f, calls := traceFixture(t)
	f.add("o1", text(llm2.RoleUser, "different task"))
	f.add("o2", text(llm2.RoleAssistant, "ok"))
	t0 := calls[0].CreatedAt
	// A concurrent call in another subflow may share the opening message;
	// the subflow tells the histories apart.
	sibling := f.call("sib", calls[2].CreatedAt.Add(time.Second), "a1", "b1", "c1", "d1")
	sibling.SubflowID = "sf-2"
	// The same subflow on another provider is billed separately.
	gateway := f.call("gw", calls[7].CreatedAt.Add(time.Minute), "a1", "x1", "d1", "w1")
	gateway.Provider = "llm-gateway-anthropic"
	calls = append(calls,
		f.call("o-1", t0.Add(30*time.Second), "o1"),
		f.call("o-2", t0.Add(90*time.Second), "o1", "o2"),
		sibling,
		gateway,
	)
	trace, err := reconstructHistory(context.Background(), f.blocks, calls)
	require.NoError(t, err)

	lineages, diag := splitLineages(trace.Steps, false)
	require.Len(t, lineages, 4)
	assert.Equal(t, lineageDiagnostics{}, diag)
	assert.Equal(t, []string{"c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8"}, lineageIds(lineages[0]))
	assert.Equal(t, []string{"o-1", "o-2"}, lineageIds(lineages[1]))
	assert.Equal(t, []string{"sib"}, lineageIds(lineages[2]))
	assert.Equal(t, []string{"gw"}, lineageIds(lineages[3]))
	// Ops are relative to the lineage's own predecessor.
	assert.Equal(t, f.want("a1", "b1", "c1", "d1"), opsOfKind(lineages[2][0], opAppend))
}

func TestSplitLineages_OverlapIsDiagnosticOnly(t *testing.T) {
	t.Parallel()
	f, calls := traceFixture(t)
	// Flow action timestamps are workflow logical times and the completion
	// update of one call routinely lands after the next call's creation, so
	// a sub-second overlap between sequential calls is bookkeeping, not
	// concurrency. It is counted but does not break the lineage.
	calls[3].CreatedAt = calls[2].UpdatedAt.Add(-500 * time.Millisecond)
	trace, err := reconstructHistory(context.Background(), f.blocks, calls)
	require.NoError(t, err)

	lineages, diag := splitLineages(trace.Steps, false)
	require.Len(t, lineages, 1)
	assert.Equal(t, []string{"c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8"}, lineageIds(lineages[0]))
	assert.Equal(t, lineageDiagnostics{Overlaps: 1}, diag)
}

func TestSplitLineages_ExcludesDivergentOverlap(t *testing.T) {
	t.Parallel()
	f, calls := traceFixture(t)
	f.add("q1", text(llm2.RoleAssistant, "unrelated reply"))
	// A call that overlaps its predecessor and whose prompt neither extends
	// nor truncates it contradicts sequential execution: the recording can't
	// say which of the two any later call descends from.
	divergent := f.call("dv", calls[2].CreatedAt.Add(time.Second), "a1", "q1")
	calls = append(calls[:3], divergent, f.call("after", calls[2].CreatedAt.Add(time.Minute), "a1", "q1", "d1"))
	trace, err := reconstructHistory(context.Background(), f.blocks, calls)
	require.NoError(t, err)

	lineages, diag := splitLineages(trace.Steps, false)
	require.Len(t, lineages, 1)
	assert.Equal(t, []string{"c1", "c2", "c3"}, lineageIds(lineages[0]))
	assert.Equal(t, lineageDiagnostics{Overlaps: 1, Excluded: 2}, diag)
}

func TestSplitLineages_IdenticalPromptSibling(t *testing.T) {
	t.Parallel()
	f, calls := traceFixture(t)
	// An overlapping call with the same prompt as its predecessor (a retry)
	// leaves ancestry formally ambiguous, but every candidate parent has the
	// same prompt, so the derived ops are identical whichever is chosen. It
	// is kept as a no-op step by default and excluded in strict mode.
	retry := f.call("rt", calls[2].CreatedAt.Add(time.Second), "a1", "b1", "c1", "d1")
	calls = append(calls[:3], retry, calls[3])
	trace, err := reconstructHistory(context.Background(), f.blocks, calls)
	require.NoError(t, err)

	lineages, diag := splitLineages(trace.Steps, false)
	require.Len(t, lineages, 1)
	assert.Equal(t, []string{"c1", "c2", "c3", "rt", "c4"}, lineageIds(lineages[0]))
	assert.Empty(t, lineages[0][3].Ops)
	assert.Equal(t, lineageDiagnostics{Overlaps: 1, IdenticalPrompts: 1}, diag)

	lineages, diag = splitLineages(trace.Steps, true)
	require.Len(t, lineages, 1)
	assert.Equal(t, []string{"c1", "c2", "c3"}, lineageIds(lineages[0]))
	assert.Equal(t, lineageDiagnostics{Overlaps: 1, IdenticalPrompts: 1, Excluded: 2}, diag)
}

func TestHydrateCalls_BlockKeysAreFlowScoped(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.add("k1", text(llm2.RoleUser, "flow one"))
	other := text(llm2.RoleUser, "flow two")
	raw, err := json.Marshal(other.Content[0])
	require.NoError(t, err)
	f.blocks["ws|"+persisted_ai.StorageKey("flow-2", "k1")] = raw

	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	c1 := f.call("c1", t0, "k1")
	c2 := f.call("c2", t0.Add(time.Minute), "k1")
	c2.FlowID = "flow-2"
	hydrated, err := hydrateCalls(context.Background(), f.blocks, []callRecord{c1, c2})
	require.NoError(t, err)
	assert.Equal(t, "flow one", hydrated[0][0].Content[0].Text)
	assert.Equal(t, "flow two", hydrated[1][0].Content[0].Text)
}

func TestRecordedUsage_PreservesBuckets(t *testing.T) {
	t.Parallel()
	c := callRecord{InputTokens: 1000, CacheReadTokens: 700, CacheWriteTokens: 200}
	assert.Equal(t, usage{Read: 700, Write: 200, Uncached: 100}, recordedUsage(c))
}

func TestPriceSchedule_TierByTotalPrompt(t *testing.T) {
	t.Parallel()
	haiku := builtinSchedules["anthropic/claude-haiku-5-5"]
	tests := []struct {
		name      string
		u         usage
		retention string
		wantCost  float64
	}{
		// Tier is chosen on the whole prompt, cached tokens included.
		{"at boundary, cheap tier", usage{Read: 99000, Write: 1000}, retention5m, (99000*0.01 + 1000*0.125) / 1e6},
		{"one over boundary, expensive tier", usage{Read: 99001, Write: 1000}, retention5m, (99001*0.05 + 1000*0.625) / 1e6},
		{"1h writes priced separately", usage{Read: 1000, Write: 1000}, retention1h, (1000*0.01 + 1000*0.20) / 1e6},
		{"1h writes, expensive tier", usage{Read: 100000, Write: 1000}, retention1h, (100000*0.05 + 1000*1.0) / 1e6},
		{"uncached billed at input", usage{Uncached: 150000}, retention5m, 150000 * 0.50 / 1e6},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.InDelta(t, tt.wantCost, cost(tt.u, haiku, tt.retention), 1e-12)
		})
	}
}
