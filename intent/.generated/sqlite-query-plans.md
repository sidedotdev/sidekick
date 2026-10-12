# SQLite query-plan regression checks

Proposed follow-up: validate queries exercised by tests without duplicating SQL
from storage methods.

- **Capture:** Enable test-only instrumentation at the SQLite driver boundary for
  both core and KV databases, covering direct calls, prepared statements, and
  transactions. Collect SQL and parameter types without persisting sensitive
  values. Merge per-process observations deterministically after parallel runs.
- **Corpus:** Check observed queries against a reviewed, version-controlled JSON
  corpus. Identify queries by database and conservatively normalized SQL,
  preserving literals and placeholder counts that can affect plans. Unknown
  queries fail validation; an explicit update command proposes additions.
  Classify migration, maintenance, and non-plannable statements separately.
- **Fixtures:** Give corpus entries deterministic synthetic datasets and parameter
  scenarios. Include realistic table sizes, workspace/flow fan-out, skew,
  active/archive ratios, and selective versus broad filters. Populate freshly
  migrated databases and run `ANALYZE` before checking plans.
- **Plans:** A separate test runs `EXPLAIN QUERY PLAN` with fixture bindings,
  without executing the captured statements. Assert query-specific properties,
  not exact plan text: flag unexpected large-table scans, temporary sorts, or
  automatic indexes. Permit justified scans explicitly; scans are not inherently
  bad. Report SQL, scenario, and plan on failure.
- **Limits:** Capture discovers only exercised paths. Maintain scenarios for
  data-dependent variants, review stale entries, and use the project's SQLite
  version. Exclude the validator's own SQL from capture. These checks detect
  access-path regressions, not runtime performance guarantees.