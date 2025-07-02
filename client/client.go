package client

import (
	"fmt"
	"net/http"
	"time"

	"sidekick/common"
)

// Client is a client for the Sidekick API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new Sidekick API client.
func NewClient() *Client {
	return &Client{
		baseURL: fmt.Sprintf("http://localhost:%d", common.GetServerPort()),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}
