package common

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/adrg/xdg"
)

var ErrGoplsInstallFailed = errors.New("failed to install gopls after multiple attempts")

// FindOrInstallGopls attempts to find gopls in PATH or XDG bin directory,
// installing it if necessary. Returns a command that can be used to execute
// gopls.
func FindOrInstallGopls() (string, error) {
	// First check if gopls is in PATH
	cmd := exec.Command("gopls", "version")
	if err := cmd.Run(); err == nil {
		return "gopls", nil
	}

	// Check if gopls exists in XDG bin directory
	goplsPath := filepath.Join(xdg.BinHome, "gopls")
	_, err := os.Stat(goplsPath)
	if err == nil {
		return goplsPath, nil
	} else {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("error checking for existence of gopls binary in XDG bin home: %w", err)
		}
	}

	// Install gopls to XDG bin directory
	for i := 0; i < 3; i++ {
		cmd := exec.Command("go", "install", "golang.org/x/tools/gopls@latest")
		cmd.Env = append(os.Environ(), fmt.Sprintf("GOBIN=%s", xdg.BinHome))

		if err = cmd.Run(); err == nil {
			return goplsPath, nil
		} else {
			if i < 2 {
				time.Sleep(time.Second)
			}
		}
	}

	return "", fmt.Errorf("%w: %v", ErrGoplsInstallFailed, err)
}
