package llm2

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithResponseDumpDir(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "secret-cookie"})
		w.Header().Set("X-Request-Id", "req_123")
		fmt.Fprintf(w, "data: {\"n\":%s}\n\n", r.URL.Query().Get("n"))
	}))
	t.Cleanup(server.Close)

	t.Run("disabled without dir", func(t *testing.T) {
		t.Parallel()
		client := &http.Client{}
		assert.Same(t, client, withResponseDumpDir(client, ""))
		assert.Nil(t, client.Transport)
	})

	t.Run("dumps each response to its own file", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "dumps")
		client := withResponseDumpDir(&http.Client{}, dir)

		for _, n := range []string{"1", "2"} {
			req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/responses?n="+n, nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer secret-token")
			resp, err := client.Do(req)
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Equal(t, "data: {\"n\":"+n+"}\n\n", string(body))
		}

		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Len(t, entries, 2)

		var dumps []string
		for _, entry := range entries {
			content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			require.NoError(t, err)
			dump := string(content)
			dumps = append(dumps, dump)
			assert.Contains(t, dump, "POST "+server.URL+"/v1/responses")
			assert.Contains(t, dump, "200 OK")
			assert.Contains(t, dump, "X-Request-Id: req_123")
			assert.NotContains(t, dump, "secret-token")
			assert.NotContains(t, dump, "secret-cookie")
		}
		assert.ElementsMatch(t, []bool{true, true}, []bool{
			containsAny(dumps, "\n\ndata: {\"n\":1}\n\n"),
			containsAny(dumps, "\n\ndata: {\"n\":2}\n\n"),
		})
	})
}

func containsAny(haystacks []string, needle string) bool {
	return slices.ContainsFunc(haystacks, func(haystack string) bool {
		return strings.Contains(haystack, needle)
	})
}
