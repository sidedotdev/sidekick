// Publishes a single self-contained HTML file as a static CodeSandbox sandbox
// via the Define API, printing the preview and editor URLs. Handy for sharing
// UX wireframes such as the ones under intent/.generated/app.
//
// Usage: go run ./scripts/publish_codesandbox path/to/page.html [more.html ...]
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const defineURL = "https://codesandbox.io/api/v1/sandboxes/define?json=1"

type defineRequest struct {
	Template string                 `json:"template"`
	Files    map[string]defineEntry `json:"files"`
}

type defineEntry struct {
	Content string `json:"content"`
}

type defineResponse struct {
	SandboxID string `json:"sandbox_id"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: publish_codesandbox <index.html> [extra files...]")
		os.Exit(2)
	}
	id, err := publish(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("preview: https://%s.csb.app/\n", id)
	fmt.Printf("editor:  https://codesandbox.io/s/%s\n", id)
}

// publish uploads the given files, using the first as index.html so the
// static template serves it at the sandbox root.
func publish(paths []string) (string, error) {
	req := defineRequest{Template: "static", Files: map[string]defineEntry{}}
	for i, p := range paths {
		content, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		name := filepath.Base(p)
		if i == 0 {
			name = "index.html"
		}
		req.Files[name] = defineEntry{Content: string(content)}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Post(defineURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("define API returned %s: %s", resp.Status, raw)
	}
	var out defineResponse
	if err := json.Unmarshal(raw, &out); err != nil || out.SandboxID == "" {
		return "", fmt.Errorf("unexpected define API response: %s", raw)
	}
	return out.SandboxID, nil
}