package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math/rand/v2"
	"net/http"
	"sort"
	"strings"
)

// fillerText is deterministic per nonce so exact-repeat calls are byte-identical
// while different trials never share a cacheable prefix beyond the instructions.
func fillerText(nonce string, words int) string {
	h := fnv.New64a()
	h.Write([]byte(nonce))
	r := rand.New(rand.NewPCG(h.Sum64(), 0x9e3779b97f4a7c15))
	vocab := []string{"ledger", "quorum", "basalt", "ferry", "lantern", "meridian", "orchard", "pivot", "quartz",
		"rivet", "saddle", "tundra", "umber", "vellum", "willow", "yonder", "zephyr", "anvil", "beacon", "cobalt",
		"dapple", "ember", "fathom", "gable", "harbor", "ingot", "juniper", "kestrel", "lumen", "mantle"}
	var sb strings.Builder
	for i := 0; i < words; i++ {
		if i > 0 {
			if i%17 == 0 {
				sb.WriteString(".\n")
			} else {
				sb.WriteByte(' ')
			}
		}
		sb.WriteString(vocab[r.IntN(len(vocab))])
		fmt.Fprintf(&sb, "%02d", r.IntN(100))
	}
	return sb.String()
}

func buildRequest(ctx context.Context, route probeRoute, a arm, key string, prompt probePrompt) (*http.Request, error) {
	input := make([]map[string]any, 0, len(prompt.messages))
	for _, m := range prompt.messages {
		input = append(input, map[string]any{"role": "user", "content": m})
	}
	body := map[string]any{
		"model":        prompt.model,
		"instructions": "You are a terse assistant used for an automated cache probe.",
		"input":        input,
		"stream":       true,
		"store":        false,
		"include":      []string{"reasoning.encrypted_content"},
	}
	if prompt.effort != "" {
		body["reasoning"] = map[string]any{"effort": prompt.effort}
	}
	if a != armNoKey && a != armSessionHeaderOnly {
		body["prompt_cache_key"] = key
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, route.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+route.token)
	req.Header.Set("Content-Type", "application/json")
	if route.accountID != "" {
		req.Header.Set("ChatGPT-Account-Id", route.accountID)
	}
	switch a {
	case armSessionHeaderOnly, armSessionHeader:
		req.Header.Set("session-id", key)
	case armAllHeaders:
		req.Header.Set("session-id", key)
		req.Header.Set("thread-id", key)
		req.Header.Set("x-client-request-id", key)
	}
	return req, nil
}

func printSummary(w io.Writer, results []callResult) {
	type agg struct {
		n, hits, errs int
		ratioSum      float64
	}
	byArmKind := map[string]*agg{}
	for _, r := range results {
		k := string(r.Arm) + "\t" + r.Kind
		a := byArmKind[k]
		if a == nil {
			a = &agg{}
			byArmKind[k] = a
		}
		a.n++
		if r.Error != "" {
			a.errs++
			continue
		}
		a.ratioSum += r.CachedRatio
		if r.CachedRatio >= 0.9 {
			a.hits++
		}
	}
	keys := make([]string, 0, len(byArmKind))
	for k := range byArmKind {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintln(w, "\n| arm | kind | calls | errors | hits (>=90%) | mean cached ratio |")
	fmt.Fprintln(w, "|---|---|--:|--:|--:|--:|")
	for _, k := range keys {
		a := byArmKind[k]
		parts := strings.Split(k, "\t")
		ok := a.n - a.errs
		mean := 0.0
		if ok > 0 {
			mean = a.ratioSum / float64(ok)
		}
		fmt.Fprintf(w, "| %s | %s | %d | %d | %d/%d | %.2f |\n", parts[0], parts[1], a.n, a.errs, a.hits, ok, mean)
	}
}
