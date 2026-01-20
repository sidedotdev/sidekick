package common

import (
	"bufio"
	"regexp"
	"strconv"
	"strings"
)

// ExtractSequenceNumbersFromReportContent extracts unique edit block sequence numbers
// from EditBlockReport content formatted as "- edit_block:N application ...".
func ExtractSequenceNumbersFromReportContent(content string) []int {
	re := regexp.MustCompile(`-\s*edit_block:(\d+)\s*application.*`)

	scanner := bufio.NewScanner(strings.NewReader(content))
	seenNumbers := make(map[int]bool)
	var uniqueSequenceNumbers []int

	for scanner.Scan() {
		line := scanner.Text()
		matches := re.FindStringSubmatch(line)

		if len(matches) > 1 {
			if num, err := strconv.Atoi(matches[1]); err == nil {
				if !seenNumbers[num] {
					seenNumbers[num] = true
					uniqueSequenceNumbers = append(uniqueSequenceNumbers, num)
				}
			}
		}
	}

	return uniqueSequenceNumbers
}

// ExtractEditBlockSequenceNumbers extracts sequence numbers from edit block definitions
// in text. It looks for "edit_block:N" patterns within code fences.
func ExtractEditBlockSequenceNumbers(text string) []int {
	re := regexp.MustCompile(`(?m)^edit_block:(\d+)\s*$`)
	matches := re.FindAllStringSubmatch(text, -1)

	var seqNums []int
	for _, match := range matches {
		if len(match) > 1 {
			if num, err := strconv.Atoi(match[1]); err == nil {
				seqNums = append(seqNums, num)
			}
		}
	}
	return seqNums
}
