package utils

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"math"
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
var spacingReplacer = strings.NewReplacer(" ", "", "\t", "")

func StringSimilarity(s1, s2 string) float64 {
	if s1 == s2 {
		return 1.0
	}

	scores := []float64{}

	if strings.TrimSpace(s1) == strings.TrimSpace(s2) {
		scores = append(scores, 0.95)
	}

	// Remove all whitespace
	s1NoSpacing := spacingReplacer.Replace(s1)
	s2NoSpacing := spacingReplacer.Replace(s2)

	// Baseline score for strings identical when whitespace is removed
	if s1NoSpacing == s2NoSpacing {
		scores = append(scores, 0.9)
	}

	// Calculate Levenshtein distance
	simOriginal := strutil.Similarity(s1, s2, distanceMetric)
	if !math.IsNaN(simOriginal) {
		scores = append(scores, simOriginal)
	}

	// Calculate Levenshtein distance with whitespace removed and weighted
	// average (giving more weight to the no-whitespace similarity)
	simNoWhitespace := strutil.Similarity(s1NoSpacing, s2NoSpacing, distanceMetric)
	weightedAvg := 0.4*simOriginal + 0.6*simNoWhitespace
	if !math.IsNaN(weightedAvg) {
		scores = append(scores, weightedAvg)
	}

	maxScore := 0.0
	for _, score := range scores {
		maxScore = max(maxScore, score)
	}

	return maxScore
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

// Hash256 takes a string and returns its SHA256 hash as a base64 encoded string
func Hash256(s string) string {
	// Compute SHA256 hash of the input string
	hash := sha256.Sum256([]byte(s))

	// Convert the hash to a base64 encoded string
	return base64.StdEncoding.EncodeToString(hash[:])
}