# Interdiff model and fuzzing

`Interdiff` compares two review patches against a common base. It constructs
sparse base files from both patches, applies each patch to that shared model,
then diffs the resulting snapshots. File existence, paths, modes and content
are compared together; changes are not subtracted as sets of added/deleted
lines.

Unobserved base regions are opaque spans identified by base path and
coordinates. They are never fabricated text. Both projections retain the same
spans in the same order, so known regions between them can be diffed independently.
This also keeps memory proportional to observed content rather than the largest
line number in a hunk. Actual line strings retain their final newline state.
Output hunks use at most five context lines on either side.

The reconstruction approach is also used by
[patchutils interdiff](https://github.com/twaugh/patchutils/blob/master/src/interdiff.c).
This implementation uses explicit opaque spans instead of filler lines or
repairing another interdiff's output. It uses
[go-gitdiff](https://github.com/bluekeyes/go-gitdiff) for Git patch parsing and
formatting, and [go-diff](https://github.com/sergi/go-diff) for line matching.
Complete lines are encoded as distinct tokens, preserving newline state and
arbitrary byte content without character-level matching.

## What the inputs can express

Complete Git diffs need not contain complete file contents. Ordinary unchanged
regions are safely shared as opaque spans. A pure rename can likewise be
compared without knowing the file's bytes.

Some comparisons genuinely require bytes that neither patch supplies. For
example, a pure copy header contains no file contents; removing that copy later
requires a deletion patch with those contents. Such comparisons return an error,
rather than an invalid patch or an empty success. `TestInterdiffCopyInformation`
demonstrates both the unavailable case and a case where the other review reveals
the necessary contents.

Binary summary lines likewise contain no binary payload. Their interdiffs
preserve available object IDs and metadata as summaries, not applicable binary
patches. This matches ordinary `git diff` output without `--binary`.
Conflicting base evidence is rejected. An empty prior review retains the API's
first-review convention: return an empty incremental diff, letting the caller
show the full review.

The review activity explicitly requests rename-only detection, overriding
`diff.renames=copies` from Git configuration. Copied files therefore include
their contents in newly generated review patches. The activity also disables
whitespace filtering before generating patches for interdiff. Binary files
remain summaries, as in the full review diff. Legacy copy-only review patches
still use the activity's explicit full-diff fallback when their contents
cannot be recovered from the input patches.

## Moving review bases

The activity records each review's base commit and total patch alongside its
displayed diff. Pinned-step reviews also retain the earlier work excluded from
that display. Equal display text, including an empty diff, does not identify
equal baselines; workflows carry the snapshot explicitly through review rounds
and target changes.

When the base changes, the activity obtains a read-only base-to-base Git diff.
`RebaseReviewDiff` merges the prior review and that base transition against
their shared sparse base, then renders the prior review relative to the new
base. Interdiff therefore compares patches in one coordinate system without
counting upstream edits as new feature work. Pinned exclusions are normalized
the same way. Normalization does not invoke Git apply/merge, create indexes,
or modify the worktree.

A transition is read once per distinct old base during an activity. Normally
there is one old review base; a newly pinned start point can require another
transition from its own base. Same-base comparisons need no transition read.
The base commits must remain available in the repository. Missing objects or
conflicting feature/upstream edits produce an explicit comparison error and
full-diff fallback, rather than guessed alignment.

`FuzzInterdiffMovingBases` supplies independent old/new base and feature
contents. Its exact-tree oracle verifies both the normalized prior review and
the resulting interdiff, including upstream insertions/deletions and unchanged
feature work. Activity tests cover whole-task and pinned reviews, merged or
rebased upstream work, reverted edits, empty reviews and identical displayed
patches with distinct provenance. Workflow tests verify that interactive target
changes return the correct snapshot for subsequent review and fulfillment.

## Independent oracle

`FuzzInterdiffTrees` starts with three concrete trees: base, prior and current.
It asks Git to generate both review patches, and first verifies that those
patches apply to their expected trees. It then applies the synthesized
interdiff with `git apply --cached --unidiff-zero`, without `--recount` or a
three-way fallback, and compares the resulting Git tree ID with the expected
tree ID. This checks every byte, path, mode and file's existence. Forward,
reverse and identity comparisons are exercised. Empty prior patches follow the
first-review convention described above.

The corpus includes historical review fixtures converted into mutable source
triples, plus seeds for every mutation operation. The mutation program varies
either review, both reviews together, or the common base:

- insertions, deletions, replacements, moved and duplicated line blocks;
- reverted work, shared work, repeated lines, overlapping and distant hunks;
- empty files, file creation/deletion, executable bits and type changes;
- renames, copies, overwrites, symlinks and gitlinks;
- trailing-newline changes, CRLF and whitespace changes;
- spaces, quotes, tabs, newlines and non-ASCII bytes in paths.

Diff generation varies context (including zero and asymmetric context), rename
detection, Myers/histogram matching, minimal matching and abbreviated/full IDs.
The content oracle is independent of the production parser and matcher.
Additional assertions check bounded context and omission of unchanged sections.

Input text is bounded to 64 KiB and 2,000 lines; mutation programs to 32
instructions. NUL bytes are replaced for the text target.
`FuzzInterdiffBinarySummaries` separately checks binary existence, modes and
object-ID transitions against Git's direct prior-to-current summary, including
all seeded combinations of existence and executable state. Pure copy headers
with unavailable contents have explicit contract tests instead of being silently
skipped by the tree oracle.

The repositories are isolated from user/system Git configuration, use a private
index, and require only local Git plumbing, not commits in the working checkout,
network access or external services.

## Running

Seed cases run during ordinary package tests:

```sh
go run ./scripts/affected_tests ./coding/diffanalysis ./coding ./dev
```

Run mutation fuzzing explicitly, one target at a time:

```sh
go test ./coding/diffanalysis -run '^FuzzInterdiffTrees$' \
  -fuzz '^FuzzInterdiffTrees$' -fuzztime=2m -parallel=4
go test ./coding/diffanalysis -run '^FuzzInterdiffBinarySummaries$' \
  -fuzz '^FuzzInterdiffBinarySummaries$' -fuzztime=1m -parallel=4
go test ./coding/diffanalysis -run '^FuzzInterdiffMovingBases$' \
  -fuzz '^FuzzInterdiffMovingBases$' -fuzztime=2m -parallel=4
```

The historical runner overlays an older implementation and its patchutils
dependency without changing the checkout. It rejects build failures, unmatched
test filters and setup-only failures as evidence of a red check. Full JSON
diagnostics and the overlay are retained under `.side/tmp`; use `-verbose` to
print diagnostics.

```sh
go run ./scripts/check_interdiff_fuzz_history \
  -revision '2bb9772^' -run '^FuzzInterdiffTrees$/seed#0$' -expect fail
go run ./scripts/check_interdiff_fuzz_history \
  -revision 2bb9772 -run '^FuzzInterdiffTrees$/seed#0$' -expect pass
```

Other verified historical checks:

| Failure family | Before fix (red) | Seed | Original fix control |
| --- | --- | --- | --- |
| Missing shared trailing context | `0cc722c^` | `74` | `0cc722c` passes |
| Excessive context in large additions | `abe91c0^` | `5` | `abe91c0` passes |
| Hunkless mode transition | `7809e8f^` | `12` | Current implementation passes |
| Omitted common lines, real review | `d0fd081^` | `96` | `d0fd081` passes |
| Counted omitted lines, real review | `33e41bf^` | `95` | `33e41bf` passes forward; current implementation passes both ways |
| Reverted trailing hunk | `2bb9772^` | `0` | `2bb9772` passes |

For the counted-omission forward-only historical control, append `/forward$`
instead of `$` to the seed filter. The original fix still fails the reverse
case; that is not treated as a successful full-seed control.