package persisted_ai

import (
	"context"
	"fmt"
	"path/filepath"
	"sidekick/coding/tree_sitter"
	"sidekick/common"
	"sidekick/embedding"
	"sidekick/env"
	"sidekick/llm"
	"sidekick/secret_manager"
	"sidekick/srv"
	"sidekick/utils"
	"strings"

	"github.com/kelindar/binary"
)

type RagActivities struct {
	DatabaseAccessor srv.Service
}

type RankedDirSignatureOutlineOptions struct {
	RankedViaEmbeddingOptions
	CharLimit int
}

type RankedViaEmbeddingOptions struct {
	WorkspaceId        string
	EnvContainer       env.EnvContainer
	RankQuery          string
	Secrets            secret_manager.SecretManagerContainer
	ModelConfig        common.ModelConfig
	AvailableProviders []common.ModelProviderPublicConfig
}

func (options RankedDirSignatureOutlineOptions) ActionParams() map[string]any {
	return map[string]interface{}{
		"rankQuery": options.RankQuery,
		"charLimit": options.CharLimit,
		"provider":  options.ModelConfig.Provider,
		"model":     options.ModelConfig.Model,
	}
}

// TODO add param for context.Context
func (ra *RagActivities) RankedDirSignatureOutline(options RankedDirSignatureOutlineOptions) (string, error) {
	// FIXME put tree sitter activities inside rag activities struct
	t := tree_sitter.TreeSitterActivities{DatabaseAccessor: ra.DatabaseAccessor}
	fileSignatureSubkeys, err := t.CreateDirSignatureOutlines(options.WorkspaceId, options.EnvContainer.Env.GetWorkingDirectory())
	if err != nil {
		return "", err
	}

	rankedFileSignatureSubkeys, err := ra.RankedSubkeys(RankedSubkeysOptions{
		RankedViaEmbeddingOptions: options.RankedViaEmbeddingOptions, // This includes AvailableProviders
		ContentType:               tree_sitter.ContentTypeFileSignature,
		Subkeys:                   fileSignatureSubkeys,
	})
	if err != nil {
		return "", err
	}

	rankedDirChunkSubkeys, err := ra.RankedDirChunkSubkeys(RankedDirChunkSubkeysOptions{
		RankedViaEmbeddingOptions: options.RankedViaEmbeddingOptions, // This includes AvailableProviders
	})
	if err != nil {
		return "", err
	}

	return ra.LimitedDirSignatureOutline(DirSignatureOutlineOptions{
		WorkspaceId:          options.WorkspaceId,
		FileSignatureSubkeys: rankedFileSignatureSubkeys,
		DirChunkSubkeys:      rankedDirChunkSubkeys,
		BasePath:             options.EnvContainer.Env.GetWorkingDirectory(),
		CharLimit:            options.CharLimit,
	})
}

type RankedSubkeysOptions struct {
	RankedViaEmbeddingOptions
	ContentType string
	Subkeys     []string
}

func (ra *RagActivities) RankedSubkeys(options RankedSubkeysOptions) ([]string, error) {
	ea := EmbedActivities{Storage: ra.DatabaseAccessor}
	err := ea.CachedEmbedActivity(context.Background(), CachedEmbedActivityOptions{
		Secrets:            options.Secrets,
		WorkspaceId:        options.WorkspaceId,
		ModelConfig:        options.ModelConfig,
		ContentType:        options.ContentType,
		Subkeys:            options.Subkeys,
		AvailableProviders: options.AvailableProviders,
	})
	if err != nil {
		return []string{}, err
	}

	va := VectorActivities{DatabaseAccessor: ra.DatabaseAccessor}

	embedder, err := getEmbedder(options.ModelConfig, options.AvailableProviders)
	if err != nil {
		return []string{}, err
	}
	// TODO /gen/basic cache the queryVector in memory
	// NOTE: "code_retrieval_query" would be ideal here, but isn't supported by text-embedding-004
	// TODO: dynamically decide task type based on model name
	// TODO: change "task type" to instead be "use_case" and we'll map to task
	// type internally in the embedder implementation
	queryVector, err := embedder.Embed(context.Background(), options.ModelConfig, options.Secrets.SecretManager, []string{options.RankQuery}, embedding.TaskTypeRetrievalQuery)
	if err != nil {
		return []string{}, fmt.Errorf("failed to embed query: %w", err)
	}

	return va.VectorSearch(VectorSearchActivityOptions{
		WorkspaceId: options.WorkspaceId,
		Provider:    options.ModelConfig.Provider,
		Model:       options.ModelConfig.Model,
		ContentType: options.ContentType,
		Subkeys:     options.Subkeys,
		Query:       queryVector[0],
		Limit:       1000,
	})
}

func getEmbedder(modelCfg common.ModelConfig, availableProviders []common.ModelProviderPublicConfig) (embedding.Embedder, error) {
	var selectedProviderConfig *common.ModelProviderPublicConfig
	for i := range availableProviders {
		// Make a copy of the provider to avoid modifying the original slice
		provider := availableProviders[i]
		if provider.Name == modelCfg.Provider {
			selectedProviderConfig = &provider
			break
		}
	}

	if selectedProviderConfig == nil {
		return nil, fmt.Errorf("configuration not found for provider named: %s", modelCfg.Provider)
	}

	// getProviderType is in the persisted_ai package (llm_activities.go)
	providerType, err := getProviderType(selectedProviderConfig.Type)
	if err != nil {
		return nil, fmt.Errorf("failed to determine provider type for %s (type %s): %w", selectedProviderConfig.Name, selectedProviderConfig.Type, err)
	}

	var embedder embedding.Embedder
	switch providerType {
	case llm.OpenaiToolChatProviderType:
		embedder = &embedding.OpenAIEmbedder{}
	case llm.GoogleToolChatProviderType:
		embedder = &embedding.GoogleEmbedder{}
	case llm.OpenaiCompatibleToolChatProviderType:
		embedder = &embedding.OpenAIEmbedder{
			BaseURL: selectedProviderConfig.BaseURL,
			// Note: Default embedding model for openai_compatible is not handled here,
			// it's expected to be part of ModelConfig if needed, or handled by OpenAIEmbedder's logic.
			// FIXME: it doesn't have to be this way though, let's mirror llm.OpenaiToolChat here instead.
		}
	case llm.ToolChatProviderType("mock"): // Assuming "mock" is a valid type string for llm.ToolChatProviderType
		return &embedding.MockEmbedder{}, nil
	// Anthropic does not have an embedder, so it's not listed here.
	default:
		return nil, fmt.Errorf("unsupported provider type %s for embedding with provider %s (model %s)", providerType, modelCfg.Provider, modelCfg.Model)
	}
	return embedder, nil
}

type DirSignatureOutlineOptions struct {
	WorkspaceId          string
	FileSignatureSubkeys []string // these are file signature subkeys
	DirChunkSubkeys      []string
	BasePath             string
	EmbeddingType        string
	CharLimit            int
}

// LimitedDirSignatureOutline returns a string containing the directory structure with signature outlines expanded only for the given subkeys.
func (ra *RagActivities) LimitedDirSignatureOutline(options DirSignatureOutlineOptions) (string, error) {
	var charCount int
	showPaths := make(map[string]bool, 0)
	signaturePaths := make(map[string]int, 0)

	dirChunkKeys := make([]string, len(options.DirChunkSubkeys))
	for i, subkey := range options.DirChunkSubkeys {
		dirChunkKeys[i] = fmt.Sprintf("%s:%s", tree_sitter.ContentTypeDirChunk, subkey)
	}
	dirChunks, err := ra.DatabaseAccessor.MGet(context.Background(), options.WorkspaceId, dirChunkKeys)
	if err != nil {
		return "", err
	}

	// include paths for dir chunks, up to 1/10th of the char limit (approximately)
chunksLoop:
	for i, chunk := range dirChunks {
		if chunk != nil {
			var text string
			err := binary.Unmarshal(chunk, &text)
			if err != nil {
				return "", fmt.Errorf("dirChunk %v for key %s failed to unmarshal: %w", chunk, dirChunkKeys[i], err)
			}

			paths := strings.Split(text, "\n")
			commonPrefix := ""

			if len(paths) > 1 {
				commonPrefix = paths[0]
				for _, path := range paths {
					commonPrefix = longestCommonPrefix(commonPrefix, path)
				}
			}

			charCount += len(commonPrefix)
			for _, path := range paths {
				lengthWithoutPrefix := len(path) - len(commonPrefix)
				if charCount+lengthWithoutPrefix > options.CharLimit/10 {
					break chunksLoop
				}
				showPaths[path] = true
				charCount += lengthWithoutPrefix
			}
		}
	}

	fileSignatureKeys := make([]string, len(options.FileSignatureSubkeys))
	for i, subkey := range options.FileSignatureSubkeys {
		fileSignatureKeys[i] = fmt.Sprintf("%s:%s", tree_sitter.ContentTypeFileSignature, subkey)
	}
	fileSignatures, err := ra.DatabaseAccessor.MGet(context.Background(), options.WorkspaceId, fileSignatureKeys)
	if err != nil {
		return "", err
	}

	// include paths for file signatures
	for i, signature := range fileSignatures {
		if signature != nil {
			var text string
			err := binary.Unmarshal(signature, &text)
			if err != nil {
				return "", fmt.Errorf("dirChunk %v for key %s failed to unmarshal: %w", signature, fileSignatureKeys[i], err)
			}

			lines := strings.Split(text, "\n")
			path := lines[0]
			outline := strings.Join(lines[1:], "\n")
			if charCount+len(path)+len(outline) > options.CharLimit {
				message := "\n[... truncated %d characters]"
				numCharactersAvailable := options.CharLimit - charCount - len(path) - len(message) - 6 // 6 is buffer to handle up to 1m-1 for the message
				if numCharactersAvailable < 10 {
					break
				}

				originalLength := len(outline)
				outline = outline[:numCharactersAvailable]
				outline += fmt.Sprintf(message, originalLength-numCharactersAvailable)
				signaturePaths[path] += len(outline) // NOTE: adding due to file signatures being chunked
				charCount += len(path) + len(outline)
				break
				//fmt.Println("charCount", charCount, "len(path)", len(path), "len(outline)", len(outline), "options.CharLimit", options.CharLimit)
				//fmt.Printf("path: %s, outline:\n%s\n\n", path, outline)
			}
			signaturePaths[path] += len(outline) // NOTE: adding due to file signatures being chunked
			charCount += len(path) + len(outline)
		}
	}

	// include parent paths for dir tree outline to work
	for path := range showPaths {
		for {
			path = filepath.Dir(path)
			if path == "." || path == "/" || path == "" {
				break
			}
			showPaths[path] = true
		}
	}
	for path := range signaturePaths {
		showPaths[path] = true
		for {
			path = filepath.Dir(path)
			if path == "." || path == "/" || path == "" {
				break
			}
			showPaths[path] = true
		}
	}

	outlines, err := tree_sitter.GetDirectorySignatureOutlines(options.BasePath, &showPaths, &signaturePaths)
	if err != nil {
		return "", err
	}

	return tree_sitter.GetFileOutlinesString(outlines)
}

type RankedDirChunkSubkeysOptions struct {
	RankedViaEmbeddingOptions
}

func (ra *RagActivities) RankedDirChunkSubkeys(options RankedDirChunkSubkeysOptions) ([]string, error) {
	basePath := options.EnvContainer.Env.GetWorkingDirectory()
	chunks := GetDirectoryChunks(basePath)

	values := make(map[string]interface{})
	hashes := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		paths := utils.Map(chunk.Paths, func(pathInfo PathInfo) string { return pathInfo.Path })
		value := strings.Join(paths, "\n")
		hash := utils.Hash256(value)
		hashes = append(hashes, hash)
		key := fmt.Sprintf("%s:%s", tree_sitter.ContentTypeDirChunk, hash)
		values[key] = value
	}
	err := ra.DatabaseAccessor.MSet(context.Background(), options.WorkspaceId, values)
	if err != nil {
		return []string{}, fmt.Errorf("error persisting dir chunk content: %w", err)
	}

	dirChunkSubkeys := hashes
	return ra.RankedSubkeys(RankedSubkeysOptions{
		RankedViaEmbeddingOptions: options.RankedViaEmbeddingOptions, // This includes AvailableProviders
		ContentType:               tree_sitter.ContentTypeDirChunk,
		Subkeys:                   dirChunkSubkeys,
	})
}
