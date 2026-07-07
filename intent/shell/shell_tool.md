# Shell Tool

## Overview

We use temporal activities to run shell scripts. LLMs can call a tool named
`run_command` for this, which has a number of problems. A new `shell` tool fixes
many of the problems with the `run_command` tool, and fully replaces it.

## Problems & Solutions

Naively executing any shell script via an activity has a few problems (P) and
some solutions (S), all of which the `shell` tool implements.

P: Different systems might have different default shells and some may not have `bash`
S: The LLM is able to deal with different shells if necessary. We'll tell the
   LLM via its environment prompt what shells are available and let it choose
   among them. If not specified, we default to `bash` when available, then to
   `sh` if not.

P: Output bloats the temporal event history
S: We convert to a persisted content block for the tool result, as we do with
   the read image tool.

P: Output in tool result can be very large, which costs a lot in tokens
S: Tool result is truncated in the middle to ~10k characters, keeping 1k at the
   start and 9k at the end. These portions are the most useful generally.

P: Truncated items might have useful info
S: A session ID

P: Some things run forever, such as starting a dev server
S: The tool arguments include a boolean for `async` (default: false) to support
   this. If the LLM fails to pass `async: true`, see next problem and solution.
   Async output retrieval is powered through a (session)[./bgx.md].

P: It can take too long to run some things so we get repeated timeouts w/ no output
S: Timeout is configurable via tool arguments, up to 10m (not recommended),
   defaulting to 30s. Furthermore, always using a (session)[./bgx.md] means we can
   return a reference to the ongoing session if the timeout is reached, instead
   of an error with empty output. We'll handle the timeout from inside the
   activity instead of waiting for activity timeout.The sync version is just a
   wrapper around async that waits until it finishes or the timeout is reached:
   effectively, we transparently "convert" it to async after the timeout from
   the caller's perspective. When timed out, the initial call will show output
   so far, along with information about how to check further session output.

 P: Activity timeouts can still happen, and mean sessions are lost
 S: We can recover via a derived idempotency key (from workflow id + activity
    id) acting as the session id.

P: Restarting workers mid-execution kills child processes
S: Disconnect the child process from the parent so it keeps running

P: Retrying an activity by just re-running the bash command isn't idempotent
S: Use a persistent (session)[./bgx.md]. Use workflow id + activity id as
   session id, which provides idempotency (assuming the environment is not
   restarted).

P: Restarting workers mid-execution means failure of the activity
S: Use activity retries with idempotency

P: Temporal activity retries can take too long to trigger due to 5m default timeout
S: Use 5s heartbeats (from an empty goroutine) and a heartbeat timeout of 10s.
   We also switch default timeout to 30s now, when not specified.

P: Users want to see the output in realtime while the activity is going
S: Use a (session)[./bgx.md] and attach to it via a websocket. Stream terminal
output directly and use wasm libghostty-vt to render it in the web UI.

P: Users want to look at the current state and continue streaming output
S: Use a session daemon 

## Session Shell Activity

Simple wrapper for temporal without much business logic. Not directly exposed to
tools etc. Meant for use only for prepared commands with known short output, or
via the wrapper activity. Returns the full output text provided by the session.

It invokes sesh via `side sesh` subcommand in local environments, and via a
dedicated remote binary in remote environments which supports both sesh and
sftp, replacing sftp-main.

## Shell Tool Activities

The session id is set to `${workflowId}/${activityId}/#{hash(command)}`. On
retries, checks for an existing session with the same id for best-effort
idempotency. Publishes flow events (parent is flow action id) for session start
and stop.

Return value contains metadata/status and session id only. Activity timeouts are
pre-empted by internal timeout, returning a result containing the same info.

If 10 running sessions already exist for the workflow, the activity fails fast
and provides info about the running sessions and instructions to kill any of
them that aren't needed. Commands performing the session kill command are
NOT run via the session but instead are invoked directly.

Calls session shell activity directly. Persists the output as a tool result
content block, and provide the block ref in the activity output.

## Composing with SSH

Composes like `ssh -c 'sesh run id_123 some-inner-command some-args'` (not literal
syntax). This means the binary must be first copied to the remote host. This is
better than the local host having the session as it can recover from ssh
disconnects gracefully by re-attaching or waiting on the existing session.

This applies to environments that require ssh for each shell script execution.

## Web Frontend

The shell flow action component, when expanded, displays the session output in
real time. It can attach to an existing running session when expanded. Uses wasm
libghostty-vt to render the terminal state and streaming updates. When the web
terminal is focused, sends keystrokes to the session via a websocket to allow
user interaction. Limits scrollback history similarly to the session daemon, but
without compression. Uses line-oriented virtualization for performance. Supports
resizing terminal size.

When flow action is complete, instead of streaming, it renders the final flow
action result.

## TUI

The TUI task progress view can also attach to a running session to display its
output. It uses the same websocket connection as the web frontend. Keystrokes
are sent in the same way. It automatically shows a small terminal view while the
flow action is running, just 3 lines tall. It should be possible for the user to
use arrow keys to select the running flow action and then go to a "fullsize"
mode. This auto-exits when the flow action is complete, or the user can press
ctrl+w to exit the fullsize mode.

## Websocket API

To power realtime viewing of session output, the existing flow event streaming
websocket endpoint and parent subscription events are used.

However, we create "virtual" flow events on the fly that are never persisted nor
use our usual streaming infrastructure. Instead, we directly invoke
`sesh attach` with the right session id, in the right environment, via a pty. We
translate the attach output to 2 new event types: 

1. `term_state`
2. `tty`

The state contains the cursor position and screen state we get from `attach` via
libghostty-vt (which the session daemon uses to iteratively create this, like
zmx does). This event is only sent once when attach starts. The tty events
contain raw tty bytes written after that screen state. This bytestream-to-event
pipeline is unbuffered for smooth realtime streaming. These "virtual" flow
events are published directly to the websocket connection, skipping indirection
via a Streamer implementation since we don't need it in this case.

Keystrokes are sent in a `key` event type, provided both the session id and the
keystrokes. These are passed through in realtime to the running `attach`
subcommand.