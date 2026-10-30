package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type publishTransport func(*http.Request) (*http.Response, error)

func (f publishTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestPublish(t *testing.T) {
	// These cases replace the default HTTP transport and must run serially.
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })

	for _, tc := range []struct {
		name     string
		status   int
		response string
		override bool
		wantID   string
		wantErr  bool
	}{
		{name: "static defaults", status: 200, response: `{"sandbox_id":"Abc-123"}`, wantID: "Abc-123"},
		{name: "custom metadata", status: 201, response: `{"sandbox_id":"custom"}`, override: true, wantID: "custom"},
		{name: "API failure", status: 400, response: `{"error":"bad request"}`, wantErr: true},
		{name: "malformed JSON", status: 200, response: `{`, wantErr: true},
		{name: "missing ID", status: 200, response: `{}`, wantErr: true},
		{name: "invalid ID", status: 200, response: `{"sandbox_id":"bad/path"}`, wantErr: true},
		{name: "wrong ID type", status: 200, response: `{"sandbox_id":123}`, wantErr: true},
		{name: "oversized response", status: 200, response: strings.Repeat(" ", 1<<20) + `{"sandbox_id":"late"}`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{
				"page.html": "<html>Hello</html>",
				"extra.css": "body { color: red; }",
			}
			paths := []string{filepath.Join(dir, "page.html"), filepath.Join(dir, "extra.css")}
			metadata := `{"name":"static-preview","main":"index.html"}`
			if tc.override {
				metadata = `{"name":"custom","main":"index.html"}`
				files["package.json"] = metadata
				paths = append(paths, filepath.Join(dir, "package.json"))
			}
			for name, content := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			http.DefaultTransport = publishTransport(func(r *http.Request) (*http.Response, error) {
				called = true
				defer r.Body.Close()
				if r.Method != http.MethodPost || r.URL.String() != defineURL || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("unexpected request: %s %s, headers %v", r.Method, r.URL, r.Header)
				}
				var payload defineRequest
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if payload.Template != "static" || len(payload.Files) != 4 {
					t.Errorf("unexpected payload: %+v", payload)
				}
				for name, want := range map[string]string{
					"index.html":          files["page.html"],
					"extra.css":           files["extra.css"],
					"package.json":        metadata,
					"sandbox.config.json": `{"template":"static"}`,
				} {
					if got := payload.Files[name].Content; got != want {
						t.Errorf("%s content = %q, want %q", name, got, want)
					}
				}
				return &http.Response{
					StatusCode: tc.status,
					Status:     http.StatusText(tc.status),
					Body:       io.NopCloser(strings.NewReader(tc.response)),
					Header:     make(http.Header),
				}, nil
			})
			id, err := publish(paths)
			if !called {
				t.Error("publish did not send a request")
			}
			if (err != nil) != tc.wantErr || id != tc.wantID {
				t.Errorf("publish() = (%q, %v), want ID %q, error %v", id, err, tc.wantID, tc.wantErr)
			}
		})
	}
}
