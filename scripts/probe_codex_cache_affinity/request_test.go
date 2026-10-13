package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildRequest_ArmHeadersAndBodyKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		arm         arm
		wantHeaders map[string]string
		wantBodyKey bool
	}{
		{armNoKey, map[string]string{"session-id": "", "thread-id": "", "x-client-request-id": ""}, false},
		{armBodyKey, map[string]string{"session-id": "", "thread-id": "", "x-client-request-id": ""}, true},
		{armSessionHeaderOnly, map[string]string{"session-id": "key-1", "thread-id": "", "x-client-request-id": ""}, false},
		{armSessionHeader, map[string]string{"session-id": "key-1", "thread-id": "", "x-client-request-id": ""}, true},
		{armAllHeaders, map[string]string{"session-id": "key-1", "thread-id": "key-1", "x-client-request-id": "key-1"}, true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(string(tt.arm), func(t *testing.T) {
			t.Parallel()
			var got *http.Request
			var gotBody map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Clone(r.Context())
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &gotBody)
				w.WriteHeader(http.StatusTeapot)
			}))
			defer server.Close()

			route := probeRoute{endpoint: server.URL, token: "tok", accountID: "acct"}
			req, err := buildRequest(t.Context(), route, tt.arm, "key-1", probePrompt{model: "m", messages: []string{"hello"}})
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()

			for name, want := range tt.wantHeaders {
				if have := got.Header.Get(name); have != want {
					t.Errorf("header %s = %q, want %q", name, have, want)
				}
			}
			if got.Header.Get("ChatGPT-Account-Id") != "acct" {
				t.Errorf("missing account header")
			}
			if got.Header.Get("Authorization") != "Bearer tok" {
				t.Errorf("missing bearer token")
			}
			key, hasKey := gotBody["prompt_cache_key"]
			if hasKey != tt.wantBodyKey {
				t.Fatalf("body has prompt_cache_key=%v, want %v", hasKey, tt.wantBodyKey)
			}
			if hasKey && key != "key-1" {
				t.Errorf("prompt_cache_key = %v, want key-1", key)
			}
			if gotBody["store"] != false || gotBody["stream"] != true {
				t.Errorf("expected store=false stream=true, got store=%v stream=%v", gotBody["store"], gotBody["stream"])
			}
		})
	}
}

func TestFillerText_DeterministicAndDistinctPerNonce(t *testing.T) {
	t.Parallel()
	a1 := fillerText("nonce-a", 500)
	a2 := fillerText("nonce-a", 500)
	b := fillerText("nonce-b", 500)
	if a1 != a2 {
		t.Fatal("filler must be deterministic for the same nonce")
	}
	if a1 == b {
		t.Fatal("filler must differ across nonces")
	}
	if len(a1) < 500*4 {
		t.Fatalf("filler too short: %d chars for 500 words", len(a1))
	}
}
