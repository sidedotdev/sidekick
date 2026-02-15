package persisted_ai

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"sidekick/common"
	"sidekick/env"
	"sidekick/llm2"

	"github.com/segmentio/ksuid"
)

const (
	readImageMaxBytes    = 20 * 1024 * 1024 // 20MB
	readImageMaxLongEdge = 1568
)

// ReadImageInput is the activity input for reading an image file and storing it in KV.
type ReadImageInput struct {
	FlowId       string           `json:"flowId"`
	WorkspaceId  string           `json:"workspaceId"`
	EnvContainer env.EnvContainer `json:"envContainer"`
	FilePath     string           `json:"filePath"`
}

// ReadImageOutput is the activity output containing the KV key for the stored image.
type ReadImageOutput struct {
	Key string `json:"key"`
}

// ReadImageActivities holds dependencies for image-reading activities.
type ReadImageActivities struct {
	Storage common.KeyValueStorage
}

// ReadImageActivity reads an image file, validates the path, clamps it for LLM limits,
// and stores the resulting data URL in KV storage under a flow-prefixed key.
func (a *ReadImageActivities) ReadImageActivity(ctx context.Context, input ReadImageInput) (*ReadImageOutput, error) {
	workDir := input.EnvContainer.Env.GetWorkingDirectory()

	resolvedPath, err := validateImagePath(workDir, input.FilePath)
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read image file: %w", err)
	}

	mimeType := http.DetectContentType(raw)
	if !strings.HasPrefix(mimeType, "image/") {
		return nil, fmt.Errorf("file is not an image: detected content type %q", mimeType)
	}

	dataURL := llm2.BuildDataURL(mimeType, raw)

	clampedURL, _, _, err := llm2.PrepareImageDataURLForLimits(dataURL, readImageMaxBytes, readImageMaxLongEdge)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare image for LLM limits: %w", err)
	}

	key := fmt.Sprintf("%s:img:%s", input.FlowId, ksuid.New().String())

	err = a.Storage.MSetRaw(ctx, input.WorkspaceId, map[string][]byte{
		key: []byte(clampedURL),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to store image in KV: %w", err)
	}

	return &ReadImageOutput{Key: key}, nil
}

// validateImagePath ensures the file path is safe: no ".." segments, not absolute,
// and resolves within the working directory.
func validateImagePath(workDir, filePath string) (string, error) {
	if filePath == "" {
		return "", fmt.Errorf("file path is empty")
	}

	if filepath.IsAbs(filePath) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", filePath)
	}

	cleaned := filepath.Clean(filePath)
	for _, part := range strings.Split(cleaned, string(filepath.Separator)) {
		if part == ".." {
			return "", fmt.Errorf("path traversal with '..' is not allowed: %s", filePath)
		}
	}

	resolved := filepath.Join(workDir, cleaned)

	// Verify resolved path is under workDir
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve working directory: %w", err)
	}
	absResolved, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("failed to resolve file path: %w", err)
	}

	if !strings.HasPrefix(absResolved, absWorkDir+string(filepath.Separator)) && absResolved != absWorkDir {
		return "", fmt.Errorf("resolved path %s is not under working directory %s", absResolved, absWorkDir)
	}

	return resolved, nil
}

// BuildKvImageURL builds a kv:-prefixed URL reference for storing in chat history.
func BuildKvImageURL(key string) string {
	return llm2.KvImagePrefix + key
}
