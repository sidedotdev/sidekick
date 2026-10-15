// probe_codex_cache_affinity is a backend mechanism test: it asks whether the
// ChatGPT/Codex Responses endpoint keys prompt-cache affinity on the body
// prompt_cache_key or on the session-id family of HTTP headers that the
// official Codex CLI sends. It speaks raw HTTP, so a positive result still has
// to be confirmed through the real llm2 provider path before it counts as a
// production fix.
//
// Each arm×trial uses a fresh key and a nonce-distinct prompt: one cold call,
// then exact repeats, then append-only extensions. Within a round, calls are
// interleaved across arms so clock-time effects don't favour one arm.
package main

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"sidekick/openai_oauth"
	"sidekick/secret_manager"
)

type arm string

const (
	armNoKey             arm = "none"
	armBodyKey           arm = "body"
	armSessionHeaderOnly arm = "session-id"
	armSessionHeader     arm = "body+session-id"
	armAllHeaders        arm = "body+session-id+thread-id+x-client-request-id"
)

var allArms = []arm{armNoKey, armBodyKey, armSessionHeaderOnly, armSessionHeader, armAllHeaders}

type probeRoute struct {
	endpoint  string
	token     string
	accountID string
}

type probePrompt struct {
	model    string
	effort   string
	messages []string
}

type callResult struct {
	Arm          arm     `json:"arm"`
	Trial        int     `json:"trial"`
	Call         int     `json:"call"`
	Kind         string  `json:"kind"`
	Key          string  `json:"key"`
	RequestID    string  `json:"requestId"`
	Model        string  `json:"model"`
	Status       int     `json:"status"`
	InputTokens  int     `json:"inputTokens"`
	CachedTokens int     `json:"cachedTokens"`
	CachedRatio  float64 `json:"cachedRatio"`
	// RawUsage is the provider's usage object verbatim; it is the evidence for
	// which billing fields (e.g. cache_write_tokens) a route actually returns.
	RawUsage    json.RawMessage `json:"rawUsage,omitempty"`
	TTFTMillis  int64           `json:"ttftMillis"`
	TotalMillis int64           `json:"totalMillis"`
	Error       string          `json:"error,omitempty"`
}

// errFatalResponse marks failures that will recur on every call (auth, quota)
// so the run stops instead of burning the remaining budget.
var errFatalResponse = errors.New("fatal provider response")

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type config struct {
	model, effort    string
	route            string
	trials, warm     int
	appends, words   int
	gap, callTimeout time.Duration
	maxCalls         int
	arms             []arm
	out              string
}

func parseFlags() (config, error) {
	var c config
	var armsFlag string
	flag.StringVar(&c.model, "model", "gpt-6-astra", "Model to probe")
	flag.StringVar(&c.effort, "effort", "low", "Reasoning effort")
	flag.StringVar(&c.route, "route", "codex", "codex (ChatGPT subscription, OAuth) or api (api.openai.com, OPENAI_API_KEY)")
	flag.IntVar(&c.trials, "trials", 3, "Trials per arm (each trial uses a fresh key and nonce)")
	flag.IntVar(&c.warm, "warm", 3, "Exact-repeat calls after the cold call")
	flag.IntVar(&c.appends, "append", 2, "Append-only calls after the warm calls")
	flag.IntVar(&c.words, "filler-words", 4500, "Filler words in the first message (must exceed the 1024-token cache minimum)")
	flag.DurationVar(&c.gap, "gap", 2*time.Second, "Pause between calls")
	flag.DurationVar(&c.callTimeout, "call-timeout", 3*time.Minute, "Per-call deadline")
	flag.IntVar(&c.maxCalls, "max-calls", 120, "Hard cap on total requests issued")
	flag.StringVar(&armsFlag, "arms", "none,body,body+session-id,body+session-id+thread-id+x-client-request-id", "Comma-separated arms to run")
	flag.StringVar(&c.out, "out", "", "Append JSONL call results here (default stdout)")
	flag.Parse()

	switch {
	case c.trials <= 0, c.words <= 0, c.maxCalls <= 0, c.callTimeout <= 0:
		return c, errors.New("-trials, -filler-words, -max-calls and -call-timeout must be positive")
	case c.warm < 0, c.appends < 0, c.gap < 0:
		return c, errors.New("-warm, -append and -gap must not be negative")
	}
	arms, err := parseArms(armsFlag)
	if err != nil {
		return c, err
	}
	c.arms = arms
	planned := len(arms) * c.trials * (1 + c.warm + c.appends)
	if planned > c.maxCalls {
		return c, fmt.Errorf("plan needs %d calls, over -max-calls %d", planned, c.maxCalls)
	}
	return c, nil
}

func run() error {
	c, err := parseFlags()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	route, err := resolveRoute(ctx, c.route)
	if err != nil {
		return err
	}

	sink := io.Writer(os.Stdout)
	if c.out != "" {
		f, err := os.OpenFile(c.out, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		sink = f
	}
	enc := json.NewEncoder(sink)

	callsPerTrial := 1 + c.warm + c.appends
	fmt.Fprintf(os.Stderr, "model=%s arms=%v trials=%d calls/trial=%d planned=%d\n",
		c.model, c.arms, c.trials, callsPerTrial, len(c.arms)*c.trials*callsPerTrial)

	type lineage struct {
		key  string
		base probePrompt
	}
	var results []callResult
	issued := 0
	failures := 0
	for trial := 0; trial < c.trials; trial++ {
		lineages := map[arm]lineage{}
		for _, a := range c.arms {
			nonce := newKey()
			lineages[a] = lineage{key: newKey(), base: probePrompt{model: c.model, effort: c.effort, messages: []string{
				fmt.Sprintf("Probe %s. Reply with exactly the single word OK and nothing else.\n\n%s", nonce, fillerText(nonce, c.words)),
			}}}
		}
		for call := 0; call < callsPerTrial; call++ {
			for _, a := range rotatedArms(c.arms, trial+call) {
				if issued >= c.maxCalls {
					return fmt.Errorf("hit -max-calls %d", c.maxCalls)
				}
				l := lineages[a]
				prompt := l.base
				kind := "warm"
				switch {
				case call == 0:
					kind = "cold"
				case call > c.warm:
					kind = "append"
					for k := 0; k < call-c.warm; k++ {
						prompt.messages = append(prompt.messages, fmt.Sprintf("Follow-up %d: again reply with exactly OK.", k+1))
					}
				}
				issued++
				res, err := doCall(ctx, route, a, l.key, prompt, c.callTimeout)
				res.Trial, res.Call, res.Kind = trial, call, kind
				results = append(results, res)
				if encErr := enc.Encode(res); encErr != nil {
					return encErr
				}
				fmt.Fprintf(os.Stderr, "trial=%d call=%d kind=%-6s arm=%-48s status=%d input=%6d cached=%6d ratio=%.2f ttft=%5dms %s\n",
					trial, call, kind, a, res.Status, res.InputTokens, res.CachedTokens, res.CachedRatio, res.TTFTMillis, res.Error)
				if err != nil {
					failures++
					if errors.Is(err, errFatalResponse) {
						printSummary(os.Stderr, results)
						return fmt.Errorf("stopping: %w", err)
					}
				}
				time.Sleep(c.gap)
			}
		}
	}
	printSummary(os.Stderr, results)
	if failures > 0 {
		return fmt.Errorf("%d of %d calls failed", failures, issued)
	}
	return nil
}

// rotatedArms shifts the arm order by round so every arm takes each position.
func rotatedArms(arms []arm, round int) []arm {
	out := make([]arm, len(arms))
	for i := range arms {
		out[i] = arms[(i+round)%len(arms)]
	}
	return out
}

func parseArms(s string) ([]arm, error) {
	var arms []arm
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		found := false
		for _, a := range allArms {
			if string(a) == part {
				arms = append(arms, a)
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown arm %q (want one of %v)", part, allArms)
		}
	}
	return arms, nil
}

const (
	codexEndpoint = "https://chatgpt.com/backend-api/codex/responses"
	apiEndpoint   = "https://api.openai.com/v1/responses"
)

func resolveRoute(ctx context.Context, name string) (probeRoute, error) {
	manager := secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
		&secret_manager.EnvSecretManager{},
		&secret_manager.KeyringSecretManager{},
		&secret_manager.LocalConfigSecretManager{},
	})
	return resolveRouteWith(ctx, name, manager, openai_oauth.GetAndMaybeRefresh)
}

type secretGetter = interface{ GetSecret(string) (string, error) }

type oauthLookup func(context.Context, secretGetter) (*openai_oauth.Credentials, bool, error)

// resolveRouteWith picks the endpoint and credential for a named route. The
// API route needs only OPENAI_API_KEY and must never touch OAuth, so that a
// metered-API probe cannot silently fall back to the subscription endpoint.
func resolveRouteWith(ctx context.Context, name string, manager secretGetter, oauth oauthLookup) (probeRoute, error) {
	switch name {
	case "api":
		apiKey, err := manager.GetSecret("OPENAI_API_KEY")
		if err != nil || apiKey == "" {
			return probeRoute{}, fmt.Errorf("resolve OPENAI_API_KEY: %v", err)
		}
		return probeRoute{endpoint: apiEndpoint, token: apiKey}, nil
	case "codex":
	default:
		return probeRoute{}, fmt.Errorf("unknown -route %q (want codex or api)", name)
	}
	credentials, found, err := oauth(ctx, manager)
	if err != nil {
		return probeRoute{}, fmt.Errorf("resolve OpenAI OAuth credentials: %w", err)
	}
	if !found {
		return probeRoute{}, fmt.Errorf("no %s credentials configured", openai_oauth.SecretName)
	}
	return probeRoute{
		endpoint:  codexEndpoint,
		token:     credentials.AccessToken,
		accountID: credentials.AccountID,
	}, nil
}

func newKey() string {
	var b [16]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

var noRedirectClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}}

func doCall(ctx context.Context, route probeRoute, a arm, key string, prompt probePrompt, timeout time.Duration) (callResult, error) {
	res := callResult{Arm: a, Key: key}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	fail := func(err error) (callResult, error) {
		res.Error = err.Error()
		res.TotalMillis = time.Since(start).Milliseconds()
		return res, err
	}
	req, err := buildRequest(ctx, route, a, key, prompt)
	if err != nil {
		return fail(err)
	}
	resp, err := noRedirectClient.Do(req)
	if err != nil {
		return fail(err)
	}
	defer resp.Body.Close()
	res.Status = resp.StatusCode
	res.RequestID = resp.Header.Get("x-request-id")
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		err := fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusPaymentRequired:
			err = fmt.Errorf("%w: %v", errFatalResponse, err)
		}
		return fail(err)
	}
	outcome, err := parseResponsesStream(resp.Body, start)
	res.TotalMillis = time.Since(start).Milliseconds()
	res.Model = outcome.Model
	res.RawUsage = outcome.RawUsage
	res.TTFTMillis = outcome.FirstOutputAfter.Milliseconds()
	if err != nil {
		res.Error = err.Error()
		return res, err
	}
	res.InputTokens = outcome.InputTokens
	res.CachedTokens = outcome.CachedTokens
	res.CachedRatio = float64(res.CachedTokens) / float64(res.InputTokens)
	return res, nil
}
