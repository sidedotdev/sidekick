package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/common"
	"sidekick/llm"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
	"strings"
	"unicode"

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

var generateBranchNamesPrompt = panicParseMustache(promptsFS, "branch_names/generate")

// GenerateBranchName generates a unique branch name using LLM-generated candidates or falls back
// to a name derived from requirements if generation fails. The returned name includes the 'side/' prefix.
func GenerateBranchName(dCtx DevContext, req BranchNameRequest, existingBranches []string) (string, error) {
	// Try LLM generation with retries
	for attempt := 0; attempt < maxRetries; attempt++ {
		candidates, err := generateBranchNameCandidates(dCtx, req)
		if err != nil {
			continue
		}

		// Try each candidate
		for _, suffix := range candidates {
			if !validateBranchNameSuffix(suffix) {
				continue
			}

			branchName := branchNamePrefix + suffix
			if !containsBranchName(existingBranches, branchName) {
				return branchName, nil
			}
		}
	}

	// Fallback: use first few words from requirements
	suffix := generateFallbackSuffix(req.Requirements)
	if !validateBranchNameSuffix(suffix) {
		return "", fmt.Errorf("failed to generate valid branch name after all attempts")
	}

	// Try with numeric suffix if needed
	branchName := branchNamePrefix + suffix
	if !containsBranchName(existingBranches, branchName) {
		return branchName, nil
	}

	// Add numeric suffix
	for i := 1; i <= maxNumericSuffix; i++ {
		numericBranchName := fmt.Sprintf("%s-%d", branchName, i)
		if len(numericBranchName) > maxBranchLength {
			return "", fmt.Errorf("failed to generate unique branch name within length limit")
		}
		if !containsBranchName(existingBranches, numericBranchName) {
			return numericBranchName, nil
		}
	}

	return "", fmt.Errorf("failed to generate unique branch name")
}

func generateBranchNameCandidates(dCtx DevContext, req BranchNameRequest) ([]string, error) {
	chatHistory := []llm.ChatMessage{
		{
			Role:    llm.ChatMessageRoleSystem,
			Content: RenderPrompt(generateBranchNamesPrompt, req),
		},
	}

	modelConfig := dCtx.GetModelConfig(common.JudgingKey, 0, "default")
	params := llm.ToolChatParams{Messages: chatHistory, ModelConfig: modelConfig}

	var branchResp BranchNameResponse
	attempts := 0
	for {
		actionCtx := dCtx.ExecContext.NewActionContext("generate_branch_names")
		chatResponse, err := persisted_ai.ForceToolCall(actionCtx, dCtx.LLMConfig, &params, &generateBranchNamesTool)
		if err != nil {
			return nil, fmt.Errorf("failed to force tool call: %v", err)
		}

		toolCall := chatResponse.ToolCalls[0]
		jsonStr := toolCall.Arguments
		err = json.Unmarshal([]byte(llm.RepairJson(jsonStr)), &branchResp)
		if err == nil {
			break
		}

		attempts++
		if attempts >= 3 {
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
		params.Messages = append(params.Messages, newMessage)
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
		if len(suffix) >= 2 {
			break
		}
	}

	if len(suffix) < 2 {
		return "new-branch"
	}

	return strings.Join(suffix, "-")
}

func containsBranchName(branches []string, name string) bool {
	for _, branch := range branches {
		if branch == name {
			return true
		}
	}
	return false
}

const (
	branchNamePrefix = "side/"
	maxBranchLength  = 80
	maxRetries       = 3
	maxNumericSuffix = 999
)

type contextKey string

const (
	toolChatterKey contextKey = "tool_chatter"
	secretsKey     contextKey = "secrets"
)

func GetToolChatter(ctx context.Context) llm.ToolChatter {
	if chatter, ok := ctx.Value(toolChatterKey).(llm.ToolChatter); ok {
		return chatter
	}
	return nil
}

func GetSecrets(ctx context.Context) secret_manager.SecretManagerContainer {
	if secrets, ok := ctx.Value(secretsKey).(secret_manager.SecretManagerContainer); ok {
		return secrets
	}
	return secret_manager.SecretManagerContainer{}
}

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
