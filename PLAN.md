# Script-based command hooks

Implement the lifecycle-hook extension in
[intent/.generated/command_script_hooks.md](intent/.generated/command_script_hooks.md),
using [the base hooks design](intent/.generated/hooks.md).
Track completed components alongside deferred integration and cleanup.

## Completed standalone components

- [x] Pin a compatible goja revision in module files and implement a fresh,
      invocation-local runner for JavaScript function bodies with ignored returns.
- [x] Provide detached camelCase data-only context and explicit host operations,
      with no implicit filesystem, process, network, or general module access.
- [x] Implement parent-bounded action timeouts, JavaScript interruption, and
      context propagation into host operations; cancellation cannot succeed.
- [x] Implement validated, transactional command-environment emissions and
      ordered composition, discarding assignments from failed actions.
- [x] Implement invocation-bound credential-proxy access with captured configuration,
      forwarding, workspace identity, and Temporal or explicit workflow identity.
- [x] Reuse authorized flow activation for scoped leases, token-preserving
      extension, isolation, and recovery without a new client renewal lifecycle.
- [x] Remove development-mode startup/access restrictions while preserving
      configured authorization, private control access, authentication, and expiry.
- [x] Add focused runner, emission, access, and configuration-driven policy tests.
- [x] Document the standalone API and deferred integration responsibilities.
- [x] Remove automatic masking and secret tracking from scripting and access;
      output policy belongs to a future invocation/logging integration.

## Configuration and event selection

This remaining work belongs to the parallel hooks integration and subsequent
cleanup. The completed standalone components and their handoff are documented in
[Command script hooks](intent/.generated/command_script_hooks.md#standalone-api).
Keep current command execution functional until that integration lands.

- [ ] Add validated hook rules, matching, and script-action configuration to
      repository configuration. Reject unknown events, filters, action fields,
      invalid values, and empty environment filters.
- [ ] Coordinate shared configuration with the base lifecycle-hook design;
      avoid a second incompatible hooks schema.
- [ ] Match `before_command` rules in workflow code against original invocation
      context. Preserve configured ordering and ordinary command approval.
- [ ] Pass matched scripts and non-secret invocation data to command execution.
      Include workspace identity; derive workflow identity inside the activity.
- [ ] After command-hook integration, replace per-environment proxy activation
      lists in `side.yml` with the Modal script rule. Retain named definitions
      and existing reverse forwards, including host 22855 to container 32855.
      Preserve configuration-driven startup, independently loaded host user and
      startup-repository authorization, and the emergency
      `SIDE_CREDENTIAL_PROXY_SIDE_AUTH_ENABLED=false` opt-out. Repository
      configuration may expand authorization when loaded by the service;
      arbitrary script/request definitions may not. Do not restore
      `SIDE_APP_ENV` gating.
- [ ] Remove the superseded activation fields from Modal, DevPod, and OpenShell
      configuration and update configuration tests only after migration.

## Script integration and output policy

- [ ] Dispatch script actions through `scripting.RunCommand`, supplying detached
      non-secret context and explicit invocation-bound operations from
      `credential_proxy/access`. Do not duplicate the runner or access lifecycle.
      Keep runtime authorization and secret resolution outside `common`, `env`,
      and `llm2`.
- [ ] Define output policy at the invocation/logging boundary for Go and JavaScript
      alike. Automatic masking is deferred, not a scripting responsibility.
      Apply any future masking before truncation, logging, or serialization;
      standalone diagnostics and errors are neither masked nor truncated.
      Never place access results or environment assignments in Temporal payloads.
- [ ] Apply successful `CommandResult` assignments in configured order. Implement
      fail/warn dispatch around runner results; failed actions contribute
      no assignments, and cancellation always propagates.

## Command execution cleanup

Do not remove still-used wrappers, provisioners, worker registrations, workflow
paths, or lease plumbing before their replacement is integrated.

- [ ] Extend the existing package-level `env.EnvRunCommandActivity` with generic
      hook execution; keep credential-proxy behavior out of `env`.
- [ ] Update command and verification/test call sites to use matched hooks with
      the ordinary command activity.
- [ ] Remove `CredentialProxyCommandActivity`, its input type, helper execution
      path, worker registration, and all callers after migration.
- [ ] Remove `dev.NewCommandActivities()`, `env.CommandActivities`, and the
      superseded provisioner registry/input/result types after migration.
      Retain needed redaction behavior in the generic command path.
- [ ] Remove command keepalive, periodic renewal, and command-end revocation
      plumbing after migration. Preserve shared proxy-service lifetime,
      authorization, expiry, and pre-command access recovery.
- [ ] Remove branch-only credential-proxy workflow compatibility branches and
      activity/version paths after checking deployment and replay requirements.
      Preserve deterministic replay for existing histories; add workflow
      versioning where required.
- [ ] Retire wrapper/provisioner-specific tests only with their obsolete paths.
      Preserve lifecycle/security coverage and add generic hook integration
      coverage without duplicating standalone runner/access tests.

## Verification

These checks belong to the deferred integration, not the completed standalone
component work. Mandatory red-green sequencing and live smoke tests are not
required.

- [ ] Test hook matching, ordering, empty hooks, fail/warn handling, successful
      assignment composition, and failed-action discard through command dispatch.
- [ ] Verify cancellation propagation, command-output redaction before
      truncation, and exclusion of runtime secrets from Temporal inputs/results
      at the integrated command boundary.
- [ ] Reuse existing standalone runner/access and proxy lifecycle/security tests
      for interruption, identity, authorization, lease reuse/recovery, credential
      separation, streaming/usage preservation, and upstream cancellation.
- [ ] Run `go run ./scripts/affected_tests` for affected integration packages;
      verify workflow replay where affected.

## Intent consistency

- [ ] When integration lands, update command execution links and implementation
      status in the intent documents. Remove obsolete links only when their
      corresponding code is removed.
- [ ] Keep the base hooks document and command extension consistent about event
      scope without losing the supplied base design.