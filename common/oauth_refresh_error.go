package common

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type OAuthRefreshError struct {
	StatusCode int
	Body       string
}

func (e *OAuthRefreshError) Error() string {
	return fmt.Sprintf("token refresh failed with status %d: %s", e.StatusCode, e.Body)
}

func (e *OAuthRefreshError) InvalidCredentials() bool {
	switch e.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return true
	case http.StatusBadRequest:
		var response struct {
			Error string `json:"error"`
		}
		// Other bad requests can indicate a protocol or client configuration
		// problem rather than a rejected refresh token.
		return json.Unmarshal([]byte(e.Body), &response) == nil && response.Error == "invalid_grant"
	default:
		return false
	}
}
