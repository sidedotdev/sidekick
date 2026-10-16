.maxrows 200

-- sum() over BIGINT yields HUGEINT, which format_bytes does not accept.
CREATE TEMP MACRO fmt_bytes(x) AS format_bytes(CAST(x AS BIGINT));

.print '## Snapshot'
SELECT
    extracted_at_utc,
    {{SINCE}} AS window_start_utc,
    fmt_bytes(large_bytes) AS large_item_threshold
FROM extraction_meta;

.print '## Largest files in the data home'
SELECT name, fmt_bytes(bytes) AS size, modified
FROM data_home_files ORDER BY bytes DESC LIMIT 15;

.print '## Database files (free = reclaimable freelist pages)'
SELECT
    name,
    fmt_bytes(file_bytes) AS file,
    fmt_bytes(wal_bytes) AS wal,
    fmt_bytes(page_size * freelist_count) AS free,
    auto_vacuum
FROM db_files ORDER BY file_bytes DESC;

.print '## Largest tables and indexes (dbstat)'
SELECT source, name, fmt_bytes(bytes) AS size, round(100.0 * unused_bytes / nullif(bytes, 0), 1) AS pct_unused
FROM table_sizes
QUALIFY row_number() OVER (PARTITION BY source ORDER BY bytes DESC) <= 8
ORDER BY source, bytes DESC;

.print '## Payload totals per source, all time vs window'
SELECT
    source,
    sum(items) AS items,
    fmt_bytes(sum(bytes)) AS all_time,
    fmt_bytes(coalesce(sum(bytes) FILTER (WHERE day >= CAST({{SINCE}} AS DATE)), 0)) AS in_window,
    fmt_bytes(coalesce(sum(bytes) FILTER (WHERE day IS NULL), 0)) AS undated,
    coalesce(sum(items) FILTER (WHERE day >= CAST({{SINCE}} AS DATE) AND bucket_min_bytes >= 1048576), 0) AS window_items_ge_1mib,
    fmt_bytes(max(max_bytes) FILTER (WHERE day >= CAST({{SINCE}} AS DATE))) AS window_max_item
FROM size_buckets GROUP BY source ORDER BY sum(bytes) DESC;

.print '## MiB written per day in the window (temporal.event is a breakdown of temporal.history_node)'
SELECT
    day,
    round(coalesce(sum(bytes) FILTER (WHERE source = 'kv'), 0) / 1048576.0, 1) AS kv,
    round(coalesce(sum(bytes) FILTER (WHERE source LIKE 'core.%'), 0) / 1048576.0, 1) AS core_flow_actions,
    round(coalesce(sum(bytes) FILTER (WHERE source = 'temporal.history_node'), 0) / 1048576.0, 1) AS temporal_history,
    coalesce(sum(items) FILTER (WHERE source = 'kv' AND bucket_min_bytes >= 1048576), 0) AS kv_ge_1mib,
    coalesce(sum(items) FILTER (WHERE source LIKE 'core.%' AND bucket_min_bytes >= 1048576), 0) AS core_ge_1mib,
    coalesce(sum(items) FILTER (WHERE source = 'temporal.event' AND bucket_min_bytes >= 1048576), 0) AS events_ge_1mib
FROM size_buckets
WHERE day >= CAST({{SINCE}} AS DATE)
GROUP BY day ORDER BY day;

.print '## Size distribution in the window (items >= 64 KiB)'
SELECT source, fmt_bytes(bucket_min_bytes) AS at_least, sum(items) AS items, fmt_bytes(sum(bytes)) AS bytes
FROM size_buckets
WHERE day >= CAST({{SINCE}} AS DATE) AND bucket_min_bytes >= 65536
GROUP BY source, bucket_min_bytes ORDER BY source, bucket_min_bytes;

.print '## Top categories by bytes in the window'
SELECT
    source,
    category,
    sum(items) AS items,
    fmt_bytes(sum(bytes)) AS bytes,
    fmt_bytes(max(max_bytes)) AS max_item,
    coalesce(sum(items) FILTER (WHERE bucket_min_bytes >= 1048576), 0) AS ge_1mib
FROM size_buckets
WHERE day >= CAST({{SINCE}} AS DATE)
GROUP BY source, category
QUALIFY row_number() OVER (PARTITION BY source ORDER BY sum(bytes) DESC) <= 10
ORDER BY source, sum(bytes) DESC;

.print '## Command-related temporal events in the window'
SELECT
    category,
    sum(items) AS events,
    fmt_bytes(sum(bytes)) AS bytes,
    fmt_bytes(max(max_bytes)) AS max_event,
    coalesce(sum(items) FILTER (WHERE bucket_min_bytes >= 262144), 0) AS ge_256kib,
    coalesce(sum(items) FILTER (WHERE bucket_min_bytes >= 1048576), 0) AS ge_1mib
FROM size_buckets
WHERE source = 'temporal.event' AND category ILIKE '%command%' AND day >= CAST({{SINCE}} AS DATE)
GROUP BY category ORDER BY sum(bytes) DESC;

.print '## Codec payloads in kv by month, referenced vs unreferenced by surviving temporal history'
.print '## (unreferenced = history deleted or never recorded; eligible for orphan cleanup)'
WITH codec AS (
    SELECT
        e.at_utc,
        e.bytes,
        EXISTS (SELECT 1 FROM codec_refs r WHERE r.codec_key = e.codec_key) AS referenced
    FROM codec_entries e
)
SELECT
    date_trunc('month', at_utc) AS month,
    count(*) AS entries,
    fmt_bytes(median(bytes)) AS median_size,
    count(*) FILTER (WHERE referenced) AS referenced,
    fmt_bytes(coalesce(sum(bytes) FILTER (WHERE referenced), 0)) AS referenced_bytes,
    count(*) FILTER (WHERE NOT referenced) AS unreferenced,
    fmt_bytes(coalesce(sum(bytes) FILTER (WHERE NOT referenced), 0)) AS unreferenced_bytes
FROM codec
GROUP BY ALL ORDER BY month;

.print '## Codec references in history whose kv entry is missing (payloads that can no longer be decoded)'
SELECT count(DISTINCT r.codec_key) AS missing_keys, count(DISTINCT r.run_id) AS affected_runs
FROM codec_refs r
WHERE NOT EXISTS (SELECT 1 FROM codec_entries e WHERE e.codec_key = r.codec_key);

.print '## Largest items in the window'
SELECT source, category, fmt_bytes(bytes) AS size, at_utc, owner, item_id
FROM large_items
WHERE at_utc >= {{SINCE}} AND source <> 'temporal.history_node'
ORDER BY bytes DESC LIMIT 40;