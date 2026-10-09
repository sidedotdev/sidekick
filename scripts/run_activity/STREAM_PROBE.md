# OpenAI Responses stream probe

The opt-in stream probe replays a stored Stream activity without publishing
events into its original flow or executing returned function calls. It still
contacts the provider and consumes provider quota.

## Controlled reproduction

Run from the repository root with access to the workflow history, persisted
chat history, and original authentication:

```sh
go run ./scripts/run_activity/ \
  --stream-probe \
  --timeout 15m \
  --probe-matrix-dir .side/tmp/openai-probe-new-run \
  --run-id 01a11e68-7af6-7101-a41b-6a634e8e1cbd \
  flow_3KRDXWCc2qkxoxhfjTqBVn3AsgA 1846
```

The output directory must not already exist. Requests and responses may contain
private conversation content. Keep these artifacts private; review them before
sharing. Request authentication headers are not written to the artifacts.

The matrix separates production behavior from explicit hosted-search controls:

- `provider-*` runs the full activity/provider loop. Subscription authentication
  uses client-executed search through `alpha/search`, followed by Responses
  continuations. API-key authentication retains hosted search.
- `hosted-*` explicitly offers hosted `web_search`, replacing the client-search
  function in the captured request when necessary. SDK and raw HTTP controls
  retain the hosted-search reproduction independently of production behavior.

Both groups compare stored, fresh, and minimal histories/tool sets. Search-only
controls omit application functions such as `advisor_guide`; subscription
provider controls still need the client-search function. Additional hosted HTTP
controls vary optional request settings and SDK-specific headers.

For each control, inspect:

- `*.request.json`: expected initial request body.
- `*.actual.request.json`: last captured outgoing Responses body.
- `*.sse`: raw Responses streams concatenated in request order.
- `*.round-NN.request.json` and `*.round-NN.sse`: individual Responses exchanges.
  These exclude the separate `alpha/search` HTTP exchanges.
- `report.json`: parsed results, updated after each control.

Initial requests are checked against their expected body, endpoint, and
authentication before transmission. Provider continuations retain endpoint and
authentication checks and may change only input history or withdraw client
search at the provider's round limit. Request mismatches identify changed
top-level fields without printing their values. A local validation error is not
evidence of provider behavior.

Each provider response stream is parsed separately. Provider success also
requires completed search results in the returned message, followed by the
requested application call or a text answer. A nonzero exit is expected when a
control fails or is inconclusive; inspect the report rather than treating process
completion as success. A successful response that never completes search is
inconclusive. For hosted provider errors, inspect `searchesCompleted` separately:
an error before search does not reproduce a post-search failure.

## Observed reproduction

With model `gpt-6-astra`, subscription authentication, and endpoint
`https://chatgpt.com/backend-api/codex/responses`, search-only requests repeatedly
received `server_error` after a completed hosted search. The failure reproduced
through the application, directly through the SDK, and through raw HTTP.

This minimal raw HTTP body also reproduced it:

```json
{
  "input": [
    {
      "content": "Use the hosted web_search tool now to find the current Go release on go.dev. After the search completes, answer briefly in plain text with your findings. Do not answer from memory.",
      "role": "user"
    }
  ],
  "model": "gpt-6-astra",
  "store": false,
  "stream": true,
  "tools": [
    {"type": "web_search"}
  ]
}
```

The minimal-header control retained authentication, JSON content type, and SSE
accept headers, without SDK-specific headers. Its captured sequence was:

1. `response.created` and `response.in_progress`.
2. A `web_search_call` starts and is marked completed.
3. An assistant message and empty output-text part are added.
4. `error` with code `server_error`.
5. `response.failed`, retaining the completed search and empty assistant message.

No function call, reasoning item, or answer-text delta appeared in that trace.
The request ID in the error was
`6e46b075-7020-4def-be62-9dbf77c3b4d6`.

Changing required tool choice to auto, lowering reasoning effort, omitting the
reasoning field, omitting encrypted-content inclusion, and omitting explicit
parallel-tool configuration also reproduced post-search errors. Removing all
those optional fields together still reproduced the failure.

These observations isolate a server-reported failure after hosted search from
stored history, function schemas, application SSE processing, and SDK response
processing. They do not identify the provider's internal defect, establish
whether its internal search results were valid, or establish behavior for other
accounts, models, or endpoints. A model control that never searches cannot answer
those questions.

One earlier fresh-application control failed local request validation and was
excluded. Subsequent runs passed that validation; the original mismatch was not
explained. Expected and actual request bodies are now both retained to diagnose
any recurrence.

## Search transport probe

```sh
go run ./scripts/run_activity/ \
  --stream-probe \
  --timeout 10m \
  --probe-search-dir .side/tmp/openai-search-new-run \
  --probe-search-models gpt-6-astra,gpt-5.5 \
  --run-id 01a11e68-7af6-7101-a41b-6a634e8e1cbd \
  flow_3KRDXWCc2qkxoxhfjTqBVn3AsgA 1846
```

Using the stored activity's endpoint and authentication, this sends a minimal
search-only request for each model and each hosted tool shape: a bare
`{"type":"web_search"}` tool and the shape Codex sends for non-Lite models.
Only authentication and content-negotiation headers are retained. It then calls
the standalone `alpha/search` endpoint with the stored model. Hosted controls
must report the requested model; models the account does not support return
HTTP 400 and are reported as invalid, not as search results.

Observed on the subscription endpoint, in two consecutive runs:

- Hosted search with `gpt-6-astra` and with `gpt-5.5`, using either tool shape,
  completed a search and then failed with `server_error`.
- `POST https://chatgpt.com/backend-api/codex/alpha/search` with `gpt-6-astra`
  returned HTTP 200 with search output and results.
- `gpt-5.4`, `gpt-5.2`, and `gpt-5.3-codex` were rejected as unsupported for
  Codex with a ChatGPT account, so they could not serve as controls.

The failure therefore is not specific to Responses Lite models or the hosted
tool shape. Current Codex clients do not depend on hosted search for Responses
Lite models: they expose a client-executed search tool and call `alpha/search`.
That endpoint is undocumented and marked alpha by Codex. API-key authentication
on `api.openai.com` has not been tested by this probe.

Production subscription requests use client-executed search inside the provider;
API-key requests retain hosted search. Neither the production implementation nor
the probe silently drops search in response to a hosted-search failure.