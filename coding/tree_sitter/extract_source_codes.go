package tree_sitter

import "regexp"

type SourceCode struct {
	LanguageName         string
	OriginalLanguageName string /* TODO: move to CodeBlock and rename appropriately */
	Content              string
}

func ExtractSourceCodes(allContent string) []SourceCode {
	r := regexp.MustCompile("```" + `(\w+?)\n((.|\n)*?\n)` + "```")
	matches := r.FindAllStringSubmatch(allContent, -1)
	if len(matches) == 0 {
		return nil
	}
	var sourceCodes []SourceCode
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		originalLanguageName := match[1]
		sourceCodes = append(sourceCodes, SourceCode{
			LanguageName:         normalizeLanguageName(originalLanguageName),
			OriginalLanguageName: originalLanguageName,
			Content:              match[2],
		})
	}
	return sourceCodes
}
