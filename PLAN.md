# Script-based command hooks

Implement the lifecycle-hook extension in
[intent/.generated/command_script_hooks.md](intent/.generated/command_script_hooks.md),
using [the base hooks design](intent/.generated/hooks.md).
Keep this file limited to unfinished work; delete it when complete.

## Configuration and event selection

- [ ] Add validated hook rules, matching, and script-action configuration to
      repository configuration. Reject unknown events, filters, action fields,
      invalid values, and empty environment filters.
- [ ] Coordinate shared configuration with the base lifecycle-hook design;
      avoid a second incompatible hooks schema. The unrelated scripting branch
      is not a prerequisite.
- [ ] Match `before_command` rules in workflow code against original invocation
      context. Preserve configured ordering and ordinary command approval.
- [ ] Pass matched scripts and non-secret invocation data to command execution.
      Include workspace identity; derive workflow identity inside the activity.
- [ ] Replace per-environment proxy activation lists in `side.yml` with the
      Modal script rule. Retain named definitions, trusted local authorization,
      ordinary reverse forwards, and development-mode policy.
- [ ] Remove the superseded activation fields from Modal, DevPod, and OpenShell
      configuration and update configuration tests.

## Script execution and host operations

- [ ] Select and pin the current compatible goja version; update module files.
- [ ] Add a generic script runner with invocation-local VMs, data-only context,
      explicit host bindings, cancellation, timeouts, and safe diagnostics.
      Do not build a plugin loader or general module system.
- [ ] Ignore script return values. Implement `emitCommandEnvironment` with
      validated string-map assignments, ordered composition, and action-local
      staging. Discard emissions on action failure.
- [ ] Expose `host.credentialProxies.acquireAccess` as a named operation bound
      through a per-invocation closure. Capture activity context, original
      configuration, forwarding, and workspace/workflow identity.
- [ ] Preserve workspace-plus-workflow-ID scoping and one-hour flow leases.
      Reuse authorized access and refresh before command execution when needed.
      Preserve token reuse during lease extension.
- [ ] Keep runtime authorization and secret resolution outside `common`, `env`,
      and `llm2`. Schema ownership in `common` is already established.
- [ ] Track acquired tokens immediately and treat emitted values as sensitive.
      Redact script diagnostics/exceptions and command results before logging,
      truncation, or serialization. Honor cancellation in every host function.

## Command execution cleanup

- [ ] Extend the existing package-level `env.EnvRunCommandActivity` with generic
      hook execution; keep credential-proxy behavior out of `env`.
- [ ] Remove `CredentialProxyCommandActivity`, its input type, helper execution
      path, worker registration, and all callers.
- [ ] Remove `dev.NewCommandActivities()`, the replacement command-activity
      method/factory, and superseded provisioner registry/input/result types.
      Retain needed redaction behavior in the generic command path.
- [ ] Remove command keepalive, periodic renewal, and command-end revocation
      plumbing. Preserve shared proxy-service lifetime, authorization, expiry,
      and pre-command access recovery.
- [ ] Update command and verification/test call sites to use matched hooks with
      the ordinary command activity.
- [ ] Remove branch-only credential-proxy workflow compatibility branches and
      activity/version paths: these implementations have never run. Check
      replay behavior against pre-existing ordinary-command histories; add
      versioning only where genuinely required for those histories.
- [ ] Replace wrapper/provisioner-specific tests with behavioral coverage of the
      generic hooks and reusable access operation.

## Verification

- [ ] Use red-green tests for new behavior: matching, ordering, empty hooks,
      explicit emissions, multiple emissions, collision precedence, ignored
      return values, fail/warn handling, and discarded partial emissions.
- [ ] Test JavaScript cancellation/timeouts and cancellation during host calls.
- [ ] Test implicit flow identity, cross-command/retry reuse, flow isolation,
      changed definitions, expiry refresh, unchanged-token lease extension, and
      recovery after lost/revoked access or service restart.
- [ ] Test authorization failures, unavailable forwarding, malformed script
      output, token exposure through exceptions, and redaction before truncation.
      Confirm runtime secrets never enter Temporal input/result payloads.
- [ ] Retain fake-upstream tests for unauthorized requests, credential
      separation, streaming/usage preservation, and cancellation.
- [ ] Run affected tests for common, scripting/hooks, env, credential_proxy,
      api, dev, and worker; verify workflow replay where affected.
- [ ] Run one small live smoke test if configured environments are available;
      otherwise retain concise execution instructions with the smoke test.

## Intent consistency

- [ ] Update intent links to final implementation locations and remove links to
      deleted wrappers/provisioning files.
- [ ] Keep the base hooks document and command extension consistent about event
      scope without losing the supplied base design.