package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"sidekick/llm2"
	"sidekick/persisted_ai"
)

// callRecord is one LLM call as recorded in the usage snapshot.
type callRecord struct {
	ID          string
	WorkspaceID string
	FlowID      string
	SubflowID   string
	Provider    string
	Model       string
	ToolsHash   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Refs        []persisted_ai.MessageRef

	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
}

// blockSource fetches persisted content blocks by storage key.
type blockSource interface {
	Blocks(ctx context.Context, workspaceID string, storageKeys []string) (map[string][]byte, error)
}

type opKind int

const (
	opAppend opKind = iota
	opInsert
	opEdit
	opDrop
)

func (k opKind) String() string {
	return [...]string{"append", "insert", "edit", "drop"}[k]
}

type editKind int

const (
	// editContent is a change not attributable to history management; it is
	// applied to counterfactual histories.
	editContent editKind = iota
	// editToolTruncation is the policy's middle-truncation of a tool result.
	editToolTruncation
	// editBlockRemoval is the policy's removal of orphaned tool blocks.
	editBlockRemoval
)

func (k editKind) String() string {
	return [...]string{"content", "toolTruncation", "blockRemoval"}[k]
}

// deltaOp is one ordered change between consecutive prompts of a history.
type deltaOp struct {
	Kind opKind
	// Msg is the appended, inserted or replacement message.
	Msg llm2.Message
	// Anchor is the identity of the unchanged message an insert precedes.
	Anchor string
	// Replaces is the identity of the message an edit supersedes.
	Replaces string
	EditKind editKind
	// Dropped is the identity of a removed message.
	Dropped string
	// Tail marks a drop that comes after the last retained message: the
	// workflow rewound, which is not a history-management decision.
	Tail bool
}

type traceStep struct {
	Call   callRecord
	Actual []llm2.Message
	Ops    []deltaOp
	Branch bool
}

type historyTrace struct {
	Steps []traceStep
}

const blockFetchBatch = 500

// reconstructHistory hydrates each call's prompt and derives the ordered
// delta ops between consecutive calls. Calls must be in the order they were
// made. Any missing block fails loudly rather than being treated as content.
func reconstructHistory(ctx context.Context, src blockSource, calls []callRecord) (*historyTrace, error) {
	hydrated, err := hydrateCalls(ctx, src, calls)
	if err != nil {
		return nil, err
	}
	trace := &historyTrace{}
	var prev []llm2.Message
	for i, call := range calls {
		step := traceStep{Call: call, Actual: hydrated[i]}
		step.Ops, step.Branch = diffPrompts(prev, hydrated[i])
		trace.Steps = append(trace.Steps, step)
		prev = hydrated[i]
	}
	return trace, nil
}

func hydrateCalls(ctx context.Context, src blockSource, calls []callRecord) ([][]llm2.Message, error) {
	// Blocks are stored under flow-scoped keys, so the same block key in two
	// flows refers to two different blocks.
	type flowKey struct{ ws, flow string }
	type wsKey struct {
		flowKey
		key string
	}
	seen := map[wsKey]bool{}
	byFlow := map[flowKey][]string{}
	for _, call := range calls {
		fk := flowKey{call.WorkspaceID, call.FlowID}
		for _, ref := range call.Refs {
			for _, blockKey := range ref.BlockKeys {
				k := wsKey{fk, blockKey}
				if !seen[k] {
					seen[k] = true
					byFlow[fk] = append(byFlow[fk], blockKey)
				}
			}
		}
	}

	blocks := map[wsKey]llm2.ContentBlock{}
	for _, call := range calls {
		fk := flowKey{call.WorkspaceID, call.FlowID}
		keys := byFlow[fk]
		if keys == nil {
			continue
		}
		delete(byFlow, fk)
		for start := 0; start < len(keys); start += blockFetchBatch {
			end := min(start+blockFetchBatch, len(keys))
			storageKeys := make([]string, 0, end-start)
			for _, blockKey := range keys[start:end] {
				storageKeys = append(storageKeys, persisted_ai.StorageKey(call.FlowID, blockKey))
			}
			values, err := src.Blocks(ctx, call.WorkspaceID, storageKeys)
			if err != nil {
				return nil, fmt.Errorf("fetching blocks for flow %s: %w", call.FlowID, err)
			}
			for i, blockKey := range keys[start:end] {
				raw, ok := values[storageKeys[i]]
				if !ok {
					return nil, fmt.Errorf("flow %s: block %s is missing from storage", call.FlowID, blockKey)
				}
				var block llm2.ContentBlock
				if err := json.Unmarshal(raw, &block); err != nil {
					return nil, fmt.Errorf("flow %s: block %s: %w", call.FlowID, blockKey, err)
				}
				persisted_ai.SetBlockKey(&block, blockKey)
				blocks[wsKey{fk, blockKey}] = block
			}
		}
	}

	out := make([][]llm2.Message, len(calls))
	for i, call := range calls {
		fk := flowKey{call.WorkspaceID, call.FlowID}
		msgs := make([]llm2.Message, 0, len(call.Refs))
		for _, ref := range call.Refs {
			msg := llm2.Message{Role: llm2.Role(ref.Role), Content: make([]llm2.ContentBlock, 0, len(ref.BlockKeys))}
			for _, blockKey := range ref.BlockKeys {
				msg.Content = append(msg.Content, blocks[wsKey{fk, blockKey}])
			}
			msgs = append(msgs, msg)
		}
		out[i] = msgs
	}
	return out, nil
}

// identity is a content fingerprint that ignores cache markers, block ids and
// persistence keys, which change without the prompt content changing.
func identity(msg llm2.Message) string {
	norm := llm2.Message{Role: msg.Role, Content: make([]llm2.ContentBlock, 0, len(msg.Content))}
	for _, block := range msg.Content {
		norm.Content = append(norm.Content, normalizeBlock(block))
	}
	raw, err := json.Marshal(norm)
	if err != nil {
		panic(fmt.Sprintf("marshal message identity: %v", err))
	}
	return string(raw)
}

func normalizeBlock(block llm2.ContentBlock) llm2.ContentBlock {
	block.Id = ""
	block.CacheControl = ""
	if block.Metadata != nil {
		meta := make(map[string]any, len(block.Metadata))
		for k, v := range block.Metadata {
			if k != persisted_ai.MetadataNamespacePersistence {
				meta[k] = v
			}
		}
		if len(meta) == 0 {
			meta = nil
		}
		block.Metadata = meta
	}
	if block.ToolResult != nil {
		tr := *block.ToolResult
		tr.Content = make([]llm2.ContentBlock, 0, len(block.ToolResult.Content))
		for _, nested := range block.ToolResult.Content {
			tr.Content = append(tr.Content, normalizeBlock(nested))
		}
		block.ToolResult = &tr
	}
	return block
}

// toolIds returns the tool call ids a message issues and the ones it answers.
func toolIds(msg llm2.Message) (uses, results map[string]bool) {
	uses, results = map[string]bool{}, map[string]bool{}
	for _, block := range msg.Content {
		if block.ToolUse != nil {
			uses[block.ToolUse.Id] = true
		}
		if block.ToolResult != nil {
			results[block.ToolResult.ToolCallId] = true
		}
	}
	return uses, results
}

func intersects(a, b map[string]bool) bool {
	for id := range a {
		if b[id] {
			return true
		}
	}
	return false
}

// isEditOf reports whether after is a revised version of before: same role
// and the same tool call issued, or the same tool call answered. A tool use
// and its own result share an id but are different messages.
func isEditOf(before, after llm2.Message) bool {
	if before.Role != after.Role {
		return false
	}
	beforeUses, beforeResults := toolIds(before)
	afterUses, afterResults := toolIds(after)
	return intersects(beforeUses, afterUses) || intersects(beforeResults, afterResults)
}

func classifyEdit(before, after llm2.Message) editKind {
	for _, block := range after.Content {
		if block.ToolResult != nil && strings.Contains(block.ToolResult.TextContent(), "characters were truncated from this tool response") {
			return editToolTruncation
		}
		if block.ToolResult != nil && strings.Contains(block.ToolResult.TextContent(), "characters truncated.") {
			return editToolTruncation
		}
	}
	if len(after.Content) < len(before.Content) {
		beforeBlocks := map[string]bool{}
		for _, block := range before.Content {
			beforeBlocks[identity(llm2.Message{Content: []llm2.ContentBlock{block}})] = true
		}
		subset := true
		for _, block := range after.Content {
			if !beforeBlocks[identity(llm2.Message{Content: []llm2.ContentBlock{block}})] {
				subset = false
				break
			}
		}
		if subset {
			return editBlockRemoval
		}
	}
	return editContent
}

// lcsMatch aligns two identity sequences, returning for each position in b
// the matched index in a (or -1). Repeated identical messages align
// positionally, so a duplicate appended later is not confused with an
// earlier occurrence.
func lcsMatch(a, b []string) []int {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	match := make([]int, m)
	for j := range match {
		match[j] = -1
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			match[j] = i
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			i++
		default:
			j++
		}
	}
	return match
}

// diffPrompts derives ordered delta ops turning prev into cur.
func diffPrompts(prev, cur []llm2.Message) ([]deltaOp, bool) {
	prevIds := make([]string, len(prev))
	for i, msg := range prev {
		prevIds[i] = identity(msg)
	}
	curIds := make([]string, len(cur))
	for j, msg := range cur {
		curIds[j] = identity(msg)
	}
	matchCur := lcsMatch(prevIds, curIds)
	matchPrev := make([]int, len(prev))
	for i := range matchPrev {
		matchPrev[i] = -1
	}
	for j, i := range matchCur {
		if i >= 0 {
			matchPrev[i] = j
		}
	}

	// Pair unmatched messages on both sides that refer to the same tool call:
	// those are edits of one message rather than a drop plus an addition.
	editOf := map[int]int{} // cur index -> prev index
	for j := range cur {
		if matchCur[j] >= 0 {
			continue
		}
		for i := range prev {
			if matchPrev[i] >= 0 {
				continue
			}
			if isEditOf(prev[i], cur[j]) {
				editOf[j] = i
				matchPrev[i] = j
				break
			}
		}
	}

	lastRetainedPrev := -1
	for i, j := range matchPrev {
		if j >= 0 {
			lastRetainedPrev = i
		}
	}

	var ops []deltaOp
	branch := false
	for i := range prev {
		if matchPrev[i] >= 0 {
			continue
		}
		tail := i > lastRetainedPrev
		branch = branch || tail
		ops = append(ops, deltaOp{Kind: opDrop, Dropped: prevIds[i], Tail: tail})
	}
	for j, msg := range cur {
		if matchCur[j] >= 0 {
			continue
		}
		if i, ok := editOf[j]; ok {
			ops = append(ops, deltaOp{Kind: opEdit, Msg: msg, Replaces: prevIds[i], EditKind: classifyEdit(prev[i], msg)})
			continue
		}
		anchor := ""
		for k := j + 1; k < len(cur); k++ {
			if matchCur[k] >= 0 {
				anchor = curIds[k]
				break
			}
		}
		if anchor == "" {
			ops = append(ops, deltaOp{Kind: opAppend, Msg: msg})
		} else {
			ops = append(ops, deltaOp{Kind: opInsert, Msg: msg, Anchor: anchor})
		}
	}
	return ops, branch
}

type lineageDiagnostics struct {
	// Overlaps counts calls created before their predecessor's UpdatedAt.
	// UpdatedAt is rewritten asynchronously after completion (activity-ref
	// decoration), so an overlap alone does not indicate concurrency.
	Overlaps int
	// IdenticalPrompts counts overlapping calls whose prompt equals the
	// predecessor's (retries): ancestry is formally ambiguous but the
	// derived ops are the same for any candidate parent.
	IdenticalPrompts int
	// Excluded counts calls dropped because their ancestry could not be
	// established.
	Excluded int
}

// splitLineages groups steps into independent histories: same workspace,
// flow, subflow, provider, model, tool set and opening message, in creation
// order. One action type within one subflow runs sequentially in the
// workflow, so creation order is ancestry. An overlapping call whose prompt
// neither extends, truncates nor repeats its predecessor contradicts that,
// and the group is cut there. In strict mode every overlap cuts the group.
func splitLineages(steps []traceStep, strict bool) ([][]traceStep, lineageDiagnostics) {
	type groupKey struct{ ws, flow, subflow, provider, model, tools, first string }
	groups := map[groupKey][]traceStep{}
	var order []groupKey
	for _, step := range steps {
		if len(step.Actual) == 0 {
			continue
		}
		c := step.Call
		k := groupKey{c.WorkspaceID, c.FlowID, c.SubflowID, c.Provider, c.Model, c.ToolsHash, identity(step.Actual[0])}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], step)
	}

	var lineages [][]traceStep
	var diag lineageDiagnostics
	for _, k := range order {
		group := groups[k]
		sort.SliceStable(group, func(i, j int) bool { return group[i].Call.CreatedAt.Before(group[j].Call.CreatedAt) })
		var lineage []traceStep
		for i, step := range group {
			if i > 0 && step.Call.CreatedAt.Before(group[i-1].Call.UpdatedAt) {
				diag.Overlaps++
				ops, branch := diffPrompts(group[i-1].Actual, step.Actual)
				if len(ops) == 0 {
					diag.IdenticalPrompts++
				}
				if strict || branch || hasInsert(ops) {
					diag.Excluded += len(group) - i
					break
				}
			}
			lineage = append(lineage, step)
		}
		lineages = append(lineages, lineage)
	}
	// Re-derive ops within each lineage so diffs are against the true
	// predecessor rather than whatever call happened to precede in the input.
	for _, lineage := range lineages {
		var prev []llm2.Message
		for i := range lineage {
			lineage[i].Ops, lineage[i].Branch = diffPrompts(prev, lineage[i].Actual)
			prev = lineage[i].Actual
		}
	}
	return lineages, diag
}

func hasInsert(ops []deltaOp) bool {
	for _, op := range ops {
		if op.Kind == opInsert {
			return true
		}
	}
	return false
}

type replayStep struct {
	PromptChars int
	// PrefixChars is the size of the prompt prefix shared with the previous
	// prompt in this lineage, i.e. what a prefix cache could serve.
	PrefixChars int
	Messages    int
	// Truncated means history management altered the history on this step.
	Truncated bool
	// SkippedOps counts recorded changes that could not be applied because
	// the counterfactual policy had already dropped their target.
	SkippedOps int
}

type policyFunc func([]llm2.Message) ([]llm2.Message, error)

func lastIndexOf(ids []string, id string) int {
	for i := len(ids) - 1; i >= 0; i-- {
		if ids[i] == id {
			return i
		}
	}
	return -1
}

// replay carries a counterfactual history through the trace: each step
// applies the recorded genuine changes to the policy's previous output, then
// runs the policy. Policy-caused changes in the recording (head drops, tool
// result truncation, orphan cleanup) are left for the policy to decide.
func replay(trace *historyTrace, provider string, policy policyFunc) ([]replayStep, error) {
	var history []llm2.Message
	var historyIds []string
	var prevSent []string
	steps := make([]replayStep, 0, len(trace.Steps))
	for _, step := range trace.Steps {
		skipped := 0
		for _, op := range step.Ops {
			switch op.Kind {
			case opDrop:
				if !op.Tail {
					continue
				}
				if i := lastIndexOf(historyIds, op.Dropped); i >= 0 {
					history = append(history[:i], history[i+1:]...)
					historyIds = append(historyIds[:i], historyIds[i+1:]...)
				} else {
					skipped++
				}
			case opEdit:
				if op.EditKind != editContent {
					continue
				}
				if i := lastIndexOf(historyIds, op.Replaces); i >= 0 {
					history[i] = op.Msg
					historyIds[i] = identity(op.Msg)
				} else {
					skipped++
				}
			case opInsert:
				if i := lastIndexOf(historyIds, op.Anchor); i >= 0 {
					history = append(history[:i], append([]llm2.Message{op.Msg}, history[i:]...)...)
					historyIds = append(historyIds[:i], append([]string{identity(op.Msg)}, historyIds[i:]...)...)
				} else {
					skipped++
					history = append(history, op.Msg)
					historyIds = append(historyIds, identity(op.Msg))
				}
			case opAppend:
				history = append(history, op.Msg)
				historyIds = append(historyIds, identity(op.Msg))
			}
		}

		managed, err := policy(history)
		if err != nil {
			return nil, fmt.Errorf("call %s: %w", step.Call.ID, err)
		}
		managedIds := make([]string, len(managed))
		for i, msg := range managed {
			managedIds[i] = identity(msg)
		}
		truncated := !equalIds(managedIds, historyIds)
		steps = append(steps, replayStep{
			PromptChars: persisted_ai.MeasureLlm2Messages(provider, managed),
			PrefixChars: commonPrefixChars(provider, prevSent, managed, managedIds),
			Messages:    len(managed),
			Truncated:   truncated,
			SkippedOps:  skipped,
		})
		// The previous prompt must stay fixed while the next step edits
		// the working history, so the two id slices cannot share storage.
		history = append([]llm2.Message(nil), managed...)
		historyIds = append([]string(nil), managedIds...)
		prevSent = managedIds
	}
	return steps, nil
}

func equalIds(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func commonPrefixChars(provider string, prevIds []string, cur []llm2.Message, curIds []string) int {
	chars := 0
	for i := 0; i < len(prevIds) && i < len(curIds) && prevIds[i] == curIds[i]; i++ {
		chars += persisted_ai.Llm2MessageLength(provider, cur[i])
	}
	return chars
}

// actualSteps measures what was really sent. Truncated here means the
// recording shows a history-management change (head drop or policy edit).
func actualSteps(trace *historyTrace, provider string) []replayStep {
	var prevIds []string
	steps := make([]replayStep, 0, len(trace.Steps))
	for _, step := range trace.Steps {
		ids := make([]string, len(step.Actual))
		for i, msg := range step.Actual {
			ids[i] = identity(msg)
		}
		truncated := false
		for _, op := range step.Ops {
			if (op.Kind == opDrop && !op.Tail) || (op.Kind == opEdit && op.EditKind != editContent) {
				truncated = true
			}
		}
		steps = append(steps, replayStep{
			PromptChars: persisted_ai.MeasureLlm2Messages(provider, step.Actual),
			PrefixChars: commonPrefixChars(provider, prevIds, step.Actual, ids),
			Messages:    len(step.Actual),
			Truncated:   truncated,
		})
		prevIds = ids
	}
	return steps
}

// tokenModel maps prompt chars to billed input tokens: a constant for the
// system prompt and tool schemas plus a per-char rate.
type tokenModel struct {
	Overhead        float64
	PerChar         float64
	R2              float64
	MedianAbsPctErr float64
	N               int
}

func (tm tokenModel) tokens(chars int) int {
	return int(math.Round(tm.Overhead + tm.PerChar*float64(chars)))
}

// fitTokenModel is an ordinary least squares fit of tokens on chars.
func fitTokenModel(chars, tokens []float64) tokenModel {
	n := float64(len(chars))
	if n < 2 {
		return tokenModel{N: len(chars)}
	}
	var sx, sy, sxx, sxy float64
	for i := range chars {
		sx += chars[i]
		sy += tokens[i]
		sxx += chars[i] * chars[i]
		sxy += chars[i] * tokens[i]
	}
	denom := n*sxx - sx*sx
	if denom == 0 {
		return tokenModel{N: len(chars)}
	}
	slope := (n*sxy - sx*sy) / denom
	intercept := (sy - slope*sx) / n
	meanY := sy / n
	var ssRes, ssTot float64
	pctErrs := make([]float64, 0, len(chars))
	for i := range chars {
		pred := intercept + slope*chars[i]
		ssRes += (tokens[i] - pred) * (tokens[i] - pred)
		ssTot += (tokens[i] - meanY) * (tokens[i] - meanY)
		if tokens[i] != 0 {
			pctErrs = append(pctErrs, math.Abs(tokens[i]-pred)/tokens[i])
		}
	}
	r2 := 1.0
	if ssTot > 0 {
		r2 = 1 - ssRes/ssTot
	}
	return tokenModel{Overhead: intercept, PerChar: slope, R2: r2, MedianAbsPctErr: median(pctErrs), N: len(chars)}
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	return percentileSorted(s, 0.5)
}

func percentileSorted(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	idx := p * float64(len(sorted)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (sorted[hi]-sorted[lo])*(idx-float64(lo))
}

// cacheModel is an idealized prefix cache. Real providers deviate (routing,
// eviction, breakpoint placement); the baseline error report quantifies that.
type cacheModel struct {
	TTL time.Duration
	// MinCacheable is the smallest prefix (tokens) a provider will cache.
	MinCacheable int
	// Granularity is the increment implicit caches hit in (OpenAI: 128).
	Granularity int
	// ExplicitWrites bills every uncached token as a cache write (Anthropic
	// with a trailing breakpoint). Otherwise uncached tokens are plain input.
	ExplicitWrites bool
}

type usage struct {
	Read     int
	Write    int
	Uncached int
}

func (u usage) total() int { return u.Read + u.Write + u.Uncached }

// recordedUsage reports the provider's billed buckets as recorded; the
// extractor normalizes InputTokens to the total across all three.
func recordedUsage(c callRecord) usage {
	return usage{
		Read:     c.CacheReadTokens,
		Write:    c.CacheWriteTokens,
		Uncached: c.InputTokens - c.CacheReadTokens - c.CacheWriteTokens,
	}
}

// simulateUsage bills each step under cm. The TTL gap is measured between
// creations because UpdatedAt is an asynchronous bookkeeping time; this
// overstates the gap by the previous call's duration, which is conservative.
func simulateUsage(steps []replayStep, calls []callRecord, tm tokenModel, cm cacheModel) []usage {
	out := make([]usage, len(steps))
	for i, step := range steps {
		total := tm.tokens(step.PromptChars)
		read := 0
		if i > 0 && step.PrefixChars > 0 {
			gap := calls[i].CreatedAt.Sub(calls[i-1].CreatedAt)
			if gap <= cm.TTL {
				read = tm.tokens(step.PrefixChars)
				if cm.Granularity > 1 {
					read -= read % cm.Granularity
				}
				if read < cm.MinCacheable {
					read = 0
				}
				read = min(read, total)
			}
		}
		u := usage{Read: read}
		if cm.ExplicitWrites {
			u.Write = total - read
		} else {
			u.Uncached = total - read
		}
		out[i] = u
	}
	return out
}

// priceTier is USD per million tokens for prompts whose total size (cached
// tokens included) is at most MaxPromptTokens; 0 means unbounded.
type priceTier struct {
	MaxPromptTokens int
	Input           float64
	CacheRead       float64
	CacheWrite      float64
	// LongTTLCacheWrite is the write price for the provider's longer
	// retention option (Anthropic 1h). Zero means the provider has a single
	// write price.
	LongTTLCacheWrite float64
}

// Cache retentions a provider sells. Billing differs by retention, so a
// schedule lists which ones it supports rather than silently pricing
// retention the provider does not offer.
const (
	retention5m       = "5m"
	retention1h       = "1h"
	retention30m      = "30m"
	retentionImplicit = "implicit"
)

// priceSchedule is a model's price list, tiers in ascending size order.
type priceSchedule struct {
	Name       string
	Tiers      []priceTier
	Retentions []string
}

func (s priceSchedule) supports(retention string) bool {
	for _, r := range s.Retentions {
		if r == retention {
			return true
		}
	}
	return false
}

func anthropicSchedule(name string, input, read, write5m, write1h float64) priceSchedule {
	return priceSchedule{Name: name, Retentions: []string{retention5m, retention1h},
		Tiers: []priceTier{{Input: input, CacheRead: read, CacheWrite: write5m, LongTTLCacheWrite: write1h}}}
}

// flatSchedule is a single-tier list with only a short explicit retention,
// used for ad-hoc and models.dev prices where the TTL options are unknown.
func flatSchedule(name string, input, read, write float64) priceSchedule {
	return priceSchedule{Name: name, Retentions: []string{retention5m},
		Tiers: []priceTier{{Input: input, CacheRead: read, CacheWrite: write}}}
}

func (s priceSchedule) tier(promptTokens int) priceTier {
	for _, t := range s.Tiers {
		if t.MaxPromptTokens == 0 || promptTokens <= t.MaxPromptTokens {
			return t
		}
	}
	return s.Tiers[len(s.Tiers)-1]
}

func (s priceSchedule) String() string {
	var parts []string
	for _, t := range s.Tiers {
		bound := "any size"
		if t.MaxPromptTokens > 0 {
			bound = fmt.Sprintf("≤%dk", t.MaxPromptTokens/1000)
		}
		p := fmt.Sprintf("%s: in %.3g read %.3g write %.3g", bound, t.Input, t.CacheRead, t.CacheWrite)
		if t.LongTTLCacheWrite > 0 {
			p += fmt.Sprintf(" long-write %.3g", t.LongTTLCacheWrite)
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, "; ")
}

// cost bills u under s for the given retention. Tier selection uses the whole
// prompt (cached tokens included). Returns NaN when s does not sell that
// retention, so unsupported combinations surface instead of being priced at
// a neighbouring rate.
func cost(u usage, s priceSchedule, retention string) float64 {
	if !s.supports(retention) {
		return math.NaN()
	}
	t := s.tier(u.total())
	write := t.CacheWrite
	if retention == retention1h {
		write = t.LongTTLCacheWrite
	}
	return (float64(u.Read)*t.CacheRead + float64(u.Write)*write + float64(u.Uncached)*t.Input) / 1e6
}

// missed re-bills u as if the request had not hit its eligible cached prefix:
// the read tokens move to the write bucket (explicit caches re-write the
// prefix) or the uncached bucket. Total prompt size, and so tier selection,
// is unchanged.
func (u usage) missed(explicitWrites bool) usage {
	if explicitWrites {
		return usage{Write: u.Write + u.Read, Uncached: u.Uncached}
	}
	return usage{Write: u.Write, Uncached: u.Uncached + u.Read}
}

// expectedCost blends an ideal hit with a miss at the given conditional hit
// probability. Short probes measure per-request hit rates well below 1 even
// when routing is correct, so policy costs are reported across a range of
// hitProb rather than only the ideal.
func expectedCost(u usage, s priceSchedule, retention string, hitProb float64, explicitWrites bool) float64 {
	if hitProb >= 1 {
		return cost(u, s, retention)
	}
	return hitProb*cost(u, s, retention) + (1-hitProb)*cost(u.missed(explicitWrites), s, retention)
}

func parseHitProbabilities(spec string) ([]float64, error) {
	var out []float64
	for _, part := range strings.Split(spec, ",") {
		p, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil || math.IsNaN(p) || math.IsInf(p, 0) || p < 0 || p > 1 {
			return nil, fmt.Errorf("hit probability %q: want a number in [0,1]", part)
		}
		out = append(out, p)
	}
	return out, nil
}

// openAIContextBoundary is the last prompt size billed as "short context"
// (OpenAI: strictly under 272k).
const openAIContextBoundary = 272000 - 1

func openAISchedule(name string, short, long [3]float64) priceSchedule {
	return priceSchedule{Name: name, Retentions: []string{retention30m, retentionImplicit}, Tiers: []priceTier{
		{MaxPromptTokens: openAIContextBoundary, Input: short[0], CacheRead: short[1], CacheWrite: short[2]},
		{Input: long[0], CacheRead: long[1], CacheWrite: long[2]},
	}}
}

// builtinSchedules are list prices as published on 2026-10-08. Anthropic
// sells 5m and 1h explicit retention; OpenAI sells a 30m explicit cache plus
// free implicit caching. Output tokens are not modeled.
var builtinSchedules = map[string]priceSchedule{
	"anthropic/claude-fable-5-1":  anthropicSchedule("Fable 5.1", 10, 0.25, 12.5, 20),
	"anthropic/claude-opus-5-5":   anthropicSchedule("Opus 5.5", 4, 0.20, 5, 8),
	"anthropic/claude-sonnet-5-5": anthropicSchedule("Sonnet 5.5", 2, 0.10, 2.5, 4),
	"anthropic/claude-haiku-5-5": {Name: "Haiku 5.5", Retentions: []string{retention5m, retention1h}, Tiers: []priceTier{
		{MaxPromptTokens: 100000, Input: 0.10, CacheRead: 0.01, CacheWrite: 0.125, LongTTLCacheWrite: 0.20},
		{Input: 0.50, CacheRead: 0.05, CacheWrite: 0.625, LongTTLCacheWrite: 1.0},
	}},
	"openai/gpt-6-astra":           openAISchedule("Astra standard", [3]float64{10, 1, 12.5}, [3]float64{20, 2, 25}),
	"openai/gpt-6-astra/flex":      openAISchedule("Astra flex", [3]float64{5, 0.5, 6.25}, [3]float64{10, 1, 12.5}),
	"openai/gpt-6-astra/fast":      openAISchedule("Astra fast", [3]float64{20, 2, 25}, [3]float64{40, 4, 50}),
	"openai/gpt-6-astra/ultrafast": openAISchedule("Astra ultrafast", [3]float64{60, 6, 75}, [3]float64{120, 12, 150}),
	"openai/gpt-6.1-sol":           openAISchedule("Sol 6.1 standard", [3]float64{2, 0.1, 2.5}, [3]float64{4, 0.2, 5}),
	"openai/gpt-6.1-sol/flex":      openAISchedule("Sol 6.1 flex", [3]float64{1, 0.05, 1.25}, [3]float64{2, 0.1, 2.5}),
	"openai/gpt-6.1-sol/fast":      openAISchedule("Sol 6.1 fast", [3]float64{4, 0.2, 5}, [3]float64{8, 0.4, 10}),
	"openai/gpt-6.1-sol/ultrafast": openAISchedule("Sol 6.1 ultrafast", [3]float64{12, 0.6, 15}, [3]float64{24, 1.2, 30}),
	"openai/gpt-6-luna":            openAISchedule("Luna standard", [3]float64{0.1, 0.01, 0.125}, [3]float64{0.2, 0.02, 0.25}),
	"openai/gpt-6-luna/flex":       openAISchedule("Luna flex", [3]float64{0.05, 0.005, 0.0625}, [3]float64{0.1, 0.01, 0.125}),
	"openai/gpt-6-luna/fast":       openAISchedule("Luna fast", [3]float64{0.2, 0.02, 0.25}, [3]float64{0.4, 0.04, 0.5}),
}

func builtinScheduleNames() []string {
	names := make([]string, 0, len(builtinSchedules))
	for n := range builtinSchedules {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func parseDuckTime(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05.999999", "2006-01-02 15:04:05", time.RFC3339Nano} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp %q", s)
}
