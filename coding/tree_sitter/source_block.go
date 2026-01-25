package tree_sitter

import (
	"strings"

	"github.com/rs/zerolog/log"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

type SourceBlock struct {
	Source    *[]byte
	Range     tree_sitter.Range
	NameRange *tree_sitter.Range
}

func (sb SourceBlock) String() string {
	if sb.Source == nil || len(*sb.Source) == 0 {
		return ""
	}
	sourceLen := uint(len(*sb.Source))
	startByte := sb.Range.StartByte
	endByte := sb.Range.EndByte
	clamped := false
	if startByte > sourceLen {
		startByte = sourceLen
		clamped = true
	}
	if endByte > sourceLen {
		endByte = sourceLen
		clamped = true
	}
	if startByte > endByte {
		startByte = endByte
		clamped = true
	}
	if clamped {
		log.Warn().
			Uint("originalStartByte", sb.Range.StartByte).
			Uint("originalEndByte", sb.Range.EndByte).
			Uint("sourceLen", sourceLen).
			Msg("SourceBlock.String: clamped out-of-bounds range, possible upstream bug")
	}
	return string((*sb.Source)[startByte:endByte])
}

func MergeAdjacentOrOverlappingSourceBlocks(sourceBlocks []SourceBlock, sourceCodeLines []string) []SourceBlock {
	// NOTE: this merge logic assumes sourceBlocks are in order of start point
	mergedSourceBlocks := []SourceBlock{}
	for _, sourceBlock := range sourceBlocks {
		if len(mergedSourceBlocks) == 0 {
			mergedSourceBlocks = append(mergedSourceBlocks, SourceBlock{Source: sourceBlock.Source, Range: sourceBlock.Range})
			continue
		}
		lastSourceBlock := &mergedSourceBlocks[len(mergedSourceBlocks)-1]
		// Check if all lines between the end of the last sourceBlock and the start of the current sourceBlock are whitespace
		allWhitespace := true
		if sourceBlock.Range.StartPoint.Row > lastSourceBlock.Range.EndPoint.Row+1 {
			for _, line := range sourceCodeLines[lastSourceBlock.Range.EndPoint.Row+1 : sourceBlock.Range.StartPoint.Row] {
				if strings.TrimSpace(line) != "" {
					allWhitespace = false
					break
				}
			}
		} else {
			allWhitespace = false
		}

		if lastSourceBlock.Range.EndPoint.Row+1 >= sourceBlock.Range.StartPoint.Row || allWhitespace {
			if sourceBlock.Range.EndPoint.Row > lastSourceBlock.Range.EndPoint.Row {
				lastSourceBlock.Range.EndPoint = sourceBlock.Range.EndPoint
				lastSourceBlock.Range.EndByte = sourceBlock.Range.EndByte
			}
		} else {
			mergedSourceBlocks = append(mergedSourceBlocks, sourceBlock)
		}
	}

	return mergedSourceBlocks
}

func countBytesInLines(startByte uint, numLines int, sourceCode []byte, direction string) uint {
	if numLines <= 0 || len(sourceCode) == 0 {
		return 0
	}

	sourceLen := uint(len(sourceCode))
	if startByte > sourceLen {
		startByte = sourceLen
	}

	count := uint(0)
	if direction == "forward" {
		for i := uint(startByte + 1); i <= uint(len(sourceCode)); i++ {
			count++
			if sourceCode[i-1] == '\n' { // end byte is exclusive and includes the newline
				numLines--
				if numLines == 0 {
					break
				}
			}
		}
	} else if direction == "backward" {
		// the startByte is at the first character of a line, but we want to
		// count the bytes in the previous line, so we start 1 index earlier
		// to avoid counting the newline at the start of the line
		if startByte == 0 {
			return 0
		}

		if startByte < sourceLen && sourceCode[startByte] == '\n' && startByte == 1 {
			// due to index decrement, and on an empty line, we need to adjust
			return 1
		}

		for i := startByte - 1; i > 0; i-- {
			if sourceCode[i-1] == '\n' { // start byte for a line is inclusive anod has no starting newline
				numLines--
				if numLines == 0 {
					break
				}
			}
			count++
		}
	}
	return count
}

// expands the source blocks to include the specified number of additional lines of context before and after
func ExpandContextLines(sourceBlocks []SourceBlock, numContextLines int, sourceCode []byte) []SourceBlock {
	sourceCodeLines := strings.Split(string(sourceCode), "\n")
	sourceLen := uint(len(sourceCode))
	numLines := uint(len(sourceCodeLines))

	for i := range sourceBlocks {
		sb := sourceBlocks[i]

		// Clamp initial range values to valid bounds
		if sb.Range.StartByte > sourceLen {
			sb.Range.StartByte = sourceLen
		}
		if sb.Range.EndByte > sourceLen {
			sb.Range.EndByte = sourceLen
		}
		if sb.Range.StartByte > sb.Range.EndByte {
			sb.Range.StartByte = sb.Range.EndByte
		}
		if numLines > 0 {
			if sb.Range.StartPoint.Row >= numLines {
				sb.Range.StartPoint.Row = numLines - 1
			}
			if sb.Range.EndPoint.Row >= numLines {
				sb.Range.EndPoint.Row = numLines - 1
			}
		}

		// NOTE: range is start-inclusive and end-exclusive

		startRow := sb.Range.StartPoint.Row
		endRow := sb.Range.EndPoint.Row
		contextBefore := min(uint(numContextLines), startRow)
		contextAfter := uint(0)
		if numLines > 0 && endRow < numLines-1 {
			contextAfter = min(uint(numContextLines), numLines-endRow-1)
		}
		sb.Range.StartPoint.Row -= contextBefore
		sb.Range.EndPoint.Row += contextAfter
		reduceBytes := countBytesInLines(sb.Range.StartByte, numContextLines, sourceCode, "backward")
		sb.Range.StartByte -= reduceBytes
		sb.Range.StartPoint.Column = 0
		sb.Range.EndByte += countBytesInLines(sb.Range.EndByte, numContextLines, sourceCode, "forward")
		// using len instead of len-1 since end is exclusive
		if numLines > 0 && sb.Range.EndPoint.Row < numLines {
			sb.Range.EndPoint.Column = uint(len(sourceCodeLines[sb.Range.EndPoint.Row]))
		}

		// startByte is in the middle of a line, so we need to go back to the start of the line
		if sb.Range.StartByte >= 1 && sb.Range.StartByte <= sourceLen && sourceCode[sb.Range.StartByte-1] != '\n' {
			sb.Range.StartByte -= countBytesInLines(sb.Range.StartByte, 1, sourceCode, "backward") + 1
		}

		// endByte is in the middle of a line, so we need to go to the end of the line
		if sb.Range.EndByte >= 1 && sb.Range.EndByte <= sourceLen && sourceCode[sb.Range.EndByte-1] != '\n' {
			sb.Range.EndByte += countBytesInLines(sb.Range.EndByte, 1, sourceCode, "forward")
		}

		// Clamp final EndByte to source length
		if sb.Range.EndByte > sourceLen {
			sb.Range.EndByte = sourceLen
		}

		if sb.Range.EndByte >= 1 && sb.Range.EndByte <= sourceLen && sourceCode[sb.Range.EndByte-1] == '\n' && sb.Range.EndByte < sourceLen {
			sb.Range.EndPoint.Column += 1 // one past the *next* newline
		}

		sourceBlocks[i] = sb
	}

	return sourceBlocks
}
