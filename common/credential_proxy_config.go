package common

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Authentication modes for side-managed credential proxies.
const (
	CredentialProxyAuthTypeAny          = "any"
	CredentialProxyAuthTypeAPI          = "api"
	CredentialProxyAuthTypeSubscription = "subscription"
)

// Built-in destinations for side-managed OpenAI authentication. Subscription
// and API credentials are accepted by different hosts, so each mode has its
// own base.
const (
	OpenAIAPIUpstream          = "https://api.openai.com/v1"
	OpenAISubscriptionUpstream = "https://chatgpt.com/backend-api/codex"
)

var sideDefaultUpstreams = map[string]map[string]string{
	"openai": {
		CredentialProxyAuthTypeAPI:          OpenAIAPIUpstream,
		CredentialProxyAuthTypeSubscription: OpenAISubscriptionUpstream,
	},
}

type CredentialProxyConfig struct {
	// Upstream is the trusted base URL for every request through the proxy.
	// For side authentication it is optional and, when set, applies to every
	// mode the proxy may resolve to.
	Upstream string `toml:"upstream,omitempty" koanf:"upstream,omitempty" json:"upstream,omitempty"`
	// Upstreams overrides the built-in per-mode destination of a side proxy.
	Upstreams map[string]string         `toml:"upstreams,omitempty" koanf:"upstreams,omitempty" json:"upstreams,omitempty"`
	Auth      CredentialProxyAuthConfig `toml:"auth" koanf:"auth" json:"auth"`
}

type CredentialProxyAuthConfig struct {
	Bearer  *CredentialSource              `toml:"bearer,omitempty" koanf:"bearer,omitempty" json:"bearer,omitempty"`
	Headers map[string]CredentialSource    `toml:"headers,omitempty" koanf:"headers,omitempty" json:"headers,omitempty"`
	Side    *CredentialProxySideAuthConfig `toml:"side,omitempty" koanf:"side,omitempty" json:"side,omitempty"`
}

// CredentialProxySideAuthConfig selects credentials Sidekick itself manages on the host.
type CredentialProxySideAuthConfig struct {
	Provider string `toml:"provider" koanf:"provider" json:"provider"`
	Profile  string `toml:"profile,omitempty" koanf:"profile,omitempty" json:"profile,omitempty"`
	// AuthType is api, subscription, or empty for the provider's usual
	// subscription-then-API fallback.
	AuthType string `toml:"auth_type,omitempty" koanf:"auth_type,omitempty" json:"authType,omitempty"`
}

// NormalizedAuthType maps the omitted auth type to CredentialProxyAuthTypeAny.
func (s CredentialProxySideAuthConfig) NormalizedAuthType() string {
	switch strings.ToLower(strings.TrimSpace(s.AuthType)) {
	case "", CredentialProxyAuthTypeAny:
		return CredentialProxyAuthTypeAny
	case CredentialProxyAuthTypeAPI:
		return CredentialProxyAuthTypeAPI
	case CredentialProxyAuthTypeSubscription:
		return CredentialProxyAuthTypeSubscription
	default:
		return strings.TrimSpace(s.AuthType)
	}
}

// NormalizedProfile maps the omitted profile to the default profile id.
func (s CredentialProxySideAuthConfig) NormalizedProfile() string {
	return NormalizeProfileId(s.Profile)
}

// ProfileKey canonicalizes the profile for comparison; profile ids are
// case-insensitive.
func (s CredentialProxySideAuthConfig) ProfileKey() string {
	return strings.ToLower(s.NormalizedProfile())
}

// PossibleModes lists the effective modes this configuration can resolve to.
func (s CredentialProxySideAuthConfig) PossibleModes() []string {
	if s.NormalizedAuthType() == CredentialProxyAuthTypeAny {
		return []string{CredentialProxyAuthTypeAPI, CredentialProxyAuthTypeSubscription}
	}
	return []string{s.NormalizedAuthType()}
}

// UpstreamForMode returns the trusted destination bound to an effective mode
// of a side proxy. Generic proxies have a single destination for any mode.
func (c CredentialProxyConfig) UpstreamForMode(mode string) (string, error) {
	if c.Auth.Side == nil {
		return c.Upstream, nil
	}
	if mode != CredentialProxyAuthTypeAPI && mode != CredentialProxyAuthTypeSubscription {
		return "", fmt.Errorf("no destination for unresolved auth mode %q", mode)
	}
	if c.Upstream != "" {
		return c.Upstream, nil
	}
	if upstream := c.Upstreams[mode]; upstream != "" {
		return upstream, nil
	}
	if upstream := sideDefaultUpstreams[c.Auth.Side.Provider][mode]; upstream != "" {
		return upstream, nil
	}
	return "", fmt.Errorf("no destination configured for %s auth mode", mode)
}

// ValidateCredentialProxies validates each definition and rejects side proxies that would be
// indistinguishable to a client selecting by provider, profile and configured
// auth type.
func ValidateCredentialProxies(proxies map[string]CredentialProxyConfig) error {
	names := make([]string, 0, len(proxies))
	for name := range proxies {
		names = append(names, name)
	}
	sort.Strings(names)
	seen := make(map[string]string)
	for _, name := range names {
		proxy := proxies[name]
		if err := proxy.Validate(); err != nil {
			return fmt.Errorf("invalid credential proxy %q: %w", name, err)
		}
		if proxy.Auth.Side == nil {
			continue
		}
		key := proxy.Auth.Side.Provider + "\x00" + proxy.Auth.Side.ProfileKey() + "\x00" + proxy.Auth.Side.NormalizedAuthType()
		if other, exists := seen[key]; exists {
			return fmt.Errorf("credential proxies %q and %q select the same provider, profile and auth type", other, name)
		}
		seen[key] = name
	}
	return nil
}

func (c CredentialProxyConfig) Validate() error {
	methods := 0
	if c.Auth.Bearer != nil {
		methods++
	}
	if c.Auth.Headers != nil {
		methods++
	}
	if c.Auth.Side != nil {
		methods++
	}
	if methods != 1 {
		return errors.New("proxy requires exactly one authentication method")
	}
	if c.Auth.Side != nil {
		return c.validateSide()
	}
	if c.Upstreams != nil {
		return errors.New("per-mode upstreams require side authentication")
	}
	if err := validateUpstream(c.Upstream); err != nil {
		return err
	}
	if c.Auth.Bearer != nil {
		return c.Auth.Bearer.Validate()
	}
	if len(c.Auth.Headers) == 0 {
		return errors.New("proxy requires at least one authentication header")
	}
	seen := make(map[string]bool)
	for name, source := range c.Auth.Headers {
		canonical := strings.ToLower(name)
		if !validAuthHeader(name) || seen[canonical] {
			return errors.New("invalid or duplicate authentication header")
		}
		seen[canonical] = true
		if err := source.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (c CredentialProxyConfig) validateSide() error {
	side := c.Auth.Side
	if _, supported := sideDefaultUpstreams[side.Provider]; !supported {
		return errors.New("side authentication requires a supported provider")
	}
	switch side.NormalizedAuthType() {
	case CredentialProxyAuthTypeAny, CredentialProxyAuthTypeAPI, CredentialProxyAuthTypeSubscription:
	default:
		return errors.New("side authentication auth_type must be api or subscription when set")
	}
	if c.Upstream != "" && c.Upstreams != nil {
		return errors.New("upstream and upstreams are mutually exclusive")
	}
	if c.Upstream != "" {
		if err := validateUpstream(c.Upstream); err != nil {
			return err
		}
	}
	for mode, upstream := range c.Upstreams {
		if mode != CredentialProxyAuthTypeAPI && mode != CredentialProxyAuthTypeSubscription {
			return fmt.Errorf("upstreams.%s is not an auth mode", mode)
		}
		if err := validateUpstream(upstream); err != nil {
			return err
		}
	}
	for _, mode := range side.PossibleModes() {
		if _, err := c.UpstreamForMode(mode); err != nil {
			return err
		}
	}
	return nil
}

func validateUpstream(upstream string) error {
	u, err := url.Parse(upstream)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return errors.New("proxy requires a fixed HTTPS upstream without credentials, query, or fragment")
	}
	return nil
}

func (s CredentialSource) Validate() error {
	if (s.Env == "") == (s.Command == "") {
		return errors.New("credential requires exactly one source")
	}
	if strings.ContainsAny(s.Env, "=\x00") {
		return errors.New("invalid credential environment variable name")
	}
	if s.Command != "" && strings.TrimSpace(s.Command) == "" {
		return errors.New("credential command must not be blank")
	}
	return nil
}

func validAuthHeader(name string) bool {
	if name == "" {
		return false
	}
	for _, b := range []byte(name) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' ||
			strings.ContainsRune("!#$%&'*+-.^_`|~", rune(b)) {
			continue
		}
		return false
	}
	switch strings.ToLower(name) {
	case "host", "connection", "content-length", "transfer-encoding", "upgrade",
		"trailer", "te", "keep-alive", "proxy-authorization", "proxy-authenticate":
		return false
	}
	return true
}

type CredentialSource struct {
	Env     string `toml:"env,omitempty" json:"env,omitempty"`
	Command string `toml:"command,omitempty" json:"command,omitempty"`
}
