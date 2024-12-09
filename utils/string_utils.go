package utils

import (
	"crypto/sha256"
	"encoding/binary"
	"strings"

	"github.com/adrg/strutil"
	"github.com/adrg/strutil/metrics"
)

func FirstN(s string, n int) string {
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}

var distanceMetric = metrics.NewLevenshtein()

func StringSimilarity(s1, s2 string) float64 {
	if s1 == s2 {
		return 1.0
	} // faster and fixes NaN issue with empty strings
	if strings.TrimSpace(s1) == strings.TrimSpace(s2) {
		return 0.95
	} // high score if exact match other than surrounding whitespace
	similarity := strutil.Similarity(s1, s2, distanceMetric)
	return similarity
}

// SliceLevenshtein calculates the Levenshtein distance between two slices of strings element by element.
// It returns both the edit distance and the edit distance ratio based on the larger slice size.
func SliceLevenshtein(s1, s2 []string) (int, float64) {
	m := len(s1)
	n := len(s2)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}

	for i := 0; i <= m; i++ {
		dp[i][0] = i
	}
	for j := 0; j <= n; j++ {
		dp[0][j] = j
	}

	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if s1[i-1] == s2[j-1] {
				dp[i][j] = dp[i-1][j-1]
			} else {
				insert := dp[i][j-1] + 1
				delete := dp[i-1][j] + 1
				dp[i][j] = min(insert, delete)
			}
		}
	}

	editDistance := dp[m][n]
	maxLen := max(m, n)
	editDistanceRatio := float64(editDistance) / float64(maxLen)

	return editDistance, editDistanceRatio
}

// Hash64 takes a string and returns a 64-bit hash using SHA256
func Hash64(s string) uint64 {
	// Compute SHA256 hash of the input string
	hash := sha256.Sum256([]byte(s))

	// Convert the first 8 bytes of the hash to a uint64
	return binary.BigEndian.Uint64(hash[:8])
}
