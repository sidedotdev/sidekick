# Desktop Go-analysis benchmark

A Linux-only, end-to-end workload for measuring Sidekick's current desktop Go
analysis and evaluating a replacement or lifecycle/configuration change.
It calls `LSPActivities`, rather than implementing lookups with text matching or
substituting the gopls CLI for desktop requests. It does not change desktop
launch flags. The current factory uses `Jsonrpc2LSPClient`; a future replacement
must be wired into the factory and exercised by the same assertions.

This benchmark is not the memory fix. A passing small-fixture run is not evidence
that multi-hour growth is fixed.

## Workload and assertions

Each cycle visits every independent working directory and:

1. Resolves a reference to a function declared in another file.
2. Resolves an imported function in a separate Go module.
3. Requests semantic references and checks the exact expected call sites. A
   same-spelled local function is deliberately excluded.
4. Alternates the edited call between two declarations, varies its line position,
   and sends the desktop's didOpen/didChange/didSave/didClose sequence.
5. Checks the new definition and the reference sets of both declarations
   immediately after the edit, without retries that could mask stale results.
6. Reintroduces a missing `strings` import and an unused `fmt` import, runs
   `AutofixActivity`, and verifies the resulting import and source content.
7. Resolves the newly imported `strings.TrimSpace` into the Go standard library.

The action recorder verifies the desktop requests exactly `source.fixAll` and
`source.organizeImports` and streams the actual returned actions. The import
fixture exercises organization, **not every possible `source.fixAll` fix**.
Inspect `actions.jsonl` when evaluating alternatives; requesting both kinds does
not establish that import organization alone can replace all current fixes.
The initial gopls v0.21.0 smoke run returned organize-imports actions only.

The dependency fixture is a deterministic, offline module using a local
`replace`; it tests semantic cross-module resolution, not a vendored text match.
The stdlib lookup additionally exercises source outside the working directory.
For realistic module graphs, use `--seed` with a representative repository.
Seed clones are made at committed HEAD; uncommitted source is not copied.
Only the clones receive the benchmark package and `go.mod` changes. `GOWORK=off`
prevents an enclosing `go.work` from changing the experiment.

Cycles include periodic idle intervals and a final idle period with clients
still connected. Initialization is included in the first cross-file operation;
its latency is visible in the operation stream and maximum. Final shutdown and
cleanup occur after memory sampling ends.

## Isolation and prerequisites

Run from a Sidekick checkout on Linux with readable `/proc`, Go, Git, and gopls
on PATH. The same gopls discovery/install helper as Sidekick is used if needed;
prepare the desired binary and dependency/build caches before measuring.
Record any GC, telemetry, toolchain, proxy, cgo, or other environment overrides
along with reports and keep them constant between runs.

Use a dedicated OS user or an otherwise idle account. By default the benchmark
refuses a run if that account already has an analysis daemon or helper. It never
terminates an editor's preexisting daemon to obtain a clean baseline.
`--allow-existing` is for contaminated diagnostics only: their memory is included,
but automated before/after comparison rejects such runs.

gopls's automatic Unix socket must fit the platform's socket-path limit. Use a
short absolute `TMPDIR` when the checkout or CI workspace has a long path. A
short alias to a directory under `.side/tmp` is sufficient. Keep this setting
constant across comparable runs.

Use a new output directory for every run. Artifacts and cloned workspaces are
retained for inspection; the benchmark refuses to overwrite an existing output
directory. They may contain local paths, commands and source edits, so review
them before sharing.

## Run

Unit tests (the real-process tests are opt-in):

```sh
go run ./scripts/affected_tests ./scripts/go_analysis_bench
```

Real correctness and cancellation/cleanup integration tests, under an isolated
account with no preexisting gopls:

```sh
SIDEKICK_GO_ANALYSIS_INTEGRATION=1 \
  go run ./scripts/affected_tests ./scripts/go_analysis_bench \
  -run 'TestBenchmarkIntegration|TestCanceledBenchmarkCleansOwnedProcesses' -count=1
```

Short smoke baseline:

```sh
go run ./scripts/go_analysis_bench \
  --label baseline-smoke --output .side/tmp/go-analysis-baseline-smoke \
  --workspaces 3 --cycles 25 \
  --idle-every 5 --idle 1s --final-idle 5s --steady-window 2s --sample 200ms
```

Long-running baseline against a representative committed repository:

```sh
go run ./scripts/go_analysis_bench \
  --label baseline --output .side/tmp/go-analysis-baseline \
  --seed /absolute/path/to/representative/go/repo \
  --workspaces 3 --cycles 0 --duration 2h \
  --idle-every 20 --idle 30s --final-idle 5m --steady-window 1m --sample 1s
```

Duration limits the workload phase, not setup or final idle. An in-progress
cycle completes unless an operation fails or times out. `--cycles 0` is required
for an exclusively duration-limited run; with both bounds, the first bound
reached ends the workload. Idle intervals count toward workload duration.

After implementing the candidate, repeat with the same seed commit, Go/gopls
versions (unless changing gopls is intentional), host, environment, caches,
workload flags and sample interval, using a new directory and baseline report:

```sh
go run ./scripts/go_analysis_bench \
  --label candidate --output .side/tmp/go-analysis-candidate \
  --seed /absolute/path/to/representative/go/repo \
  --workspaces 3 --cycles 0 --duration 2h \
  --idle-every 20 --idle 30s --final-idle 5m --steady-window 1m --sample 1s \
  --compare .side/tmp/go-analysis-baseline/report.json
```

Run several paired trials, keeping warm/cold cache treatment consistent. The
comparison requires matching workload fingerprints, successful correctness
checks and final-idle samples, and rejects preexisting analysis processes.
Timed runs may complete different numbers of cycles; report that throughput
difference as well as memory and latency.

Use `--daemon-names gopls,replacement-daemon` if a candidate uses a separately
detached/shared service. Merely measuring its stdio client is not sufficient.
Additional non-daemon helpers are discovered through ancestry. The current
factory is local-only; this harness does not measure SSH-host RSS.

## Artifacts and measurement definitions

- `report.json`: configuration (durations in nanoseconds), revisions and diff
  checksum, fixture checksum, Go environment, gopls version, host/kernel,
  completed cycles, correctness failures, peak/final-idle memory and latency.
  Failures produce a nonzero exit and a report with `error` when setup reached
  artifact creation. Do not interpret a failed run as an improvement.
- `memory.jsonl`: timestamped process snapshots with phase, PID, parent PID,
  start ticks, executable/stat name, command line and RSS. Optional metadata
  failures are recorded per process.
- `operations.jsonl`: timestamped operation, workspace, cycle, elapsed duration
  in nanoseconds, and any error. Includes correctness validation time.
- `actions.jsonl`: actual code-action parameters, returned actions and errors.
- `comparison.json`: candidate/baseline ratios for analysis and combined memory
  and per-operation p95 latency. A ratio below one means lower memory or latency.

Analysis RSS is the simultaneous sum of same-user analysis daemons, their
descendants, and descendants of the harness, excluding the harness itself.
All matching same-user daemon basenames are included even if detached or already
running. `(PID, start ticks)` tracks previously observed descendants after
reparenting; dead identities are pruned. The default daemon name is always
`gopls`. The snapshots allow auditing which frontends, shared servers, telemetry
processes, Go commands and compiler helpers contributed to each measurement.

RSS is a conservative process sum, **not PSS or unique physical memory**:
shared mappings, including a transient fork child's inherited pages, can be
counted more than once. A fixed sample interval can miss shorter peaks.
Required UID, ancestry, identity and RSS reads fail loudly on permission,
parse or I/O errors; only vanished processes (`ENOENT`/`ESRCH`) are skipped.
Executable/cmdline metadata is optional: failures retain the process's RSS and
stat name, so an inaccessible gopls executable does not hide its daemon.
A stat/directory PID mismatch is re-read once and must resolve or disappear.

Peak RSS is the largest simultaneous sampled sum, not a sum of per-process
peaks. Steady RSS is the median of samples in the last `--steady-window` of the
final idle phase; it is not proof that a long-run plateau has been reached.
Combined peak and steady metrics include the harness's own RSS. Use these
combined numbers if comparing a future in-process analyzer with external gopls.
Inspect the complete time series for slopes and idle retention rather than
relying on a single final number.

Snapshots and operations stream to disk. In-memory state consists of aggregate
peaks, live process identities, a bounded final-idle window and fixed latency
histograms per operation. p50/p95 use 1 ms ceiling buckets up to 60 seconds;
an overflow percentile uses the exact observed maximum as a conservative upper
bound. Maxima retain exact durations. No full-session sample or operation list
is retained. JSONL files and gopls's own log files can still grow on disk.

## Cleanup safety

Accounting scope is deliberately broader than cleanup ownership. Each run
generates a random environment token, inherited by its subprocesses. Cleanup
signals only processes whose environment contains that exact token, never a
process selected merely by its executable name, startup absence or parentage.
The harness PID is excluded. PID/start-time identity and ownership are
revalidated before signaling, using an opened process handle.

On success, failure, SIGINT or SIGTERM, connections are closed and ownership
scans repeat with a bounded deadline to catch children spawned during shutdown.
Permission to inspect an environment is not assumed; inability to prove
ownership never authorizes a signal. SIGKILL/crashes cannot run cleanup.
If interrupted that way, inspect the dedicated account's processes manually
before starting another clean run. Do not kill unrelated editor processes.

## Initial validation

The first sustained standalone-fixture smoke run used Go 1.26.4 and gopls
v0.21.0, three working directories, 25 cycles, 1-second idle periods every five
cycles, a 5-second final idle, a 2-second steady window and 200 ms sampling.
It completed in about 41 seconds with 600 successful desktop operations,
75 organize-imports actions, periodic/final-idle samples, and no live processes
remaining under the dedicated account.

Observed analysis RSS was about **1,141 MiB peak / 936 MiB final-idle median**;
including the harness it was about **1,248 MiB peak / 1,046 MiB median**.
These are conservative summed RSS figures, including frontends, a shared server,
telemetry processes and transient Go/compiler helpers. A transient fork
snapshot contributed to the peak. This is a smoke observation on a tiny
fixture, **not a two-hour baseline, a demonstrated leak, or a before/after fix
result**. The long-running representative-repository procedure above is still
required for evaluating a fix.

Unit coverage includes proc-stat parsing, detached-daemon/helper accounting,
metadata-unavailable accounting, PID reuse/history pruning, bounded observer
state, exact reference checks, invalid-run comparison rejection and output
correctness. Real integration coverage checks repeated edits across working
directories and canceled-run cleanup. The separate call-hierarchy lint tool is
not modified; its compatibility must be checked when changing the analyzer.