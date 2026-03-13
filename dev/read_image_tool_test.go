package dev

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sidekick/env"
	"sidekick/llm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockKVStorage struct {
	data map[string][]byte
}

func newMockKVStorage() *mockKVStorage {
	return &mockKVStorage{data: make(map[string][]byte)}
}

func (m *mockKVStorage) MGet(_ context.Context, _ string, keys []string) ([][]byte, error) {
	results := make([][]byte, len(keys))
	for i, k := range keys {
		results[i] = m.data[k]
	}
	return results, nil
}

func (m *mockKVStorage) MSet(_ context.Context, _ string, values map[string]interface{}) error {
	for k, v := range values {
		if b, ok := v.([]byte); ok {
			m.data[k] = b
		}
	}
	return nil
}

func (m *mockKVStorage) MSetRaw(_ context.Context, _ string, values map[string][]byte) error {
	for k, v := range values {
		m.data[k] = v
	}
	return nil
}

func (m *mockKVStorage) DeletePrefix(_ context.Context, _ string, _ string) error {
	return nil
}

func (m *mockKVStorage) GetKeysWithPrefix(_ context.Context, _ string, _ string) ([]string, error) {
	return nil, nil
}

func createTestImage(t *testing.T, dir, filename string) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for x := 0; x < 10; x++ {
		for y := 0; y < 10; y++ {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	path := filepath.Join(dir, filename)
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, png.Encode(f, img))
	return path
}

func TestValidateImagePath(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()

	tests := []struct {
		name      string
		filePath  string
		wantErr   bool
		errSubstr string
	}{
		{
			name:      "empty path",
			filePath:  "",
			wantErr:   true,
			errSubstr: "empty",
		},
		{
			name:      "absolute path",
			filePath:  "/etc/passwd",
			wantErr:   true,
			errSubstr: "absolute",
		},
		{
			name:      "dotdot traversal",
			filePath:  "../secret.txt",
			wantErr:   true,
			errSubstr: "'..'",
		},
		{
			name:      "nested dotdot traversal",
			filePath:  "subdir/../../secret.txt",
			wantErr:   true,
			errSubstr: "'..'",
		},
		{
			name:      "dotdot that resolves under workdir",
			filePath:  "a/../b.png",
			wantErr:   true,
			errSubstr: "'..'",
		},
		{
			name:     "valid relative path",
			filePath: "image.png",
			wantErr:  false,
		},
		{
			name:     "valid nested path",
			filePath: "subdir/image.png",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resolved, err := validateImagePath(workDir, tt.filePath)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			} else {
				require.NoError(t, err)
				assert.True(t, strings.HasPrefix(resolved, workDir))
			}
		})
	}
}

func TestReadImageActivity_Success(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	createTestImage(t, workDir, "test.png")

	storage := newMockKVStorage()
	activities := &ReadImageActivities{Storage: storage}

	tc := &llm.ToolCall{Id: "call-abc", Name: "read_image"}
	input := ReadImageInput{
		EnvContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: workDir}},
		FilePath:     "test.png",
		FlowId:       "flow-123",
		ToolCall:     tc,
		WorkspaceId:  "ws-456",
	}

	output, err := activities.ReadImageActivity(context.Background(), input)
	require.NoError(t, err)
	require.NotNil(t, output)

	ref := output.Ref
	assert.Equal(t, "user", ref.Role)
	require.Len(t, ref.BlockKeys, 1)
	assert.True(t, strings.HasPrefix(ref.BlockKeys[0], "block_"))

	// Verify content block was stored under the standard msg namespace
	key := "flow-123:msg:" + ref.BlockKeys[0]
	stored := storage.data[key]
	require.NotNil(t, stored, "content block should be stored under {flowId}:msg:{blockId}")

	// Verify the stored block is a valid tool result with image content
	var block map[string]interface{}
	require.NoError(t, json.Unmarshal(stored, &block))
	assert.Equal(t, "tool_result", block["type"])

	toolResult, ok := block["toolResult"].(map[string]interface{})
	require.True(t, ok, "stored block should have a toolResult field")
	assert.Equal(t, "call-abc", toolResult["toolCallId"])
	assert.Equal(t, "read_image", toolResult["name"])

	content, ok := toolResult["content"].([]interface{})
	require.True(t, ok, "toolResult should have content array")
	require.Len(t, content, 1)

	imgBlock, ok := content[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "image", imgBlock["type"])

	imgRef, ok := imgBlock["image"].(map[string]interface{})
	require.True(t, ok, "image block should have image ref")
	url, ok := imgRef["url"].(string)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(url, "data:image/png;base64,"), "image URL should be a data URL")
}

func TestReadImageActivity_PathTraversal(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()

	storage := newMockKVStorage()
	activities := &ReadImageActivities{Storage: storage}

	input := ReadImageInput{
		EnvContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: workDir}},
		FilePath:     "../escape.png",
		FlowId:       "flow-123",
		WorkspaceId:  "ws-456",
	}

	_, err := activities.ReadImageActivity(context.Background(), input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'..'")
}

func TestReadImageActivity_BothURLAndFilePath(t *testing.T) {
	t.Parallel()

	storage := newMockKVStorage()
	activities := &ReadImageActivities{Storage: storage}

	input := ReadImageInput{
		EnvContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: t.TempDir()}},
		FilePath:     "test.png",
		URL:          "http://example.com/image.png",
		FlowId:       "flow-123",
		WorkspaceId:  "ws-456",
	}

	_, err := activities.ReadImageActivity(context.Background(), input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")
}

func TestReadImageActivity_NeitherURLNorFilePath(t *testing.T) {
	t.Parallel()

	storage := newMockKVStorage()
	activities := &ReadImageActivities{Storage: storage}

	input := ReadImageInput{
		EnvContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: t.TempDir()}},
		FlowId:       "flow-123",
		WorkspaceId:  "ws-456",
	}

	_, err := activities.ReadImageActivity(context.Background(), input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provide either")
}

func TestReadImageActivity_AbsolutePath(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()

	storage := newMockKVStorage()
	activities := &ReadImageActivities{Storage: storage}

	input := ReadImageInput{
		EnvContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: workDir}},
		FilePath:     "/etc/passwd",
		FlowId:       "flow-123",
		WorkspaceId:  "ws-456",
	}

	_, err := activities.ReadImageActivity(context.Background(), input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute")
}

func TestReadImageActivity_NotAnImage(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	textFile := filepath.Join(workDir, "not_image.txt")
	require.NoError(t, os.WriteFile(textFile, []byte("this is not an image"), 0644))

	storage := newMockKVStorage()
	activities := &ReadImageActivities{Storage: storage}

	input := ReadImageInput{
		EnvContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: workDir}},
		FilePath:     "not_image.txt",
		FlowId:       "flow-123",
		WorkspaceId:  "ws-456",
	}

	_, err := activities.ReadImageActivity(context.Background(), input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an image")
}

func TestReadImageActivity_FileNotFound(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()

	storage := newMockKVStorage()
	activities := &ReadImageActivities{Storage: storage}

	input := ReadImageInput{
		EnvContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: workDir}},
		FilePath:     "nonexistent.png",
		FlowId:       "flow-123",
		WorkspaceId:  "ws-456",
	}

	_, err := activities.ReadImageActivity(context.Background(), input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read image file")
}

func TestReadImageActivity_URL_Success(t *testing.T) {
	t.Parallel()

	// Create a test image and serve it via httptest
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for x := 0; x < 10; x++ {
		for y := 0; y < 10; y++ {
			img.Set(x, y, color.RGBA{R: 0, G: 255, B: 0, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	pngBytes := buf.Bytes()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngBytes)
	}))
	defer srv.Close()

	storage := newMockKVStorage()
	activities := &ReadImageActivities{Storage: storage}

	tc := &llm.ToolCall{Id: "call-url", Name: "read_image"}
	input := ReadImageInput{
		EnvContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: t.TempDir()}},
		URL:          srv.URL + "/test.png",
		FlowId:       "flow-url",
		ToolCall:     tc,
		WorkspaceId:  "ws-url",
	}

	output, err := activities.ReadImageActivity(context.Background(), input)
	require.NoError(t, err)
	require.NotNil(t, output)

	ref := output.Ref
	assert.Equal(t, "user", ref.Role)
	require.Len(t, ref.BlockKeys, 1)

	key := "flow-url:msg:" + ref.BlockKeys[0]
	stored := storage.data[key]
	require.NotNil(t, stored)

	var block map[string]interface{}
	require.NoError(t, json.Unmarshal(stored, &block))
	assert.Equal(t, "tool_result", block["type"])
}

func TestReadImageActivity_URL_NotAnImage(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("this is not an image"))
	}))
	defer srv.Close()

	storage := newMockKVStorage()
	activities := &ReadImageActivities{Storage: storage}

	input := ReadImageInput{
		EnvContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: t.TempDir()}},
		URL:          srv.URL + "/text.txt",
		FlowId:       "flow-123",
		WorkspaceId:  "ws-456",
	}

	_, err := activities.ReadImageActivity(context.Background(), input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an image")
}

func TestReadImageActivity_URL_InvalidScheme(t *testing.T) {
	t.Parallel()

	storage := newMockKVStorage()
	activities := &ReadImageActivities{Storage: storage}

	input := ReadImageInput{
		EnvContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: t.TempDir()}},
		URL:          "ftp://example.com/image.png",
		FlowId:       "flow-123",
		WorkspaceId:  "ws-456",
	}

	_, err := activities.ReadImageActivity(context.Background(), input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http or https")
}

func TestReadImageActivity_URL_HTTPError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	storage := newMockKVStorage()
	activities := &ReadImageActivities{Storage: storage}

	input := ReadImageInput{
		EnvContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: t.TempDir()}},
		URL:          srv.URL + "/missing.png",
		FlowId:       "flow-123",
		WorkspaceId:  "ws-456",
	}

	_, err := activities.ReadImageActivity(context.Background(), input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 404")
}

func TestFetchImageFromURL_EmptyURL(t *testing.T) {
	t.Parallel()

	_, _, err := fetchImageFromURL("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}
