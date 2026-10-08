-- Loads the per-source CSVs written by payload_bloat. Sizes only; no payload
-- content is ever extracted.
CREATE OR REPLACE TABLE extraction_meta AS
SELECT
    CAST('{{EXTRACTED_AT}}' AS TIMESTAMP) AS extracted_at_utc,
    CAST({{LARGE_BYTES}} AS BIGINT) AS large_bytes,
    '{{DATA_HOME}}' AS data_home;

-- size_bucket b holds items with 2^(b-1) <= bytes < 2^b; day is NULL when the
-- item's write time is unknown.
CREATE OR REPLACE TABLE size_buckets AS
SELECT
    *,
    CASE WHEN size_bucket = 0 THEN CAST(0 AS BIGINT) ELSE CAST(1 AS BIGINT) << (size_bucket - 1) END AS bucket_min_bytes
FROM read_csv('{{DIR}}/*_buckets.csv', header = true, quote = '"', escape = '"', columns = {
    'source': 'VARCHAR', 'category': 'VARCHAR', 'day': 'DATE', 'size_bucket': 'INTEGER',
    'items': 'BIGINT', 'bytes': 'BIGINT', 'max_bytes': 'BIGINT'});

CREATE OR REPLACE TABLE large_items AS
SELECT * FROM read_csv('{{DIR}}/*_large.csv', header = true, quote = '"', escape = '"', columns = {
    'source': 'VARCHAR', 'category': 'VARCHAR', 'item_id': 'VARCHAR', 'owner': 'VARCHAR',
    'at_utc': 'TIMESTAMP', 'bytes': 'BIGINT'});

CREATE OR REPLACE TABLE db_files AS
SELECT * FROM read_csv('{{DIR}}/*_db_files.csv', header = true, quote = '"', escape = '"', columns = {
    'source': 'VARCHAR', 'name': 'VARCHAR', 'path': 'VARCHAR', 'file_bytes': 'BIGINT', 'wal_bytes': 'BIGINT',
    'page_size': 'BIGINT', 'page_count': 'BIGINT', 'freelist_count': 'BIGINT', 'auto_vacuum': 'INTEGER'});

CREATE OR REPLACE TABLE table_sizes AS
SELECT * FROM read_csv('{{DIR}}/*_table_sizes.csv', header = true, quote = '"', escape = '"', columns = {
    'source': 'VARCHAR', 'name': 'VARCHAR', 'pages': 'BIGINT', 'bytes': 'BIGINT', 'unused_bytes': 'BIGINT'});

CREATE OR REPLACE TABLE temporal_executions AS
SELECT * FROM read_csv('{{DIR}}/*_executions.csv', header = true, quote = '"', escape = '"', columns = {
    'workflow_id': 'VARCHAR', 'run_id': 'VARCHAR', 'workflow_type': 'VARCHAR', 'start_time': 'TIMESTAMP',
    'info_bytes': 'BIGINT', 'state_bytes': 'BIGINT'});

-- Codec keys referenced from surviving temporal history.
CREATE OR REPLACE TABLE codec_refs AS
SELECT * FROM read_csv('{{DIR}}/*_codec_refs.csv', header = true, quote = '"', escape = '"', columns = {
    'codec_key': 'VARCHAR', 'run_id': 'VARCHAR'});

-- Every offloaded payload stored in kv, regardless of size.
CREATE OR REPLACE TABLE codec_entries AS
SELECT * FROM read_csv('{{DIR}}/*_codec_entries.csv', header = true, quote = '"', escape = '"', columns = {
    'codec_key': 'VARCHAR', 'at_utc': 'TIMESTAMP', 'bytes': 'BIGINT'});

CREATE OR REPLACE TABLE data_home_files AS
SELECT * FROM read_csv('{{DIR}}/data_home_files.csv', header = true, quote = '"', escape = '"', columns = {
    'name': 'VARCHAR', 'bytes': 'BIGINT', 'modified': 'TIMESTAMP'});