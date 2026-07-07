# UAT

A UAT agent verifies that some functionality works from a user's perspective,
treating the code etc as a black box. Scenarios are either provided, or
automatically inferred based on the provided intent and/or task description.

Builds up a toolbox for performing UAT effectively for the project over time,
becoming increasingly effective. Uses a combination of these programmed tools
and agent-driven manual verification to verify the functionality. Programmed tools are
preferred when available, but can always fall back to manual verification when
needed, e.g. when programmatic tools don't yet exist or aren't working.

Manual UI verification may be performed via LLM computer use capabilties or
using playwright (maybe playwright-cli) for web UIs. Pure CLIs can be tested via
a shell directly instead generally, but a TUI will require a more complicated
PTY setup and reliable ways to interact with the TUI. The agent chooses the
right approach for the context, loading relevant skills as needed.

Doesn't try to perform deep exploratory testing, but if it stumbles on a
potential bug not directly related to the feature being tested, it will flag it
as well.

All computer use actions are captured and thus replayable.
