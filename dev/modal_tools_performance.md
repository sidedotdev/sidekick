# Modal repository-tool performance

## Reproducing the measurement

With Modal credentials configured:

```sh
SIDE_MODAL_TOOLS_PERF=true go test ./dev \
  -run '^TestModalToolsPerformance$' -count=1 -v -timeout 3m
```

The test reuses a sandbox and installs a small, tracked Git fixture through a
bundle. Each scenario is warmed once, then measured five times. Assertions check
that the requested definitions, missing-symbol hint, and search results are
actually returned. It is opt-in because it uses a live Modal sandbox.

To measure only searches:

```sh
SIDE_MODAL_TOOLS_PERF=true go test ./dev \
  -run '^TestModalToolsPerformance$/modal/bulk-search$' -count=1 -v -timeout 3m
```

## Observed warm latency

Darwin/arm64 client, existing Modal sandbox, 100 small Go files. These are
end-to-end tool-activity measurements, not isolated SSH timings. No LSP server
was used.

| Scenario | Modal before | Modal with workspace RPCs | Local with changes |
| --- | ---: | ---: | ---: |
| Ten requested symbol definitions | 766 ms | 97 ms | 7 ms |
| Missing symbol in the requested file, present elsewhere | 815 ms | 327 ms | 154 ms |
| Ten repository searches | 15.23 s | 371 ms | 211 ms |

These figures compare the baseline and final verified runs. Earlier eight-worker
search runs had medians between 464 and 786 ms. Four- and sixteen-worker
experiments did not show a consistent advantage, so concurrency remains bounded
at eight. Cloud scheduling and network variability are substantial; these
numbers are not latency guarantees.

## What changed

* Known definition files are prefetched together over the pooled agent channel.
  The existing per-invocation read cache supplies the bytes to local parsing.
  Missing, oversized, and unreadable files retain individual-read fallback.
* Search setup creates the core ignore file and probes ignore-file availability
  in one agent operation, once per tool call.
* A search RPC performs file listing, glob filtering, and the dependent
  `git grep` invocation without sending the intermediate file list back for
  another command round trip.
* Independent searches run concurrently inside the existing activity. Result
  order is unchanged, and the workflow-level orchestration remains unchanged.

Workspace operations use the existing agent admission gate, activity heartbeat,
cancellation, and working-directory checks. Intermediate responses are bounded;
oversized combined results fall back to the existing search path.

## Remaining costs

The client still parses definitions and formats search results. Missing-symbol
lookups still perform repository discovery, and LSP/related-symbol workloads
have additional costs not represented by the definition benchmark. Individual
SFTP operations remain for fallback reads and other filesystem tools. A network
round trip therefore remains a meaningful floor even when the sandbox is warm.