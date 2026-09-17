package credential_proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"sidekick/common"
)

// Proxy provisioning supplied by the host to sandbox clients. Only
// these two variables are injected; neither carries provider credentials.
const (
	TokenEnvVar   = "SIDE_CREDENTIAL_PROXY_TOKEN"
	ProxiesEnvVar = "SIDE_CREDENTIAL_PROXIES"
)

// PathPrefix precedes the proxy name in every forwarded request path.
const PathPrefix = "/credential-proxies/"

// SideBinding is non-secret metadata describing which host credentials a
// side proxy was bound to at activation, so clients can apply the request
// semantics of the effective mode.
type SideBinding struct {
	Provider string `json:"provider"`
	Profile  string `json:"profile"`
	AuthType string `json:"authType"`
	// ConfiguredAuthType is the definition's own selector ("any" when
	// omitted) so an explicit proxy and an "any" proxy that resolved to the
	// same mode stay distinguishable.
	ConfiguredAuthType string `json:"configuredAuthType"`
}

// ProvisionedProxy describes one activated proxy as seen from the sandbox.
// URL is the proxy base; callers append the upstream-relative path.
type ProvisionedProxy struct {
	URL      string       `json:"url"`
	Upstream string       `json:"upstream"`
	Side     *SideBinding `json:"side,omitempty"`
}

type Provisioning struct {
	Token   string
	Proxies map[string]ProvisionedProxy
}

// ProvisioningFromEnv reads proxy provisioning from the environment. It reports false only when neither
// variable is present; present-but-empty, partial or malformed provisioning is
// an error so a sandbox never silently falls back to local credentials.
func ProvisioningFromEnv() (Provisioning, bool, error) {
	token, tokenPresent := os.LookupEnv(TokenEnvVar)
	raw, proxiesPresent := os.LookupEnv(ProxiesEnvVar)
	if !tokenPresent && !proxiesPresent {
		return Provisioning{}, false, nil
	}
	if token == "" || raw == "" {
		return Provisioning{}, true, errors.New("incomplete credential proxy configuration")
	}
	var proxies map[string]ProvisionedProxy
	if err := json.Unmarshal([]byte(raw), &proxies); err != nil || len(proxies) == 0 {
		return Provisioning{}, true, errors.New("invalid credential proxy configuration")
	}
	for name, proxy := range proxies {
		if err := validateProxyURL(name, proxy.URL); err != nil {
			return Provisioning{}, true, err
		}
		if proxy.Side != nil && !proxy.Side.valid() {
			return Provisioning{}, true, errors.New("invalid credential proxy configuration")
		}
	}
	return Provisioning{Token: token, Proxies: proxies}, true, nil
}

// valid requires a resolved effective mode that agrees with an explicitly
// configured one; "any" is only ever a configured selector.
func (s SideBinding) valid() bool {
	if s.Provider == "" || s.Profile == "" {
		return false
	}
	if s.AuthType != common.CredentialProxyAuthTypeAPI && s.AuthType != common.CredentialProxyAuthTypeSubscription {
		return false
	}
	switch s.ConfiguredAuthType {
	case common.CredentialProxyAuthTypeAny:
		return true
	case common.CredentialProxyAuthTypeAPI, common.CredentialProxyAuthTypeSubscription:
		return s.ConfiguredAuthType == s.AuthType
	default:
		return false
	}
}

// canonicalProfile mirrors the host's profile identity rules: profile ids
// are case-insensitive and an empty id means the default profile.
func canonicalProfile(profile string) string {
	if profile == "" {
		profile = common.DefaultProfileId
	}
	return strings.ToLower(profile)
}

func validateProxyURL(name, address string) error {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || u.Path != PathPrefix+name {
		return errors.New("invalid credential proxy configuration")
	}
	return nil
}

// SelectSide picks the proxy matching a request's provider, profile and auth
// type. Explicit modes prefer an explicitly configured proxy, then an "any"
// proxy that resolved to that mode. "any" prefers an "any" proxy, then
// subscription, then api, mirroring the host's own fallback order.
// Selection depends only on binding metadata, never on proxy names: two
// proxies with the same (provider, profile, configured mode) identity are
// ambiguous and fail closed, as do malformed bindings.
func (p Provisioning) SelectSide(provider, profile, authType string) (ProvisionedProxy, error) {
	profile = canonicalProfile(profile)
	byConfigured := make(map[string]ProvisionedProxy)
	for _, proxy := range p.Proxies {
		side := proxy.Side
		if side == nil || !strings.EqualFold(side.Provider, provider) || canonicalProfile(side.Profile) != profile {
			continue
		}
		if !side.valid() {
			return ProvisionedProxy{}, fmt.Errorf("invalid credential proxy binding for provider %q profile %q", provider, profile)
		}
		if _, duplicate := byConfigured[side.ConfiguredAuthType]; duplicate {
			return ProvisionedProxy{}, fmt.Errorf("ambiguous credential proxies for provider %q profile %q auth type %q", provider, profile, side.ConfiguredAuthType)
		}
		byConfigured[side.ConfiguredAuthType] = proxy
	}
	var candidates []string
	switch authType {
	case common.CredentialProxyAuthTypeAPI, common.CredentialProxyAuthTypeSubscription:
		candidates = []string{authType, common.CredentialProxyAuthTypeAny}
	case common.CredentialProxyAuthTypeAny, "":
		authType = common.CredentialProxyAuthTypeAny
		candidates = []string{common.CredentialProxyAuthTypeAny, common.CredentialProxyAuthTypeSubscription, common.CredentialProxyAuthTypeAPI}
	default:
		return ProvisionedProxy{}, fmt.Errorf("unsupported auth type %q", authType)
	}
	for _, configured := range candidates {
		proxy, ok := byConfigured[configured]
		if !ok {
			continue
		}
		// An "any" proxy only satisfies an explicit request when it resolved
		// to that mode; for "any" requests, any resolved mode is acceptable.
		if authType == common.CredentialProxyAuthTypeAny || proxy.Side.AuthType == authType {
			return proxy, nil
		}
	}
	return ProvisionedProxy{}, fmt.Errorf("%w for provider %q profile %q auth type %q", ErrNoProxyProvisioned, provider, profile, authType)
}

// ErrNoProxyProvisioned distinguishes an absent matching proxy from
// malformed or ambiguous provisioning, which callers must never treat as benign.
var ErrNoProxyProvisioned = errors.New("no credential proxy provisioned")
