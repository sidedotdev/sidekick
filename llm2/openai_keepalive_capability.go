package llm2

import (
	"os"
	"sidekick/common"
	"strconv"
	"strings"
)

// ExpectsKeepalives reports whether the route's upstream is known to keep
// streams alive, so prolonged silence indicates a hung stream.
//
// The ChatGPT codex backend (subscription auth) sends ": keep-alive" SSE
// comments every ~5-11s while reasoning, observed with gpt-5.5 and
// gpt-5.6-sol via scripts/probe_openai_keepalive, so all models are assumed
// to get them. Proxy routes have independent behavior.
func (p OpenAIResponsesProvider) ExpectsKeepalives(request StreamRequest) bool {
	providerName := request.Options.ModelConfig.NormalizedProviderName()
	authType := common.NormalizeProviderAuthType(string(p.AuthType))
	if authType == common.ProviderAuthTypeAny {
		if providerName != "OPENAI" {
			authType = common.ProviderAuthTypeAPI
		} else {
			credentials, err := openAICredentialsForRequest(request.SecretManager, providerName, authType)
			if err != nil {
				return false
			}
			authType = credentials.authType
		}
	}

	switch authType {
	case common.ProviderAuthTypeSubscription:
		return p.BaseURL == "" || strings.TrimRight(p.BaseURL, "/") == openAIChatGPTResponsesBaseURL
	case common.ProviderAuthTypeAPI:
		return p.apiExpectsKeepalives(request)
	default:
		return false
	}
}

// apiExpectsKeepalives assumes {"type":"keepalive"} events from the OpenAI API
// for the GPT-5.6 family and GPT-6+. This is a policy assumption: a
// gpt-5.6-sol probe has yet to capture one, though it never went silent long
// enough to need one.
func (p OpenAIResponsesProvider) apiExpectsKeepalives(request StreamRequest) bool {
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
	return baseURL == "" || strings.TrimRight(baseURL, "/") == "https://api.openai.com/v1"
}
