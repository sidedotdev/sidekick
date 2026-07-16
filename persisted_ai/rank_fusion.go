package persisted_ai

import "sort"

// rrf_k is the constant used in Reciprocal Rank Fusion calculations.
// Higher values diminish the impact of high rankings.
const rrf_k = 60

// BaselineRankWeight is the default weight given to a ranked list when no
// other relative weighting applies. It is the implicit weight of the baseline
// RankQuery in RAG rank fusion; other weights are interpreted relative to it.
const BaselineRankWeight = 1.0

// bm25RankWeight scales lexical BM25 rankings relative to embedding-based
// rankings when fused. Each weighted query's BM25 ranking contributes with
// its query weight multiplied by this factor.
const bm25RankWeight = 1.0

// WeightedRanking is a ranked list of items (ordered most-relevant-first)
// contributing to a fused result with the given weight.
type WeightedRanking struct {
	Items  []string
	Weight float64
}

// FuseResults combines multiple weighted ranked lists into a single ranked
// list using Reciprocal Rank Fusion. Each list contributes
// Weight * 1.0/(rrf_k+rank+1) to each item's accumulated score, with rank
// starting at 0. Items not present in a list contribute nothing for that list.
// Tie-breaking is by accumulated score descending, then earliest first-position
// across all lists, then item string ascending. Given a single entry the
// entry's Items are returned unchanged regardless of weight.
func FuseResults(rankings []WeightedRanking) []string {
	if len(rankings) == 0 {
		return nil
	}
	if len(rankings) == 1 {
		return rankings[0].Items
	}

	scores := make(map[string]float64)
	firstPos := make(map[string]int)
	totalPos := 0

	for _, ranking := range rankings {
		for rank, item := range ranking.Items {
			scores[item] += ranking.Weight * 1.0 / float64(rrf_k+rank+1)
			if _, seen := firstPos[item]; !seen {
				firstPos[item] = totalPos
			}
			totalPos++
		}
	}

	type scoredItem struct {
		item     string
		score    float64
		firstPos int
	}
	items := make([]scoredItem, 0, len(scores))
	for item, score := range scores {
		items = append(items, scoredItem{item, score, firstPos[item]})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].score != items[j].score {
			return items[i].score > items[j].score
		}
		if items[i].firstPos != items[j].firstPos {
			return items[i].firstPos < items[j].firstPos
		}
		return items[i].item < items[j].item
	})

	result := make([]string, len(items))
	for i, item := range items {
		result[i] = item.item
	}
	return result
}
