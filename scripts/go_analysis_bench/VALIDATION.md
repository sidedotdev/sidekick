# Sustained desktop Go-analysis validation

## Scope and controls

The comparison uses the shared-daemon implementation at
`2144e05a3af74547fa8b186d786586b3aff35293` and the owned-session implementation
at `367911517dbc0b5a79ae61490a0af5a7682256fa`. Both execute the same embedded
semantic fixture, seeded into clones of the **baseline** Sidekick revision.
Changing the seed to the candidate revision would change the workload.

The multi-workspace pair runs for 30 minutes per implementation, alternating
three working directories. The single-workspace pair runs for 16 minutes per
implementation: long enough to cross three five-minute age boundaries without
workspace switching. Both use repeated queries/edits, ten-second idle periods
every twenty cycles, a 75-second final idle, one-second sampling and a
ten-second final-idle median window. The short periodic idles are deliberately
below the one-minute eviction threshold. Thus the single-workspace active
phase tests age recycling rather than repeatedly relying on idle eviction.

Every cycle asserts cross-file, dependency and post-edit definitions, semantic
references and autofixes. See the benchmark README for the exact workload.
Neither a successful LSP response nor an unchanged reference count alone is a
correctness assertion.

Analysis memory is summed simultaneous RSS, including gopls frontends, shared
servers, telemetry children and Go/compiler helpers. Combined figures also
include the benchmark process. RSS double-counts shared mappings and sampling
can miss sub-second peaks. Final-idle zero means no retained analysis process,
**not** zero active cost. Five-minute active windows are reported separately.

Runs use a dedicated OS account with no preexisting analysis processes.
Benchmark integration tests run under a different isolated account, so they
cannot contaminate the measurements. This sandbox is Linux 4.19.0-gvisor,
x86-64, with 17 visible CPUs, Go 1.26.4 and gopls v0.21.0. Caches are writable
by the benchmark account and prewarmed before either implementation runs.
Both sides are rerun after any cache preparation change. These are paired
observations, not randomized replicates or a prediction of latency on every
desktop.

## Accepted multi-workspace pair

Both corrected-cache runs completed their 30-minute workload and 75-second
final idle with **zero correctness failures**. The baseline completed 283
cycles, 849 workspace visits and 6,792 operations; the candidate completed
223 cycles, 669 visits and 5,352 operations. The candidate therefore completed
about 21% fewer cycles under this deliberately alternating workload.

| Summed RSS | Shared-daemon baseline | Owned sessions |
| --- | ---: | ---: |
| Sampled analysis peak | 3,035.8 MiB | 946.6 MiB |
| Final-idle analysis median, last ten seconds | 2,822.4 MiB | 0.0 MiB |
| Sampled peak including harness | 3,143.9 MiB | 1,038.0 MiB |
| Final-idle median including harness | 2,930.9 MiB | 110.9 MiB |

The sampled analysis peak decreased **68.8%**, including helpers and shared
daemons. Candidate analysis RSS reached zero during final idle, before explicit
benchmark cleanup. The harness itself remained alive and is included in the
combined measurements.

The six full five-minute **active** windows distinguish sustained working cost
from final-idle eviction:

| Elapsed workload minutes | Baseline median analysis RSS | Candidate median analysis RSS |
| --- | ---: | ---: |
| 0–5 | 2,830.3 MiB | 280.5 MiB |
| 5–10 | 2,891.9 MiB | 304.7 MiB |
| 10–15 | 2,894.3 MiB | 299.9 MiB |
| 15–20 | 2,900.8 MiB | 299.1 MiB |
| 20–25 | 2,900.5 MiB | 275.5 MiB |
| 25–30 | 2,899.5 MiB | 292.3 MiB |

Periodic idle samples are excluded from those active medians. A final cycle
extends beyond the duration boundary, producing a small partial seventh
window; it is retained in the raw summary but not presented as another full
five-minute observation.

Latency includes correctness assertions. Values below are **p50 / p95
milliseconds**, in the benchmark's fixed operation order:

| Operation | Baseline | Owned sessions |
| --- | ---: | ---: |
| Cross-file definition, first query of workspace visit | 4 / 6 | 2,078 / 2,166 |
| Dependency definition | 4 / 5 | 3 / 7 |
| Standard-library definition | 312 / 353 | 119 / 131 |
| References | 5 / 8 | 5 / 9 |
| Edit synchronization | 1 / 1 | 1 / 2 |
| Post-edit definition freshness | 115 / 280 | 12 / 15 |
| Post-edit reference freshness | 535 / 591 | 9 / 10 |
| Autofix | 830 / 922 | 181 / 208 |

The first query after every workspace switch now pays initialization cost.
This is a substantial latency trade-off, not a uniformly faster analyzer.
Subsequent operations within that visit reuse the session; a reference request
that initializes a session would also pay startup cost. Maximum observed
cross-file definition latency was **16.67 seconds baseline / 15.62 seconds
candidate**, so neither p95 should be read as a worst-case bound.

Reports and phase-separated summaries are retained in
[results/prewarmed-multi-baseline](results/prewarmed-multi-baseline) and
[results/prewarmed-multi-candidate](results/prewarmed-multi-candidate).
The latter also contains the validated before/after comparison. Both reports
have the same seed revision, fixture checksum and workload fingerprint.

These observations demonstrate substantially lower active and retained memory
on this workload, **not** attainment of 50 MB or a hard per-query RSS cap.
The single-workspace control separately checks age recycling without the
workspace switches that drive this pair's frequent session replacement.

## Accepted single-workspace age control

Both implementations completed the 16-minute workload without correctness
failures. The baseline completed **376 cycles / 3,008 operations**; the
candidate completed **1,130 cycles / 9,040 operations**, about three times as
many cycles under the same duration and idle policy.

| Summed RSS | Shared-daemon baseline | Owned sessions |
| --- | ---: | ---: |
| Sampled analysis peak | 1,488.4 MiB | 1,097.9 MiB |
| Final-window analysis median | 1,340.1 MiB | 0.0 MiB |
| Sampled peak including harness | 1,600.8 MiB | 1,203.9 MiB |
| Final-window median including harness | 1,452.6 MiB | 110.3 MiB |
| Samples in the last ten-second window | 11 | 1 |

The analysis peak decreased **26.2%**. The candidate's final ten-second window
contains only **one** sample because a **12.20-second sampling gap** overlaps
that window. Earlier final-idle samples also show zero analysis RSS after
expiry, but the final-window value is not a well-sampled steady-state plateau.
The complete stream is retained; neither the sampling gap nor its cause is
hidden by the aggregate median.

Active-window medians provide separate evidence:

| Elapsed workload minutes | Baseline median analysis RSS | Candidate median analysis RSS |
| --- | ---: | ---: |
| 0–5 | 1,371.2 MiB | 1,064.5 MiB |
| 5–10 | 1,391.9 MiB | 520.7 MiB |
| 10–15 | 1,392.3 MiB | 423.5 MiB |
| 15–end, partial window | 1,391.4 MiB | 390.0 MiB |

The baseline's frontend and shared server remain observable throughout the
workload and final idle. The candidate has four long-lived non-telemetry gopls
identities, first observed approximately **1, 302, 603 and 909 seconds** after
run start. The three completed sessions have observed lifetimes of about
**300, 300 and 305 seconds**. This directly exercises age replacement without
workspace switching. Replacement happens on the next operation; a short idle
can therefore carry a session slightly beyond five minutes. The fourth
session disappears during final idle. Telemetry and other helpers are included
in all memory totals, even though they are excluded from these server-lifetime
observations.

| Operation, p50 / p95 milliseconds | Baseline | Owned sessions |
| --- | ---: | ---: |
| Cross-file definition | 4 / 5 | 3 / 4 |
| Dependency definition | 4 / 5 | 3 / 3 |
| Standard-library definition | 119 / 195 | 117 / 133 |
| References | 551 / 587 | 5 / 5 |
| Edit synchronization | 1 / 1 | 1 / 19 |
| Post-edit definition freshness | 119 / 170 | 10 / 11 |
| Post-edit reference freshness | 559 / 589 | 8 / 10 |
| Autofix | 571 / 640 | 146 / 219 |

Age-related startup costs are infrequent enough not to dominate definition
p95 in this control. Maximum cross-file definition latency was **3.29 seconds
baseline / 2.08 seconds candidate**. Other operations still have outliers:
candidate standard-library definition reached **15.87 seconds**. Edit-sync
p95 also increased, so the fix is not a universal latency improvement.

Reports, comparisons and phase-separated summaries are retained in
[results/prewarmed-single-baseline](results/prewarmed-single-baseline) and
[results/prewarmed-single-candidate](results/prewarmed-single-candidate).

## Retained evidence and interpretation

[results/manifest.json](results/manifest.json) records executable revisions and
hashes, report hashes, original JSONL hashes, operation/action counts, cache
controls and hashes of the retained artifacts. Each accepted run directory
contains its report, window summary, peak process snapshot and complete
gzip-compressed memory, operation and code-action streams. Candidate directories
also contain the validated comparison. Across the four accepted runs, all
**24,192 operations** succeeded, and all **3,024 code-action requests** retained
both `source.fixAll` and `source.organizeImports`.

Verify the retained bytes and decompressed streams from the repository root:

```sh
python3 - <<'PY'
import gzip
import hashlib
import json
from pathlib import Path

root = Path("scripts/go_analysis_bench/results")
manifest = json.loads((root / "manifest.json").read_text())
for name, expected in manifest["artifacts"].items():
    actual = hashlib.sha256((root / name).read_bytes()).hexdigest()
    assert actual == expected, name
for name, run in manifest["runs"].items():
    for filename, expected in run["rawSha256"].items():
        raw = gzip.decompress((root / name / (filename + ".gz")).read_bytes())
        assert hashlib.sha256(raw).hexdigest() == expected, (name, filename)
print("Retained artifact and original-stream checksums verified")
PY
```

To independently regenerate a window summary, copy a retained `report.json`
and decompress its `memory.jsonl.gz` into a new directory under `.side/tmp`,
then run the README's summarizer command on that directory. No original
sandbox paths or live processes are needed to summarize the snapshots.

The measurements support bounded session retention and substantially lower
memory on this representative workload while preserving semantic behavior.
They do not prove an unbounded leak in the old version, an arbitrarily long
plateau in the new version, or a universal bound on one query's memory.
The initial single-workspace peak still exceeds one GiB, far above the
aspirational 50 MB scale. Five-minute recycling limits retained session age,
not the size of a large workspace's initial analysis.

The selected fix also removes shared-daemon/debug/trace launch behavior;
these runs compare the complete fix, not isolated causal contributions from
each setting. They demonstrate the latency cost of workspace switching and
the benefit of reusing a session within a workspace. Longer machine-specific
soaks remain useful. An upgrade can leave an old shared daemon behind:
inspect old Sidekick instances and their daemons separately rather than
terminating unrelated editor processes.

## Provenance

The harness obtains Git metadata from its **runtime cwd**, not from the
binary. Each baseline run must execute with cwd set to its baseline build
checkout. An initial run from the candidate cwd was interrupted and rejected;
it is not part of the retained comparison.

The measured binaries were independently rebuilt from their source checkouts
and compared byte-for-byte:

| Implementation | Build revision | Executable SHA-256 |
| --- | --- | --- |
| Shared daemon | `2144e05a3af74547fa8b186d786586b3aff35293` | `7868151133c48eb32784c97073e7780d23a716eb4d92492d70fbb8d8ff4937f4` |
| Owned sessions | `367911517dbc0b5a79ae61490a0af5a7682256fa` | `0654ec3f11e50f624e5904127b35ee811480f73ee91e52f6facbd9e6e531fff2` |

The candidate runtime checkout also contains this step's test, diagnostic and
documentation changes. None changes the desktop implementation or benchmark
workload; rebuilding there produced the identical candidate executable.
Its report's diff checksum describes that runtime checkout, not additional
binary behavior. Preserve the binary/source manifest when reproducing with a
dirty checkout; a revision field alone is insufficient evidence.

## Reproduction

Use an isolated account, writable caches and a short enough `TMPDIR` for the
baseline daemon's Unix socket. On long checkout paths, a short symlink to a
scratch directory under `.side/tmp` avoids the Unix socket path limit. Do not
use `--allow-existing` for accepted comparisons. Do not terminate another
user's editor daemon to make a run pass.

Run this from the current repository, under that account. The two detached
worktrees must not already exist:

```sh
set -eu
export GOWORK=off GOTOOLCHAIN=local
ROOT=$(pwd -P)
BEFORE="$ROOT/.side/tmp/validation-before"
AFTER="$ROOT/.side/tmp/validation-after"
OUT="$ROOT/.side/tmp/validation-$(date +%Y%m%dT%H%M%S)"
mkdir "$OUT"

git worktree add --detach "$BEFORE" 2144e05a3af74547fa8b186d786586b3aff35293
git worktree add --detach "$AFTER" 367911517dbc0b5a79ae61490a0af5a7682256fa
(cd "$BEFORE" && go build -buildvcs=false -o "$OUT/before" ./scripts/go_analysis_bench)
(cd "$AFTER" && go build -buildvcs=false -o "$OUT/after" ./scripts/go_analysis_bench)
sha256sum "$OUT/before" "$OUT/after" > "$OUT/binaries.sha256"
git -C "$BEFORE" rev-parse HEAD > "$OUT/before.revision"
git -C "$AFTER" rev-parse HEAD > "$OUT/after.revision"
git -C "$BEFORE" diff --exit-code HEAD
git -C "$AFTER" diff --exit-code HEAD

go env -json GOPROXY GOSUMDB GOMODCACHE GOCACHE GOTOOLCHAIN \
  > "$OUT/cache-environment.json"
(cd "$BEFORE" && go list -m -json ... \
  > "$OUT/modules-warm.json" 2> "$OUT/modules-warm.stderr")
if test -s "$OUT/modules-warm.stderr"; then
  cat "$OUT/modules-warm.stderr" >&2
  exit 1
fi
(cd "$BEFORE" && GOPROXY=off GOSUMDB=off GOVCS='*:off' GOTOOLCHAIN=local \
  go list -m -json ... \
  > "$OUT/modules-offline.json" 2> "$OUT/modules-offline.stderr")
if test -s "$OUT/modules-offline.stderr"; then
  cat "$OUT/modules-offline.stderr" >&2
  exit 1
fi
python3 - "$OUT" > "$OUT/modules-check.txt" <<'PY'
import json
import pathlib
import sys

decoder = json.JSONDecoder()
for name in ("modules-warm.json", "modules-offline.json"):
    text = (pathlib.Path(sys.argv[1]) / name).read_text(encoding="utf-8")
    count = 0
    while text.strip():
        text = text.lstrip()
        module, end = decoder.raw_decode(text)
        if not isinstance(module, dict) or "Error" in module:
            raise SystemExit(f"{name}: invalid module result: {module!r}")
        count += 1
        text = text[end:]
    if not count:
        raise SystemExit(f"{name}: no module results")
    print(f"{name}: {count} modules, no Error fields")
PY

run_pair() {
  name=$1
  workspaces=$2
  duration=$3
  baseline="$OUT/$name-before"
  candidate="$OUT/$name-after"
  (cd "$BEFORE" && "$OUT/before" \
    --label "$name-before" --output "$baseline" --seed "$BEFORE" \
    --workspaces "$workspaces" --cycles 0 --duration "$duration" \
    --idle-every 20 --idle 10s --final-idle 75s \
    --steady-window 10s --sample 1s)
  (cd "$AFTER" && "$OUT/after" \
    --label "$name-after" --output "$candidate" --seed "$BEFORE" \
    --workspaces "$workspaces" --cycles 0 --duration "$duration" \
    --idle-every 20 --idle 10s --final-idle 75s \
    --steady-window 10s --sample 1s --compare "$baseline/report.json")
  go run ./scripts/go_analysis_bench/summarize \
    --input "$baseline" --window 5m > "$OUT/$name-before-windows.json"
  go run ./scripts/go_analysis_bench/summarize \
    --input "$candidate" --window 5m > "$OUT/$name-after-windows.json"
}
run_pair multi 3 30m
run_pair single 1 16m
```

The run duration bounds the workload, including periodic idle, but excludes
setup and final idle; the last cycle can finish after the bound. Comparisons
require matching workload fingerprints, successful correctness checks and
uncontaminated startup. Timed runs can complete different cycle counts.
Keep reports, comparisons, raw JSONL streams and binary/source manifests.
Use longer durations, such as two hours, for machine-specific soak testing.

## Regression and lint verification

```sh
go run ./scripts/affected_tests \
  ./coding/lsp ./coding ./dev ./scripts/go_analysis_bench/... \
  ./scripts/lint_chat_history_append -race -count=1

# Run separately under an account without preexisting analysis processes.
SIDEKICK_GO_ANALYSIS_INTEGRATION=1 \
  go run ./scripts/affected_tests ./scripts/go_analysis_bench \
  -run 'TestBenchmarkIntegration|TestCanceledBenchmarkCleansOwnedProcesses' \
  -race -count=1

go run ./scripts/lint_chat_history_append
go vet ./coding/lsp ./coding ./dev ./scripts/go_analysis_bench/... \
  ./scripts/lint_chat_history_append
```

- `TestGoSemanticsAcrossSessionRecycling` exercises real gopls across workspace,
  age and idle recycling, checking semantic results and post-recycle edits and
  autofixes rather than only cache state.
- `TestRelatedSymbolsAcrossWorkspaceEvictionAndEdits` alternates workspaces,
  changes call targets and caller names, excludes a same-spelled local function,
  and asserts exact caller context and current reference lines.
- `TestLSPDocumentSyncWhenApplyingEditBlocks_golang` asserts the exact shifted
  reference range after applying an edit through the desktop activity, then
  asserts the same result after workspace eviction and initialization from disk.
- Summarizer tests distinguish active/idle windows, reused PIDs and telemetry
  helpers; malformed, inconsistent, failed or contaminated artifacts are rejected.

Red/green checks used reversible Go overlays, without modifying the selected
implementation. Disabling age expiry, workspace cleanup or idle expiry caused
the corresponding session regression to fail. Skipping document sync reproduced
the stale-file out-of-range error in the edit-block regression. Skipping
workspace cleanup also failed the related-symbol eviction assertion. The
summarizer failed against its initial no-op implementation and against a
mutation accepting missing final-idle samples; all tests pass with the actual
implementation.

The relevant package race run passed 232 coding/LSP tests, 1,402 dev tests,
42 benchmark tests and 59 lint tests; 12 dev tests and two opt-in benchmark
integration tests were skipped there. The two real benchmark integration tests
passed separately under their isolated account. The summarizer's 21 tests also
pass with the race detector. A repository-wide affected-tests run passed all
four changed packages, reusing cached passes for 94 unaffected packages.

The real repository lint command reported no violations. As a positive
call-hierarchy control, the lint package and fixture were copied into
`.side/tmp/lint-live`, its snapshot was removed, and
`TestFindViolations_Fixture` was run there. Real gopls regenerated four prepare
keys and six incoming-call keys; the test detected the expected direct and
transitive workflow callers while excluding sanctioned/test callers.
The committed snapshot was not changed. This verifies the independent lint
client, not a mock desktop call-hierarchy implementation.

## Rejected cache-setup trial

The first provenance-correct pair used a module cache that was readable but
not fully writable by the isolated account. The candidate failed at workspace
0, cycle 160: autofix exceeded the unchanged 60-second operation limit.
Its report has one correctness failure and no final-idle samples. Reported
zero steady RSS is therefore **missing data**, not successful idle eviction.
The complete failed report and compressed operation, action and memory streams
are preserved in [results/rejected-cache-run](results/rejected-cache-run),
with a supervisor log and SHA-256 manifest.

A metadata probe returned 833 module objects without `Error` fields, yet its
stderr contained **1,471 encoded HTTP/2 GET method headers**, each matching
`http2: Transport encoding header ":method" = "GET"`, and **1,471
permission-denied diagnostics** while trying to cache missing metadata.
The literal substring `HTTP GET` occurs zero times; that nonmatching count is
not a network-access control. After correcting ownership and prewarming,
a repeated probe emitted no stderr or HTTP/2 traces. This establishes a real
cache defect, **not that it caused the autofix timeout**; HTTP/2 tracing alone
does not rule out other network access.

The separate seed-metadata controls returned **832 module objects**, with no
`Error` fields or stderr, both online and with `GOPROXY=off`, `GOSUMDB=off`,
`GOVCS=*:off` and `GOTOOLCHAIN=local`. An empty-module-cache negative control
exited with status 1 and `module lookup disabled by GOPROXY=off`. These controls
demonstrate that the seed's module metadata was available offline, not that
every benchmark operation was network-isolated. The original probes, stderr
and control metadata are retained in
[results/cache-preflight](results/cache-preflight) and the artifact manifest.

Both implementations were rerun with the corrected, equally prewarmed cache,
unchanged workload, durations and operation limit. Neither the failed candidate
nor its earlier baseline is included in the accepted before/after comparison.

### Excluded baseline observations

The excluded baseline completed 304 cycles and 7,296 operations with zero
correctness failures. Its report records the baseline source revision and an
empty Git diff. Summed analysis RSS peaked at **2,104.4 MiB** and retained
**1,460.5 MiB** in the final-idle median. Including the harness, these figures
were **2,215.9 MiB peak / 1,575.4 MiB final idle**.

The six full five-minute active windows had analysis RSS medians of
**1,396.2, 1,484.9, 1,496.0, 1,503.0, 1,504.4 and 1,523.3 MiB**.
The last cycle extended into a seventh window with only five active samples;
its median was 1,513.0 MiB. These active measurements are separate from the
75-second final idle.

The peak snapshot includes a transient child of the shared server with the
same command and 568.9 MiB RSS, consistent with fork-before-exec. Summed RSS
counts those inherited mappings again. The sampled total exceeds 2 GiB, but
must not be described as a demonstrated multi-gigabyte persistent working set.

This run demonstrates substantial idle retention, not an unbounded leak:
the modest active-window increase could include cache warm-up, and a
30-minute observation cannot prove the reported behavior over arbitrarily
long sessions.