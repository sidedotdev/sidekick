package tree_sitter

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

type SourceBlock struct {
	Source    *[]byte
	Range     sitter.Range
	NameRange *sitter.Range
}

func (sb SourceBlock) String() string {
	return string((*sb.Source)[sb.Range.StartByte:sb.Range.EndByte])
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

func countBytesInLines(startByte uint32, numLines int, sourceCode []byte, direction string) uint32 {
	if numLines <= 0 {
		return 0
	}

	count := uint32(0)
	if direction == "forward" {
		for i := int(startByte + 1); i <= len(sourceCode); i++ {
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

		if sourceCode[startByte] == '\n' && startByte == 1 {
			// due to index decrement, and on an empty line, we need to adjust
			return 1
		}

		for i := int(startByte - 1); i > 0; i-- {
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
	for i := range sourceBlocks {
		sb := sourceBlocks[i]

		// NOTE: range is start-inclusive and end-exclusive

		startRow := sb.Range.StartPoint.Row
		endRow := sb.Range.EndPoint.Row
		contextBefore := min(uint32(numContextLines), startRow)
		contextAfter := min(uint32(numContextLines), uint32(len(sourceCodeLines))-endRow-1)
		sb.Range.StartPoint.Row -= contextBefore
		sb.Range.EndPoint.Row += contextAfter
		reduceBytes := countBytesInLines(sb.Range.StartByte, numContextLines, sourceCode, "backward")
		sb.Range.StartByte -= reduceBytes
		sb.Range.StartPoint.Column = 0
		sb.Range.EndByte += countBytesInLines(sb.Range.EndByte, numContextLines, sourceCode, "forward")
		// using length since end is exclusive
		sb.Range.EndPoint.Column = uint32(len(sourceCodeLines[sb.Range.EndPoint.Row]))

		// startByte is in the middle of a line, so we need to go back to the start of the line
		if sb.Range.StartByte >= 1 && sourceCode[sb.Range.StartByte-1] != '\n' {
			sb.Range.StartByte -= countBytesInLines(sb.Range.StartByte, 1, sourceCode, "backward") + 1
		}

		// endByte is in the middle of a line, so we need to go to the end of the line
		if sb.Range.EndByte >= 1 && sourceCode[sb.Range.EndByte-1] != '\n' {
			sb.Range.EndByte += countBytesInLines(sb.Range.EndByte, 1, sourceCode, "forward")
		}

		if sb.Range.EndByte >= 1 && sourceCode[sb.Range.EndByte-1] == '\n' && int(sb.Range.EndByte) < len(sourceCode) {
			sb.Range.EndPoint.Column += 1 // one past the *next* newline
		}

		sourceBlocks[i] = sb
	}

	return sourceBlocks
}
