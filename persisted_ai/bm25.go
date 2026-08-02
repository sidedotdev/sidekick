package persisted_ai

import (
	"math"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode"
)

const (
	bm25K1 = 1.2
	bm25B  = 0.75
	// bm25NGramSize is the character n-gram length emitted alongside whole
	// subtokens so that partially matching keywords still score.
	bm25NGramSize = 3
)

// tokenizeBM25 lowercases text, extracts alphanumeric tokens, splits
// camelCase identifiers into subtokens (snake_case splits naturally since
// underscores are separators), and emits character n-grams for subtokens
// longer than bm25NGramSize.
func tokenizeBM25(text string) []string {
	var tokens []string
	appendToken := func(sub string) {
		if sub == "" {
			return
		}
		sub = strings.ToLower(sub)
		tokens = append(tokens, sub)
		if len(sub) > bm25NGramSize {
			for i := 0; i+bm25NGramSize <= len(sub); i++ {
				tokens = append(tokens, sub[i:i+bm25NGramSize])
			}
		}
	}

	runes := []rune(text)
	start := -1
	flush := func(end int) {
		if start >= 0 {
			for _, sub := range splitCamelCase(runes[start:end]) {
				appendToken(sub)
			}
			start = -1
		}
	}
	for i, r := range runes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if start < 0 {
				start = i
			}
		} else {
			flush(i)
		}
	}
	flush(len(runes))
	return tokens
}

// splitCamelCase splits a token at lower-to-upper transitions and at the end
// of acronym runs followed by lowercase (e.g. HTTPServer -> HTTP, Server).
func splitCamelCase(runes []rune) []string {
	if len(runes) < 2 {
		return []string{string(runes)}
	}
	var subs []string
	start := 0
	for i := 1; i < len(runes); i++ {
		lowerToUpper := !unicode.IsUpper(runes[i-1]) && unicode.IsUpper(runes[i])
		acronymEnd := unicode.IsUpper(runes[i-1]) && unicode.IsUpper(runes[i]) &&
			i+1 < len(runes) && unicode.IsLower(runes[i+1])
		if lowerToUpper || acronymEnd {
			subs = append(subs, string(runes[start:i]))
			start = i
		}
	}
	return append(subs, string(runes[start:]))
}

// RankBM25 scores documents against the query using BM25 over an index built
// on the fly, returning document indices ordered most-relevant-first.
// Zero-score documents are excluded and ties break by original index
// ascending. Document tokenization runs on a bounded worker pool.
func RankBM25(query string, documents []string) []int {
	queryTokens := tokenizeBM25(query)
	if len(queryTokens) == 0 || len(documents) == 0 {
		return nil
	}

	docTermCounts := make([]map[string]int, len(documents))
	docLengths := make([]int, len(documents))
	indices := make(chan int)
	var wg sync.WaitGroup
	for range min(runtime.NumCPU(), len(documents)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range indices {
				tokens := tokenizeBM25(documents[i])
				counts := make(map[string]int, len(tokens))
				for _, token := range tokens {
					counts[token]++
				}
				docTermCounts[i] = counts
				docLengths[i] = len(tokens)
			}
		}()
	}
	for i := range documents {
		indices <- i
	}
	close(indices)
	wg.Wait()

	totalLength := 0
	for _, length := range docLengths {
		totalLength += length
	}
	if totalLength == 0 {
		return nil
	}
	avgLength := float64(totalLength) / float64(len(documents))

	queryTermCounts := make(map[string]int, len(queryTokens))
	for _, token := range queryTokens {
		queryTermCounts[token]++
	}

	numDocs := float64(len(documents))
	scores := make([]float64, len(documents))
	for term, queryTermFreq := range queryTermCounts {
		df := 0
		for _, counts := range docTermCounts {
			if counts[term] > 0 {
				df++
			}
		}
		if df == 0 {
			continue
		}
		// Lucene-style smoothed IDF: unlike the classic formulation it stays
		// positive when a term appears in most documents, so common terms
		// never zero out matches in small corpora.
		idf := math.Log(1 + (numDocs-float64(df)+0.5)/(float64(df)+0.5))
		for i, counts := range docTermCounts {
			tf := float64(counts[term])
			if tf == 0 {
				continue
			}
			norm := bm25K1 * (1 - bm25B + bm25B*float64(docLengths[i])/avgLength)
			scores[i] += float64(queryTermFreq) * idf * tf * (bm25K1 + 1) / (tf + norm)
		}
	}

	ranked := make([]int, 0, len(documents))
	for i, score := range scores {
		if score > 0 {
			ranked = append(ranked, i)
		}
	}
	// ranked starts in ascending index order, so a stable sort on score alone
	// yields deterministic index-ascending tie-breaking
	sort.SliceStable(ranked, func(a, b int) bool {
		return scores[ranked[a]] > scores[ranked[b]]
	})
	return ranked
}
