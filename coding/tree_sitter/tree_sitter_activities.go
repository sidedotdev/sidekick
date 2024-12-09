package tree_sitter

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sidekick/db"
	"strings"
)

type TreeSitterActivities struct {
	DatabaseAccessor db.DatabaseAccessor
}

const ContentTypeFileSignature = "file:signature"
const ContentTypeDirChunk = "dir:chunk"

// TODO move to RagActivities
// TODO add param for context.Context
func (t *TreeSitterActivities) CreateDirSignatureOutlines(workspaceId string, directoryPath string) ([]uint64, error) {
	// FIXME perf: have a way to skip getting outlines for the ones we already set in the DB, eg using checksums
	outlines, err := GetDirectorySignatureOutlines(directoryPath, nil, nil)

	if err != nil {
		return []uint64{}, err
	}

	values := make(map[string]interface{})
	hashes := make([]uint64, 0, len(outlines))
	for _, outline := range outlines {
		if outline.OutlineType == OutlineTypeFileSignature && outline.Content != "" {
			outlineChunks := splitOutlineIntoChunks(outline.Content, defaultGoodChunkSize, defaultMaxChunkSize)
			for _, chunk := range outlineChunks {
				value := outline.Path + "\n" + chunk
				hash := hash64(value)
				hashes = append(hashes, hash)
				key := fmt.Sprintf("%s:%s:%d", workspaceId, ContentTypeFileSignature, hash)
				values[key] = value
			}
		}
	}

	err = t.DatabaseAccessor.MSet(context.Background(), values)
	if err != nil {
		return []uint64{}, err
	}

	return hashes, nil
}

const defaultMaxChunkSize = 10000
const defaultGoodChunkSize = 3000

func splitOutlineIntoChunks(s string, goodChunkSize int, maxChunkSize int) []string {
	if s == "" {
		return []string{}
	}

	chunks := []string{}

	// first split based on change in indentation from indented to outdented
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

// hash64 takes a string and returns a 64-bit hash using SHA256
func hash64(s string) uint64 {
	// Compute SHA256 hash of the input string
	hash := sha256.Sum256([]byte(s))

	// Convert the first 8 bytes of the hash to a uint64
	return binary.BigEndian.Uint64(hash[:8])
}
