package common

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rs/zerolog/log"
)

// walkerVersion is the sidekick release version, injected at build time via
// -ldflags "-X sidekick/common.walkerVersion=...". Used for downloading
// pre-built binaries from GitHub releases.
var walkerVersion string

// walkerSourceHashOverride is injected at build time for locally installed dev
// CLIs that can't compute the source hash at runtime (running outside the repo).
// The install script pre-builds walker binaries and caches them keyed by this hash.
var walkerSourceHashOverride string

const walkerReleaseBaseURL = "https://github.com/org-sidedev/sidekick/releases/download"

// walkerSourceFiles are hashed to determine when a dev-mode walker binary
// needs to be rebuilt.
var walkerSourceFiles = []string{
	"cmd/side-walker/main.go",
}

func findModuleRoot() (string, error) {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("find module root: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func walkerSourceHash() (string, error) {
	moduleRoot, err := findModuleRoot()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, relPath := range walkerSourceFiles {
		f, err := os.Open(filepath.Join(moduleRoot, relPath))
		if err != nil {
			return "", fmt.Errorf("open %s: %w", relPath, err)
		}
		if _, err := io.Copy(h, f); err != nil {
			f.Close()
			return "", err
		}
		f.Close()
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:12], nil
}

// NormalizeArch converts uname-style architecture names to GOARCH values.
func NormalizeArch(arch string) string {
	switch strings.ToLower(arch) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return strings.ToLower(arch)
	}
}

// NormalizeOS converts uname-style OS names to GOOS values.
func NormalizeOS(osName string) string {
	switch strings.ToLower(osName) {
	case "darwin", "macos":
		return "darwin"
	case "linux":
		return "linux"
	default:
		return strings.ToLower(osName)
	}
}

func buildWalkerBinary(targetOS, targetArch, outputPath string) error {
	moduleRoot, err := findModuleRoot()
	if err != nil {
		return err
	}

	cmd := exec.Command("go", "build",
		"-ldflags", "-s -w",
		"-o", outputPath,
		"./cmd/side-walker",
	)
	cmd.Dir = moduleRoot
	cmd.Env = append(os.Environ(),
		"GOOS="+targetOS,
		"GOARCH="+targetArch,
		"CGO_ENABLED=0",
	)

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("build walker binary: %w\n%s", err, out)
	}
	return nil
}

// downloadWalkerBinary downloads a pre-built walker binary from GitHub releases.
func downloadWalkerBinary(targetOS, targetArch, outputPath string) error {
	binaryName := fmt.Sprintf("side-walker-%s-%s", targetOS, targetArch)
	url := fmt.Sprintf("%s/v%s/%s", walkerReleaseBaseURL, walkerVersion, binaryName)

	log.Info().Str("url", url).Msg("downloading walker binary")

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download walker binary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download walker binary: HTTP %d from %s", resp.StatusCode, url)
	}

	f, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(outputPath)
		return fmt.Errorf("write walker binary: %w", err)
	}

	return nil
}

// GetWalkerBinaryPath returns the path to a cached walker binary for the given
// target OS and architecture. It uses a three-tier resolution strategy:
//  1. Live source available (running from repo): build from source, cache by hash
//  2. Embedded source hash (local dev install): use pre-built binary from cache
//  3. Release version set: download pre-built binary from GitHub releases
func GetWalkerBinaryPath(targetOS, targetArch string) (string, error) {
	targetOS = NormalizeOS(targetOS)
	targetArch = NormalizeArch(targetArch)

	cacheDir, err := GetSidekickCacheHome()
	if err != nil {
		return "", err
	}

	walkerDir := filepath.Join(cacheDir, "walker-binaries")
	if err := os.MkdirAll(walkerDir, 0755); err != nil {
		return "", err
	}

	// Tier 1: live source available — build from source with checksum-based caching
	hash, liveErr := walkerSourceHash()
	if liveErr == nil {
		binaryName := fmt.Sprintf("side-walker-%s-%s-%s", targetOS, targetArch, hash)
		binaryPath := filepath.Join(walkerDir, binaryName)

		if _, err := os.Stat(binaryPath); err == nil {
			log.Debug().Str("path", binaryPath).Msg("using cached walker binary")
			return binaryPath, nil
		}

		log.Info().
			Str("os", targetOS).
			Str("arch", targetArch).
			Msg("building walker binary from source")

		if err := buildWalkerBinary(targetOS, targetArch, binaryPath); err != nil {
			return "", err
		}

		return binaryPath, nil
	}

	// Tier 2: embedded hash from local dev install — look for pre-built binary in cache
	if walkerSourceHashOverride != "" {
		binaryName := fmt.Sprintf("side-walker-%s-%s-%s", targetOS, targetArch, walkerSourceHashOverride)
		binaryPath := filepath.Join(walkerDir, binaryName)

		if _, err := os.Stat(binaryPath); err == nil {
			log.Debug().Str("path", binaryPath).Msg("using pre-built walker binary from cache")
			return binaryPath, nil
		}

		return "", fmt.Errorf("pre-built walker binary not found at %s (hash %s)", binaryPath, walkerSourceHashOverride)
	}

	// Tier 3: release version — download from GitHub releases
	if walkerVersion == "" {
		return "", fmt.Errorf("walker source not available and no release version set: %w", liveErr)
	}

	binaryName := fmt.Sprintf("side-walker-%s-%s-%s", targetOS, targetArch, walkerVersion)
	binaryPath := filepath.Join(walkerDir, binaryName)

	if _, err := os.Stat(binaryPath); err == nil {
		log.Debug().Str("path", binaryPath).Msg("using cached release walker binary")
		return binaryPath, nil
	}

	if err := downloadWalkerBinary(targetOS, targetArch, binaryPath); err != nil {
		os.Remove(binaryPath)
		return "", err
	}

	return binaryPath, nil
}

// GetLocalWalkerBinaryPath returns the path to a walker binary for the host.
func GetLocalWalkerBinaryPath() (string, error) {
	return GetWalkerBinaryPath(runtime.GOOS, runtime.GOARCH)
}
