package env

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"sidekick/common"

	"github.com/rs/zerolog/log"
)

// modalSeedRegistryCap bounds how many sandbox names are remembered per
// repo; older entries age out along with their usefulness as seeds.
const modalSeedRegistryCap = 5

var modalSeedMu sync.Mutex

// modalSeedRegistryPath is the host-side index mapping a local repo dir to
// sandbox names recently created for it, newest first. New sandboxes
// bootstrap from the latest guard snapshot among these, inheriting the repo
// (with deepened history) and any installed dependencies instead of
// starting from scratch.
func modalSeedRegistryPath() (string, error) {
	dataHome, err := common.GetSidekickDataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataHome, "modal", "seed_sandboxes.json"), nil
}

func readModalSeedRegistry() map[string][]string {
	registry := map[string][]string{}
	path, err := modalSeedRegistryPath()
	if err != nil {
		return registry
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return registry
	}
	if err := json.Unmarshal(data, &registry); err != nil {
		return map[string][]string{}
	}
	return registry
}

// modalSeedCandidates returns sandbox names recently created for repoDir,
// newest first.
func modalSeedCandidates(repoDir string) []string {
	if repoDir == "" {
		return nil
	}
	modalSeedMu.Lock()
	defer modalSeedMu.Unlock()
	return readModalSeedRegistry()[filepath.Clean(repoDir)]
}

// modalRegisterSeedSandbox records, best-effort, that a sandbox was created
// for repoDir, making its future snapshots seed candidates.
func modalRegisterSeedSandbox(repoDir, sandboxName string) {
	if repoDir == "" || sandboxName == "" {
		return
	}
	modalSeedMu.Lock()
	defer modalSeedMu.Unlock()
	registry := readModalSeedRegistry()
	key := filepath.Clean(repoDir)
	names := []string{sandboxName}
	for _, name := range registry[key] {
		if name != sandboxName && len(names) < modalSeedRegistryCap {
			names = append(names, name)
		}
	}
	registry[key] = names

	path, err := modalSeedRegistryPath()
	if err != nil {
		log.Warn().Err(err).Msg("failed to resolve modal seed registry path")
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		log.Warn().Err(err).Msg("failed to create modal seed registry dir")
		return
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		log.Warn().Err(err).Msg("failed to encode modal seed registry")
		return
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		log.Warn().Err(err).Msg("failed to write modal seed registry")
		return
	}
	if err := os.Rename(tmpPath, path); err != nil {
		log.Warn().Err(err).Msg("failed to write modal seed registry")
	}
}