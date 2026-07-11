package common

import "os"

// ActiveEnvTypeEnvVar is injected into every command run via an Env so that
// spawned processes (e.g. tests) can detect which kind of Sidekick environment
// they are running inside. Its value is the env type string, e.g. "local",
// "local_git_worktree", "devpod", "openshell" or "modal".
const ActiveEnvTypeEnvVar = "SIDE_ACTIVE_ENV_TYPE"

// localEnvTypes mirrors env.EnvTypeLocal and env.EnvTypeLocalGitWorktree,
// which cannot be referenced here since the env package depends on common.
var localEnvTypes = map[string]bool{
	"local":              true,
	"local_git_worktree": true,
}

// IsActiveEnvNonLocal reports whether the current process runs inside a
// non-local Sidekick environment (e.g. a devpod or openshell container). Such
// environments never have credentials like LLM API keys or AWS profiles, nor
// support container-in-container tooling, so integration/e2e tests use this to
// skip. When ActiveEnvTypeEnvVar is unset or set to a local type, this returns
// false and behavior is unchanged.
func IsActiveEnvNonLocal() bool {
	envType := os.Getenv(ActiveEnvTypeEnvVar)
	return envType != "" && !localEnvTypes[envType]
}
