# Modal idling validation

## Automated coverage

The following complementary regression tests passed, including repeated
race-detector runs. They run without Modal credentials.

- `coding/lsp/TestManagedAnalysisSSHSessionActivity` uses real SSH sessions
  and the side-agent binary with mocked analysis responses. It checks that
  activity admission prevents sealing and the watchdog sees work during
  definitions, references, document sync, autofixes, and cancellation cleanup.
  After release, sealing succeeds and the watchdog reports idle while the
  cached SSH connection remains open and reusable.
- `coding/lsp/TestClientActivityReconnectsAfterIdleShutdown` uses test clients
  to check both a previously disconnected connection and a disconnect
  discovered during wake. The first subsequent query replaces the client and
  succeeds without a caller retry.
- `coding/lsp/TestGoSemanticsAcrossSessionRecycling` uses real gopls across
  workspace eviction, idle expiration, and age recycling. It checks
  cross-file/dependency definitions, changed reference locations, removal of
  stale references, exclusion of shadowed symbols, and import fixes after
  edits. It also checks that both `source.fixAll` and
  `source.organizeImports` are requested; the import fixture does not by
  itself establish coverage of every `source.fixAll` action.

There is no single live Modal test implementing the complete
active-work → idle-with-open-connection → wake-and-semantic-freshness sequence.
The closest existing credential-gated test,
`dev/TestModalApplyEditBlocksIntegration`, checks creating, updating, and
deleting a text file on Modal, not analysis freshness or idle shutdown.
It requires `SIDE_E2E_TEST=true`, a local execution environment, and Modal
credentials; ordinary test execution does not enable it automatically.

## Live probe

Status: **blocked by missing credentials; live behavior not validated**.

On 2026-10-16 at 20:18:57 UTC, the existing read-only diagnostic was run:

```sh
go run ./scripts/modal_sandbox_status side-go-analysis-credential-probe
```

Both the guard snapshot lookup and sandbox lookup reported:

```text
missing token_id or token_secret, please set in .modal.toml, environment variables, or via NewClientWithOptions()
```

The diagnostic exited with status zero despite reporting lookup errors.
This is not a successful connectivity or lifecycle check. No sandbox was
created, and no live active-work, idle-shutdown, or wake timing was observed.
The automated checks above are complementary evidence, not a substitute
measurement of actual Modal shutdown and restore timing.

## Retention policy

The desktop cache retains at most one workspace client per activity owner.
It closes the client after one minute without an operation and recycles it
before the next operation once it is five minutes old. Neither limit
terminates an in-flight operation.