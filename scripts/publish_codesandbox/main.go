// Publish a self-contained HTML page: go run ./scripts/publish_codesandbox path/to/page.html
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: publish_codesandbox <self-contained.html> (inline local assets first)")
	}
	page, err := os.ReadFile(os.Args[1])
	if err != nil {
		return err
	}
	type file struct {
		Content string `json:"content"`
	}
	payload := struct {
		Template string          `json:"template"`
		Files    map[string]file `json:"files"`
	}{
		Template: "static",
		Files: map[string]file{
			"index.html":          {Content: string(page)},
			"package.json":        {Content: `{"name":"static-preview","main":"index.html"}`},
			"sandbox.config.json": {Content: `{"template":"static"}`},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	response, err := client.Post("https://codesandbox.io/api/v1/sandboxes/define?json=1", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read publish response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("publish: %s: %s", response.Status, data)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("decode publish response: %w", err)
	}
	var id string
	if err := json.Unmarshal(result["sandbox_id"], &id); err != nil || id == "" || strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-") != "" {
		return fmt.Errorf("publish returned no valid sandbox ID: %s", data)
	}
	fmt.Printf("https://%s.csb.app/\n", id)
	return nil
}
