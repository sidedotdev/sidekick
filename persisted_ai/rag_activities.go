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
	"sidekick/secret_manager"
	"sidekick/srv"
	"sidekick/utils"
	"strings"
	"time"

	"github.com/kelindar/binary"
	"github.com/rs/zerolog/log"
)

type RagActivities struct {
	DatabaseAccessor srv.Storage
}

type RankedDirSignatureOutlineOptions struct {
	RankedViaEmbeddingOptions
	CharLimit int
}

// WeightedRankQuery is an additional embedding query merged into RAG ranking
// alongside the baseline RankQuery, contributing with the given weight when
// fused.
type WeightedRankQuery struct {
	Query  string
	Weight float64
}

type RankedViaEmbeddingOptions struct {
	WorkspaceId  string
	EnvContainer env.EnvContainer
	// RankQuery is the baseline embedding query, contributing with weight 1.0.
	RankQuery string
	// WeightedRankQueries are additional queries merged into the ranking. Each
	// is independently chunked, embedded, and searched; resulting ranked lists
	// are fused with the baseline using their declared weights.
	WeightedRankQueries []WeightedRankQuery
	Secrets             secret_manager.SecretManagerContainer
	ModelConfig         common.ModelConfig
}

func (options RankedDirSignatureOutlineOptions) ActionParams() map[string]any {
	params := map[string]interface{}{
		"rankQuery": options.RankQuery,
		"charLimit": options.CharLimit,
		"provider":  options.ModelConfig.Provider,
		"model":     options.ModelConfig.Model,
	}
	if len(options.WeightedRankQueries) > 0 {
		params["weightedRankQueries"] = options.WeightedRankQueries
	}
	return params
}

// RankedDirSignatureOutline generates a ranked outline of the directory structure based on the query.
func (ra *RagActivities) RankedDirSignatureOutline(ctx context.Context, options RankedDirSignatureOutlineOptions) (string, error) {
	// FIXME put tree sitter activities inside rag activities struct
	t := tree_sitter.TreeSitterActivities{DatabaseAccessor: ra.DatabaseAccessor}

	maxChars, err := embedding.GetModelMaxChars(options.ModelConfig)
	if err != nil {
		return "", fmt.Errorf("failed to calculate embedding char limits: %w", err)
	}

	stepStart := time.Now()
	logStep := func(step string) {
		log.Debug().Str("step", step).Dur("duration", time.Since(stepStart)).Msg("ranked dir signature outline step")
		stepStart = time.Now()
	}

	cacheState := ra.loadOutlineWalkCache(ctx, options.WorkspaceId, options.EnvContainer)
	logStep("load outline cache")

	// A single raw entry set feeds all three consumers below (signature
	// outlines, dir chunk paths and the final limited outline). With a cached
	// snapshot it is reconstructed from cache + git diff without walking;
	// only a cold cache pays for a full walk.
	rawEntries, cached, err := tree_sitter.GetDirectoryRawOutlinesFromCache(ctx, options.EnvContainer, cacheState.cache)
	if err != nil {
		// Reconstruction failures (eg transient read errors) are recoverable:
		// the full walk below re-records every path from scratch.
		log.Debug().Err(err).Msg("outline cache reconstruction failed, falling back to full walk")
		cached = false
	}
	if !cached {
		rawEntries, err = tree_sitter.GetDirectoryRawOutlines(ctx, options.EnvContainer, cacheState.cache)
		if err != nil {
			return "", err
		}
	}
	logStep("raw outlines")
	log.Debug().Bool("cachedOutlines", cached).Int("rawEntryCount", len(rawEntries)).Msg("directory raw outlines")
	if len(rawEntries) == 0 {
		log.Warn().Bool("cachedOutlines", cached).Msg("no raw outline entries found; ranked outline will be empty")
	}
	ra.storeOutlineWalkCache(ctx, options.WorkspaceId, cacheState)

	fileSignatureSubkeys, err := t.PersistDirSignatureOutlines(ctx, options.WorkspaceId, tree_sitter.OutlinesFromRawEntries(rawEntries, nil, nil), maxChars)
	if err != nil {
		return "", err
	}
	logStep("persist dir signature outlines")

	rankedFileSignatureSubkeys, err := ra.RankedSubkeys(ctx, RankedSubkeysOptions{
		RankedViaEmbeddingOptions: options.RankedViaEmbeddingOptions,
		ContentType:               tree_sitter.ContentTypeFileSignature,
		Subkeys:                   fileSignatureSubkeys,
	})
	if err != nil {
		return "", err
	}
	logStep("rank file signature subkeys")

	pathInfos := make([]PathInfo, len(rawEntries))
	for i, entry := range rawEntries {
		pathInfos[i] = PathInfo{Path: "/" + entry.RelativePath, IsDir: entry.IsDir, present: true}
	}
	rankedDirChunkSubkeys, err := ra.RankedDirChunkSubkeys(ctx, RankedDirChunkSubkeysOptions{
		RankedViaEmbeddingOptions: options.RankedViaEmbeddingOptions,
		pathInfos:                 pathInfos,
	})
	if err != nil {
		return "", err
	}
	logStep("rank dir chunk subkeys")

	outline, err := ra.LimitedDirSignatureOutline(ctx, DirSignatureOutlineOptions{
		WorkspaceId:          options.WorkspaceId,
		FileSignatureSubkeys: rankedFileSignatureSubkeys,
		DirChunkSubkeys:      rankedDirChunkSubkeys,
		CharLimit:            options.CharLimit,
		rawEntries:           rawEntries,
	})
	logStep("limited dir signature outline")
	return outline, err
}

type RankedSubkeysOptions struct {
	RankedViaEmbeddingOptions
	ContentType string
	Subkeys     []string
}

func (ra *RagActivities) RankedSubkeys(ctx context.Context, options RankedSubkeysOptions) ([]string, error) {
	if strings.TrimSpace(options.RankQuery) == "" {
		return []string{}, errors.New("Attempted to perform RAG with an empty query")
	}

	ea := EmbedActivities{Storage: ra.DatabaseAccessor}
	err := ea.CachedEmbedActivity(ctx, CachedEmbedActivityOptions{
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

	// Get model-specific character limits
	maxQueryChars, err := embedding.GetModelMaxChars(options.ModelConfig)
	goodQueryChars := min(maxQueryChars, tree_sitter.DefaultPreferredChunkChars)
	if err != nil {
		return []string{}, fmt.Errorf("failed to calculate embedding limits: %w", err)
	}

	// Build the full list of weighted queries: baseline RankQuery first, then
	// any additional weighted queries. Each query is independently chunked so
	// that each chunk's ranked search result contributes a separate ranking
	// (sharing its parent query's weight) to the final fusion.
	weightedQueries := make([]WeightedRankQuery, 0, 1+len(options.WeightedRankQueries))
	weightedQueries = append(weightedQueries, WeightedRankQuery{Query: options.RankQuery, Weight: BaselineRankWeight})
	weightedQueries = append(weightedQueries, options.WeightedRankQueries...)

	var queryChunks []string
	var chunkWeights []float64
	var rerankQueryParts []string
	for _, wq := range weightedQueries {
		query := strings.TrimSpace(wq.Query)
		if query == "" {
			continue
		}
		rerankQueryParts = append(rerankQueryParts, query)
		var chunks []string
		if len(wq.Query) > maxQueryChars {
			chunks = splitQueryIntoChunks(wq.Query, goodQueryChars, maxQueryChars)
		} else {
			chunks = []string{wq.Query}
		}
		for _, c := range chunks {
			queryChunks = append(queryChunks, c)
			chunkWeights = append(chunkWeights, wq.Weight)
		}
	}

	// NOTE: "code_retrieval_query" would be ideal here, but isn't supported by text-embedding-004
	// TODO: dynamically decide task type based on model name, as all task types arent supported by all models.
	// TODO: change "task type" to instead be "use_case" and we'll map to task
	// type internally in the embedder implementation
	taskType := embedding.TaskTypeRetrievalQuery

	// Embed all chunks
	// TODO /gen/basic cache query vectors in memory, for when the same query is rerun twice
	queryVectors, err := BatchEmbed(ctx, options.ModelConfig, options.Secrets.SecretManager, queryChunks, taskType)
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

	rankings := make([]WeightedRanking, len(resultSets))
	for i, set := range resultSets {
		rankings[i] = WeightedRanking{Items: set, Weight: chunkWeights[i]}
	}
	rankings = append(rankings, ra.bm25WeightedRankings(ctx, options.WorkspaceId, options.ContentType, options.Subkeys, weightedQueries)...)

	reranker, err := GetReranker(options.Secrets.SecretManager)
	if err != nil {
		return nil, fmt.Errorf("failed to get reranker: %w", err)
	}

	fusedSubkeys := FuseResults(rankings)
	return ra.rerankSubkeys(
		ctx,
		options.WorkspaceId,
		options.ContentType,
		strings.Join(rerankQueryParts, "\n\n"),
		fusedSubkeys,
		reranker,
	)
}

// bm25WeightedRankings hydrates the documents behind the given subkeys and
// produces one lexical BM25 ranking per non-empty weighted query, so lexical
// results can be fused with embedding-based rankings. It is best-effort: on
// storage or unmarshal errors it logs at debug level and returns nil so the
// embedding-only path still works.
func (ra *RagActivities) bm25WeightedRankings(
	ctx context.Context,
	workspaceId string,
	contentType string,
	subkeys []string,
	weightedQueries []WeightedRankQuery,
) []WeightedRanking {
	if len(subkeys) == 0 {
		return nil
	}

	contentKeys := make([]string, len(subkeys))
	for i, subkey := range subkeys {
		contentKeys[i] = fmt.Sprintf("%s:%s", contentType, subkey)
	}
	values, err := ra.DatabaseAccessor.MGet(ctx, workspaceId, contentKeys)
	if err != nil {
		log.Debug().Err(err).Msg("bm25: failed to hydrate documents, skipping lexical ranking")
		return nil
	}
	if len(values) != len(contentKeys) {
		log.Debug().Int("got", len(values)).Int("expected", len(contentKeys)).Msg("bm25: unexpected hydration result count, skipping lexical ranking")
		return nil
	}

	documents := make([]string, 0, len(values))
	documentSubkeys := make([]string, 0, len(values))
	for i, value := range values {
		if value == nil {
			continue
		}
		var document string
		if err := binary.Unmarshal(value, &document); err != nil {
			log.Debug().Err(err).Str("key", contentKeys[i]).Msg("bm25: failed to unmarshal document, skipping lexical ranking")
			return nil
		}
		documents = append(documents, document)
		documentSubkeys = append(documentSubkeys, subkeys[i])
	}
	if len(documents) == 0 {
		return nil
	}

	var rankings []WeightedRanking
	for _, wq := range weightedQueries {
		if strings.TrimSpace(wq.Query) == "" {
			continue
		}
		rankedIndices := RankBM25(wq.Query, documents)
		if len(rankedIndices) == 0 {
			continue
		}
		items := make([]string, len(rankedIndices))
		for i, docIdx := range rankedIndices {
			items[i] = documentSubkeys[docIdx]
		}
		rankings = append(rankings, WeightedRanking{Items: items, Weight: wq.Weight * bm25RankWeight})
	}
	return rankings
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

type DirSignatureOutlineOptions struct {
	WorkspaceId          string
	FileSignatureSubkeys []string // these are file signature subkeys
	DirChunkSubkeys      []string
	EmbeddingType        string
	CharLimit            int
	// rawEntries provides the pre-walked raw outline entries the subset
	// outline is derived from, so no additional walk or parse is needed.
	rawEntries []tree_sitter.RawOutlineEntry
}

// LimitedDirSignatureOutline returns a string containing the directory structure with signature outlines expanded only for the given subkeys.
func (ra *RagActivities) LimitedDirSignatureOutline(ctx context.Context, options DirSignatureOutlineOptions) (string, error) {
	var charCount int
	showPaths := make(map[string]bool, 0)
	signaturePaths := make(map[string]int, 0)

	dirChunkKeys := make([]string, len(options.DirChunkSubkeys))
	for i, subkey := range options.DirChunkSubkeys {
		dirChunkKeys[i] = fmt.Sprintf("%s:%s", tree_sitter.ContentTypeDirChunk, subkey)
	}
	dirChunks, err := ra.DatabaseAccessor.MGet(ctx, options.WorkspaceId, dirChunkKeys)
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
	fileSignatures, err := ra.DatabaseAccessor.MGet(ctx, options.WorkspaceId, fileSignatureKeys)
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

	outlines := tree_sitter.OutlinesFromRawEntries(options.rawEntries, &showPaths, &signaturePaths)
	return tree_sitter.GetFileOutlinesString(outlines)
}

type RankedDirChunkSubkeysOptions struct {
	RankedViaEmbeddingOptions
	// pathInfos optionally provides pre-walked paths so chunking needs no
	// additional walk. In-process only: it intentionally does not serialize.
	pathInfos []PathInfo
}

func (ra *RagActivities) RankedDirChunkSubkeys(ctx context.Context, options RankedDirChunkSubkeysOptions) ([]string, error) {
	var chunks []DirChunk
	if options.pathInfos != nil {
		chunks = GetDirectoryChunksFromPaths(options.pathInfos)
	} else {
		var err error
		chunks, err = GetDirectoryChunks(ctx, options.EnvContainer)
		if err != nil {
			return []string{}, fmt.Errorf("get directory chunks: %w", err)
		}
	}

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
	err := ra.DatabaseAccessor.MSet(ctx, options.WorkspaceId, values)
	if err != nil {
		return []string{}, fmt.Errorf("error persisting dir chunk content: %w", err)
	}

	dirChunkSubkeys := hashes
	return ra.RankedSubkeys(ctx, RankedSubkeysOptions{
		RankedViaEmbeddingOptions: options.RankedViaEmbeddingOptions,
		ContentType:               tree_sitter.ContentTypeDirChunk,
		Subkeys:                   dirChunkSubkeys,
	})
}

/*
 */
func (ra *RagActivities) rerankSubkeys(
	ctx context.Context,
	workspaceId string,
	contentType string,
	query string,
	fusedSubkeys []string,
	reranker Reranker,
) ([]string, error) {
	if reranker == nil || len(fusedSubkeys) == 0 {
		return fusedSubkeys, nil
	}

	candidateCount := min(len(fusedSubkeys), rerankCandidateLimit)
	contentKeys := make([]string, candidateCount)
	for i, subkey := range fusedSubkeys[:candidateCount] {
		contentKeys[i] = fmt.Sprintf("%s:%s", contentType, subkey)
	}

	values, err := ra.DatabaseAccessor.MGet(ctx, workspaceId, contentKeys)
	if err != nil {
		return nil, fmt.Errorf("failed to hydrate rerank candidates: %w", err)
	}
	if len(values) != candidateCount {
		return nil, fmt.Errorf("hydrated %d rerank candidates, expected %d", len(values), candidateCount)
	}

	documents := make([]string, candidateCount)
	for i, value := range values {
		if value == nil {
			return nil, fmt.Errorf("missing value for rerank candidate key: %s", contentKeys[i])
		}
		if err := binary.Unmarshal(value, &documents[i]); err != nil {
			return nil, fmt.Errorf("value %v for rerank candidate key %s failed to unmarshal: %w", value, contentKeys[i], err)
		}
	}

	rerankedSubkeys, err := reranker.Rerank(ctx, query, documents)
	if err != nil {
		return nil, fmt.Errorf("failed to rerank subkeys: %w", err)
	}

	subkeysByDocument := make(map[string][]string, candidateCount)
	for i, document := range documents {
		subkeysByDocument[document] = append(subkeysByDocument[document], fusedSubkeys[i])
	}

	result := make([]string, 0, len(fusedSubkeys))
	for _, document := range rerankedSubkeys {
		subkeys := subkeysByDocument[document]
		if len(subkeys) == 0 {
			return nil, fmt.Errorf("reranker returned an unknown or duplicate document")
		}
		result = append(result, subkeys[0])
		if len(subkeys) == 1 {
			delete(subkeysByDocument, document)
		} else {
			subkeysByDocument[document] = subkeys[1:]
		}
	}
	if len(result) != candidateCount {
		return nil, fmt.Errorf("reranker returned %d candidates, expected %d", len(result), candidateCount)
	}

	return append(result, fusedSubkeys[candidateCount:]...), nil
}
