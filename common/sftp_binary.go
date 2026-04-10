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

// sftpVersion is the sidekick release version, injected at build time via
// -ldflags "-X sidekick/common.sftpVersion=...". Used for downloading
// pre-built binaries from GitHub releases.
var sftpVersion string

// sftpSourceHashOverride is injected at build time for locally installed dev
// CLIs that can't compute the source hash at runtime (running outside the repo).
var sftpSourceHashOverride string

const sftpReleaseBaseURL = "https://github.com/org-sidedev/sidekick/releases/download"

// sftpSourceFiles are hashed to determine when a dev-mode sftp binary
// needs to be rebuilt.
var sftpSourceFiles = []string{
	"cmd/side-sftp/main.go",
}

func sftpSourceHash() (string, error) {
	moduleRoot, err := findModuleRoot()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, relPath := range sftpSourceFiles {
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

func buildSFTPBinary(targetOS, targetArch, outputPath string) error {
	moduleRoot, err := findModuleRoot()
	if err != nil {
		return err
	}

	cmd := exec.Command("go", "build",
		"-ldflags", "-s -w",
		"-o", outputPath,
		"./cmd/side-sftp",
	)
	cmd.Dir = moduleRoot
	cmd.Env = append(os.Environ(),
		"GOOS="+targetOS,
		"GOARCH="+targetArch,
		"CGO_ENABLED=0",
	)

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("build sftp binary: %w\n%s", err, out)
	}
	return nil
}

// downloadSFTPBinary downloads a pre-built binary from GitHub releases and
// returns the SHA256 content hash (truncated to 12 hex chars) of the binary.
func downloadSFTPBinary(targetOS, targetArch, outputPath string) (string, error) {
	assetName := fmt.Sprintf("side-sftp-%s-%s", targetOS, targetArch)
	url := fmt.Sprintf("%s/v%s/%s", sftpReleaseBaseURL, sftpVersion, assetName)

	log.Info().Str("url", url).Msg("downloading sftp binary")

	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("download sftp binary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download sftp binary: HTTP %d from %s", resp.StatusCode, url)
	}

	f, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return "", fmt.Errorf("create output file: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		os.Remove(outputPath)
		return "", fmt.Errorf("write sftp binary: %w", err)
	}

	return fmt.Sprintf("%x", h.Sum(nil))[:12], nil
}

// GetSFTPBinaryPath returns the path to a cached side-sftp binary for the
// given target OS and architecture. It uses the same three-tier resolution
// strategy as GetWalkerBinaryPath.
func GetSFTPBinaryPath(targetOS, targetArch string) (string, error) {
	targetOS = NormalizeOS(targetOS)
	targetArch = NormalizeArch(targetArch)

	cacheDir, err := GetSidekickCacheHome()
	if err != nil {
		return "", err
	}

	sftpDir := filepath.Join(cacheDir, "sftp-binaries")
	if err := os.MkdirAll(sftpDir, 0755); err != nil {
		return "", err
	}

	// Tier 1: live source available — build from source with checksum-based caching
	hash, liveErr := sftpSourceHash()
	if liveErr == nil {
		binaryName := fmt.Sprintf("side-sftp-%s-%s-%s", targetOS, targetArch, hash)
		binaryPath := filepath.Join(sftpDir, binaryName)

		if _, err := os.Stat(binaryPath); err == nil {
			log.Debug().Str("path", binaryPath).Msg("using cached sftp binary")
			return binaryPath, nil
		}

		log.Info().
			Str("os", targetOS).
			Str("arch", targetArch).
			Msg("building sftp binary from source")

		if err := buildSFTPBinary(targetOS, targetArch, binaryPath); err != nil {
			return "", err
		}

		return binaryPath, nil
	}

	// Tier 2: embedded hash from local dev install
	if sftpSourceHashOverride != "" {
		binaryName := fmt.Sprintf("side-sftp-%s-%s-%s", targetOS, targetArch, sftpSourceHashOverride)
		binaryPath := filepath.Join(sftpDir, binaryName)

		if _, err := os.Stat(binaryPath); err == nil {
			log.Debug().Str("path", binaryPath).Msg("using pre-built sftp binary from cache")
			return binaryPath, nil
		}

		return "", fmt.Errorf("pre-built sftp binary not found at %s (hash %s)", binaryPath, sftpSourceHashOverride)
	}

	// Tier 3: release version — download from GitHub releases, cache by content hash
	if sftpVersion == "" {
		return "", fmt.Errorf("sftp source not available and no release version set: %w", liveErr)
	}

	// Check if we already downloaded this version (version→hash mapping file).
	prefix := fmt.Sprintf("side-sftp-%s-%s", targetOS, targetArch)
	versionFile := filepath.Join(sftpDir, prefix+".version")
	if data, err := os.ReadFile(versionFile); err == nil {
		parts := strings.SplitN(strings.TrimSpace(string(data)), ":", 2)
		if len(parts) == 2 && parts[0] == sftpVersion {
			cachedHash := parts[1]
			binaryPath := filepath.Join(sftpDir, fmt.Sprintf("side-sftp-%s-%s-%s", targetOS, targetArch, cachedHash))
			if _, err := os.Stat(binaryPath); err == nil {
				log.Debug().Str("path", binaryPath).Msg("using cached release sftp binary")
				return binaryPath, nil
			}
		}
	}

	tmpPath := filepath.Join(sftpDir, prefix+".tmp")
	contentHash, err := downloadSFTPBinary(targetOS, targetArch, tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return "", err
	}

	binaryName := fmt.Sprintf("side-sftp-%s-%s-%s", targetOS, targetArch, contentHash)
	binaryPath := filepath.Join(sftpDir, binaryName)
	if err := os.Rename(tmpPath, binaryPath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("rename downloaded sftp binary: %w", err)
	}

	// Persist the version→hash mapping for future cache lookups.
	_ = os.WriteFile(versionFile, []byte(sftpVersion+":"+contentHash), 0644)

	return binaryPath, nil
}

// GetLocalSFTPBinaryPath returns the path to an sftp binary for the host.
func GetLocalSFTPBinaryPath() (string, error) {
	return GetSFTPBinaryPath(runtime.GOOS, runtime.GOARCH)
}

func init() {
	// Reuse walkerVersion when sftp-specific version is not injected, since
	// both binaries share the same release version.
	if sftpVersion == "" {
		sftpVersion = walkerVersion
	}
	// NOTE: sftpSourceHashOverride must NOT fall back to walkerSourceHashOverride
	// because each binary hashes different source files, producing different hashes.
}
