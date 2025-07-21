package persisted_ai

import (
	"context"
	"errors"
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
	DatabaseAccessor srv.Storage
}

type RankedDirSignatureOutlineOptions struct {
	RankedViaEmbeddingOptions
	CharLimit int
}

type RankedViaEmbeddingOptions struct {
	WorkspaceId  string
	EnvContainer env.EnvContainer
	RankQuery    string
	Secrets      secret_manager.SecretManagerContainer
	ModelConfig  common.ModelConfig
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

	maxChars, err := embedding.GetModelMaxChars(options.ModelConfig)
	if err != nil {
		return "", fmt.Errorf("failed to calculate embedding char limits: %w", err)
	}

	fileSignatureSubkeys, err := t.CreateDirSignatureOutlines(options.WorkspaceId, options.EnvContainer.Env.GetWorkingDirectory(), maxChars)
	if err != nil {
		return "", err
	}

	rankedFileSignatureSubkeys, err := ra.RankedSubkeys(RankedSubkeysOptions{
		RankedViaEmbeddingOptions: options.RankedViaEmbeddingOptions,
		ContentType:               tree_sitter.ContentTypeFileSignature,
		Subkeys:                   fileSignatureSubkeys,
	})
	if err != nil {
		return "", err
	}

	rankedDirChunkSubkeys, err := ra.RankedDirChunkSubkeys(RankedDirChunkSubkeysOptions{options.RankedViaEmbeddingOptions})
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
	if strings.TrimSpace(options.RankQuery) == "" {
		return []string{}, errors.New("Attempted to perform RAG with an empty query")
	}

	ea := EmbedActivities{Storage: ra.DatabaseAccessor}
	err := ea.CachedEmbedActivity(context.Background(), CachedEmbedActivityOptions{
		Secrets:     options.Secrets,
		WorkspaceId: options.WorkspaceId,
		ModelConfig: options.ModelConfig,
		ContentType: options.ContentType,
		Subkeys:     options.Subkeys,
	})
	if err != nil {
		return []string{}, err
	}

	va := VectorActivities{DatabaseAccessor: ra.DatabaseAccessor}

	embedder, err := getEmbedder(options.ModelConfig)
	if err != nil {
		return []string{}, err
	}

	// Get model-specific character limits
	maxQueryChars, err := embedding.GetModelMaxChars(options.ModelConfig)
	goodQueryChars := min(maxQueryChars, tree_sitter.DefaultPreferredChunkChars)
	if err != nil {
		return []string{}, fmt.Errorf("failed to calculate embedding limits: %w", err)
	}

	// Split query into chunks if it exceeds max size, otherwise it's a single chunk.
	var queryChunks []string
	if len(options.RankQuery) > maxQueryChars {
		queryChunks = splitQueryIntoChunks(options.RankQuery, goodQueryChars, maxQueryChars)
	} else {
		queryChunks = []string{options.RankQuery}
	}

	// NOTE: "code_retrieval_query" would be ideal here, but isn't supported by text-embedding-004
	// TODO: dynamically decide task type based on model name, as all task types arent supported by all models.
	// TODO: change "task type" to instead be "use_case" and we'll map to task
	// type internally in the embedder implementation
	taskType := embedding.TaskTypeRetrievalQuery

	// Embed all chunks
	// TODO /gen/basic cache query vectors in memory, for when the same query is rerun twice
	queryVectors, err := embedder.Embed(context.Background(), options.ModelConfig, options.Secrets.SecretManager, queryChunks, taskType)
	if err != nil {
		return []string{}, fmt.Errorf("failed to embed query chunks: %w", err)
	}
	if len(queryVectors) == 0 {
		return []string{}, nil
	}

	// get closest results, one result set for each query chunk
	resultSets, err := va.MultiVectorSearch(MultiVectorSearchOptions{
		WorkspaceId: options.WorkspaceId,
		Provider:    options.ModelConfig.Provider,
		Model:       options.ModelConfig.Model,
		ContentType: options.ContentType,
		Subkeys:     options.Subkeys,
		Queries:     queryVectors,
		Limit:       1000,
	})
	if err != nil {
		return []string{}, fmt.Errorf("failed multi-vector search: %w", err)
	}

	// rank-fusion to merge result sets. note: still works if there's just one result set
	return FuseResultsRRF(resultSets), nil
}

// splitQueryIntoChunks splits a query into chunks based on sentence boundaries and size limits.
// Unlike tree_sitter.splitOutlineIntoChunks which is specialized for code outlines,
// this function is optimized for natural language queries.
func splitQueryIntoChunks(query string, goodChunkSize int, maxChunkSize int) []string {
	if query == "" {
		return []string{}
	}

	// First try splitting on sentence boundaries
	sentences := strings.FieldsFunc(query, func(r rune) bool {
		return r == '.' || r == '?' || r == '!'
	})

	var chunks []string
	currentChunk := ""

	// Combine sentences into chunks
	for _, sentence := range sentences {
		sentence = strings.TrimSpace(sentence)
		if sentence == "" {
			continue
		}

		// Add sentence punctuation back
		sentence = sentence + "."

		// If adding this sentence would exceed goodChunkSize, start a new chunk
		if len(currentChunk)+len(sentence)+1 > goodChunkSize && currentChunk != "" {
			chunks = append(chunks, strings.TrimSpace(currentChunk))
			currentChunk = sentence
		} else {
			if currentChunk != "" {
				currentChunk += " "
			}
			currentChunk += sentence
		}
	}

	// Add the last chunk if any
	if currentChunk != "" {
		chunks = append(chunks, strings.TrimSpace(currentChunk))
	}

	// If any chunks are still too large, split them on word boundaries
	for i := 0; i < len(chunks); i++ {
		if len(chunks[i]) > maxChunkSize {
			words := strings.Fields(chunks[i])
			currentChunk = ""
			newChunks := []string{}

			for _, word := range words {
				if len(currentChunk)+len(word)+1 > maxChunkSize {
					if currentChunk != "" {
						newChunks = append(newChunks, strings.TrimSpace(currentChunk))
					}
					currentChunk = word
				} else {
					if currentChunk != "" {
						currentChunk += " "
					}
					currentChunk += word
				}
			}

			if currentChunk != "" {
				newChunks = append(newChunks, strings.TrimSpace(currentChunk))
			}

			// Replace the original chunk with the new chunks
			chunks = append(chunks[:i], append(newChunks, chunks[i+1:]...)...)
			i += len(newChunks) - 1
		}
	}

	return chunks
}

func getEmbedder(config common.ModelConfig) (embedding.Embedder, error) {
	var embedder embedding.Embedder
	providerType, err := getProviderType(config.Provider)
	if err != nil {
		return nil, err
	}
	switch providerType {
	case llm.OpenaiToolChatProviderType:
		embedder = &embedding.OpenAIEmbedder{}
	case llm.GoogleToolChatProviderType:
		embedder = &embedding.GoogleEmbedder{}
	case llm.OpenaiCompatibleToolChatProviderType:
		// FIXME pass in the providers in the parameters instead of loading the
		// config directly here
		localConfig, err := common.LoadSidekickConfig(common.GetSidekickConfigPath())
		if err != nil {
			return nil, fmt.Errorf("failed to load local config: %w", err)
		}
		for _, p := range localConfig.Providers {
			if p.Type == string(providerType) && p.Name == config.Provider {
				return &embedding.OpenAIEmbedder{
					BaseURL: p.BaseURL,
				}, nil
			}
		}
		return nil, fmt.Errorf("configuration not found for provider named: %s", config.Provider)
	case llm.ToolChatProviderType("mock"):
		return &embedding.MockEmbedder{}, nil
	default:
		return nil, fmt.Errorf("unsupported provider type %s for provider %s", providerType, config.Provider)
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
		RankedViaEmbeddingOptions: options.RankedViaEmbeddingOptions,
		ContentType:               tree_sitter.ContentTypeDirChunk,
		Subkeys:                   dirChunkSubkeys,
	})
}
