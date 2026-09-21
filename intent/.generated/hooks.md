# Lifecycle hooks

Repositories configure lifecycle hooks through `hooks` in `side.yml`. Each event
maps to an ordered list of rules with optional `match` filters and typed actions.
The implemented scope is the `run_tests` event and the `modal.refresh_volumes`
action.

[Command script hooks](command_script_hooks.md) extends this design with `script`
actions and the per-command `before_command` event. Its standalone runner,
environment emissions, and credential-proxy binding are implemented; hook
configuration/dispatch and command integration remain separate work. That
extension does not change the `before_tests` batch semantics specified here.

```yaml
hooks:
  run_tests:
    - match:
        env_types: [modal]
      actions:
        - type: modal.refresh_volumes
```

## Matching and validation

Omitting `match` matches all invocations. An empty `match` object also matches
all invocations. When supplied, `match.env_types` must be nonempty and contain
only supported environment names: `local`, `local_git_worktree`, `devpod`,
`openshell`, and `modal`. It matches any listed type.

Each rule requires an `actions` list. Empty rule and action lists are valid
no-ops. Unknown events, action types (including `shell`), filters, fields, and
invalid values are rejected before execution, including in rules that would
not match the invocation.

Matching occurs once against the original invocation context. Matching rules
and their actions run serially in configuration order. Action types own their
validation and execution independently of event dispatch.

## Event: `run_tests`

`run_tests` runs after environment readiness and before any commands in a
nonempty test batch launch in parallel. It neither replaces nor wraps test
execution. Every nonempty retry batch dispatches the event again, including
automatic heartbeat retries and user-approved retries containing only pending
commands. Completed command results are retained; those commands are not rerun
as part of the pending batch.

Empty batches emit no event. Individual command execution does not emit hooks,
and hook actions do not emit nested events. `before_tests` is not an alias and
is rejected.

## Action: `modal.refresh_volumes`

`modal.refresh_volumes` reloads the current invocation's Modal sandbox volumes
using host credentials. It does not recreate the sandbox, change environment
identity, or update the event payload. Credentials are never exposed to sandbox
or hook processes.

Refresh accepts no action-specific arguments; only the common `type`,
`timeout_seconds`, and `on_error` fields are supported. Execution outside Modal
fails subject to the configured error policy. Host actions are explicitly
allowlisted, not arbitrary host shell execution.

## Timeouts, errors, and diagnostics

Actions accept a positive `timeout_seconds` (default 30), bounded by the parent
operation's lifetime. Fractional seconds are supported, provided the value is
representable as a duration of at least one nanosecond. Actions also accept
`on_error: fail | warn` (default `fail`).

An action failure or timeout with `fail` stops dispatch and prevents that test
batch from launching. With `warn`, the error is reported and dispatch continues;
the batch may launch after the remaining actions finish. Cancellation always
propagates, even with `warn`, and prevents the batch from launching.
`on_error` does not suppress configuration validation errors.

Action errors identify the configuration location and action type. Structured
diagnostics record the event, duration, and outcome, with action diagnostics
also identifying the location and type. Workflow replay does not duplicate
these diagnostics. Filesystem side effects are not rolled back.

## Retries and concurrency

Hooks may repeat after test retries or worker failure; they are not an
exactly-once side-effect mechanism. Each dispatched action uses a single-attempt
activity rather than its own automatic activity retry policy.

Ordering is local to an invocation, not a sandbox-wide lock. Volume refresh may
temporarily hide mounts or fail with open files; it neither coordinates
independent commands nor makes concurrent cache writes safe.

## Deferred scope

Shell actions and payload updates are deferred and are not supported
configuration. `test_results` is only a possible future event: its timing and
payload semantics are undefined, and configuring it is rejected.