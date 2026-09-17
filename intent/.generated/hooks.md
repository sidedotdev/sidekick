# Lifecycle hooks

Repositories configure lifecycle hooks through `hooks` in `side.yml`. Each event
maps to an ordered list of rules with optional `match` filters and typed actions.

```yaml
hooks:
  before_tests:
    - match:
        env_types: [modal]
      actions:
        - type: modal.refresh_volumes
```

## Matching and execution

Omitting `match` matches all invocations. The `env_types` filter matches any listed
environment; entries must name supported environments and the list must not be
empty.

Matching occurs once against the original event context. Matching rules and
their actions run serially in configuration order. Unknown events, types,
filters, fields, and invalid values are rejected before execution.

## Event

`before_tests` runs after environment readiness, before a nonempty test batch
launches its commands in parallel. Each retry emits it for pending commands.
Empty batches emit no event. Individual command execution does not emit hooks,
and hook actions do not emit nested events.

## Actions

- `shell` requires `command` and accepts a repository-relative `working_dir`.
  It runs in the selected environment under ordinary command approval rules.
  Versioned event context arrives as camelCase JSON on stdin: event, environment
  type, repository working directory, and pending test commands.
  Stdout and stderr are diagnostics, not payload replacements.
- `modal.refresh_volumes` reloads the current Modal sandbox's volumes using host
  credentials, takes no additional arguments, and fails outside Modal.
  Credentials are never exposed to hook processes. Cache refresh runs at
  `before_tests`, before that batch's parallel commands.

Action types own their validation and execution independently of event dispatch.
Results contain diagnostics and an optional validated payload update; shell and
refresh actions leave the payload unchanged. Payload updates cannot change
environment identity, trigger rematching, or bypass final command approval.

## Failure and concurrency

Actions accept a positive `timeout_seconds` (default 30), bounded by the parent
operation's lifetime, and `on_error: fail | warn` (default `fail`). Failure stops
dispatch; warning reports the error and proceeds. Cancellation always propagates.
Errors identify the configuration location and action type; diagnostics record
the event, duration, and outcome. Failed actions supply no payload update, and
filesystem side effects are not rolled back.

Hooks may repeat after retries or worker failure. Ordering is local to an
invocation, not a sandbox-wide lock. Volume refresh may temporarily hide mounts
or fail with open files; it neither coordinates independent commands nor makes
concurrent cache writes safe. Host actions are allowlisted, not arbitrary shell
execution.