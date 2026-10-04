package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"sidekick/common"

	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func compressionTestPayload(t *testing.T) []byte {
	t.Helper()
	items := make([]map[string]any, 100)
	for i := range items {
		items[i] = map[string]any{"id": i, "title": "task title " + strconv.Itoa(i), "status": "in_progress"}
	}
	body, err := json.Marshal(map[string]any{"items": items})
	require.NoError(t, err)
	return body
}

func newCompressionTestRouter(payload []byte) *gin.Engine {
	r := gin.New()
	r.Use(CompressionMiddleware())
	r.GET("/data", func(c *gin.Context) {
		c.Header("Content-Length", strconv.Itoa(len(payload)))
		c.Data(http.StatusOK, "application/json", payload)
	})
	r.HEAD("/data", func(c *gin.Context) {
		c.Header("Content-Length", strconv.Itoa(len(payload)))
		c.Status(http.StatusOK)
	})
	r.GET("/precompressed", func(c *gin.Context) {
		c.Header("Content-Encoding", "br")
		c.Data(http.StatusOK, "application/octet-stream", payload)
	})
	r.GET("/streamed", func(c *gin.Context) {
		c.Status(http.StatusOK)
		c.Writer.Flush()
		_, _ = c.Writer.Write(payload)
	})
	return r
}

func decodeBody(t *testing.T, encoding string, body []byte) []byte {
	t.Helper()
	switch encoding {
	case "":
		return body
	case "gzip":
		reader, err := gzip.NewReader(bytes.NewReader(body))
		require.NoError(t, err)
		decoded, err := io.ReadAll(reader)
		require.NoError(t, err)
		return decoded
	case "zstd":
		decoder, err := zstd.NewReader(nil)
		require.NoError(t, err)
		defer decoder.Close()
		decoded, err := decoder.DecodeAll(body, nil)
		require.NoError(t, err)
		return decoded
	default:
		t.Fatalf("unexpected encoding %q", encoding)
		return nil
	}
}

func TestCompressionMiddleware(t *testing.T) {
	t.Parallel()

	payload := compressionTestPayload(t)
	router := newCompressionTestRouter(payload)

	tests := []struct {
		name             string
		method           string
		headers          map[string]string
		expectedEncoding string
	}{
		{name: "no Accept-Encoding", headers: map[string]string{}},
		{name: "gzip", headers: map[string]string{"Accept-Encoding": "gzip"}, expectedEncoding: "gzip"},
		{name: "zstd", headers: map[string]string{"Accept-Encoding": "zstd"}, expectedEncoding: "zstd"},
		{name: "zstd preferred when both offered", headers: map[string]string{"Accept-Encoding": "gzip, zstd"}, expectedEncoding: "zstd"},
		{name: "zstd preferred over higher weighted gzip", headers: map[string]string{"Accept-Encoding": "zstd;q=0.9, gzip;q=1.0"}, expectedEncoding: "zstd"},
		{name: "zstd disabled by q=0", headers: map[string]string{"Accept-Encoding": "zstd;q=0, gzip"}, expectedEncoding: "gzip"},
		{name: "case and whitespace insensitive", headers: map[string]string{"Accept-Encoding": " GZIP ; Q=0.5 "}, expectedEncoding: "gzip"},
		{name: "wildcard", headers: map[string]string{"Accept-Encoding": "*"}, expectedEncoding: "zstd"},
		{name: "wildcard with zstd excluded", headers: map[string]string{"Accept-Encoding": "zstd;q=0, *"}, expectedEncoding: "gzip"},
		{name: "all disabled", headers: map[string]string{"Accept-Encoding": "zstd;q=0, gzip;q=0"}},
		{name: "identity", headers: map[string]string{"Accept-Encoding": "identity"}},
		{name: "unknown encoding", headers: map[string]string{"Accept-Encoding": "br, deflate"}},
		{name: "malformed q value", headers: map[string]string{"Accept-Encoding": "gzip;q=abc"}},
		{
			name: "websocket upgrade",
			headers: map[string]string{
				"Accept-Encoding": "zstd, gzip",
				"Connection":      "Upgrade",
				"Upgrade":         "websocket",
			},
		},
		{name: "range request", headers: map[string]string{"Accept-Encoding": "zstd, gzip", "Range": "bytes=0-10"}},
		{name: "HEAD request", method: http.MethodHead, headers: map[string]string{"Accept-Encoding": "zstd, gzip"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			method := tt.method
			if method == "" {
				method = http.MethodGet
			}
			req := httptest.NewRequest(method, "/data", nil)
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)

			require.Equal(t, http.StatusOK, rr.Code)
			assert.Equal(t, tt.expectedEncoding, rr.Header().Get("Content-Encoding"))

			if tt.expectedEncoding == "" {
				assert.NotContains(t, rr.Header().Values("Vary"), "Accept-Encoding")
				assert.Equal(t, strconv.Itoa(len(payload)), rr.Header().Get("Content-Length"))
				if method == http.MethodGet {
					assert.Equal(t, payload, rr.Body.Bytes())
				}
				return
			}

			assert.Contains(t, rr.Header().Values("Vary"), "Accept-Encoding")
			assert.Empty(t, rr.Header().Get("Content-Length"))
			assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
			assert.Less(t, rr.Body.Len(), len(payload))
			assert.Equal(t, payload, decodeBody(t, tt.expectedEncoding, rr.Body.Bytes()))
		})
	}
}

func TestCompressionMiddleware_ResponseAlreadyEncoded(t *testing.T) {
	t.Parallel()

	payload := compressionTestPayload(t)
	router := newCompressionTestRouter(payload)

	req := httptest.NewRequest(http.MethodGet, "/precompressed", nil)
	req.Header.Set("Accept-Encoding", "zstd, gzip")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "br", rr.Header().Get("Content-Encoding"))
	assert.Equal(t, payload, rr.Body.Bytes())
}

func TestCompressionMiddleware_FlushBeforeWrite(t *testing.T) {
	t.Parallel()

	payload := compressionTestPayload(t)
	router := newCompressionTestRouter(payload)

	for _, encoding := range []string{"zstd", "gzip"} {
		t.Run(encoding, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/streamed", nil)
			req.Header.Set("Accept-Encoding", encoding)
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)

			require.Equal(t, http.StatusOK, rr.Code)
			assert.True(t, rr.Flushed)
			assert.Equal(t, encoding, rr.Header().Get("Content-Encoding"))
			assert.Equal(t, payload, decodeBody(t, encoding, rr.Body.Bytes()))
		})
	}
}

func TestDefineRoutesCompressesResponses(t *testing.T) {
	t.Parallel()

	ctrl := newLocalConfigController(t, common.LocalConfig{
		Profiles: []common.ProfileConfig{{Id: "work", Name: "Work"}},
	}, nil)
	router := DefineRoutes(ctrl, TestAllowedOrigins())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/profiles", nil)
	req.Header.Set("Accept-Encoding", "zstd")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "zstd", rr.Header().Get("Content-Encoding"))
	assert.True(t, strings.HasPrefix(rr.Header().Get("Content-Type"), "application/json"))

	var response struct {
		Profiles []common.Profile `json:"profiles"`
	}
	require.NoError(t, json.Unmarshal(decodeBody(t, "zstd", rr.Body.Bytes()), &response))
	assert.Contains(t, response.Profiles, common.Profile{Id: "work", Name: "Work"})
}
