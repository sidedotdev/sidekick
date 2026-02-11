package dev

import (
	"encoding/json"
	"errors"
	"fmt"
	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/persisted_ai"
	"sidekick/utils"
	"slices"
	"strings"
	"unicode"

	"github.com/invopop/jsonschema"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/workflow"
)

type BranchNameRequest struct {
	Requirements    string   `json:"requirements" jsonschema:"description=The requirements text to use as context for generating branch names"`
	Hints           string   `json:"editHints" jsonschema:"description=Additional hints that might provide context for branch name generation"`
	ExcludeBranches []string `json:"-"` // Branch names to exclude (e.g., from prior failed attempts)
}

type SubmitBranchNamesParams struct {
	Candidates []string `json:"candidates" jsonschema:"description=List of 3 candidate branch name suffixes in kebab-case format\\, ordered best to worst\\, each 2-4 words long\\, without any prefix or slash etc"`
}

var generateBranchNamesTool = llm.Tool{
	Name:        "submit_branch_names",
	Description: "Generate meaningful, descriptive, human-readable branch names based on requirements and edit hints. Returns 3 candidates in kebab-case format.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&SubmitBranchNamesParams{}),
}

var generateBranchNamesPrompt = panicParseMustache(promptsFS, "branch_names/generate")

// GenerateBranchName generates a unique branch name using LLM-generated candidates or falls back
// to a name derived from requirements if generation fails. The returned name includes the 'side/' prefix.
func GenerateBranchName(eCtx flow_action.ExecContext, req BranchNameRequest) (string, error) {
	var output env.EnvRunCommandActivityOutput
	err := workflow.ExecuteActivity(eCtx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer: *eCtx.EnvContainer,
		Command:      "git",
		Args:         []string{"branch", "--list", "--format=%(refname:short)"},
	}).Get(eCtx, &output)
	if err != nil {
		return "", fmt.Errorf("failed to get existing branches: %v", err)
	}
	branchesSlice := strings.Split(strings.TrimSpace(output.Stdout), "\n")
	branchesSlice = utils.Map(branchesSlice, strings.TrimSpace)
	branches := make(map[string]bool)
	for _, branch := range branchesSlice {
		branches[branch] = true
	}
	// Also exclude branches from prior failed attempts (race condition handling)
	for _, branch := range req.ExcludeBranches {
		branches[branch] = true
	}

	// Try LLM generation with retries
	candidates, err := generateBranchNameCandidates(eCtx, req)
	if err != nil {
		log.Error().Err(err).Msg("failed to generate branch names via LLM")
	}

	// Try each candidate
	for _, suffix := range candidates {
		if !validateBranchNameSuffix(suffix) {
			continue
		}

		branchName := branchNamePrefix + suffix
		if _, exists := branches[branchName]; !exists {
			return branchName, nil
		}
	}

	// Try with numeric suffix if needed
	for i := 2; i <= maxNumericSuffix; i++ {
		for _, suffix := range candidates {
			branchName := fmt.Sprintf("%s%s-%d", branchNamePrefix, suffix, i)
			if _, exists := branches[branchName]; !exists {
				return branchName, nil
			}
		}
	}

	// Fallback: use first few words from requirements
	// NOTE: this fallback should be only be if we got no candidates, i.e. llm
	// call failed, or if all candidates are taken, even with numeric suffixes
	suffix := generateFallbackSuffix(req.Requirements)
	if !validateBranchNameSuffix(suffix) {
		return "", fmt.Errorf("failed to generate valid branch name after all attempts")
	}
	branchName := branchNamePrefix + suffix
	if _, exists := branches[branchName]; !exists {
		return branchName, nil
	}

	return "", fmt.Errorf("failed to generate unique branch name")
}

func generateBranchNameCandidates(eCtx flow_action.ExecContext, req BranchNameRequest) ([]string, error) {
	reqMap := make(map[string]any)
	utils.Transcode(req, &reqMap)
	chatHistory := NewVersionedChatHistory(eCtx, eCtx.WorkspaceId)
	chatHistory.Append(llm.ChatMessage{
		Role:    llm.ChatMessageRoleUser,
		Content: RenderPrompt(generateBranchNamesPrompt, reqMap),
	})

	modelConfig := eCtx.GetModelConfig(common.SummarizationKey, 0, "small")

	var branchResp SubmitBranchNamesParams
	attempts := 0
	for {
		actionCtx := eCtx.NewActionContext("generate.branch_names")
		msgResponse, err := persisted_ai.ForceToolCallWithTrackOptionsV2(actionCtx, flow_action.TrackOptions{FailuresOnly: true}, modelConfig, chatHistory, &generateBranchNamesTool)
		if err != nil {
			return nil, fmt.Errorf("failed to force tool call: %v", err)
		}

		toolCalls := msgResponse.GetMessage().GetToolCalls()
		toolCall := toolCalls[0]
		jsonStr := toolCall.Arguments
		err = json.Unmarshal([]byte(llm.RepairJson(jsonStr)), &branchResp)
		if err == nil {
			// if any are valid, we're done
			if slices.ContainsFunc(branchResp.Candidates, validateBranchNameSuffix) {
				break
			}
			err = errors.New("Those candidates are invalid, not following the presribed format. Let's try that again, change it up.")
		}

		attempts++
		if attempts >= maxBranchNameGenerationAttempts {
			return nil, fmt.Errorf("%w: %v", llm.ErrToolCallUnmarshal, err)
		}

		// Get LLM to self-correct with error message
		newMessage := llm.ChatMessage{
			IsError:    true,
			Role:       llm.ChatMessageRoleTool,
			Content:    err.Error(),
			Name:       toolCall.Name,
			ToolCallId: toolCall.Id,
		}
		chatHistory.Append(newMessage)
	}

	if len(branchResp.Candidates) == 0 {
		return nil, fmt.Errorf("no branch name candidates generated")
	}

	return branchResp.Candidates, nil
}

func generateFallbackSuffix(requirements string) string {
	// Split requirements into words
	words := strings.FieldsFunc(requirements, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})

	// Take first few words, convert to lowercase
	var suffix []string
	for i := 0; i < len(words) && i < 4; i++ {
		word := strings.ToLower(words[i])
		if len(word) > 0 {
			suffix = append(suffix, word)
		}
		if len(strings.Join(suffix, "-")) >= maxBranchLength {
			break
		}
	}

	return strings.Join(suffix, "-")
}

const (
	branchNamePrefix                = "side/"
	maxBranchLength                 = 80
	maxBranchNameGenerationAttempts = 3
	maxNumericSuffix                = 9
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
