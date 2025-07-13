package persisted_ai

import "sort"

// rrf_k is the constant used in Reciprocal Rank Fusion calculations.
// Higher values diminish the impact of high rankings.
const rrf_k = 60

// FuseResultsRRF combines multiple ranked lists into a single ranked list using
// Reciprocal Rank Fusion. Each input list should be ordered by relevance (most
// relevant first). Items not present in a list are considered to have infinite rank.
func FuseResultsRRF(rankedLists [][]string) []string {
	if len(rankedLists) == 0 {
		return nil
	}
	if len(rankedLists) == 1 {
		return rankedLists[0]
	}

	// Track scores and first positions for each item
	scores := make(map[string]float64)
	firstPos := make(map[string]int)
	totalPos := 0

	// Calculate RRF scores and track first positions
	for _, list := range rankedLists {
		for rank, item := range list {
			// Use 1-based ranking in RRF formula
			scores[item] += 1.0 / float64(rrf_k+rank+1)

			// Record first position if not seen before
			if _, seen := firstPos[item]; !seen {
				firstPos[item] = totalPos
			}
			totalPos++
		}
	}

	// Convert to slice for sorting
	type scoredItem struct {
		item     string
		score    float64
		firstPos int
	}
	items := make([]scoredItem, 0, len(scores))
	for item, score := range scores {
		items = append(items, scoredItem{item, score, firstPos[item]})
	}

	// Sort by score descending, break ties by first position, then by item string
	sort.Slice(items, func(i, j int) bool {
		if items[i].score != items[j].score {
			return items[i].score > items[j].score
		}
		if items[i].firstPos != items[j].firstPos {
			return items[i].firstPos < items[j].firstPos
		}
		return items[i].item < items[j].item
	})

	// Extract final ordered list
	result := make([]string, len(items))
	for i, item := range items {
		result[i] = item.item
	}
	return result
}
