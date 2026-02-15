package persisted_ai

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sidekick/env"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	input := ReadImageInput{
		FlowId:      "flow-123",
		WorkspaceId: "ws-456",
		EnvContainer: env.EnvContainer{
			Env: &env.LocalEnv{WorkingDirectory: workDir},
		},
		FilePath: "test.png",
	}

	output, err := activities.ReadImageActivity(context.Background(), input)
	require.NoError(t, err)
	require.NotNil(t, output)
	assert.True(t, strings.HasPrefix(output.Key, "flow-123:img:"))

	stored := storage.data[output.Key]
	require.NotNil(t, stored)
	assert.True(t, strings.HasPrefix(string(stored), "data:image/"))
}

func TestReadImageActivity_PathTraversal(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()

	storage := newMockKVStorage()
	activities := &ReadImageActivities{Storage: storage}

	input := ReadImageInput{
		FlowId:      "flow-123",
		WorkspaceId: "ws-456",
		EnvContainer: env.EnvContainer{
			Env: &env.LocalEnv{WorkingDirectory: workDir},
		},
		FilePath: "../escape.png",
	}

	_, err := activities.ReadImageActivity(context.Background(), input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'..'")
}

func TestReadImageActivity_AbsolutePath(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()

	storage := newMockKVStorage()
	activities := &ReadImageActivities{Storage: storage}

	input := ReadImageInput{
		FlowId:      "flow-123",
		WorkspaceId: "ws-456",
		EnvContainer: env.EnvContainer{
			Env: &env.LocalEnv{WorkingDirectory: workDir},
		},
		FilePath: "/etc/passwd",
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
		FlowId:      "flow-123",
		WorkspaceId: "ws-456",
		EnvContainer: env.EnvContainer{
			Env: &env.LocalEnv{WorkingDirectory: workDir},
		},
		FilePath: "not_image.txt",
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
		FlowId:      "flow-123",
		WorkspaceId: "ws-456",
		EnvContainer: env.EnvContainer{
			Env: &env.LocalEnv{WorkingDirectory: workDir},
		},
		FilePath: "nonexistent.png",
	}

	_, err := activities.ReadImageActivity(context.Background(), input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read image file")
}

func TestBuildKvImageURL(t *testing.T) {
	t.Parallel()
	url := BuildKvImageURL("flow-1:img:abc123")
	assert.Equal(t, "kv:flow-1:img:abc123", url)
}
