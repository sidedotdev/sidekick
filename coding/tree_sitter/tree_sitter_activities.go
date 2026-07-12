package tree_sitter

import (
	"context"
	"fmt"
	"sidekick/env"
	"sidekick/srv"
	"sidekick/utils"
	"strings"
)

// TODO move to persisted_ai package
const DefaultPreferredChunkChars = 3000

type TreeSitterActivities struct {
	DatabaseAccessor srv.Storage
}

const ContentTypeFileSignature = "file:signature"
const ContentTypeDirChunk = "dir:chunk"

// TODO move to RagActivities
func (t *TreeSitterActivities) CreateDirSignatureOutlines(ctx context.Context, workspaceId string, ec env.EnvContainer, maxCharacterLimit int) ([]string, error) {
	return t.CreateDirSignatureOutlinesCached(ctx, workspaceId, ec, maxCharacterLimit, nil)
}

// CreateDirSignatureOutlinesCached is CreateDirSignatureOutlines with an
// optional walk cache that skips reading and parsing files unchanged since a
// previously cached repo state.
func (t *TreeSitterActivities) CreateDirSignatureOutlinesCached(ctx context.Context, workspaceId string, ec env.EnvContainer, maxCharacterLimit int, cache *OutlineWalkCache) ([]string, error) {
	outlines, err := GetDirectorySignatureOutlinesCached(ctx, ec, nil, nil, cache)

	if err != nil {
		return []string{}, err
	}

	return t.PersistDirSignatureOutlines(ctx, workspaceId, outlines, maxCharacterLimit)
}

// PersistDirSignatureOutlines chunks the given signature outlines and
// persists them under content-hash subkeys, returning those subkeys.
func (t *TreeSitterActivities) PersistDirSignatureOutlines(ctx context.Context, workspaceId string, outlines []FileOutline, maxCharacterLimit int) ([]string, error) {
	values := make(map[string]interface{})
	hashes := make([]string, 0, len(outlines))
	for _, outline := range outlines {
		if outline.OutlineType == OutlineTypeFileSignature && outline.Content != "" {
			goodCharacterLimit := DefaultPreferredChunkChars
			if maxCharacterLimit < goodCharacterLimit {
				goodCharacterLimit = maxCharacterLimit
			}
			outlineChunks := splitOutlineIntoChunks(outline.Content, goodCharacterLimit, maxCharacterLimit)
			for _, chunk := range outlineChunks {
				value := outline.Path + "\n" + chunk
				hash := utils.Hash256(value)
				hashes = append(hashes, hash)
				key := fmt.Sprintf("%s:%s", ContentTypeFileSignature, hash)
				values[key] = value
			}
		}
	}

	err := t.DatabaseAccessor.MSet(context.Background(), workspaceId, values)
	if err != nil {
		return []string{}, err
	}

	return hashes, nil
}

func splitOutlineIntoChunks(s string, goodChunkSize int, maxChunkSize int) []string {
	if s == "" {
		return []string{}
	}

	chunks := []string{}

	// first split based on change in indentation from indented to outdented
	// TODO: we could have better, more coherent chunks if we tried chunking
	// when outdenting to 0 indentation instead
	lines := strings.Split(s, "\n")
	currentIndentation := -1
	currentChunk := ""
	for _, line := range lines {
		indentation := countIndentation(line)
		if currentIndentation == -1 {
			currentIndentation = indentation
		}
		// when outdenting, start a new chunk
		if indentation < currentIndentation {
			chunks = append(chunks, strings.Trim(currentChunk, "\n"))
			currentChunk = ""
		}
		if currentChunk != "" {
			currentChunk += "\n"
		}
		currentChunk += line
		currentIndentation = indentation
	}
	// Append the last chunk
	if currentChunk != "" {
		chunks = append(chunks, strings.Trim(currentChunk, "\n"))
	}

	// merge chunks that are too short
	for i := 0; i < len(chunks)-1; i++ {
		if len(chunks[i])+len(chunks[i+1]) <= goodChunkSize {
			chunks[i] += "\n" + chunks[i+1]
			chunks = append(chunks[:i+1], chunks[i+2:]...)
			i--
		}
	}

	// if any are still too long, split based on empty (whitespace only) lines
	for i := 0; i < len(chunks); i++ {
		if len(chunks[i]) > maxChunkSize {
			lines := strings.Split(chunks[i], "\n")
			currentChunk = ""
			for j, line := range lines {
				if strings.TrimSpace(line) == "" && len(currentChunk) > goodChunkSize {
					chunks = append(chunks[:i+1], chunks[i:]...)
					chunks[i] = currentChunk
					chunks[i+1] = strings.Trim(strings.Join(lines[j:], "\n"), "\n")
					break
				}
				if currentChunk != "" {
					currentChunk += "\n"
				}
				currentChunk += line
			}
		}
	}

	// if any are still too long, split anywhere until short enough
	for i := 0; i < len(chunks); i++ {
		if len(chunks[i]) > maxChunkSize {
			lines := strings.Split(chunks[i], "\n")
			currentChunk = ""
			for j, line := range lines {
				if len(currentChunk) > goodChunkSize {
					chunks = append(chunks[:i+1], chunks[i:]...)
					chunks[i] = currentChunk
					chunks[i+1] = strings.Trim(strings.Join(lines[j:], "\n"), "\n")
					break
				}
				if currentChunk != "" {
					currentChunk += "\n"
				}
				currentChunk += line
			}
		}
	}

	return chunks
}

func countIndentation(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}
