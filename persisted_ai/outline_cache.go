package persisted_ai

import (
	"context"
	"fmt"
	"strings"

	"sidekick/coding/tree_sitter"
	"sidekick/env"

	"github.com/kelindar/binary"
	"github.com/rs/zerolog/log"
)

// dirOutlineCacheKeyPrefix namespaces per-git-tree raw outline maps in
// workspace key-value storage. It embeds the snapshot version fingerprint so
// entries written by different extraction/encoding logic are simply never
// found.
var dirOutlineCacheKeyPrefix = "dirOutlineRaw:v" + tree_sitter.OutlineSnapshotVersion + ":"

// dirOutlineCacheMaxAncestors bounds how far back in history we look for the
// closest ancestor with a cached outline.
const dirOutlineCacheMaxAncestors = 100

// outlineCacheState describes the repo state an outline walk cache was loaded
// against, so the updated cache can be persisted under the right key.
type outlineCacheState struct {
	cache *tree_sitter.OutlineWalkCache
	// storeKey is the storage key (derived from HEAD's git tree) to persist
	// walk results under. It is empty when persisting is impossible (no git
	// state), unsafe (dirty working tree, whose outlines do not reflect
	// HEAD's tree) or pointless (the cache was loaded from that same key
	// with nothing affected).
	storeKey string
	// evictKey is the ancestor snapshot key the load hit, deleted once a
	// newer snapshot is successfully stored so per-tree snapshots don't
	// accumulate without bound along a lineage.
	evictKey string
}

// loadOutlineWalkCache builds an outline walk cache from the closest git
// ancestor with a persisted outline, marking paths changed since that
// ancestor (plus all working-tree changes) as affected. Best-effort: on any
// git or storage failure it returns a state that disables caching rather
// than an error, since the walk works fine without it.
func (ra *RagActivities) loadOutlineWalkCache(ctx context.Context, workspaceId string, ec env.EnvContainer) outlineCacheState {
	trees, dirtyPaths, err := gitRepoState(ctx, ec, dirOutlineCacheMaxAncestors)
	if err != nil || len(trees) == 0 {
		log.Debug().Err(err).Msg("outline cache disabled: failed to read git state")
		return outlineCacheState{}
	}
	state := outlineCacheState{
		cache: &tree_sitter.OutlineWalkCache{},
	}
	// Never persist while the working tree is dirty: a snapshot stored under
	// HEAD's tree would either omit dirty tracked paths or carry their
	// uncommitted content, making those files vanish from (or go stale in)
	// later clean runs at the same HEAD.
	if len(dirtyPaths) == 0 {
		state.storeKey = dirOutlineCacheKeyPrefix + trees[0]
	}

	keys := make([]string, len(trees))
	for i, tree := range trees {
		keys[i] = dirOutlineCacheKeyPrefix + tree
	}
	values, err := ra.DatabaseAccessor.MGet(ctx, workspaceId, keys)
	if err != nil {
		log.Debug().Err(err).Msg("outline cache lookup failed")
		return state
	}
	hitIdx := -1
	for i, value := range values {
		if value != nil {
			hitIdx = i
			break
		}
	}
	if hitIdx == -1 {
		return state
	}
	var raw map[string]string
	if err := binary.Unmarshal(values[hitIdx], &raw); err != nil {
		log.Debug().Err(err).Msg("outline cache entry failed to unmarshal")
		return state
	}
	if len(raw) == 0 {
		// Defense against poisoned entries: an empty snapshot would
		// reconstruct an empty tree on every exact-tree hit. Treat it as a
		// miss so a full walk re-stores a real snapshot under the same key.
		log.Warn().Str("key", keys[hitIdx]).Msg("ignoring empty outline cache snapshot")
		return state
	}

	affected := make(map[string]bool, len(dirtyPaths))
	for _, p := range dirtyPaths {
		affected[p] = true
	}
	if hitIdx > 0 {
		changed, err := gitChangedPaths(ctx, ec, trees[hitIdx])
		if err != nil {
			log.Debug().Err(err).Msg("outline cache hit unusable: failed to diff against cached ancestor")
			return state
		}
		for _, p := range changed {
			affected[p] = true
		}
	}
	state.cache.Raw = raw
	state.cache.Affected = affected
	if hitIdx == 0 {
		state.storeKey = ""
	} else {
		state.evictKey = keys[hitIdx]
	}
	return state
}

// storeOutlineWalkCache persists the raw outlines recorded while producing
// entries (via a full walk or cache reconstruction) under the state's store
// key. Best-effort.
func (ra *RagActivities) storeOutlineWalkCache(ctx context.Context, workspaceId string, state outlineCacheState) {
	if state.storeKey == "" {
		return
	}
	updated := state.cache.UpdatedSnapshot()
	if len(updated) == 0 {
		return
	}
	data, err := binary.Marshal(updated)
	if err != nil {
		log.Debug().Err(err).Msg("outline cache marshal failed")
		return
	}
	if err := ra.DatabaseAccessor.MSetRaw(ctx, workspaceId, map[string][]byte{state.storeKey: data}); err != nil {
		log.Debug().Err(err).Msg("outline cache store failed")
		return
	}
	if state.evictKey != "" && state.evictKey != state.storeKey {
		if err := ra.DatabaseAccessor.MDelete(ctx, workspaceId, []string{state.evictKey}); err != nil {
			log.Debug().Err(err).Msg("outline cache ancestor eviction failed")
		}
	}
}

func runGit(ctx context.Context, ec env.EnvContainer, args ...string) (string, error) {
	out, err := ec.Env.RunCommand(ctx, env.EnvRunCommandInput{Command: "git", Args: args})
	if err != nil {
		return "", err
	}
	if out.ExitStatus != 0 {
		return "", fmt.Errorf("git %s exited with status %d: %s", strings.Join(args, " "), out.ExitStatus, out.Stderr)
	}
	return out.Stdout, nil
}

// gitStateSeparator delimits the outputs of the two git commands that
// gitRepoState combines into a single execution.
const gitStateSeparator = "===SIDEKICK-GIT-STATE==="

// gitRepoState returns the tree hashes of HEAD and its recent ancestors (most
// recent first) plus working-tree changes relative to HEAD (including
// untracked files; renames and copies yield both sides). Both are gathered in
// one command execution, since each execution costs a full network round trip
// on SSH-backed environments.
func gitRepoState(ctx context.Context, ec env.EnvContainer, limit int) (trees []string, dirtyPaths []string, err error) {
	script := fmt.Sprintf("git log --format=%%T -n %d && printf '%s' && git status --porcelain -z -uall", limit, gitStateSeparator)
	out, err := ec.Env.RunCommand(ctx, env.EnvRunCommandInput{Command: "sh", Args: []string{"-c", script}})
	if err != nil {
		return nil, nil, err
	}
	if out.ExitStatus != 0 {
		return nil, nil, fmt.Errorf("git state script exited with status %d: %s", out.ExitStatus, out.Stderr)
	}
	parts := strings.SplitN(out.Stdout, gitStateSeparator, 2)
	if len(parts) != 2 {
		return nil, nil, fmt.Errorf("git state output missing separator")
	}
	for _, line := range strings.Split(parts[0], "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			trees = append(trees, line)
		}
	}
	return trees, parseGitStatusZ(parts[1]), nil
}

// parseGitStatusZ extracts all paths from `git status --porcelain -z` output.
// Over-inclusion is safe (it only forces a recompute), so ambiguous tokens
// are treated as paths.
func parseGitStatusZ(out string) []string {
	var paths []string
	tokens := strings.Split(out, "\x00")
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if token == "" {
			continue
		}
		if len(token) > 3 && token[2] == ' ' {
			paths = append(paths, token[3:])
			// renames and copies carry the source path as an extra
			// NUL-separated token
			if token[0] == 'R' || token[0] == 'C' {
				i++
				if i < len(tokens) && tokens[i] != "" {
					paths = append(paths, tokens[i])
				}
			}
		} else {
			paths = append(paths, token)
		}
	}
	return paths
}

// gitChangedPaths returns repo-relative paths that differ between the given
// tree and HEAD. Rename detection is disabled so a committed rename reports
// both its source and destination paths, not just the destination.
func gitChangedPaths(ctx context.Context, ec env.EnvContainer, tree string) ([]string, error) {
	out, err := runGit(ctx, ec, "diff", "--name-only", "--no-renames", "-z", tree, "HEAD")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}
