# bgx

bgx is a terminal session management cli tool similar to screen/tmux, but
designed for async programmatic use. It is inspired by
(zmx)[https://github.com/neurosnap/zmx], generally copying its approach and
sharing with it the following features:

1. separate client and daemon(s)
1. daemon per session
1. allow multiple clients to attach to the same session
1. overlapping [sub-commands](#commands), though with a slightly different flavor
1. uses the same libghostty-vt approach for rendering latest terminal state

But is customized for our needs:

1. Re-implemented in golang so it's usable both as a library and as a cli tool.
1. The run subcommand is always async.
1. A session is only ever used to execute a single command, sessions can not be
   continued after the command exits.
1. An info subcommand provides metadata about a session such as whether it
   exists, start time, output size, duration, custom metadata and exit code (if
   exited).
1. Stores first 1mb and last 9mb (ignoring uncompressed buffer) of scrollback,
   discarding the middle. Both history sizes are configurable. Storage defaults
   to memory but can be configured to use disk, either in a tmp directory or a
   custom path.
1. Uses zstd compression for the unbuffered scrollback history, recompressing
   after buffering every new uncompressed 1mb chunk. Avoids backpressure on the
   commands in the session, which requires a dynamic buffer size. Falls back to
   temporarily not compressing if the buffer size while compressing grows beyond
   10mb, avoiding too much memory bloat in the case of very high throughput
   writes where compression isn't keeping up.
1. All commands default to json output, other than history and attach.
1. The daemon retains the history for the last 10 sessions finished/killed per
   workflow, writing these to a temp directory.
1. Sessions can be tagged with an arbitrary map of metadata when created, which
   is included when sessions are listed, or used as a filter directly against
   top-level metadata keys in the list subcommand.

## Commands

- run <id> [--overwrite-id] [--metadata key=value...] <command...>
   - Run command async in new session. Session ends when command exits or is
     explicitly killed
   - If id already exists on an ended session, fails unless --overwrite-id is
     passed
   - Quotes not needed for command
   - Interactive prompts will *not* hang as long as someone attaches to the
     session to unblock it or sends input to it via the send subcommand
   - To run a script, callers may use something like `bash -c '...'`
- wait <id>
   - Wait for session to finish. Returns exit code.
- kill <id> 
- history <id>
- attach <id>
   - Session must exist and be running
   - Attaches to session, outputs "current" rendered terminal state and
     continues to update it by streaming raw output since that state
   - Writes input to session PTY
   - Detach with ctrl+\
- send <id> <text...>
   - Send raw input to session PTY without attaching
- list|ls
- version
- help

## Testing / Verification

End-to-end tests verify the observable behavior of each of the CLI tool's
subcommands in a black-box manner.

## Implementation

Uses adrg/xdg, urfave/cli/v3 and libghostty-vt (or go bindings to it), but no
other dependencies.