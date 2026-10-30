.mode markdown

.print '## Snapshot'
SELECT source_path, cutoff_utc, extracted_at_utc, {{SINCE}} AS report_since_utc FROM extraction_meta;

.print '## Data issues (all extracted calls; flagged calls are excluded below)'
SELECT
    count(*) AS calls,
    count(*) FILTER (created_unparsed) AS created_unparsed,
    count(*) FILTER (updated_unparsed) AS updated_unparsed,
    count(*) FILTER (params_invalid_json) AS params_invalid_json,
    count(*) FILTER (result_invalid_json) AS result_invalid_json,
    count(*) FILTER (NOT has_usage AND NOT result_invalid_json) AS no_usage,
    count(*) FILTER (usage_unparseable) AS usage_unparseable,
    count(*) FILTER (negative_usage) AS negative_usage,
    count(*) FILTER (cache_exceeds_input) AS cache_exceeds_input
FROM llm_calls;

.print '## Unreported cache fields by provider (NULL, not zero: old instrumentation or provider omission)'
SELECT
    provider,
    count(*) FILTER (has_usage) AS calls_with_usage,
    count(*) FILTER (has_usage AND cache_read_tokens IS NULL) AS cache_read_missing,
    count(*) FILTER (has_usage AND cache_write_tokens IS NULL) AS cache_write_missing
FROM llm_calls
WHERE created_at >= {{SINCE}}
GROUP BY ALL
ORDER BY calls_with_usage DESC;

.print '## Prices in use (models.dev). provider_matched=false means the price came from an unrelated provider and is NOT used for cost'
SELECT provider, model, provider_matched, input_per_mtok, cache_read_per_mtok, cache_write_per_mtok, output_per_mtok
FROM model_prices
WHERE (provider, model) IN (SELECT DISTINCT provider, model FROM llm_calls WHERE created_at >= {{SINCE}})
ORDER BY provider_matched DESC, provider, model;

-- Prices only trusted when models.dev knew the model under this provider.
CREATE OR REPLACE TEMP VIEW trusted_prices AS
SELECT * FROM model_prices WHERE provider_matched;

CREATE OR REPLACE TEMP VIEW report_calls AS
SELECT
    c.*,
    l.history_key,
    l.prev_id,
    l.link_class,
    l.dropped_messages,
    l.input_delta,
    l.prev_input_tokens,
    l.gap_seconds,
    coalesce(l.model_changed, false) AS model_changed,
    coalesce(l.tools_changed, false) AS tools_changed,
    coalesce(l.subflow_changed, false) AS subflow_changed,
    l.link_class IN ('extend', 'extend_tail_change')
        AND NOT l.model_changed AND NOT l.tools_changed
        AND l.prev_input_tokens IS NOT NULL AS growth_link,
    p.input_per_mtok,
    p.cache_read_per_mtok,
    p.cache_write_per_mtok,
    -- Unpriced cache tiers fall back to the plain input price.
    (c.input_tokens - coalesce(c.cache_read_tokens, 0) - coalesce(c.cache_write_tokens, 0)) * p.input_per_mtok / 1e6
        + coalesce(c.cache_read_tokens, 0) * coalesce(p.cache_read_per_mtok, p.input_per_mtok) / 1e6
        + coalesce(c.cache_write_tokens, 0) * coalesce(p.cache_write_per_mtok, p.input_per_mtok) / 1e6
        + c.output_tokens * p.output_per_mtok / 1e6 AS cost_usd
FROM llm_calls c
LEFT JOIN llm_call_links l USING (id)
LEFT JOIN trusted_prices p ON p.provider IS NOT DISTINCT FROM c.provider AND p.model IS NOT DISTINCT FROM c.model
WHERE c.created_at >= {{SINCE}}
    AND c.has_usage
    AND c.input_tokens IS NOT NULL
    AND c.output_tokens IS NOT NULL
    AND NOT (c.usage_unparseable OR c.negative_usage OR c.cache_exceeds_input OR c.params_invalid_json);

.print '## Spend by role and model (models.dev prices; read/write shares only over calls reporting the field)'
SELECT
    action_type AS role,
    provider,
    model,
    count(*) AS calls,
    count(DISTINCT history_key) AS histories,
    round(sum(input_tokens) / 1e6, 2) AS input_mtok,
    round(sum(cache_read_tokens) / nullif(sum(input_tokens) FILTER (cache_read_tokens IS NOT NULL), 0), 3) AS read_share,
    round(sum(cache_write_tokens) / nullif(sum(input_tokens) FILTER (cache_write_tokens IS NOT NULL), 0), 3) AS write_share,
    round(sum(output_tokens) / 1e6, 2) AS output_mtok,
    round(sum(cost_usd), 2) AS cost_usd,
    round(100 * sum(cost_usd) / sum(sum(cost_usd)) OVER (), 1) AS cost_pct,
    count(*) FILTER (cost_usd IS NULL) AS unpriced_calls
FROM report_calls
GROUP BY ALL
ORDER BY cost_usd DESC NULLS LAST, calls DESC;

CREATE OR REPLACE TEMP VIEW role_model_stats AS
SELECT
    action_type AS role,
    provider,
    model,
    sum(cost_usd) AS cost_usd,
    count(*) FILTER (link_class = 'first') AS histories,
    count(*) FILTER (growth_link) AS growth_links,
    -- Negative deltas on growth links contradict the append-only assumption;
    -- they are kept in the averages and counted so the contradiction is visible.
    count(*) FILTER (growth_link AND input_delta < 0) AS negative_growth_links,
    median(input_delta) FILTER (growth_link) AS g_median,
    avg(input_delta) FILTER (growth_link) AS g_mean,
    quantile_cont(input_delta, 0.9) FILTER (growth_link) AS g_p90,
    count(*) FILTER (link_class = 'extend_tail_change') AS tail_changes,
    count(*) FILTER (link_class = 'mutate') AS mutations,
    count(*) FILTER (link_class = 'truncate') AS drops,
    median(prev_input_tokens) FILTER (link_class = 'truncate') AS pre_drop_median,
    median(input_tokens) FILTER (link_class = 'truncate') AS post_drop_median,
    -- Paired per-drop shrink, not a difference of independent medians.
    median(prev_input_tokens - input_tokens) FILTER (link_class = 'truncate') AS drop_size_median,
    median(dropped_messages) FILTER (link_class = 'truncate') AS dropped_msgs_median,
    median(cache_read_tokens / nullif(input_tokens, 0)) FILTER (link_class = 'truncate') AS post_drop_read_share,
    count(*) FILTER (model_changed OR tools_changed) AS model_or_tools_changes,
    coalesce(sum(cache_write_tokens) > 0, false) AS writes_reported
FROM report_calls
GROUP BY ALL;

.print '## History growth (g) and drops, tokens. drops = continuity broke AND input shrank >= 20% (head-truncation candidates); mutations = broke without shrink'
SELECT
    role,
    model,
    histories,
    growth_links,
    negative_growth_links,
    round(g_median) AS g_median,
    round(g_mean) AS g_mean,
    round(g_p90) AS g_p90,
    tail_changes,
    mutations,
    drops,
    round(growth_links / nullif(drops, 0), 1) AS growth_links_per_drop,
    round(pre_drop_median) AS pre_drop_median,
    round(post_drop_median) AS post_drop_median,
    round(drop_size_median) AS drop_size_median,
    dropped_msgs_median,
    round(post_drop_read_share, 3) AS post_drop_read_share,
    model_or_tools_changes
FROM role_model_stats
ORDER BY cost_usd DESC NULLS LAST;

.print '## Cache reuse on growth links: hit = cache_read / previous call input; miss = hit < 0.5'
SELECT
    action_type AS role,
    model,
    CASE WHEN gap_seconds < 300 THEN 'a <5m' WHEN gap_seconds < 3600 THEN 'b 5m-1h' ELSE 'c >=1h' END AS gap_bucket,
    count(*) AS links,
    round(median(cache_read_tokens / nullif(prev_input_tokens, 0)), 3) AS hit_median,
    round(avg(CAST(cache_read_tokens < 0.5 * prev_input_tokens AS INTEGER)), 3) AS miss_rate
FROM report_calls
WHERE growth_link AND cache_read_tokens IS NOT NULL
GROUP BY ALL
ORDER BY role, model, gap_bucket;

.print '## Buffer model: B* = sqrt(2 g K (rho-1)); rho = write/read price when the provider reports cache writes, else input/read; K = median post-drop input (tokens). Assumes non-truncating calls always hit the cache'
WITH priced AS (
    SELECT
        s.*,
        p.provider_matched,
        -- models.dev lists a write premium for providers whose usage never
        -- reports cache writes (OpenAI); in that case misses cost plain input.
        CASE WHEN s.writes_reported THEN coalesce(p.cache_write_per_mtok, p.input_per_mtok) ELSE p.input_per_mtok END
            / p.cache_read_per_mtok AS rho
    FROM role_model_stats s
    LEFT JOIN trusted_prices p ON p.provider IS NOT DISTINCT FROM s.provider AND p.model IS NOT DISTINCT FROM s.model
)
SELECT
    role,
    model,
    round(rho, 1) AS rho,
    round(g_mean) AS g_mean,
    round(post_drop_median) AS k_post_drop,
    round(drop_size_median) AS drop_size_median,
    CASE
        WHEN rho IS NULL THEN NULL
        WHEN rho <= 1 THEN NULL
        WHEN g_mean IS NULL OR g_mean <= 0 THEN NULL
        WHEN post_drop_median IS NULL THEN NULL
        ELSE round(sqrt(2 * g_mean * post_drop_median * (rho - 1)))
    END AS b_star,
    CASE
        WHEN rho IS NULL THEN 'no trusted price'
        WHEN rho <= 1 THEN 'no re-write penalty (rho <= 1)'
        WHEN g_mean IS NULL THEN 'no growth links'
        WHEN g_mean <= 0 THEN 'history not growing on average'
        WHEN post_drop_median IS NULL THEN 'no drops observed; K unknown'
    END AS inapplicable_reason
FROM priced
ORDER BY cost_usd DESC NULLS LAST;