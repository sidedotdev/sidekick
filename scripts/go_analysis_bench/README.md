# Desktop Go-analysis benchmark

A reproducible local workload for semantic definitions across files and
dependencies, related-symbol references, `source.fixAll` /
`source.organizeImports` requests, applied import fixes, and freshness after
edits. It cycles through working directories, repeats queries/edits, and idles
periodically and at the end. Incorrect results or failed operations fail the run.

## Retained implementation

Desktop analysis uses owned, standalone gopls processes rather than an automatic
shared daemon. Each activity owner caches at most one workspace client, closes
it after **one minute idle**, and replaces it before the next operation once it
is **five minutes old**. In-flight operations are not terminated by eviction.
Worker shutdown closes the owner. This bounds retention/lifetime, not peak RSS
for a large active workspace; roughly 50 MB has not been achieved.

The policy is identical across environments. Managed SSH channels explicitly
register with the side-agent; actual operations hold activity leases and
heartbeats through cleanup, while idle cached connections do not pin the
environment. Disconnected clients are replaced before the next query.
Document-sync sequences hold a single lease, preserving edit freshness across
recycling. The separate chat-history lint tool keeps its call-hierarchy client.

## Reproduce

Use a dedicated Linux account with no existing gopls processes. The monitor
includes all same-user matching daemons, so an editor's server contaminates
comparison. Install matching Go/gopls versions and prewarm module/build caches
equally for both binaries, outside measurement.

From the repository root, choose unused scratch paths:

```sh
ROOT=$(pwd)
BASE="$ROOT/.side/tmp/go-analysis-before-src"
git worktree add --detach "$BASE" 2144e05

go -C "$BASE" mod download
go -C "$ROOT" mod download
go -C "$BASE" list -p=2 -export -test ./... > .side/tmp/before-prewarm.txt
go -C "$ROOT" list -p=2 -export -test ./... > .side/tmp/after-prewarm.txt
go -C "$BASE" build -o "$ROOT/.side/tmp/go-analysis-before" ./scripts/go_analysis_bench
go -C "$ROOT" build -o "$ROOT/.side/tmp/go-analysis-after" ./scripts/go_analysis_bench

(
  cd "$BASE"
  "$ROOT/.side/tmp/go-analysis-before" \
    --label before --output "$ROOT/.side/tmp/go-analysis-before-run" \
    --seed "$BASE" --workspaces 3 --cycles 0 --duration 30m \
    --idle-every 20 --idle 10s --final-idle 75s \
    --steady-window 10s --sample 1s
)

"$ROOT/.side/tmp/go-analysis-after" \
  --label after --output "$ROOT/.side/tmp/go-analysis-after-run" \
  --seed "$BASE" --workspaces 3 --cycles 0 --duration 30m \
  --idle-every 20 --idle 10s --final-idle 75s \
  --steady-window 10s --sample 1s \
  --compare "$ROOT/.side/tmp/go-analysis-before-run/report.json"
```

The historical baseline requires its Git history. Run each binary from its own
source checkout: provenance is read from the runtime working directory, not
embedded in the executable.

Use `--workspaces 1 --duration 16m` for the age-recycling control, or increase
duration for longer trials. For a quick correctness smoke, omit `--seed` and use
`--workspaces 3 --cycles 5 --idle-every 1 --idle 1s --final-idle 65s`.
Duration bounds the workload, not setup/final idle; an in-progress cycle finishes.
Repeat paired trials with consistent cache treatment and report cycle counts
alongside latency/memory. Comparison rejects mismatched workloads, failed checks,
missing final-idle samples, and preexisting analysis processes.

## Artifacts and accounting

Runs write `report.json`, `comparison.json` when requested, and streamed
`memory.jsonl`, `operations.jsonl`, and `actions.jsonl` to the selected output
directory.

Analysis RSS sums daemons and helpers, including detached shared daemons and
Go/compiler children, but excludes the harness. Combined metrics include the
harness and are needed when comparing an in-process replacement. PID/start-time
tracking follows observed reparented children. RSS comes from procfs; optional
executable/command metadata failures do not hide memory. Required accounting
failures fail the run. This measures local processes, not SSH-host RSS.

RSS is not PSS: shared pages may be counted more than once. Sampled peaks can
miss short-lived processes. Final-idle RSS is a median of the last
`--steady-window`, not evidence of a long-run plateau. Inspect active/idle phases
and the full time series rather than inferring active cost from final eviction.

Generate phase-separated summaries on demand; generated windows need not be
checked in:

```sh
go run ./scripts/go_analysis_bench/summarize \
  --input /absolute/path/to/completed-run --window 5m
```

The summarizer validates identities, RSS sums, report/sample agreement,
successful checks, and uncontaminated startup.

Cleanup only signals processes with the run's inherited random environment
token, rechecking identity and ownership. Accounting is deliberately broader
than cleanup ownership. Never kill unrelated editor processes. SIGINT/SIGTERM
trigger bounded cleanup; SIGKILL/crashes require manual inspection.

## Regression checks

```sh
go run ./scripts/affected_tests ./scripts/go_analysis_bench/... \
  ./coding/lsp ./coding ./dev ./env ./sideagent ./worker \
  ./scripts/lint_chat_history_append
```

Real benchmark correctness/cancellation checks require an isolated account:

```sh
SIDEKICK_GO_ANALYSIS_INTEGRATION=1 \
  go run ./scripts/affected_tests ./scripts/go_analysis_bench \
  -run 'TestBenchmarkIntegration|TestCanceledBenchmarkCleansOwnedProcesses' -count=1
```

Regression tests retain semantic resolution, test/dependency references,
autofixes, edit freshness, workspace/idle/age recycling, cancellation, SSH
routing, and managed-background activity/heartbeat/watchdog behavior.
Loopback SSH and reconnection tests exercise the idle protocol without secrets;
live Modal idle→wake→semantic-freshness timing remains an end-to-end coverage gap.
The retained local memory measurements do not validate live Modal timing.