You are working on a project named "sidekick". Thus, the root directory of the
project houses the "sidekick" go package.

All frontend code is within the top-level "frontend" directory, always add that
as the first directory when specifying any frontend path. We use vue3 with
typescript `<script setup lang="ts">`. Use em and rem instead of px. Use
existing color variables (in frontend/src/assets/base.css) instead of
hard-coding colors. Don't assume light or dark theme, the existing variables
auto-adjust based on light vs dark. We use bun and bunx instead of npm, eg
`bun run test:unit` and `bun run type-check`.

Write unit tests for new behavior that closely matches intent and asserts actual
behavior rather than implementation specifics. When writing go tests, use a real
DB via sqlite.NewTestSqliteStorage for the srv.Storage, rather than defining a
mock database accessor, which should never be used. Prefer table-style tests in
general, but break into separate test functions when there are a very large
number of test cases, to keep the test function sizes reasonable (less than a
few hundred lines). Make tests parallel with t.Parallel() as much as possible
(including subtests) when it is safe to do so.

You can use `go test`, or our wrapper, `gotestreport`, taking the same
arguments. The latter is usually better as it will omit verbose output from
passing tests when there are partial package failures, and show you skipped
tests.

Temporal: New activities/workflows should be registered in the worker. Changes
to workflow logic should be "deterministic", meaning that the same set of
activities & side effects should be called in workflow replays. If that's not
possible, add a new workflow version that gates the new logic, while retaining
the old logic for old workflow executions. Activities should almost always take
structs for input instead of multiple arguments. Output should also be dedicated
structs, even if wrapping a single type.

To debug workflow replays, you must use `fmt.Println` since workflow loggers are
suppressed during replay.

Logs always use zerolog in go. JSON serialization is always in camelCase, not
snake_case.

New comments should be added sparingly. When added, comments must be concise and
avoid repeating what is plainly visible in the code directly.

You may freely `go run ./scripts/*` to run any scripts in the `scripts`
directory to aid in debugging or verifying changes. You must always verify
changes when possible.

Do NOT use the `temporal` command, instead favor our ./scripts/* or write
new ones as needed. Debugging real running temporal workflows/activites can be
very complex, especially determism issues. For workflow bugs, prefer looking at
event history via the dump workflow event script, and insert pervasive and
extensive fmt.Printf calls, then run the workflow repeatedly to rapidly binary
search the line where things go wrong and view the state of local variables.
This helps narrow down a root cause alongside inspecting the event history.

## Complex Debugging

When debugging complex issues, make sure you have an automated minimal
reproduction of the issue as a matter of course, ideally as a checked-in
regression test if that is appropriate/possible. Then, to root cause, use or
create custom, generalized diagnostic tooling to test hypotheses. These
diagnostics should be designed to required very few manual commands, and should
also support summarizing and filtering the diagnostic data as needed via
parameters, as opposed to manually adding `| grep something | tail -1` etc,
which is much less reliable.

Double-check that your diagnostics work as
expected and don't produce false results, failing fast with loud errors
especially when summarizing/filtering/processing data. You should improve on
this custom diagnostic tooling in order to reduce effort to root cause,
retaining improvements for future debugging sessions to leverage.
Diagnostics are much better than guesses.

When evidence supports a hypothesis but isn't absolutely definitive, you must
validate further before acting on it. Confirm the phenomenon is real and
reproducible, and verify each result (positive, negative, or null) before
reasoning from it.

1. List the hypotheses consistent with the current evidence, including
   competing explanations.
2. Attempt to falsify your best hypotheses, stating up front what each outcome
   would prove. Run cheap partial checks first; they can end a line of inquiry
   early, but a confirmed hypothesis requires complete falsification, with
   negative and positive controls where feasible, free of confounds.
3. Repeat, updating the hypothesis set as results arrive, until the surviving
   conclusion would hold up in court.
4. Probe for systematic or methodological errors you haven't yet considered,
   converting unknown unknowns into known ones you can name. Then report the
   conclusion as probably true, claiming no more than the evidence shows, since
   an unrecognized error may still remain.

If a result seems off or impossible, suspect a systematic error and investigate
before proceeding.

Confirming tests leave alternatives alive; surviving complete falsification is
what makes evidence definitive.
