-- Snapshot of LLM calls (generate.* flow actions) from the core sqlite DB.
-- Only metadata, usage and message-ref identity are copied; message content,
-- tool definitions and secrets stay behind.
LOAD sqlite;
SET sqlite_all_varchar = true;
ATTACH {{CORE_DB}} AS core (TYPE sqlite, READ_ONLY);

-- Parses Go's time.String() format ("2026-10-02 23:48:43.473192 +0000 UTC")
-- into a UTC TIMESTAMP, honoring the offset. NULL when the value doesn't match.
CREATE OR REPLACE TEMP MACRO parse_go_time(s) AS
    TRY_CAST(regexp_extract(s, '^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(\.\d+)?) [+-]\d{4}( \S+)?$', 1) AS TIMESTAMP)
    - to_minutes(
        (CASE regexp_extract(s, ' ([+-])\d{4}( \S+)?$', 1) WHEN '-' THEN -1 ELSE 1 END)
        * (TRY_CAST(regexp_extract(s, ' [+-](\d{2})\d{2}( \S+)?$', 1) AS INTEGER) * 60
           + TRY_CAST(regexp_extract(s, ' [+-]\d{2}(\d{2})( \S+)?$', 1) AS INTEGER))
    );

CREATE OR REPLACE TABLE extraction_meta AS
SELECT
    {{SOURCE}} AS source_path,
    CAST({{CUTOFF}} AS TIMESTAMP) AS cutoff_utc,
    CAST({{EXTRACTED_AT}} AS TIMESTAMP) AS extracted_at_utc;

CREATE OR REPLACE TEMP TABLE source_calls AS
SELECT
    id,
    workspace_id,
    flow_id,
    nullif(subflow_id, '') AS subflow_id,
    subflow_name,
    action_type,
    action_status,
    created AS created_raw,
    updated AS updated_raw,
    parse_go_time(created) AS created_at,
    parse_go_time(updated) AS updated_at,
    action_params,
    action_result,
    TRY_CAST(action_params AS JSON) AS params,
    TRY_CAST(action_result AS JSON) AS result
FROM core.flow_actions
WHERE action_type LIKE 'generate.%';

DETACH core;

CREATE OR REPLACE TABLE llm_calls AS
WITH usage_raw AS (
    SELECT
        *,
        json_extract_string(result, '$.usage.inputTokens') AS input_raw,
        json_extract_string(result, '$.usage.outputTokens') AS output_raw,
        json_extract_string(result, '$.usage.cacheReadInputTokens') AS cache_read_raw,
        json_extract_string(result, '$.usage.cacheWriteInputTokens') AS cache_write_raw,
        TRY_CAST(json_extract(params, '$.messages.refs') AS JSON[]) AS refs_json
    FROM source_calls
    -- Unparseable timestamps are kept (and flagged) rather than silently dropped.
    WHERE created_at < (SELECT cutoff_utc FROM extraction_meta) OR created_at IS NULL
), typed AS (
    SELECT
        *,
        TRY_CAST(input_raw AS BIGINT) AS input_tokens,
        TRY_CAST(output_raw AS BIGINT) AS output_tokens,
        TRY_CAST(cache_read_raw AS BIGINT) AS cache_read_tokens,
        TRY_CAST(cache_write_raw AS BIGINT) AS cache_write_tokens
    FROM usage_raw
)
SELECT
    id,
    workspace_id,
    flow_id,
    subflow_id,
    subflow_name,
    action_type,
    action_status,
    created_raw,
    updated_raw,
    created_at,
    updated_at,
    coalesce(json_extract_string(result, '$.provider'), json_extract_string(params, '$.provider')) AS provider,
    coalesce(json_extract_string(result, '$.model'), json_extract_string(params, '$.model')) AS model,
    json_extract_string(result, '$.profileId') AS profile_id,
    json_extract_string(result, '$.authType') AS auth_type,
    coalesce(json_extract_string(result, '$.reasoningEffort'), json_extract_string(params, '$.reasoningEffort')) AS reasoning_effort,
    json_extract_string(params, '$.speed') AS speed,
    json_extract_string(result, '$.stopReason') AS stop_reason,
    json_extract_string(result, '$.id') AS response_id,
    coalesce(json_type(result, '$.usage') = 'OBJECT', false) AS has_usage,
    -- NULL means the provider response didn't report the field, which is
    -- distinct from a reported zero.
    input_tokens,
    output_tokens,
    cache_read_tokens,
    cache_write_tokens,
    json_extract_string(params, '$.messages.type') AS history_type,
    -- One identity string per message: its block keys. A changed key means the
    -- message content or its cache markers changed.
    list_transform(
        coalesce(refs_json, []),
        r -> array_to_string(TRY_CAST(json_extract(r, '$.blockKeys') AS VARCHAR[]), ',')
    ) AS msg_refs,
    list_transform(coalesce(refs_json, []), r -> json_extract_string(r, '$.role')) AS msg_roles,
    md5(coalesce(CAST(json_extract(params, '$.tools') AS VARCHAR), '')) AS tools_hash,
    coalesce(json_array_length(json_extract(params, '$.tools')), 0) AS n_tools,
    created_at IS NULL AS created_unparsed,
    updated_raw IS NOT NULL AND updated_at IS NULL AS updated_unparsed,
    action_params IS NOT NULL AND params IS NULL AS params_invalid_json,
    action_result IS NOT NULL AND result IS NULL AS result_invalid_json,
    (input_raw IS NOT NULL AND input_tokens IS NULL)
        OR (output_raw IS NOT NULL AND output_tokens IS NULL)
        OR (cache_read_raw IS NOT NULL AND cache_read_tokens IS NULL)
        OR (cache_write_raw IS NOT NULL AND cache_write_tokens IS NULL) AS usage_unparseable,
    coalesce(input_tokens < 0, false)
        OR coalesce(output_tokens < 0, false)
        OR coalesce(cache_read_tokens < 0, false)
        OR coalesce(cache_write_tokens < 0, false) AS negative_usage,
    coalesce(coalesce(cache_read_tokens, 0) + coalesce(cache_write_tokens, 0) > input_tokens, false) AS cache_exceeds_input
FROM typed;

-- Length of the longest common ordered prefix of two ref lists.
CREATE OR REPLACE TEMP MACRO common_prefix_len(a, b) AS
    coalesce(
        list_position(list_transform(range(1, least(len(a), len(b)) + 1), i -> a[i] = b[i]), false) - 1,
        least(len(a), len(b))
    );

-- Pairs each call with the previous call that likely shares its chat history:
-- same workspace, flow, role and first message ref (initial instructions are
-- always retained). Continuity is judged on the ordered prefix, so reordered
-- refs never count as an extension. link_class is evidence, not proof:
--   extend              previous refs form an ordered prefix of the current
--   extend_tail_change  continuity holds except for the last 1-2 previous
--                       messages; the cache breakpoint moving off the old
--                       last message is the usual cause, but an edit to those
--                       messages looks identical
--   truncate            continuity broke earlier AND input shrank by >= 20%:
--                       head-truncation candidate
--   mutate              continuity broke earlier without material shrink:
--                       edits, reordering or marker moves in retained messages
CREATE OR REPLACE TABLE llm_call_links AS
WITH keyed AS (
    SELECT *, workspace_id || '|' || flow_id || '|' || action_type || '|' || msg_refs[1] AS history_key
    FROM llm_calls
    WHERE created_at IS NOT NULL AND len(msg_refs) > 0
), lagged AS (
    SELECT
        *,
        lag(id) OVER w AS prev_id,
        lag(msg_refs) OVER w AS prev_refs,
        lag(input_tokens) OVER w AS prev_input_tokens,
        lag(updated_at) OVER w AS prev_updated_at,
        lag(provider) OVER w AS prev_provider,
        lag(model) OVER w AS prev_model,
        lag(tools_hash) OVER w AS prev_tools_hash,
        lag(subflow_id) OVER w AS prev_subflow_id
    FROM keyed
    WINDOW w AS (PARTITION BY history_key ORDER BY created_at, id)
), compared AS (
    SELECT
        *,
        common_prefix_len(prev_refs, msg_refs) AS common_prefix,
        len(list_filter(prev_refs, r -> NOT list_contains(msg_refs, r))) AS dropped_messages
    FROM lagged
)
SELECT
    id,
    history_key,
    prev_id,
    len(prev_refs) AS prev_n_messages,
    len(msg_refs) AS n_messages,
    common_prefix,
    -- Set-based count of vanished messages, regardless of order.
    dropped_messages,
    len(prev_refs) - common_prefix AS prefix_lost,
    CASE
        WHEN prev_id IS NULL THEN 'first'
        WHEN common_prefix = len(prev_refs) THEN 'extend'
        WHEN len(prev_refs) - common_prefix <= 2 AND dropped_messages <= 2 THEN 'extend_tail_change'
        WHEN input_tokens <= 0.8 * prev_input_tokens THEN 'truncate'
        ELSE 'mutate'
    END AS link_class,
    input_tokens - prev_input_tokens AS input_delta,
    prev_input_tokens,
    date_diff('millisecond', prev_updated_at, created_at) / 1000.0 AS gap_seconds,
    prev_id IS NOT NULL AND (provider IS DISTINCT FROM prev_provider OR model IS DISTINCT FROM prev_model) AS model_changed,
    prev_id IS NOT NULL AND tools_hash IS DISTINCT FROM prev_tools_hash AS tools_changed,
    prev_id IS NOT NULL AND subflow_id IS DISTINCT FROM prev_subflow_id AS subflow_changed
FROM compared;