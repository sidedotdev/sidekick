package dev

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sidekick/common"
	"sidekick/env"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/persisted_ai"

	"github.com/invopop/jsonschema"
)

const (
	readImageMaxBytes    = 20 * 1024 * 1024 // 20MB
	readImageMaxLongEdge = 1568
)

type ReadImageParams struct {
	FilePath string `json:"file_path,omitempty" jsonschema:"description=The path to the image file relative to the current working directory. Absolute paths and '..' traversal are not allowed. Provide either file_path or url but not both."`
	URL      string `json:"url,omitempty" jsonschema:"description=A URL pointing to an image to fetch and display. Provide either file_path or url but not both."`
}

var readImageTool = llm.Tool{
	Name:        "read_image",
	Description: "Reads an image file and adds it to the conversation history so the model can see it. The file path must be relative to the current working directory; absolute paths and '..' segments are disallowed. Alternatively, a URL can be provided to fetch a remote image.",
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
	EnvContainer env.EnvContainer `json:"envContainer"`
	FilePath     string           `json:"filePath"`
	URL          string           `json:"url,omitempty"`
	FlowId       string           `json:"flowId"`
	ToolCall     *llm.ToolCall    `json:"toolCall,omitempty"`
	WorkspaceId  string           `json:"workspaceId"`
}

// ReadImageOutput contains the persisted message ref for the image content block.
type ReadImageOutput struct {
	Ref persisted_ai.MessageRef `json:"ref"`
}

// ReadImageActivities holds dependencies for image-reading activities.
type ReadImageActivities struct {
	Storage common.KeyValueStorage
}

// ReadImageActivity reads an image from a file or URL, clamps it for LLM limits,
// builds a tool result content block, and persists it to KV using the standard
// {flowId}:msg:{blockId} namespace so cascade delete handles cleanup.
func (a *ReadImageActivities) ReadImageActivity(ctx context.Context, input ReadImageInput) (*ReadImageOutput, error) {
	var raw []byte
	var mimeType string
	var err error

	if input.URL != "" {
		raw, mimeType, err = fetchImageFromURL(input.URL)
		if err != nil {
			return nil, err
		}
	} else {
		workDir := input.EnvContainer.Env.GetWorkingDirectory()
		resolvedPath, pathErr := validateImagePath(workDir, input.FilePath)
		if pathErr != nil {
			return nil, pathErr
		}

		raw, err = os.ReadFile(resolvedPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read image file: %w", err)
		}

		mimeType = http.DetectContentType(raw)
		if !strings.HasPrefix(mimeType, "image/") {
			return nil, fmt.Errorf("file is not an image: detected content type %q", mimeType)
		}
	}

	dataURL := llm2.BuildDataURL(mimeType, raw)

	clampedURL, _, _, err := llm2.PrepareImageDataURLForLimits(dataURL, readImageMaxBytes, readImageMaxLongEdge)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare image for LLM limits: %w", err)
	}

	imageBlock := llm2.ContentBlock{Type: llm2.ContentBlockTypeImage, Image: &llm2.ImageRef{Url: clampedURL}}
	finalBlock := imageBlock

	if input.ToolCall != nil {
		// Build a tool result content block wrapping the image
		toolResultBlock := llm2.ToolResultBlock{
			Name:    readImageTool.Name,
			Content: []llm2.ContentBlock{imageBlock},
		}
		toolResultBlock.ToolCallId = input.ToolCall.Id
		toolResultBlock.Name = input.ToolCall.Name
		finalBlock = llm2.ContentBlock{
			Type:       llm2.ContentBlockTypeToolResult,
			ToolResult: &toolResultBlock,
		}
	}

	ref, err := persisted_ai.PersistContentBlock(
		ctx, a.Storage, input.FlowId, input.WorkspaceId, string(llm2.RoleUser), finalBlock,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to persist content block: %w", err)
	}

	return &ReadImageOutput{Ref: *ref}, nil
}

const (
	fetchImageTimeout  = 30 * time.Second
	fetchImageMaxBytes = 20 * 1024 * 1024 // 20MB
)

func fetchImageFromURL(imageURL string) ([]byte, string, error) {
	if imageURL == "" {
		return nil, "", fmt.Errorf("image URL is empty")
	}
	if !strings.HasPrefix(imageURL, "http://") && !strings.HasPrefix(imageURL, "https://") {
		return nil, "", fmt.Errorf("image URL must use http or https scheme: %s", imageURL)
	}

	client := &http.Client{Timeout: fetchImageTimeout}
	resp, err := client.Get(imageURL) //nolint:gosec
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch image from URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("failed to fetch image from URL: HTTP %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, fetchImageMaxBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("failed to read image response body: %w", err)
	}
	if len(raw) > fetchImageMaxBytes {
		return nil, "", fmt.Errorf("image from URL exceeds maximum size of %d bytes", fetchImageMaxBytes)
	}

	mimeType := http.DetectContentType(raw)
	if !strings.HasPrefix(mimeType, "image/") {
		return nil, "", fmt.Errorf("URL content is not an image: detected content type %q", mimeType)
	}

	return raw, mimeType, nil
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
