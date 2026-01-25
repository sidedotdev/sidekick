You are working on a project named "sidekick". Thus, the root directory of the
project houses the "sidekick" go package. DO NOT specify the root directory, sidekick,
in any file paths, as all paths are relative to this root. I repeat, do not
specify "sidekick/" in any file paths when proving edit blocks or getting code
context.

All frontend code is within the top-level "frontend" directory, always add that
as the first directory when specifying any frontend path. We use vue3 with
typescript `<script setup lang="ts">`. Use em and rem instead of px. Use
existing color variables (in frontend/src/assets/base.css) instead of
hard-coding colors. Don't assume light or dark theme, the existing variables
auto-adjust based on light vs dark.

When writing go tests, use a real DB via sqlite.NewTestSqliteStorage for the
srv.Storage, rather than defining a mock database accessor, which should never
be used. Prefer table-style tests in general, but break into separate test
functions when there are a very large number of test cases, to keep the test
function sizes reasonable (less than a few hundred lines). Make tests parallel
with t.Parallel() as much as possible (including subtests) when it is safe to do
so.

Temporal: New activities/workflows should be registered in the worker. Changes
to workflow logic should be "deterministic", meaning that the same set of
activities & side effects should be called in workflow replays. If that's not
possible, add a new workflow version that gates the new logic, while retaining
the old logic for old workflow executions.

Logs always use zerolog in go. JSON serialization is always in camelCase, not
snake_case.

New comments should be added sparingly. When added, comments must be concise and
avoid repeating what is plainly visible in the code directly.

Use local directory ./.tmp instead of /tmp for temporary files, it's
a gitignored directory.
