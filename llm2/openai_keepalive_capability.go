package llm2

import (
	"os"
	"sidekick/common"
	"strconv"
	"strings"
)

// ExpectsKeepalives assumes support for the direct-API GPT-5.6 family and GPT-6+.
// Keepalives have only been observed with gpt-5.6-sol, NOT with GPT-6+;
// extending support to those models is a policy assumption, not a measurement.
// Subscription and proxy routes have independent behavior.
func (p OpenAIResponsesProvider) ExpectsKeepalives(request StreamRequest) bool {
	model := request.Options.Model
	if model == "" {
		model = p.DefaultModel
	}
	if model != "gpt-5.6" && !strings.HasPrefix(model, "gpt-5.6-") {
		version, ok := strings.CutPrefix(model, "gpt-")
		if !ok {
			return false
		}
		majorText, _, _ := strings.Cut(version, ".")
		majorText, _, _ = strings.Cut(majorText, "-")
		major, err := strconv.Atoi(majorText)
		if err != nil || major < 6 {
			return false
		}
	}
	baseURL := p.BaseURL
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_BASE_URL")
	}
	if baseURL != "" && strings.TrimRight(baseURL, "/") != "https://api.openai.com/v1" {
		return false
	}
	authType := common.NormalizeProviderAuthType(string(p.AuthType))
	if authType == common.ProviderAuthTypeSubscription {
		return false
	}
	if authType == common.ProviderAuthTypeAPI {
		return true
	}
	providerName := request.Options.ModelConfig.NormalizedProviderName()
	if providerName != "OPENAI" {
		return true
	}
	credentials, err := openAICredentialsForRequest(request.SecretManager, providerName, authType)
	return err == nil && !credentials.useOAuth
}
