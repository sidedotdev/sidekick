package env

import "fmt"

// ConflictSnapshotRefPrefix is worktree-private so independent flows cannot
// overwrite each other's baselines. Pins survive until finalization or abort.
const ConflictSnapshotRefPrefix = "refs/worktree/sidekick/conflict-snapshots/"

func captureGitStateFragment() string {
	return `# Patches cannot encode conflict stages or pending merge parents.
mkdir "$tmpdir/git-state"
for name in index ORIG_HEAD MERGE_HEAD MERGE_MSG MERGE_MODE MERGE_AUTOSTASH MERGE_RR AUTO_MERGE; do
  path=$(git rev-parse --git-path "$name")
  if [ -f "$path" ]; then cp "$path" "$tmpdir/git-state/$name"; fi
done
if [ -f "$tmpdir/git-state/index" ]; then
  GIT_INDEX_FILE="$tmpdir/git-state/index" git update-index --no-split-index
fi
`
}

// A different repository may lack objects referenced by the index or merge
// metadata even after branch synchronization. Enumerating their closure before
// packing also includes trees and blobs that are not reachable from any commit.
func capturePortableGitStateFragment() string {
	return captureGitStateFragment() + fmt.Sprintf(`git for-each-ref --format='%%(objectname) %%(refname)' %s > "$tmpdir/git-state/snapshot-refs"
if [ -f "$tmpdir/git-state/MERGE_HEAD" ] || [ -s "$tmpdir/git-state/snapshot-refs" ]; then wip_dirty=1; fi
if [ "$wip_dirty" = 1 ]; then
  GIT_INDEX_FILE="$tmpdir/git-state/index" git ls-files --stage > "$tmpdir/index-entries"
  # Gitlinks belong to another repository; intent-to-add entries may have no object.
  awk '$1 != "160000" && $2 !~ /^0+$/ {print $2}' "$tmpdir/index-entries" > "$tmpdir/object-roots"
  for name in ORIG_HEAD MERGE_HEAD MERGE_AUTOSTASH AUTO_MERGE; do
    if [ -f "$tmpdir/git-state/$name" ]; then cat "$tmpdir/git-state/$name" >> "$tmpdir/object-roots"; fi
  done
  awk '{print $1}' "$tmpdir/git-state/snapshot-refs" >> "$tmpdir/object-roots"
  sort -u "$tmpdir/object-roots" > "$tmpdir/object-roots-sorted"
  git rev-list --objects --no-object-names --stdin --not HEAD < "$tmpdir/object-roots-sorted" > "$tmpdir/objects"
  # Explicit blob/tree roots are not excluded by the negative commit traversal.
  git rev-list --objects --no-object-names HEAD^{tree} > "$tmpdir/head-objects"
  awk 'NR == FNR {known[$1]=1; next} !($1 in known)' "$tmpdir/head-objects" "$tmpdir/objects" > "$tmpdir/extra-objects"
  git pack-objects --stdout < "$tmpdir/extra-objects" > "$tmpdir/git-state/objects.pack"
fi
`, shellQuote(ConflictSnapshotRefPrefix))
}
