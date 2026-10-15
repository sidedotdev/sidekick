package fflag

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"
	ffclient "github.com/thomaspoignant/go-feature-flag"
	"github.com/thomaspoignant/go-feature-flag/retriever/httpretriever"
)

func TestCachedRetriever(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		cacheState string
		want       string
		wantCache  string
		warnings   []string
	}{
		{
			name: "HTTP success refreshes cache", status: http.StatusOK,
			body: "fresh flags", cacheState: "cached", want: "fresh flags",
			wantCache: "fresh flags",
		},
		{
			name: "HTTP success creates cache directory", status: http.StatusOK,
			body: "fresh flags", cacheState: "missing directory", want: "fresh flags",
			wantCache: "fresh flags",
		},
		{
			name: "YAML validation is not performed by retriever", status: http.StatusOK,
			body: "invalid: [yaml", cacheState: "cached", want: "invalid: [yaml",
			wantCache: "invalid: [yaml",
		},
		{
			name: "HTTP failure uses cache", status: http.StatusServiceUnavailable,
			cacheState: "cached", want: "cached flags", wantCache: "cached flags",
			warnings: []string{"http"},
		},
		{
			name: "missing cache uses defaults", status: http.StatusServiceUnavailable,
			cacheState: "missing", want: string(defaultFlags),
			warnings: []string{"http", "cache-read"},
		},
		{
			name: "unreadable cache uses defaults", status: http.StatusServiceUnavailable,
			cacheState: "directory", want: string(defaultFlags),
			warnings: []string{"http", "cache-read"},
		},
		{
			name: "cache directory failure uses defaults", status: http.StatusServiceUnavailable,
			cacheState: "blocked directory", want: string(defaultFlags),
			warnings: []string{"http", "cache-directory"},
		},
		{
			name: "cache directory failure preserves HTTP success", status: http.StatusOK,
			body: "fresh flags", cacheState: "blocked directory", want: "fresh flags",
			warnings: []string{"cache-directory"},
		},
		{
			name: "cache write failure preserves HTTP success", status: http.StatusOK,
			body: "fresh flags", cacheState: "directory", want: "fresh flags",
			warnings: []string{"cache-write"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cacheDir := filepath.Join(t.TempDir(), "cache")
			cachePath := filepath.Join(cacheDir, "flags.yml")
			switch tt.cacheState {
			case "blocked directory":
				require.NoError(t, os.WriteFile(cacheDir, []byte("not a directory"), 0600))
			case "missing directory":
			default:
				require.NoError(t, os.MkdirAll(cacheDir, 0755))
			}
			switch tt.cacheState {
			case "cached":
				require.NoError(t, os.WriteFile(cachePath, []byte("cached flags"), 0600))
			case "directory":
				require.NoError(t, os.Mkdir(cachePath, 0755))
			}
			t.Setenv("SIDE_CACHE_HOME", cacheDir)

			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			retriever := &CachedRetriever{Retriever: httpretriever.Retriever{URL: server.URL}}
			var logs bytes.Buffer
			logger := zerolog.New(&logs)
			got, err := retriever.Retrieve(logger.WithContext(context.Background()))
			require.NoError(t, err)
			require.Equal(t, tt.want, string(got))
			require.Equal(t, 1, requests, "HTTP must be tried even when a cache exists")
			if tt.wantCache != "" {
				cached, err := os.ReadFile(cachePath)
				require.NoError(t, err)
				require.Equal(t, tt.wantCache, string(cached))
			}

			var warnings []string
			decoder := json.NewDecoder(&logs)
			for decoder.More() {
				var entry map[string]interface{}
				require.NoError(t, decoder.Decode(&entry))
				require.Equal(t, "warn", entry["level"])
				require.NotEmpty(t, entry["error"])
				require.NotEmpty(t, entry["message"])
				operation, ok := entry["operation"].(string)
				require.True(t, ok, "warning must identify the failed operation")
				warnings = append(warnings, operation)
			}
			require.ElementsMatch(t, tt.warnings, warnings)
		})
	}
}

func TestCachedRetrieverTriesHTTPOnEveryCall(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "flags.yml")
	t.Setenv("SIDE_CACHE_HOME", filepath.Dir(cachePath))
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch requests {
		case 1:
			_, _ = w.Write([]byte("first"))
		case 2:
			_, _ = w.Write([]byte("second"))
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	retriever := &CachedRetriever{Retriever: httpretriever.Retriever{URL: server.URL}}
	for _, want := range []string{"first", "second", "second"} {
		got, err := retriever.Retrieve(context.Background())
		require.NoError(t, err)
		require.Equal(t, want, string(got))
		cached, err := os.ReadFile(cachePath)
		require.NoError(t, err)
		require.Equal(t, want, string(cached))
	}
	require.Equal(t, 3, requests)
}

func TestCachedRetrieverEmbeddedDefaults(t *testing.T) {
	canonical, err := os.ReadFile("flags.yml")
	require.NoError(t, err)
	require.NotEmpty(t, canonical)
	require.Equal(t, canonical, defaultFlags)

	t.Setenv("SIDE_CACHE_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	retriever := &CachedRetriever{Retriever: httpretriever.Retriever{URL: server.URL}}
	got, err := retriever.Retrieve(context.Background())
	require.NoError(t, err)
	require.Equal(t, canonical, got)

	client, err := ffclient.New(ffclient.Config{Retriever: retriever})
	require.NoError(t, err, "embedded defaults must initialize the feature-flag library")
	t.Cleanup(client.Close)
}

func TestCachedRetrieverLogsWithoutContextLogger(t *testing.T) {
	t.Setenv("SIDE_CACHE_HOME", t.TempDir())
	var logs bytes.Buffer
	originalLogger := log.Logger
	log.Logger = zerolog.New(&logs)
	t.Cleanup(func() { log.Logger = originalLogger })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	retriever := &CachedRetriever{Retriever: httpretriever.Retriever{URL: server.URL}}
	_, _ = retriever.Retrieve(context.Background())

	decoder := json.NewDecoder(&logs)
	var operations []string
	for decoder.More() {
		var entry map[string]interface{}
		require.NoError(t, decoder.Decode(&entry))
		require.Equal(t, "warn", entry["level"])
		require.NotEmpty(t, entry["error"])
		operation, ok := entry["operation"].(string)
		require.True(t, ok)
		operations = append(operations, operation)
	}
	require.ElementsMatch(t, []string{"http", "cache-read"}, operations)
}

type flagTestTransport func(*http.Request) (*http.Response, error)

func (f flagTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestNewFFlagRetrieverSelection(t *testing.T) {
	tests := []struct {
		name        string
		env         string
		status      int
		missingFile bool
		wantError   bool
		wantHTTP    bool
		wantCache   bool
	}{
		{name: "production HTTP", env: "production", status: http.StatusOK, wantHTTP: true, wantCache: true},
		{name: "production offline", env: "production", status: http.StatusServiceUnavailable, wantHTTP: true},
		{name: "unset environment offline", status: http.StatusServiceUnavailable, wantHTTP: true},
		{name: "other environment offline", env: "staging", status: http.StatusServiceUnavailable, wantHTTP: true},
		{name: "development uses file", env: "development"},
		{name: "development missing file fails", env: "development", missingFile: true, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SIDE_APP_ENV", tt.env)
			cacheDir := t.TempDir()
			t.Setenv("SIDE_CACHE_HOME", cacheDir)
			filePath := filepath.Join(t.TempDir(), "local-flags.yml")
			if !tt.missingFile {
				require.NoError(t, os.WriteFile(filePath, defaultFlags, 0600))
			}

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write(defaultFlags)
			}))
			defer server.Close()
			originalTransport := http.DefaultTransport
			requests := 0
			http.DefaultTransport = flagTestTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				require.Equal(t, "https://genflow.dev/sidekick/flags.yml", r.URL.String())
				redirected := r.Clone(r.Context())
				redirected.URL.Scheme = "http"
				redirected.URL.Host = server.Listener.Addr().String()
				return originalTransport.RoundTrip(redirected)
			})
			t.Cleanup(func() { http.DefaultTransport = originalTransport })

			flags, err := NewFFlag(filePath)
			if tt.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				flags.Client.Close()
			}
			if tt.wantHTTP {
				require.Equal(t, 1, requests)
			} else {
				require.Zero(t, requests)
			}
			cached, err := os.ReadFile(filepath.Join(cacheDir, "flags.yml"))
			if tt.wantCache {
				require.NoError(t, err)
				require.Equal(t, defaultFlags, cached)
			} else {
				require.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}
