package dev

import (
	"sidekick/llm"

	"github.com/invopop/jsonschema"
)

type BranchNameRequest struct {
	Requirements string `json:"requirements" jsonschema:"description=The requirements text to use as context for generating branch names"`
	EditHints    string `json:"edit_hints" jsonschema:"description=Additional edit hints that provide context for branch name generation"`
}

type BranchNameResponse struct {
	Candidates []string `json:"candidates" jsonschema:"description=List of 3 candidate branch name suffixes in kebab-case format\\, each 2-4 words long\\, without the 'side/' prefix"`
}

var generateBranchNamesTool = llm.Tool{
	Name:        "generate_branch_names",
	Description: "Generate meaningful, human-readable branch name suffixes based on requirements and edit hints. Returns 3 candidates in kebab-case format.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&BranchNameRequest{}),
}

const (
	branchNamePrefix = "side/"
	maxBranchLength  = 80
)

// validateBranchNameSuffix checks if a branch name suffix meets the required format:
// - Must be in kebab-case (lowercase with hyphens)
// - Must contain 2-4 words
// - Must not exceed maxBranchLength when combined with prefix
// - Must only contain alphanumeric characters and hyphens
func validateBranchNameSuffix(suffix string) bool {
	if len(suffix) == 0 || len(branchNamePrefix+suffix) > maxBranchLength {
		return false
	}

	// Count words by counting hyphens + 1
	wordCount := 1
	for i, c := range suffix {
		switch {
		case c >= 'a' && c <= 'z':
			continue
		case c >= '0' && c <= '9':
			continue
		case c == '-':
			// Disallow consecutive hyphens or hyphen at start/end
			if i == 0 || i == len(suffix)-1 || suffix[i-1] == '-' {
				return false
			}
			wordCount++
		default:
			return false
		}
	}

	return wordCount >= 2 && wordCount <= 4
}
