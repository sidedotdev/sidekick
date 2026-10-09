package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// OAuthRefreshError preserves the response needed to distinguish credential
// rejection from service failures.
type OAuthRefreshError struct {
	StatusCode int
	Body       string
}

func (e *OAuthRefreshError) Error() string {
	return fmt.Sprintf("token refresh failed with status %d: %s", e.StatusCode, e.Body)
}

func IsOAuthCredentialRejection(err error) bool {
	var refreshErr *OAuthRefreshError
	if !errors.As(err, &refreshErr) {
		return false
	}

	switch refreshErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return true
	case http.StatusBadRequest:
		var response struct {
			Error string `json:"error"`
		}
		return json.Unmarshal([]byte(refreshErr.Body), &response) == nil && response.Error == "invalid_grant"
	default:
		return false
	}
}
