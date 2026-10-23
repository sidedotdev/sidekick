# Modal repository-tool performance

## Reproducing the measurement

With Modal credentials configured:

```sh
SIDE_MODAL_TOOLS_PERF=true go test ./dev \
  -run '^TestModalToolsPerformance$' -count=1 -v -timeout 25m
```

The fixture is this repository's own checkout, mirrored into a reusable sandbox
as a single-commit repository (the sync is skipped when the tree hash matches).
The first run also installs Go and gopls into the sandbox. Each scenario runs
once cold, then is measured five times warm. Assertions check that the requested
definitions, related-symbol hints, missing-symbol hint and search results are
actually returned. It is opt-in because it uses a live Modal sandbox.

To measure a subset:

```sh
SIDE_MODAL_TOOLS_PERF=true go test ./dev \
  -run '^TestModalToolsPerformance$/modal/(noop-command|definitions)' -count=1 -v -timeout 25m
```

## Observed warm latency

Darwin/arm64 client, existing Modal sandbox, Sidekick checkout, gopls running
on each side. End-to-end tool-activity medians; the noop command is a single
agent round trip with no repository work and is the transport floor.

| Scenario | Local | Modal |
| --- | ---: | ---: |
| Noop command (transport floor) | – | 115–300 ms |
| Definitions RPC with no requests | – | 137 ms |
| Seven symbols across four files (tree-sitter only) | 25 ms | 245–605 ms |
| Symbol resolved through gopls from a referencing file | 25 ms | ~600 ms |
| Definition with related-symbols hint (gopls references) | 78 ms | ~700 ms |
| Missing symbol: repository-wide symbol index | 1.5 s | 2.0 s |
| Five searches (mixed globs incl. `**/*.go`) | 1.3 s | 0.9 s |

The ranges reflect two runs under different network conditions; Modal latency
tracks the transport floor, i.e. every scenario costs one round trip plus the
sandbox's own CPU time. Before this work the same tools issued many SFTP reads
and SSH command executions per call (a ten-search bulk call measured 15 s).

## How it works

`bulk_search_repository` and `get_symbol_definitions` are implemented once, in
`repotools`, against a small `Repository` interface for file access. The worker
runs that implementation against an `env.Env`; the side-agent runs the same
implementation against the sandbox filesystem. Environments that expose a
side-agent (`env.AgentProvider.WithAgent`) run the whole tool call as one RPC:
the caller in `dev`/`coding` chooses the RPC, `env` only lends the pooled agent
connection.

Language-server work stays on the side where the files are. `coding/lsp/core`
holds the transport-agnostic LSP client and session cache; the agent keeps warm
gopls sessions per repository across requests (the first gopls start on a cold
sandbox took 11–25 s to index this repository), while the worker keeps using
its existing SSH-launched sessions for non-agent environments.

The agent is built with CGO so it can link the tree-sitter parser
(`coding/tree_sitter/parser`), which is the same parser the worker uses.

## Remaining costs

The sandbox's CPU is the remaining cost after the single round trip: parsing
large files and, for the missing-symbol hint, indexing every file in the
repository. Other filesystem tools still use individual SFTP operations.