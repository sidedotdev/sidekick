# Sidekick Codebase Issues Assessment

**Branch audited:** `devpod` (commit `48df791`, the active development trunk — ~926 commits ahead of `main`).
**Date:** 2026-07-07.
**Method:** Parallel multi-agent audit. Each finding was verified by reading (and in some cases executing) the actual code. Findings that could not be substantiated were discarded. Line numbers are from the `devpod` branch.

> **How to read this.** Findings are ranked by severity (Critical → Low). "New on devpod" means introduced by development since `main`; "Pre-existing" means it was present on `main` and is still present on `devpod`. Each entry has a concrete failure scenario and a suggested fix. A **Verified NOT an issue** section at the end records things that were checked and cleared — read it before re-investigating.

---

## Summary table

| # | Sev | Area | Issue | Origin |
|---|-----|------|-------|--------|
| 1 | 🔴 Critical | coding/lsp | Concurrent map read/write on `InitializedClients` → worker `fatal error` crash | Pre-existing |
| 2 | 🔴 Critical | gitwalk | Data race on `entry.data`/`entry.size` during prefetch → worker crash | New on devpod |
| 3 | 🔴 Critical | srv/redis | Flow-event stream TTL never applied → unbounded Redis memory growth | Pre-existing |
| 4 | 🔴 Critical | worker | `w.Start()` error silently dropped → worker hangs dead, no log | Pre-existing |
| 5 | 🔴 High | frontend/intent | IntentCanvas has no `flowId` watch → edits saved to the **wrong flow's** file | New on devpod |
| 6 | 🔴 High | env (sandbox) | SFTP `ssh` subprocess + goroutine leaked per remote-file activity | New on devpod |
| 7 | 🔴 High | srv/redis | `GetTasks` panics on a stale kanban id → kanban board handler crashes | Pre-existing |
| 8 | 🔴 High | frontend | FlowView stale-response race renders/overwrites the wrong flow | Pre-existing |
| 9 | 🔴 High | llm2 | Bedrock hardcodes AWS profile `"personal"` → all Bedrock auth fails | New on devpod |
| 10 | 🔴 High | api | Entire API + WS surface unauthenticated; non-loopback bind supported | Pre-existing |
| 11 | 🟠 Medium | coding/tree_sitter | `tree.RootNode()` nil-deref panic (in goroutine → crashes worker) | New on devpod |
| 12 | 🟠 Medium | dev | JSON "repair" promotes a JSON-valued string to an object → corrupts tool-call args | New on devpod |
| 13 | 🟠 Medium | env (sandbox) | Docker watchdog restarts the shared daemon → kills all concurrent sandboxes | New on devpod |
| 14 | 🟠 Medium | gitwalk | `ls-tree` C-quoted paths never unquoted → wrong paths for non-ASCII filenames | New on devpod |
| 15 | 🟠 Medium | dev | Batched edit application → repo-wide checks see a half-written tree, drop valid edits | New on devpod |
| 16 | 🟠 Medium | dev | Leaked-XML-param recovery truncates any string containing `<parameter name="…">` | New on devpod |
| 17 | 🟠 Medium | env (sandbox) | Same-basename repos share one sandbox/workspace → cross-repo isolation failure | New on devpod |
| 18 | 🟠 Medium | coding/tree_sitter | Directory outline spawns one goroutine per file before throttling → blow-up | New on devpod |
| 19 | 🟠 Medium | api | Merge-approval handler missing `return` → finalizes merge with nil target branch | Pre-existing |
| 20 | 🟠 Medium | api | `open-in-ide` unauthenticated + arg-injection on Windows/WSL | Pre-existing |
| 21 | 🟠 Medium | frontend | Workspace switch orphans/duplicates the task-changes WebSocket | New on devpod |
| 22 | 🟠 Medium | frontend | Workspace switch reuses stale `lastTaskStreamId` cursor | Pre-existing |
| 23 | 🟠 Medium | srv/jetstream | Flow-event channel sends lack `ctx.Done()` guard → goroutine leak | Pre-existing |
| 24 | 🟠 Medium | coding/lsp | gopls subprocess + pipes leaked when `Initialize` fails | Pre-existing |
| 25 | 🟠 Medium | embedding | OpenAI embedder assigns vectors by response order, ignoring `Index` | Pre-existing |
| 26 | 🟠 Medium | frontend | TaskModal start/delete failures only `console.error`, no UI feedback | Pre-existing |
| 27 | 🟠 Medium | frontend | Hardcoded `ws://` (no `wss`) breaks realtime over HTTPS | Pre-existing |
| 28 | 🟡 Low | llm2 | Bedrock omits `block_started` for text blocks (streaming consumers) | New on devpod |
| 29 | 🟡 Low | persisted_ai | Tool-result truncation splits UTF-8 runes → invalid UTF-8 to provider | New on devpod |
| 30 | 🟡 Low | llm2 | Fast-mode beta header sent without `speed` param for non-opus models | New on devpod |
| 31 | 🟡 Low | frontend | IntentCanvas finish-polling loop keeps running after unmount | New on devpod |
| 32 | 🟡 Low | env (sandbox) | Sandbox not torn down when provisioning fails after creation | New on devpod |
| 33 | 🟡 Low | frontend | ChatView SSE never closed on unmount + topicId not re-watched | Pre-existing |
| 34 | 🟡 Low | frontend | FlowView `onUnmounted` clears only some debounce timers | Pre-existing |
| 35 | 🟡 Low | api | `FlowEventsWebsocketHandler` ignores channel-closed signal | Pre-existing |
| 36 | 🟡 Low | srv/sqlite | `GetKeysWithPrefix` LIKE pattern doesn't escape `%`/`_` | Pre-existing |
| 37 | 🟡 Low | srv/jetstream | Task consumer nil-deref if `ErrConsumerNameAlreadyInUse` | Pre-existing |
| 38 | 🟡 Low | coding/tree_sitter | Unsigned underflow on empty trailing line (`.vue`/`.svelte`) | New on devpod |
| 39 | 🟡 Low | coding | Unsigned underflow: empty file + wildcard prints garbage line range | Pre-existing |
| 40 | 🟡 Low | llm (legacy) | Busy-spin goroutine in heartbeat (legacy `llm/` path only) | Pre-existing |
| 41 | 🟡 Low | poll_failures | `workspaceId` Sprintf'd into Temporal visibility query (not attacker-reachable) | Pre-existing |

---

## 🔴 Critical

### 1. Concurrent map read/write on `InitializedClients` → worker crash
**`coding/lsp/lsp_activities.go:22`, `coding/lsp/find_references_activity.go:84-116`** · Pre-existing · Confidence: high

`InitializedClients` is a plain `map[string]LSPClient`. The only lock is a **per-key** mutex from `InitializationLockers.LoadOrStore(key, …)` (`key = baseDir + ":" + lang`). Two calls with *different* keys take *different* mutexes, so they concurrently read (`InitializedClients[key]`, :92) and write (`InitializedClients[key] = …`, :116) the same map — a data race that triggers Go's runtime `fatal error: concurrent map read and map write` / `concurrent map writes`, which is unrecoverable and kills the whole worker process.

**Reachability (verified):** `lspActivities` is a single shared `*LSPActivities` (`worker/worker.go:123`) registered once, and Temporal runs activities concurrently with no `MaxConcurrentActivityExecutionSize` cap. Different keys arise naturally: different languages in one repo, and — more robustly — **a worktree per flow**, so two concurrent flows hit different `baseDir`s. Two concurrent `TextDocumentDidOpen`/`GetSymbolDefinitionLocations`/`FindReferences` executions are enough.

> Note: the original repro ("`BulkGetSymbolDefinitions` fans out goroutines") was **wrong** — that fan-out is on `CodingActivities` and never calls `findOrInitClient`. The race is real; the trigger is concurrent *LSP activities* across keys.

**Fix:** guard `InitializedClients` with its own `sync.Mutex`/`RWMutex`, or make it a `sync.Map`.

### 2. Data race on `entry.data`/`entry.size` during gitwalk prefetch → worker crash
**`gitwalk/gitwalk.go:375` vs `:466`/`:470` (and `Size()` at `:424`)** · New on devpod · Confidence: high

In `runPrefetched`, the main loop calls `fn(e)` per entry without waiting on `e.done`; only `Open()` waits. After `fn` returns, main executes `e.data = nil` (:375) **with no lock**, while a prefetch worker may still be inside `e.preload()` writing `e.data`/`e.size` under `e.mu`. When the callback returns *without* opening a given file (normal when filtering by path/name), main and the worker touch `e.data` concurrently → data race (UB, immediate crash under `-race`). `Size()` reads `e.size` with no lock/`done` wait, racing the same write.

**Fix:** only null/read `e.data`/`e.size` after `<-e.done`, or take `e.mu`.

### 3. Flow-event stream TTL never applied → unbounded Redis memory
**`srv/redis/flow_event.go:26` and `:48`** · Pre-existing · Confidence: high

`db.Client.TTL(ctx, streamKey).SetVal(time.Hour * 24)` — `TTL` is a **read** command; `.SetVal(...)` only mutates the returned `*DurationCmd`'s in-memory field and issues **no write** to Redis. The intended 24h expiry is never applied, so every flow-event stream key persists forever and Redis memory grows without bound over the deployment's lifetime.

**Fix:** `db.Client.Expire(ctx, streamKey, 24*time.Hour)`.

### 4. `w.Start()` failure silently dropped → worker hangs dead
**`worker/worker.go:257`** · Pre-existing · Confidence: high

`if err != nil { log.Fatal().Err(err) }` — zerolog only logs/exits on a *terminating* method (`.Msg()`/`.Send()`). With bare `.Err(err)` the event is never dispatched: nothing is logged and `os.Exit` is never called. A failed Temporal worker start is swallowed; `StartWorker` proceeds and returns a non-running worker, and `main` blocks on the signal channel forever — a silent hang polling no tasks. Every other `log.Fatal` in the file chains `.Msg(...)`; this is the lone exception.

**Fix:** add `.Msg("failed to start worker")` (or `.Send()`).

---

## 🔴 High

### 5. IntentCanvas has no `flowId` watch → edits saved to the wrong flow's file
**`frontend/src/views/IntentCanvasView.vue`** (load only in `onMounted` :1148-1160; save `scheduleSave`/`persistQueued` :1061-1081; `App.vue:222` `RouterView` has no `:key`) · New on devpod · Confidence: high (bug), medium (reachability)

`flowId` is `computed(() => route.params.id)` and `apiBase`/`flowBase` derive from it reactively, but all data loading happens **only in `onMounted`** — there is no `watch(flowId, …)`. Because `RouterView` has no `:key`, navigating `/flows/A/intent → /flows/B/intent` reuses the same component instance. Result: `files`/`content`/`subtasks`/`clarifications` stay flow **A**'s while `apiBase` now points at flow **B**. The debounced save then PUTs the displayed (A) content to **B's** intent file — silent cross-flow data corruption. (FlowView handles this exact case with a `watch`; IntentCanvas omits it.)

**Fix:** add `watch(flowId, reload)` that re-fetches and resets state, or put `:key="$route.fullPath"` on the intent `RouterView`.

### 6. SFTP `ssh` subprocess + goroutine leaked per remote-file activity
**`env/remote_sftp.go:67-127` (`dialLocked`), `env/environment.go:76` (`Env` has no `Close`)** · New on devpod · Confidence: high

`dialLocked` starts a long-lived `ssh` child running the remote sftp-server and hands its pipes to `sftp.NewClientPipe` (which spawns a reader goroutine). The only path that kills/`Wait`s that child (`closeLocked`) is called solely from retry and benchmark helpers. The `Env` interface exposes no `Close()`, and nothing in activity/workflow teardown closes the connection. Since `EnvContainer` is re-serialized per activity and the `sftp` field is unexported (never deserialized), **every** remote file op (`ReadFile`/`ReadDir`/`WriteFile`/`Stat`/walks/…) dials a fresh persistent `ssh` process that is never reaped. On the long-lived worker this leaks a zombie process, a blocked goroutine, and pipe FDs per activity; file walks/reads are extremely common, so it accumulates fast.

**Fix:** add `Close()` to the `Env` interface and call it on activity/context teardown; or pool/reuse the sftp client keyed by connection.

### 7. `GetTasks` panics on a stale kanban id → kanban board handler crashes
**`srv/redis/task.go:204`** · Pre-existing · Confidence: high

`json.Unmarshal([]byte(taskJson.(string)), …)` — the `.(string)` assertion runs on an `MGet` result element that is a nil interface for any missing key → `interface conversion: nil, not string` panic. Reachable because `DeleteTask` removes the value key and the kanban set membership in **separate, non-atomic** Redis calls; once desynced, *every* `GetTasks` for that status panics. `GetTask` (archived path) handles `redis.Nil`; `GetTasks` doesn't.

**Fix:** skip/`comma-ok` nil elements in the `MGet` loop; consider reconciling set membership on miss.

### 8. FlowView stale-response race overwrites the current flow
**`frontend/src/views/FlowView.vue:549-585` (clobber at :557), watcher :600-606** · Pre-existing · Confidence: high

`setupFlow` is `async` and the route watcher `await`s it but doesn't serialize concurrent invocations; there's no `AbortController` or per-invocation guard. Rapid A→B navigation: `setupFlow(A)` parks on `await flowPromise` while `setupFlow(B)` completes (closes A's sockets, sets `currentFlowIdForSockets=B`); then A's promise resolves and overwrites `flow.value`, `workspace.value`, and loading flags with flow A's data on top of B. URL says B, pane shows A.

**Fix:** capture `newFlowId` and bail after each `await` if `newFlowId !== currentFlowIdForSockets`; ideally an `AbortController` per navigation.

### 9. Bedrock hardcodes AWS profile `"personal"` → all Bedrock auth fails
**`llm2/bedrock_provider.go:26,39-47,68-71`; constructed without `Profile` at `persisted_ai/llm2_activities.go:286-289`** · New on devpod · Confidence: high

`resolveProfile()` returns the hardcoded `bedrockFallbackProfile = "personal"` whenever neither the struct field nor `AWS_PROFILE` is set — and the provider is built with only `AuthType`/`CustomHeaders`, so `Profile` is never populated. That value goes to `LoadDefaultConfig(..., WithSharedConfigProfile("personal"))`, which (verified against the pinned `aws-sdk-go-v2/config@v1.27.27`) returns `SharedConfigProfileNotExistError` if no profile literally named `personal` exists — **before any request**. So any environment without a `~/.aws/config` profile named exactly `personal` (IAM instance/task roles, static env-var creds, a `default` profile) cannot use Bedrock at all; even valid env-var credentials don't help because the explicit-profile load fails first.

**Fix:** only pass `WithSharedConfigProfile` when a profile is actually configured; drop the `"personal"` default.

### 10. Entire API + WebSocket surface unauthenticated; non-loopback bind supported
**`api/api.go:198-278` (no authn/authz middleware), `common/hosts_and_ports.go:13`, `api/cors.go:22`** · Pre-existing · Confidence: high (localhost-only is defensible; the exposure mode is the risk)

`DefineRoutes` wires every route with only `otelgin` + CORS — no auth layer anywhere. Default bind is `127.0.0.1` (fine), but `SIDE_SERVER_HOST=0.0.0.0` is a *supported* mode gated only by a `log.Warn`. In that mode any network peer gets full unauthenticated control: mutate/delete workspaces, tasks, flows; cancel/reset Temporal workflows; hit the IDE endpoint (#20). CORS doesn't help — `IsAllowed` returns `true` for an empty `Origin` (`api/cors.go:22`), and non-browser clients simply omit `Origin`.

**Fix:** if non-loopback binding is a real use case, add an auth layer (token/loopback-only enforcement) rather than a warning; at minimum reject empty-Origin for state-changing requests.

---

## 🟠 Medium

### 11. `tree.RootNode()` nil-deref panic (crashes worker via goroutine)
**`coding/tree_sitter/signature_outline.go:326`, `symbol_outline.go:232`, `symbol_definition.go:294/458`, `header.go:88`** · New on devpod · Confidence: medium

Every wrapper writes `tree := parser.Parse(...); if tree != nil { defer tree.Close() }` — explicitly acknowledging `tree` can be nil — then passes it to an internal that calls `tree.RootNode()` with no nil check. If `Parse` returns nil (pathologically large/unparseable input), it panics. In `GetDirectorySignatureOutlines` this runs inside a spawned `go func()` worker, so the panic isn't recoverable by Temporal → whole worker crashes.

**Fix:** return an empty result/error when `tree == nil` inside the internal functions.

### 12. JSON "repair" promotes a JSON-valued string to an object → corrupts tool-call args
**`llm/json.go` — `tryParseStringsAsJsonRawMessages:257`, `processJsonStrings`/`tryParseStringAsJson:346/12`, from `repairJson:56,76`** · New on devpod · Confidence: high (verified by executing the function)

The common `RepairJson` path (used on every tool call) promotes a string *value* that is itself valid JSON into a real object/array:
- `{"content":"{\"name\":\"pkg\"}"}` → `{"content":{"name":"pkg"}}`
- `{"path":"a.json","content":"[1,2,3]"}` → `{"content":[1,2,3],"path":"a.json"}`

So when the agent creates/edits a JSON file (`package.json`, `tsconfig.json`, …) and the tool passes the file body as a string arg whose value is complete JSON, the subsequent `Unmarshal` into a `string` struct field fails ("cannot unmarshal object into … string") — the tool call breaks or the content is lost. Under `RepairJsonFull` the mangled value is persisted into history. (Strings that aren't complete valid JSON are correctly left alone — so it fires precisely on the JSON-file-editing case.)

**Fix:** don't recursively parse string values whose parsed form is an object/array; restrict promotion to the documented recovery cases only.

### 13. Docker watchdog restarts the shared daemon → kills all concurrent sandboxes
**`env/docker_health.go:74-153`** · New on devpod · Confidence: medium

Every devpod command is wrapped in `withDockerEngineWatchdog`. After ~3 failed probes (~45s) it calls `RestartDockerEngine` (`systemctl restart docker` / `docker desktop restart`), tearing down **every** container for **all** concurrent workflows — not just the stalled one. Under many concurrent agents, one genuinely slow engine (heavy parallel image builds) can cascade into a global restart. `RestartDockerEngine` also has no mutex/singleflight, so simultaneous watchdogs race on kill+relaunch. Default-enabled via `SIDE_AUTO_RESTART_DOCKER`. Affected ops return retryable errors, limiting data loss, but the cross-tenant blast radius is real.

**Fix:** singleflight the restart; gate it behind stronger evidence the daemon (not one build) is wedged; consider per-workflow isolation before a global restart.

### 14. `ls-tree` C-quoted paths never unquoted → wrong paths for non-ASCII filenames
**`gitwalk/gitwalk.go:193`, `:218`, `:495`** · New on devpod · Confidence: high (paths wrong), severity scales with repo contents

`git ls-tree -r -t` (no `-z`) emits paths with special/non-ASCII bytes wrapped in quotes with octal escapes (verified: `uni_café.txt` → `"uni_caf\303\251.txt"`). The code takes the raw post-tab text verbatim, so `Path()`/`Name()` become the escaped, quote-wrapped string. Consequences: bogus paths; `.sideignore` matching and `filepath.Base` operate on the wrong string; and since `porcelain.go` uses `-z` (unquoted), an overlay modification of the same file won't match the tracked entry in `mergeOverlay` → mis-merged as a duplicate Add.

**Fix:** use `-z` (NUL-terminated, disables quoting) for both `ls-tree` passes.

### 15. Batched edit application → repo-wide checks see a half-written tree, drop valid edits
**`dev/apply_edit_blocks.go:162-175`** · New on devpod (commit `61f1302`) · Confidence: medium

The change from sequential to per-file-goroutine edit application serializes only git index ops (`gitMu`); file writes and `runAutofixCommands` (arbitrary shell) run concurrently across file groups. With a project-wide check (`go build ./...`, `tsc --noEmit`) and multiple files edited in one turn, file A's check can compile the project while file B's goroutine is mid-write or running a repo-wide autofix (`gofmt -w .`, `prettier --write .`). A's check then sees a half-written B and fails spuriously → A's valid edit is restored and reported as failed to the model (dropped edit); the reverse (bad edit passing) is also possible. Concurrent repo-wide autofix commands can also race on the same files. Intermittent/config-dependent, but a genuine regression from fully-sequential behavior.

**Fix:** run repo-wide check/autofix commands under a shared lock (or once, after all writes), or scope checks to the file being applied.

### 16. Leaked-XML-param recovery truncates any string containing `<parameter name="…">`
**`llm/json.go:150-183` (`extractLeakedXmlParams`), via `RepairJsonFull`** · New on devpod · Confidence: high

Unlike the sibling `extractLeakedXmlTagFields` (which only promotes when preceding content ends in a closing tag), this fires on *any* occurrence of the literal `<parameter name="`. Verified: `{"analysis":"… <parameter name=\"steps\">[1,2]</parameter>"}` → `{"analysis":"… ","steps":[1,2]}`. So when Sidekick works on LLM-tooling code/docs (or any text mentioning tool-call XML), a legitimate string arg is silently truncated at that point and spurious top-level fields are injected. Persisted under `RepairJsonFull`.

**Fix:** apply the same guard the tag-field path uses (only promote in the genuine leak shape), or make the pattern far more specific.

### 17. Same-basename repos share one sandbox/workspace → cross-repo isolation failure
**`env/openshell.go:49-55` (`OpenShellSandboxName`), `env/environment.go:804` (`DevPodWorkspaceName`)** · New on devpod · Confidence: medium

Both derive identity solely from `filepath.Base(repoDir)` (`"side--"+basename` / bare `basename`). Two distinct repos with the same basename (`acme/api` and `beta/api`) collide onto the **same** sandbox/workspace and the same SSH ControlMaster socket. The reuse path (`OpenShellCheckSandboxActivity`) reuses any alive sandbox with that name *without verifying provenance*, so workspace B can land in a container built/synced for workspace A and see its code. (The code's own `FIXME` flags the name isn't even sanitized.)

**Fix:** include a workspace-id / full-path hash in the sandbox/workspace name; verify provenance before reuse.

### 18. Directory outline spawns one goroutine per file before throttling
**`coding/tree_sitter/signature_outline.go:162-168`** · New on devpod · Confidence: high (pattern), impact scales with repo size

The loop does `wg.Add(1); go func(){ sem <- struct{}{}; … }()` for every file, so on N files it creates N goroutines immediately and only *then* throttles to 15 concurrent. On a repo with tens of thousands of files that's tens of thousands of blocked goroutines (hundreds of MB of stacks) plus scheduler pressure, defeating the `maxConcurrency` bound.

**Fix:** acquire the semaphore *before* `go`, or use a worker pool over a task channel.

### 19. Merge-approval handler missing `return` → finalizes merge with nil target branch
**`api/api.go:1344-1346` (`CompleteFlowActionHandler`)** · Pre-existing · Confidence: high (bug); medium (downstream impact)

For `RequestKindMergeApproval` with a nil `targetBranch`, the handler writes `c.JSON(400, …)` but does **not** `return` (the sibling checks around it do). Execution falls through: it builds the dev agent, relays the response, and persists the flow action as `Complete` with a nil target branch, then writes a second response. A merge-approval gets finalized without a target branch.

**Fix:** add the missing `return` after the 400.

### 20. `open-in-ide` unauthenticated + arg-injection on Windows/WSL
**`api/ide_api.go` (`OpenInIdeHandler`; route `api.go:215`; exec `:134`; Windows path `:118`,`:125`)** · Pre-existing · Confidence: medium

`req.FilePath` is only checked non-empty, never sanitized, and formatted into `vscode://`/`idea://`/`zed://` URIs. On darwin/linux the args are passed as an array (`open`/`xdg-open`) so classic shell injection isn't reachable; the **Windows/WSL `cmd /c start url`** path re-parses and is argument-injection-prone. Endpoint is unauthenticated (reachable by any Origin-less client, or a LAN peer under #10).

**Fix:** validate/allowlist the path; avoid `cmd /c start` (use `rundll32 url.dll,FileProtocolHandler` or an arg-safe invocation); authenticate the endpoint.

### 21. Workspace switch orphans/duplicates the task-changes WebSocket
**`frontend/src/views/KanbanView.vue:82-95` + `:116` + `:136-139`** · New on devpod (side effect of the `socketClosed` reset fix) · Confidence: high

`onclose` reads module-level `socketClosed` at fire time. On workspace switch, `uninitialize()` sets `socketClosed=true` and `socket.close()` (async), then `initialize()` synchronously sets `socketClosed=false` and assigns a new socket. When the old socket's `onclose` finally fires, `socketClosed` is already `false`, so its early-return is skipped and it schedules a reconnect after 1s — creating a third socket that overwrites the reference and leaks the just-created one. Every workspace switch accumulates a stray reconnecting socket (duplicate task streams).

**Fix:** capture a per-socket "closed intentionally" flag in the closure instead of the shared module variable.

### 22. Workspace switch reuses stale `lastTaskStreamId` cursor
**`frontend/src/views/KanbanView.vue:31,60,63,136-139`** · Pre-existing · Confidence: high

The workspace `watch` calls `uninitialize()`/`initialize()` but never resets `lastTaskStreamId`, so the new workspace's task socket connects with `?lastTaskStreamId=<previous workspace's id>` — task-change replay for the new workspace starts from a foreign cursor.

**Fix:** reset `lastTaskStreamId` in `uninitialize()`.

### 23. Flow-event channel sends lack `ctx.Done()` guard → goroutine leak
**`srv/jetstream/flow_event.go:80,115,119,134`** · Pre-existing · Confidence: medium

In `consumeFlowEvents`, `errCh <-`/`eventCh <- event` sends have no `select { case <-ctx.Done(): … }`. If the websocket consumer has returned and cancelled the context, these block forever on the unbuffered channel; `consContext.Stop()/Closed()` waits for in-flight handlers, so the function never returns and the outer `wg.Wait()` never completes — goroutines and the NATS consumer leak. The sibling `StreamFlowActionChanges` and the redis equivalent guard correctly.

**Fix:** wrap each send in a `select` with `<-ctx.Done()`.

### 24. gopls subprocess + pipes leaked when `Initialize` fails
**`coding/lsp/lsp_client.go:84, 97-123`** · Pre-existing · Confidence: medium

The gopls process is `cmd.Start()`ed but never `Wait()`ed/killed anywhere. If `Initialize` fails on the `initialize`/`initialized` call, it returns an error without closing `rwc` or terminating gopls → orphaned process + pipes. Since `findOrInitClient` doesn't cache on init failure, each retry spawns another orphan; gopls stderr is also never drained.

**Fix:** on init failure, close `rwc` and `Process.Kill()`+`Wait()`; drain stderr.

### 25. OpenAI embedder assigns vectors by response order, ignoring `Index`
**`embedding/openai_embed.go:47-49`** · Pre-existing · Confidence: medium

`embeddingVectors[i] = embedding.Embedding` binds by loop position and discards `embedding.Index`. The API returns an explicit `Index` precisely because `data` order isn't contractually guaranteed. If the provider/proxy reorders, vectors bind to the wrong source text and are cached under the wrong keys — silently wrong vector search, no error. (The Google embedder at least validates counts.)

**Fix:** index into `embeddingVectors[embedding.Index]`.

### 26. TaskModal start/delete failures only `console.error`, no UI feedback
**`frontend/src/components/TaskModal.vue:702-705, 780-782`** · Pre-existing · Confidence: high

On the primary "start task" and "delete" paths, `if (!response.ok) { console.error(...); return }` gives zero UI feedback. Start can also leave a task stranded in `drafting` (autoSave POSTed it, the `to_do` PUT failed) while the user believes it launched. (The `autoSave` path correctly sets `saveStatus='error'`, so this is an inconsistency.)

**Fix:** surface an error state to the user like `autoSave` does.

### 27. Hardcoded `ws://` (no `wss`) breaks realtime over HTTPS
**`frontend/src/views/FlowView.vue:164, 304`, `KanbanView.vue:63`** · Pre-existing · Confidence: high

All WebSocket URLs are `ws://${window.location.host}/…` with no `wss://` branch. Served over HTTPS, browsers block these as mixed content, so task updates / action & event streaming silently fail. A correct helper already exists (`DevRunControls.vue getWebSocketUrl()`) — it just wasn't applied here.

**Fix:** use the `wss`-aware helper for all socket URLs.

---

## 🟡 Low

### 28. Bedrock omits `block_started` for text blocks
**`llm2/bedrock_provider.go:342-368`** · New on devpod · Confidence: medium — text is streamed as bare `EventTextDelta` with no preceding `EventBlockStarted` (Anthropic/Google both emit it). A live streaming consumer that opens a text block on `block_started` gets deltas for a block it never opened. The final reconstructed `MessageResponse` is correct (lazy block creation), so impact is limited to realtime consumers.

### 29. Tool-result truncation splits UTF-8 runes → invalid UTF-8 to provider
**`persisted_ai/llm2_chat_history_management.go:429-445` (`truncateToolResultMiddle`)** · New on devpod · Confidence: medium — slices text on raw byte offsets (`oldText[:half]`, `oldText[len-half:]`); with multibyte content (common in tool output) this splits runes, producing invalid UTF-8. Some providers reject it (request fails); others mangle. **Fix:** snap offsets to rune boundaries (`utf8.DecodeLastRune`).

### 30. Fast-mode beta header sent without `speed` param for non-opus models
**`llm2/anthropic_provider.go:71-73, 126, 251`** · New on devpod · Confidence: medium — the `fast-mode-2026-02-01` beta header is appended whenever `Speed=="fast"` for *any* model, but the `speed:"fast"` body param is only injected when the model contains `opus-4-8`. Configure `speed: fast` on a non-opus Anthropic model → header sent without the param: silent no-op at best, a 400 rejecting the unsupported beta at worst. **Fix:** gate header and body param on the same condition.

### 31. IntentCanvas finish-polling loop keeps running after unmount
**`frontend/src/views/IntentCanvasView.vue:887-922`; `onBeforeUnmount:1162-1169`** · New on devpod · Confidence: medium — `confirmFinish`'s `while (showFinishDialog.value)` polls every 1s until complete/error; `onBeforeUnmount` doesn't clear it, so navigating away mid-finish leaves it fetching against a dead component indefinitely. **Fix:** cancel the loop on unmount.

### 32. Sandbox not torn down when provisioning fails after creation
**`dev/dev_context.go:298-400`** · New on devpod · Confidence: medium — if sync/worktree creation fails *after* the sandbox/workspace was created, `setupDevContext` returns the error and the container keeps running; `handleFlowCancel` only cleans up on `ErrCanceled`. Bounded by deterministic-name reuse (reused next attempt, not unbounded growth). **Fix:** defer teardown on the non-cancel error path.

### 33. ChatView SSE never closed on unmount + topicId not re-watched
**`frontend/src/views/ChatView.vue:17, 119`** · Pre-existing · Confidence: high — `new SSE(...)` is never `.close()`d and there's no `onUnmounted`; `topicId` is captured once with no `watch`, so navigating between topics on the reused component shows the wrong topic. **Fix:** close SSE on unmount; watch the route param.

### 34. FlowView `onUnmounted` clears only some debounce timers
**`frontend/src/views/FlowView.vue:649-664`** · Pre-existing · Confidence: high — clears only `subflowStatusUpdateDebounceTimers`; `subflowTreeDebounceTimer`, `subscribeStreamDebounceTimers`, `subflowProcessingDebounceTimers` (which can fire `fetch`) are left pending and may run after teardown. **Fix:** clear all timers on unmount.

### 35. `FlowEventsWebsocketHandler` ignores channel-closed signal
**`api/api.go:1849`** · Pre-existing · Confidence: high (defect), low (impact) — `case flowEvent := <-flowEventCh:` without the `, ok` check; when the streamer closes the channel this case is perpetually ready and emits zero-value events until `ctx.Done()` is also selected. The sibling action-changes handler checks `ok`. **Fix:** check `ok` and return on close.

### 36. `GetKeysWithPrefix` LIKE pattern doesn't escape `%`/`_`
**`srv/sqlite/storage.go:306-307`** · Pre-existing · Confidence: medium (correctness), low (current impact) — `key LIKE ?` with `prefix+"%"` and no `ESCAPE`; `_` in flow ids is a wildcard, so cascade-delete prefixes could over-match. Not triggerable with current ksuid ids, but a genuine defect if key formats change. **Fix:** escape `%`/`_` and add `ESCAPE '\'`.

### 37. Task consumer nil-deref if `ErrConsumerNameAlreadyInUse`
**`srv/jetstream/task.go:64,69`** · Pre-existing · Confidence: low — the error is swallowed but `consumer` is nil, so `consumer.Consume(...)` panics. Unlikely with current config (no durable name), but logically unsound. **Fix:** don't proceed with a nil consumer.

### 38. Unsigned underflow on empty trailing line (`.vue`/`.svelte`)
**`coding/tree_sitter/symbol_outline.go:549`** · New on devpod · Confidence: high (underflow), low (blast radius) — `Column: uint(len(lines[len-1]) - 1)`; for a file ending in a newline the last element is `""` → `uint(-1)` = huge, landing in `Declaration.EndPoint`. **Fix:** guard empty last line.

### 39. Empty file + wildcard prints garbage line range
**`coding/coding_activities.go:767`** · Pre-existing · Confidence: high (underflow), low (impact) — `Row: uint(lineCount) - 1` underflows for an empty file; `SourceBlock.String()` guards the OOB range so no crash, but the header prints `Lines: 1-18446744073709551616`. Display-only. **Fix:** guard `lineCount == 0`.

### 40. Busy-spin goroutine in heartbeat (legacy `llm/` path only)
**`llm/openai_tool_chat.go:37`, `llm/google_tool_chat.go:57`** · Pre-existing · Confidence: high — `case <-heartbeatCtx.Done():` has an empty body and no `return`; once the ctx is done the case fires continuously → the goroutine spins burning a CPU core until `ctx` is cancelled. The sibling `openai_responses_tool_chat.go:39` returns correctly. **`llm2` is clean** (its heartbeat in `persisted_ai/llm2_activities.go` returns properly). Only reachable via the legacy `executeChatStreamLegacy` backcompat path. **Fix:** add `return` (or just let the planned `llm/` removal delete it — low priority if no live traffic uses `llm`).

### 41. `workspaceId` Sprintf'd into Temporal visibility query
**`poll_failures/poll_failures_activities.go:27`** · Pre-existing · Confidence: high (unsanitized), low (not exploitable) — interpolated with no escaping, but `workspaceId` is server-generated (`ws_`+ksuid) so it can't carry a quote, and the calling workflow isn't wired up. Query-injection-shaped, not reachable with attacker input today. **Fix:** parameterize/escape if this ever takes user input.

---

## ✅ Verified NOT an issue (checked and cleared)

- **Sandbox command-permission relaxation is NOT exploitable / NOT spoofable.** The relaxed `BaseCommandPermissionsForIsolatedEnv` policy (auto-approve unless explicitly denied) is only selected by `envType` (`devpod`/`openshell`), and that *same* `envType` is what constructs the real `DevPodEnv`/`OpenShellEnv` that the command actually runs in. `GetType()` is a hardcoded per-type constant, and `envType` comes from workflow input / `side.yml`, not tool args or model output. There is no path where the policy says "sandbox" but execution lands on the host. *Residual by-design risk:* inside a sandbox the agent can freely exfiltrate the mounted/synced repo over the network (network commands auto-approve); host LLM secrets are **not** injected into the sandbox by default (`envVarsToInject` = `GIT_EDITOR` + env-type + port-forwards only).
- **Command injection in `env/` sandbox code — clean.** `unix.RunCommandActivity` uses `exec.CommandContext(cmd, args...)` (no shell); every hand-built remote shell string routes interpolated values through `shellQuote` (correct `'` escaping).
- **XSS in the frontend — clean.** No `v-html`/`innerHTML`/`insertAdjacentHTML`/`document.write` anywhere in `frontend/src`; markdown renders via `vue-markdown-render` with HTML disabled.
- **JSON repair on unrepairable input — safe.** Returns the original input unchanged rather than a half-transformed value.
- **`flow_action/global_state.go` — clean.** All shared state consistently guarded by `mu`; pointer returns are to immutable, freshly-assigned data.
- **Chat-history v4 tool-pairing — clean.** `cleanLlm2ToolCallsAndResponses` never emits orphaned tool-use/tool-result; keep-budget can't collapse to zero (100k-token floor).
- **`coding/unix` command Wait/pipe handling, `apply_workspace_edit` offset math, git worktree cleanup, `gitwalk/porcelain.go` `-z` rename parsing, `gitwalk/catfile` pooling — all verified clean.**
- **SQL layer (`srv/sqlite`, `srv/redis`) — parameterized**, dynamic `IN (...)` uses placeholders; workspace scoping filters by `workspace_id` (no cross-workspace IDOR at storage).
- **Secrets — not logged or returned;** stored in OS keyring; OAuth uses PKCE.
- **`llm2` prefix/reasoning-effort resolution — clean** (delimiter-aware matcher prevents `gpt-5` shadowing `gpt-5.1`, etc.).

---

## Non-bug quality concerns & recommendations

> Structural assessment (largely branch-independent — the duplications below exist on both `main` and `devpod`).

**Top structural liabilities — two incomplete migrations running in parallel:**

1. **Two live LLM subsystems, `llm/` vs `llm2/`.** Same callers (`dev/*`, all of `persisted_ai/`) depend on *both*; providers are duplicated. Every LLM change is reasoned about twice. → Declare `llm2` the target, freeze `llm`, burn down remaining `sidekick/llm` importers until the package can be deleted. *(You confirmed this is the plan — `llm` is backcompat-only.)* This also carries away findings #40.
2. **Three storage backends behind a 38-method passthrough `Delegator`** (`srv/redis` + `srv/sqlite` + `srv/jetstream`), each re-implementing the same domain ops; `scripts/*migrate*` confirm a mid-flight Redis→SQLite/JetStream transition. → Pick targets, reach parity, delete `srv/redis` (carries away #3, #7, #23, #37).
3. **Dead code keeping a personal fork alive.** `llm/old_anthropic_tool_chat.go` has zero non-test refs and is the only consumer of the vendored `github.com/ehsanul/anthropic-go/v3` fork. → Delete file + test, drop the fork from `go.mod`.

**CI / hygiene (cheap, high leverage):**

4. CI runs no `type-check` (vue-tsc) and no `lint` despite both scripts existing — type/lint errors land on the trunk unnoticed. The Bun cache key hashes `frontend/bun.lockb` but the committed file is `frontend/bun.lock`, so the cache never hits (every run reinstalls). Two lockfiles committed (`package-lock.json` + `bun.lock`) despite Bun being mandated. → Add type-check + non-fixing lint gates, fix the cache key, delete the npm lockfile.

**Frontend architecture:**

5. **No API-client layer** — ~40 raw `fetch('/api/v1/...')` calls across 14 files, each re-implementing error handling (see #26/#33 — the silent-failure bugs are symptoms). → Introduce a typed `useApi` composable.
6. **No server-state store** — state is a hand-rolled `reactive()` holding only `workspaceId`; every view refetches overlapping data with no shared cache/invalidation (the FlowView/IntentCanvas/Kanban switching bugs #5/#8/#21/#22 are symptoms). → Adopt Pinia or TanStack Query.

**Lower priority:** god files (`api/api.go` ~1700 LOC / 28 routes, `dev/apply_edit_blocks.go`, `dev/` as a flat ~14k-LOC package) → split by resource; giant components (`TaskModal.vue`, `IntentCanvasView.vue`, `FlowView.vue`) → extract composables; accessibility largely absent (69 `@click` vs 3 `aria-*`, weakest Vue lint ruleset); untested load-bearing packages (`diffp/`, `gitwalk/`, `worker/`, `temporal/`); no `//go:build integration` separation; stale frontend deps (Vitest `0.34`, ESLint 8); `frontend/README.md` is untouched Vite scaffold with an invalid `bun ci` command; 264 TODO/FIXME markers (53 FIXME).

---

## Suggested triage order

**Fix first (small, localized, high impact):** #1, #2, #3, #4 (two crashes, a worker hang, an unbounded leak) — all a few lines each. Then #5, #9, #12 (silent data/behavior corruption reachable in normal use).
**Then:** the sandbox resource leaks (#6, #13, #17) and the remaining Medium correctness bugs.
**Structural, in parallel:** start the `llm`/`llm2` and `srv` consolidations — they retire whole classes of the bugs above.

*This assessment was produced by automated multi-agent code review. Every finding was verified against the actual code, but confidence levels are noted per-item; treat Medium/Low-confidence reachability claims as "worth confirming during the fix."*
