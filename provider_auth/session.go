package provider_auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sidekick/common"
	"sidekick/llm"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

type OAuthStatus string

const (
	OAuthStatusPending   OAuthStatus = "pending"
	OAuthStatusSucceeded OAuthStatus = "succeeded"
	OAuthStatusFailed    OAuthStatus = "failed"
)

const OAuthSessionTimeout = 5 * time.Minute

var (
	ErrOAuthSessionCanceled = errors.New("OAuth session canceled")
	ErrOAuthSessionFinished = errors.New("OAuth session already finished")
)

// OAuthSession runs one browser-based OAuth login without prompting: the
// authorization code arrives via the loopback callback when available, or via
// SubmitManualCode when the user pastes it, and the resulting credential is
// saved to the keyring for the session's profiles.
type OAuthSession struct {
	Provider   string
	ProfileIds []string
	Method     Method
	AuthURL    string

	verifier        string
	state           string
	authBaseURL     string
	redirectURI     string
	callbackErr     error
	callbackServer  *http.Server
	callbackResults <-chan callbackResult
	manualCodes     chan string
	ctx             context.Context
	cancel          context.CancelFunc
	done            chan struct{}

	mu     sync.Mutex
	status OAuthStatus
	err    error
}

func StartOAuthSession(provider, profileId string, method Method) (*OAuthSession, error) {
	return StartOAuthSessionForProfiles(provider, []string{profileId}, method)
}

func StartOAuthSessionForProfiles(provider string, profileIds []string, method Method) (*OAuthSession, error) {
	if err := ValidateMethod(provider, method); err != nil {
		return nil, err
	}
	if !method.IsOAuth() {
		return nil, fmt.Errorf("%w: %q does not use OAuth", ErrUnsupportedMethod, method)
	}
	if len(profileIds) == 0 {
		return nil, fmt.Errorf("no profile given for %s credentials", provider)
	}

	normalizedProfileIds := make([]string, 0, len(profileIds))
	for _, profileId := range profileIds {
		normalizedProfileIds = append(normalizedProfileIds, common.NormalizeProfileId(profileId))
	}

	s := &OAuthSession{
		Provider:    NormalizeProvider(provider),
		ProfileIds:  normalizedProfileIds,
		Method:      method,
		verifier:    oauth2.GenerateVerifier(),
		manualCodes: make(chan string, 1),
		done:        make(chan struct{}),
		status:      OAuthStatusPending,
	}

	switch s.Provider {
	case ProviderAnthropic:
		s.prepareAnthropic()
	case ProviderOpenAI:
		if err := s.prepareOpenAI(); err != nil {
			return nil, err
		}
	}

	s.ctx, s.cancel = context.WithTimeout(context.Background(), OAuthSessionTimeout)
	go s.run()
	return s, nil
}

func (s *OAuthSession) prepareAnthropic() {
	s.state = s.verifier
	s.authBaseURL = ConsoleAuthURL
	s.redirectURI = AnthropicManualRedirectURI

	// The claude.ai subscription flow supports an RFC 8252 loopback redirect,
	// so completing login in the browser finishes auth without copy-pasting.
	// The console flow only supports the hosted callback page, which requires
	// pasting the displayed code.
	if s.Method == MethodOAuthSubscription {
		s.authBaseURL = ClaudeProMaxAuthURL
		server, redirectURI, results, err := startAnthropicOAuthCallbackServer(s.verifier)
		if err != nil {
			s.callbackErr = err
		} else {
			s.callbackServer = server
			s.callbackResults = results
			s.redirectURI = redirectURI
		}
	}

	s.AuthURL = BuildAuthorizationURL(s.authBaseURL, s.redirectURI, s.verifier)
}

func (s *OAuthSession) prepareOpenAI() error {
	state, err := GenerateRandomState()
	if err != nil {
		return err
	}
	s.state = state

	// Without a listener the browser still lands on the loopback redirect URI,
	// whose URL the user can paste back as the manual code.
	server, redirectURI, results, err := startOpenAIOAuthCallbackServer()
	s.redirectURI = redirectURI
	if err != nil {
		s.callbackErr = err
	} else {
		s.callbackServer = server
		s.callbackResults = results
	}

	s.AuthURL = BuildOpenAIAuthorizeURL(s.verifier, s.state, s.redirectURI)
	return nil
}

// CallbackError reports why the loopback callback server couldn't start, in
// which case the authorization code must be submitted manually.
func (s *OAuthSession) CallbackError() error {
	return s.callbackErr
}

func (s *OAuthSession) Done() <-chan struct{} {
	return s.done
}

func (s *OAuthSession) Status() (OAuthStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status, s.err
}

func (s *OAuthSession) Cancel() {
	s.cancel()
}

// SubmitManualCode accepts a pasted authorization code or callback URL. Input
// that fails validation is rejected without ending the session, so the user
// can try again.
func (s *OAuthSession) SubmitManualCode(input string) error {
	select {
	case <-s.done:
		return ErrOAuthSessionFinished
	default:
	}

	code, err := s.parseManualInput(input)
	if err != nil {
		return err
	}

	select {
	case s.manualCodes <- code:
		return nil
	default:
		return errors.New("an authorization code was already submitted")
	}
}

func (s *OAuthSession) parseManualInput(input string) (string, error) {
	if s.Provider == ProviderAnthropic {
		return ParseAnthropicAuthorizationInput(input, s.verifier)
	}

	result, err := openAICallbackFromInput(input)
	if err != nil {
		return "", err
	}
	if err := validateOpenAIState(result.state, s.state); err != nil {
		return "", err
	}
	return result.code, nil
}

func (s *OAuthSession) run() {
	defer close(s.done)
	defer s.cancel()
	if s.callbackServer != nil {
		defer s.callbackServer.Close()
	}

	code, err := s.awaitCode()
	if err == nil {
		err = s.saveCredentials(code)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.status = OAuthStatusFailed
		s.err = err
	} else {
		s.status = OAuthStatusSucceeded
	}
}

func (s *OAuthSession) awaitCode() (string, error) {
	select {
	case result := <-s.callbackResults:
		if result.err != nil {
			return "", result.err
		}
		if err := s.validateCallbackState(result.state); err != nil {
			return "", err
		}
		return result.code, nil
	case code := <-s.manualCodes:
		return code, nil
	case <-s.ctx.Done():
		if errors.Is(s.ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("timed out waiting for authorization")
		}
		return "", ErrOAuthSessionCanceled
	}
}

func (s *OAuthSession) validateCallbackState(state string) error {
	if s.Provider == ProviderOpenAI {
		return validateOpenAIState(state, s.state)
	}
	if state != s.verifier {
		return fmt.Errorf("state mismatch: expected verifier but got different state")
	}
	return nil
}

func (s *OAuthSession) saveCredentials(code string) error {
	if s.Provider == ProviderOpenAI {
		creds, err := ExchangeOpenAICode(code, s.verifier, s.redirectURI)
		if err != nil {
			return fmt.Errorf("failed to exchange authorization code: %w", err)
		}
		return SaveOpenAIOAuthCredentials(creds, s.ProfileIds)
	}

	tokens, err := ExchangeCodeForTokens(s.authBaseURL, s.redirectURI, code, s.verifier)
	if err != nil {
		return fmt.Errorf("failed to exchange code for tokens: %w", err)
	}

	if s.Method == MethodOAuthCreateKey {
		apiKey, err := CreateAPIKeyWithOAuth(tokens.AccessToken)
		if err != nil {
			return err
		}
		if err := StoreSecretForProfiles(s.ProfileIds, llm.AnthropicApiKeySecretName, apiKey); err != nil {
			return fmt.Errorf("error storing API key in keyring: %w", err)
		}
		return nil
	}

	return SaveAnthropicOAuthCredentials(AnthropicCredentialsFromTokens(tokens, time.Now()), s.ProfileIds)
}
