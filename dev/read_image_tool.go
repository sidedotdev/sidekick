package dev

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"sidekick/common"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/persisted_ai"

	"github.com/invopop/jsonschema"
	"github.com/segmentio/ksuid"
)

const (
	readImageMaxBytes    = 20 * 1024 * 1024 // 20MB
	readImageMaxLongEdge = 1568
)

type ReadImageParams struct {
	FilePath string `json:"file_path" jsonschema:"description=The path to the image file relative to the current working directory. Absolute paths and '..' traversal are not allowed."`
}

var readImageTool = llm.Tool{
	Name:        "read_image",
	Description: "Reads an image file and adds it to the conversation history so the model can see it. The file path must be relative to the current working directory; absolute paths and '..' segments are disallowed.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&ReadImageParams{}),
}

// supportsImageToolResults returns true if the provider/model combination
// supports multimodal (image) content inside tool results.
func supportsImageToolResults(config common.ModelConfig) bool {
	providerName := strings.ToLower(config.Provider)
	switch providerName {
	case "anthropic":
		return true
	case "openai", "openai_compatible", "openai_responses_compatible":
		return true
	case "google":
		return !strings.Contains(config.Model, "gemini-2") && !strings.Contains(config.Model, "gemini-1")
	default:
		return false
	}
}

// ReadImageInput is the activity input for reading an image file and storing it in KV.
type ReadImageInput struct {
	FlowId      string `json:"flowId"`
	WorkspaceId string `json:"workspaceId"`
	WorkDir     string `json:"workDir"`
	FilePath    string `json:"filePath"`
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
	resolvedPath, err := validateImagePath(input.WorkDir, input.FilePath)
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

	// Reject raw paths containing ".." before cleaning, so that inputs like
	// "a/../b.png" are disallowed even though they resolve safely.
	for _, part := range strings.Split(filePath, "/") {
		if part == ".." {
			return "", fmt.Errorf("path traversal with '..' is not allowed: %s", filePath)
		}
	}

	cleaned := filepath.Clean(filePath)

	resolved := filepath.Join(workDir, cleaned)

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
	return persisted_ai.KvImagePrefix + key
}
