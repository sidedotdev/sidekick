package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"sidekick/common"
	"sidekick/flow_action"
	"sidekick/provider_auth"
	"sidekick/secret_manager"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"github.com/segmentio/ksuid"
)

// providerOAuthSessionRetention keeps finished sessions around long enough
// for clients polling their status to observe the outcome.
const providerOAuthSessionRetention = 10 * time.Minute

// providerOAuthSessionCancelWait bounds how long starting a session waits for
// a superseded session to release the shared loopback callback port.
const providerOAuthSessionCancelWait = 5 * time.Second

type providerOAuthSession interface {
	AuthorizationURL() string
	Status() (provider_auth.OAuthStatus, error)
	SubmitManualCode(input string) error
	Done() <-chan struct{}
	Cancel()
}

type startProviderOAuthFunc func(provider, profileId string, method provider_auth.Method) (providerOAuthSession, error)

type sharedProviderOAuthSession struct {
	*provider_auth.OAuthSession
}

func (s sharedProviderOAuthSession) AuthorizationURL() string {
	return s.AuthURL
}

func startSharedProviderOAuth(provider, profileId string, method provider_auth.Method) (providerOAuthSession, error) {
	session, err := provider_auth.StartOAuthSession(provider, profileId, method)
	if err != nil {
		return nil, err
	}
	return sharedProviderOAuthSession{session}, nil
}

type providerOAuthEntry struct {
	provider  string
	profileId string
	method    provider_auth.Method
	session   providerOAuthSession
}

type providerOAuthSessions struct {
	mu       sync.Mutex
	sessions map[string]*providerOAuthEntry
}

func newProviderOAuthSessions() *providerOAuthSessions {
	return &providerOAuthSessions{sessions: map[string]*providerOAuthEntry{}}
}

func (s *providerOAuthSessions) get(sessionId string) (*providerOAuthEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.sessions[sessionId]
	return entry, ok
}

func (s *providerOAuthSessions) add(sessionId string, entry *providerOAuthEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionId] = entry
}

func (s *providerOAuthSessions) remove(sessionId string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionId)
}

// cancelProvider ends the provider's earlier sessions, since they share a
// loopback callback port and only the latest login attempt can use it.
func (s *providerOAuthSessions) cancelProvider(provider string) {
	s.mu.Lock()
	var superseded []providerOAuthSession
	for _, entry := range s.sessions {
		if entry.provider == provider {
			superseded = append(superseded, entry.session)
		}
	}
	s.mu.Unlock()

	for _, session := range superseded {
		session.Cancel()
		select {
		case <-session.Done():
		case <-time.After(providerOAuthSessionCancelWait):
		}
	}
}

func (ctrl *Controller) providerOAuthStarter() startProviderOAuthFunc {
	if ctrl.startProviderOAuth != nil {
		return ctrl.startProviderOAuth
	}
	return startSharedProviderOAuth
}

func providerAuthProfileId(raw string) (string, error) {
	profileId := common.NormalizeProfileId(strings.TrimSpace(raw))
	if err := common.ValidateProfileId(profileId); err != nil {
		return "", err
	}
	return profileId, nil
}

func isProviderAuthInputError(err error) bool {
	return errors.Is(err, provider_auth.ErrUnsupportedProvider) || errors.Is(err, provider_auth.ErrUnsupportedMethod)
}

type startProviderOAuthRequest struct {
	Provider  string `json:"provider"`
	ProfileId string `json:"profileId"`
	Method    string `json:"method"`
}

func (ctrl *Controller) StartProviderOAuthHandler(c *gin.Context) {
	var req startProviderOAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}
	provider := provider_auth.NormalizeProvider(req.Provider)
	method := provider_auth.Method(req.Method)
	profileId, err := providerAuthProfileId(req.ProfileId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := provider_auth.ValidateMethod(provider, method); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !method.IsOAuth() {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("method %q does not use OAuth", method)})
		return
	}

	ctrl.providerOAuth.cancelProvider(provider)
	session, err := ctrl.providerOAuthStarter()(provider, profileId, method)
	if err != nil {
		status := http.StatusInternalServerError
		if isProviderAuthInputError(err) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	sessionId := "poa_" + ksuid.New().String()
	entry := &providerOAuthEntry{provider: provider, profileId: profileId, method: method, session: session}
	ctrl.providerOAuth.add(sessionId, entry)
	go ctrl.finishProviderOAuth(sessionId, entry)

	c.JSON(http.StatusOK, gin.H{"sessionId": sessionId, "authUrl": session.AuthorizationURL()})
}

func (ctrl *Controller) finishProviderOAuth(sessionId string, entry *providerOAuthEntry) {
	<-entry.session.Done()
	if status, _ := entry.session.Status(); status == provider_auth.OAuthStatusSucceeded {
		ctrl.broadcastProviderAuthUpdated(context.Background(), provider_auth.NewCredentialUpdatedEvent(entry.provider, entry.profileId, entry.method))
	}
	sessions := ctrl.providerOAuth
	time.AfterFunc(providerOAuthSessionRetention, func() { sessions.remove(sessionId) })
}

func providerOAuthStatusResponse(session providerOAuthSession) gin.H {
	status, err := session.Status()
	response := gin.H{"status": string(status)}
	if err != nil {
		response["error"] = err.Error()
	}
	return response
}

func (ctrl *Controller) GetProviderOAuthStatusHandler(c *gin.Context) {
	entry, ok := ctrl.providerOAuth.get(c.Param("sessionId"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "OAuth session not found"})
		return
	}
	c.JSON(http.StatusOK, providerOAuthStatusResponse(entry.session))
}

func (ctrl *Controller) SubmitProviderOAuthCodeHandler(c *gin.Context) {
	entry, ok := ctrl.providerOAuth.get(c.Param("sessionId"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "OAuth session not found"})
		return
	}
	var body struct {
		Input string `json:"input"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Input) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Authorization code is required"})
		return
	}
	if err := entry.session.SubmitManualCode(body.Input); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, provider_auth.ErrOAuthSessionFinished) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, providerOAuthStatusResponse(entry.session))
}

func (ctrl *Controller) ProviderAPIKeyExistsHandler(c *gin.Context) {
	profileId, err := providerAuthProfileId(c.Query("profileId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	secretName, err := provider_auth.APIKeySecretName(c.Query("provider"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	_, err = ctrl.profileSecretManager(profileId).GetSecret(secretName)
	if err != nil && !errors.Is(err, secret_manager.ErrSecretNotFound) {
		log.Error().Err(err).Str("profileId", profileId).Str("secretName", secretName).Msg("Failed to check for provider API key")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check for API key"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"exists": err == nil})
}

type saveProviderAPIKeyRequest struct {
	Provider  string `json:"provider"`
	ProfileId string `json:"profileId"`
	ApiKey    string `json:"apiKey"`
}

func (ctrl *Controller) SaveProviderAPIKeyHandler(c *gin.Context) {
	var req saveProviderAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}
	profileId, err := providerAuthProfileId(req.ProfileId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := provider_auth.ValidateMethod(req.Provider, provider_auth.MethodAPIKey); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.ApiKey) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "API key is required"})
		return
	}

	if err := provider_auth.SaveAPIKey(req.Provider, profileId, req.ApiKey); err != nil {
		log.Error().Err(err).Str("provider", req.Provider).Str("profileId", profileId).Msg("Failed to save provider API key")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save API key"})
		return
	}

	ctrl.broadcastProviderAuthUpdated(c.Request.Context(), provider_auth.NewCredentialUpdatedEvent(req.Provider, profileId, provider_auth.MethodAPIKey))
	c.JSON(http.StatusOK, gin.H{})
}

// CredentialUpdatedHandler relays credentials saved outside the server, e.g.
// by `side auth`, to flows blocked on them.
func (ctrl *Controller) CredentialUpdatedHandler(c *gin.Context) {
	var event provider_auth.CredentialUpdatedEvent
	if err := c.ShouldBindJSON(&event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}
	profileId, err := providerAuthProfileId(event.ProfileId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := provider_auth.ValidateMethod(event.Provider, event.Method); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctrl.broadcastProviderAuthUpdated(c.Request.Context(), provider_auth.NewCredentialUpdatedEvent(event.Provider, profileId, event.Method))
	c.JSON(http.StatusOK, gin.H{})
}

// broadcastProviderAuthUpdated tells every active flow about a saved
// credential; flows decide for themselves whether it unblocks them.
func (ctrl *Controller) broadcastProviderAuthUpdated(ctx context.Context, event provider_auth.CredentialUpdatedEvent) {
	workspaces, err := ctrl.service.GetAllWorkspaces(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list workspaces for provider auth update")
		return
	}
	signal := flow_action.ProviderAuthUpdated{
		Provider:  event.Provider,
		ProfileId: event.ProfileId,
		Method:    string(event.Method),
		AuthType:  event.AuthType,
	}
	for _, workspace := range workspaces {
		ctrl.signalActiveFlows(ctx, workspace.Id, flow_action.SignalNameProviderAuthUpdated, signal)
	}
}

// providerAuthMethodOffered reports whether choice is one of the methods a
// provider_auth request offered. Offered methods are read back from persisted
// action params, so they are decoded generically.
func providerAuthMethodOffered(offered any, choice string) bool {
	if choice == "" {
		return false
	}
	data, err := json.Marshal(offered)
	if err != nil {
		return false
	}
	var methods []provider_auth.MethodInfo
	if err := json.Unmarshal(data, &methods); err != nil {
		return false
	}
	for _, method := range methods {
		if string(method.Method) == choice {
			return true
		}
	}
	return false
}
