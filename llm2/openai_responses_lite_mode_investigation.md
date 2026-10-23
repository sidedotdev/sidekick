# OpenAI Responses Lite investigation

**AI-generated investigation notes.**
Investigated **2026-10-09**. **Sidekick does not enable Responses Lite.**
Subscription search runs inside the provider through `/alpha/search`, using
ordinary Responses requests. API-key requests retain hosted `web_search`.

## Observed in Codex source

Reference: `openai/codex`, commit
`4bc83aa5f7ebfec8492bb5747affe0fc8433fcad`; paths below are under `codex-rs/`.

- `models-manager/models.json` configures `use_responses_lite` **per model**:
  `gpt-6-astra` is true; `gpt-5.5` is false in this snapshot.
- `core/src/client.rs` sends `x-openai-internal-codex-responses-lite: true`
  for HTTP. WebSocket requests use client metadata
  `ws_request_header_x_openai_internal_codex_responses_lite: "true"`.
- The same client omits top-level `tools`, prepending an input item with
  `type: "additional_tools"`, `role: "developer"`, and `tools` instead.
  `tools/src/tool_spec.rs` groups unnamespaced function/custom tools into the
  `functions` namespace; other namespaces are retained.
- It sets `parallel_tool_calls: false` and `reasoning.context: "all_turns"`.
  The ordinary path omits reasoning context; its comment describes the default
  as `current_turn`.
- `core/src/tools/spec_plan.rs` excludes all hosted tools for Lite models.
  Its comment explicitly says Lite accepts client-executed tool schemas,
  not hosted Responses tools. Lite enables standalone search independently of
  the `StandaloneWebSearch` feature flag: `web.run` is executed by the client,
  using `/alpha/search` (`ext/web-search/src/tool.rs`,
  `codex-api/src/endpoint/search.rs`).

These are Codex's implementation choices and stated restrictions, **not proof
that the backend enforces every field as a requirement**. No rationale for
the mode's existence was established.

## Observed in live probes

Same `gpt-6-astra`, account and subscription HTTP endpoint
(`chatgpt.com/backend-api/codex/responses`), interleaved controls:

- Ordinary format with hosted search: four of five runs failed with
  `server_error` after search; one completed.
- Lite format with hosted search: no hosted search executed in five runs.
  These are **inconclusive search controls**, not successful searches.
- Client-run `/alpha/search` plus result continuation succeeded in both formats:
  three ordinary-format runs and five Lite-format runs. Two earlier ordinary
  runs stopped at the probe's one-follow-up limit, without a provider error.

The probes establish these outcomes, not why the backend fails. API-key
behavior was not tested by this investigation. Local evidence was saved in
`.side/tmp/lite-probe-{1,2}` (git-ignored).