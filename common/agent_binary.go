package common

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/rs/zerolog/log"
)

// agentVersion is the sidekick release version, injected at build time via
// -ldflags "-X sidekick/common.agentVersion=...". Used for downloading
// pre-built binaries from GitHub releases.
var agentVersion string

// agentSourceHashOverride is injected at build time for locally installed dev
// CLIs that can't compute the source hash at runtime (running outside the repo).
var agentSourceHashOverride string

const agentReleaseBaseURL = "https://github.com/org-sidedev/sidekick/releases/download"

// agentSourceFiles are hashed to determine when a dev-mode side-agent binary
// needs to be rebuilt. Both ends of an agent channel are version-locked via
// this hash: the remote upload path embeds it, so a rebuilt agent is always
// re-uploaded and the protocol is compatible by construction.
var agentSourceFiles = []string{
	"cmd/side-agent/main.go",
	"sideagent/client.go",
	"sideagent/gc.go",
	"sideagent/protocol.go",
	"sideagent/server.go",
	"sideagent/sftp.go",
}

func findModuleRoot() (string, error) {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("find module root: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
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

func agentSourceHash() (string, error) {
	moduleRoot, err := findModuleRoot()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, relPath := range agentSourceFiles {
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

func buildAgentBinary(targetOS, targetArch, outputPath string) error {
	moduleRoot, err := findModuleRoot()
	if err != nil {
		return err
	}

	cmd := exec.Command("go", "build",
		"-ldflags", "-s -w",
		"-o", outputPath,
		"./cmd/side-agent",
	)
	cmd.Dir = moduleRoot
	cmd.Env = append(os.Environ(),
		"GOOS="+targetOS,
		"GOARCH="+targetArch,
		"CGO_ENABLED=0",
	)

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("build agent binary: %w\n%s", err, out)
	}
	return nil
}

// downloadAgentBinary downloads a pre-built binary from GitHub releases and
// returns the SHA256 content hash (truncated to 12 hex chars) of the binary.
func downloadAgentBinary(targetOS, targetArch, outputPath string) (string, error) {
	assetName := fmt.Sprintf("side-agent-%s-%s", targetOS, targetArch)
	url := fmt.Sprintf("%s/v%s/%s", agentReleaseBaseURL, agentVersion, assetName)

	log.Info().Str("url", url).Msg("downloading agent binary")

	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("download agent binary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download agent binary: HTTP %d from %s", resp.StatusCode, url)
	}

	f, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return "", fmt.Errorf("create output file: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		os.Remove(outputPath)
		return "", fmt.Errorf("write agent binary: %w", err)
	}

	return fmt.Sprintf("%x", h.Sum(nil))[:12], nil
}

// GetAgentBinaryPath returns the path to a cached side-agent binary for the
// given target OS and architecture. Live source builds are cached by source
// hash, local installations use their embedded source hash, and releases are
// downloaded by version and cached by content hash.
func GetAgentBinaryPath(targetOS, targetArch string) (string, error) {
	targetOS = NormalizeOS(targetOS)
	targetArch = NormalizeArch(targetArch)

	cacheDir, err := GetSidekickCacheHome()
	if err != nil {
		return "", err
	}

	agentDir := filepath.Join(cacheDir, "agent-binaries")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return "", err
	}

	// Tier 1: live source available — build from source with checksum-based caching
	hash, liveErr := agentSourceHash()
	if liveErr == nil {
		binaryName := fmt.Sprintf("side-agent-%s-%s-%s", targetOS, targetArch, hash)
		binaryPath := filepath.Join(agentDir, binaryName)

		if _, err := os.Stat(binaryPath); err == nil {
			log.Debug().Str("path", binaryPath).Msg("using cached agent binary")
			return binaryPath, nil
		}

		log.Info().
			Str("os", targetOS).
			Str("arch", targetArch).
			Msg("building agent binary from source")

		if err := buildAgentBinary(targetOS, targetArch, binaryPath); err != nil {
			return "", err
		}

		return binaryPath, nil
	}

	// Tier 2: embedded hash from local dev install
	if agentSourceHashOverride != "" {
		binaryName := fmt.Sprintf("side-agent-%s-%s-%s", targetOS, targetArch, agentSourceHashOverride)
		binaryPath := filepath.Join(agentDir, binaryName)

		if _, err := os.Stat(binaryPath); err == nil {
			log.Debug().Str("path", binaryPath).Msg("using pre-built agent binary from cache")
			return binaryPath, nil
		}

		return "", fmt.Errorf("pre-built agent binary not found at %s (hash %s)", binaryPath, agentSourceHashOverride)
	}

	// Tier 3: release version — download from GitHub releases, cache by content hash
	if agentVersion == "" {
		return "", fmt.Errorf("agent source not available and no release version set: %w", liveErr)
	}

	// Check if we already downloaded this version (version→hash mapping file).
	prefix := fmt.Sprintf("side-agent-%s-%s", targetOS, targetArch)
	versionFile := filepath.Join(agentDir, prefix+".version")
	if data, err := os.ReadFile(versionFile); err == nil {
		parts := strings.SplitN(strings.TrimSpace(string(data)), ":", 2)
		if len(parts) == 2 && parts[0] == agentVersion {
			cachedHash := parts[1]
			binaryPath := filepath.Join(agentDir, fmt.Sprintf("side-agent-%s-%s-%s", targetOS, targetArch, cachedHash))
			if _, err := os.Stat(binaryPath); err == nil {
				log.Debug().Str("path", binaryPath).Msg("using cached release agent binary")
				return binaryPath, nil
			}
		}
	}

	tmpPath := filepath.Join(agentDir, prefix+".tmp")
	contentHash, err := downloadAgentBinary(targetOS, targetArch, tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return "", err
	}

	binaryName := fmt.Sprintf("side-agent-%s-%s-%s", targetOS, targetArch, contentHash)
	binaryPath := filepath.Join(agentDir, binaryName)
	if err := os.Rename(tmpPath, binaryPath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("rename downloaded agent binary: %w", err)
	}

	// Persist the version→hash mapping for future cache lookups.
	_ = os.WriteFile(versionFile, []byte(agentVersion+":"+contentHash), 0644)

	return binaryPath, nil
}

// GetLocalAgentBinaryPath returns the path to an agent binary for the host.
func GetLocalAgentBinaryPath() (string, error) {
	return GetAgentBinaryPath(runtime.GOOS, runtime.GOARCH)
}

// agentIdentityPattern constrains the remote identity to path- and
// shell-safe characters, since it is embedded unquoted in remote commands.
var agentIdentityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// GetAgentRemoteIdentity returns the identity embedded in the remote
// side-agent binary name (side-agent-<identity>). It is platform-independent
// — a host only runs binaries built for its own platform — so remote paths
// are constructible before the remote platform is known, which the
// connect-attempt-as-check bootstrap depends on. Dev tiers use the source
// hash, so a rebuilt agent is re-installed by construction. Releases
// deliberately deviate from the content-addressed local cache naming and use
// the version instead: a binary content hash differs per platform and would
// break the platform independence above, while a release version pins
// per-platform content just as well because release artifacts are immutable.
// Byte integrity is enforced where it actually matters — the install-time
// checksum read-back verifies the transferred bytes, which name-based
// identity alone never did.
func GetAgentRemoteIdentity() (string, error) {
	identity := ""
	hash, err := agentSourceHash()
	switch {
	case err == nil:
		identity = hash
	case agentSourceHashOverride != "":
		identity = agentSourceHashOverride
	case agentVersion != "":
		identity = "v" + agentVersion
	default:
		return "", fmt.Errorf("agent source not available and no release version set: %w", err)
	}
	if !agentIdentityPattern.MatchString(identity) {
		return "", fmt.Errorf("agent identity %q contains unsafe characters", identity)
	}
	return identity, nil
}
