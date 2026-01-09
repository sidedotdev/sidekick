package api

import (
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// OpenInIdeRequest defines the request body for opening a file in an IDE
type OpenInIdeRequest struct {
	Ide      string `json:"ide"`
	FilePath string `json:"filePath"`
	Line     *int   `json:"line,omitempty"`
	BaseDir  string `json:"baseDir,omitempty"`
}

// OpenInIdeHandler handles requests to open files in an IDE
func (ctrl *Controller) OpenInIdeHandler(c *gin.Context) {
	var req OpenInIdeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	if req.Ide != "vscode" && req.Ide != "intellij" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid IDE type. Must be 'vscode' or 'intellij'"})
		return
	}

	if req.FilePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filePath is required"})
		return
	}

	if err := openInIde(req.Ide, req.FilePath, req.Line, req.BaseDir); err != nil {
		log.Error().Err(err).Str("ide", req.Ide).Str("filePath", req.FilePath).Msg("Failed to open file in IDE")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open file in IDE"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func openInIde(ide, filePath string, line *int, baseDir string) error {
	if ide == "vscode" {
		return openInVSCode(filePath, line, baseDir)
	}
	return openInIntelliJ(filePath, line)
}

func openInVSCode(filePath string, line *int, baseDir string) error {
	lineFragment := ""
	if line != nil {
		lineFragment = fmt.Sprintf(":%d", *line)
	}

	if baseDir != "" {
		// First open the base directory to ensure the correct window is focused
		if err := openURL(fmt.Sprintf("vscode://file/%s?windowId=_blank", baseDir)); err != nil {
			return fmt.Errorf("failed to open base directory: %w", err)
		}
		// Wait a bit for VSCode to process the first request
		time.Sleep(250 * time.Millisecond)
	}

	// Open the specific file
	url := fmt.Sprintf("vscode://file/%s%s", filePath, lineFragment)
	if baseDir == "" {
		url += "?windowId=_blank"
	}
	return openURL(url)
}

func openInIntelliJ(filePath string, line *int) error {
	lineFragment := ""
	if line != nil {
		lineFragment = fmt.Sprintf(":%d", *line)
	}
	url := fmt.Sprintf("idea://open?file=%s%s", filePath, lineFragment)
	return openURL(url)
}

// openURL opens the specified URL in the default handler
func openURL(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	case "darwin":
		cmd = "open"
		args = []string{url}
	default: // "linux", "freebsd", "openbsd", "netbsd"
		if isWSL() {
			cmd = "cmd.exe"
			args = []string{"/c", "start", url}
		} else {
			cmd = "xdg-open"
			args = []string{url}
		}
	}
	if len(args) > 1 {
		args = append(args[:1], append([]string{""}, args[1:]...)...)
	}
	return exec.Command(cmd, args...).Start()
}

// isWSL checks if running inside Windows Subsystem for Linux
func isWSL() bool {
	releaseData, err := exec.Command("uname", "-r").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(releaseData)), "microsoft")
}
