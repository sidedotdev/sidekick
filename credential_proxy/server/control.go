package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"sidekick/common"
)

type ActivationRequest struct {
	Definitions map[string]common.CredentialProxyConfig `json:"definitions"`
	Active      []string                                `json:"active"`
	FlowID      string                                  `json:"flowId,omitempty"`
}

// NewControlHandler must only be served over the private host control socket,
// never on the sandbox-forwarded inference listener.
func NewControlHandler(
	capabilities *Capabilities,
	loadTrusted func() (map[string]common.CredentialProxyConfig, error),
	allowSideSources func() bool,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/renew" || r.URL.Path == "/revoke" {
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", http.MethodPost)
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			token := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if len(token) != len(prefix)+64 || token[:len(prefix)] != prefix {
				http.Error(w, "invalid capability", http.StatusUnauthorized)
				return
			}
			token = token[len(prefix):]
			if r.URL.Path == "/revoke" {
				capabilities.Revoke(token)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			trusted, err := loadTrusted()
			if err != nil {
				http.Error(w, "trusted configuration unavailable", http.StatusServiceUnavailable)
				return
			}
			if err := capabilities.RenewAuthorized(token, trusted, allowSideSources()); err != nil {
				http.Error(w, "renewal denied", http.StatusForbidden)
				return
			}
			capabilities.mu.RLock()
			expiry := capabilities.handlers[token].expiresAt
			capabilities.mu.RUnlock()
			w.Header().Set("X-Capability-Expires-At", expiry.Format(time.RFC3339Nano))
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path != "/activate" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request ActivationRequest
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, "invalid activation request", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			http.Error(w, "invalid activation request", http.StatusBadRequest)
			return
		}
		trusted, err := loadTrusted()
		if err != nil {
			http.Error(w, "trusted configuration unavailable", http.StatusServiceUnavailable)
			return
		}
		var activation Activation
		if request.FlowID != "" {
			activation, err = capabilities.ActivateFlow(request.FlowID, request.Definitions, trusted, request.Active, allowSideSources())
		} else {
			activation, err = capabilities.Activate(request.Definitions, trusted, request.Active, allowSideSources())
		}
		if err != nil {
			http.Error(w, "activation denied", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(activation); err != nil && request.FlowID == "" {
			capabilities.Revoke(activation.Token)
		}
	})
}

func RequestCapability(ctx context.Context, socket string, activation ActivationRequest) (Activation, error) {
	body, err := json.Marshal(activation)
	if err != nil {
		return Activation{}, errors.New("invalid activation request")
	}
	if len(body) > 1<<20 {
		return Activation{}, errors.New("activation request too large")
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/activate", bytes.NewReader(body))
	if err != nil {
		return Activation{}, errors.New("failed to create activation request")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return Activation{}, ctx.Err()
		}
		return Activation{}, errors.New("credential proxy control unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Activation{}, errors.New("credential proxy activation denied")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<16+1))
	if err != nil || len(data) > 1<<16 {
		return Activation{}, errors.New("invalid activation response")
	}
	var result Activation
	if json.Unmarshal(data, &result) != nil ||
		(len(activation.Active) > 0 && (len(result.Token) != 64 || len(result.Proxies) != len(activation.Active))) ||
		(len(activation.Active) == 0 && (result.Token != "" || len(result.Proxies) != 0)) {
		return Activation{}, errors.New("invalid activation response")
	}
	for _, name := range activation.Active {
		if _, ok := result.Proxies[name]; !ok {
			return Activation{}, errors.New("invalid activation response")
		}
	}
	return result, nil
}

func RenewCapability(ctx context.Context, socket, token string) error {
	return updateCapability(ctx, socket, token, "/renew")
}

func RevokeCapability(ctx context.Context, socket, token string) error {
	return updateCapability(ctx, socket, token, "/revoke")
}

func updateCapability(ctx context.Context, socket, token, path string) error {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost"+path, nil)
	if err != nil {
		return errors.New("invalid capability operation")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("credential proxy control unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return errors.New("capability operation denied")
	}
	return nil
}
