package llm

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type anthropicOAuthRefreshError struct {
	statusCode int
	body       string
}

func (e *anthropicOAuthRefreshError) Error() string {
	return fmt.Sprintf("token refresh failed with status %d: %s", e.statusCode, e.body)
}

func (e *anthropicOAuthRefreshError) invalidCredentials() bool {
	switch e.statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return true
	case http.StatusBadRequest:
		var response struct {
			Error string `json:"error"`
		}
		// Other bad requests can indicate a protocol or client configuration
		// problem rather than a rejected refresh token.
		return json.Unmarshal([]byte(e.body), &response) == nil && response.Error == "invalid_grant"
	default:
		return false
	}
}
