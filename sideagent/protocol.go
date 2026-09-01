// Package sideagent implements sidekick's remote helper protocol: a single
// long-lived stdio channel (typically one SSH session) over which the client
// sends structured exec requests and receives buffered results. Argv is
// passed verbatim in framed JSON messages instead of being assembled into a
// shell command line, which sidesteps shell quoting entirely (quoting
// correctly across all shell implementations is practically impossible; see
// https://ruuda.nl/2026/deptool). Reusing one channel also makes each command
// cost a single network round trip instead of per-exec SSH session setup.
package sideagent

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// maxFrameSize bounds a single frame's payload, protecting both sides from
// unbounded allocations on a corrupted stream.
const maxFrameSize = 64 << 20

// ExecRequest asks the server to run argv[0] with the remaining argv entries
// as arguments, with no intervening shell.
type ExecRequest struct {
	ID uint64 `json:"id"`
	// Dir is the working directory for the command; empty means the server
	// process's own working directory.
	Dir  string   `json:"dir,omitempty"`
	Argv []string `json:"argv"`
	// Env entries (KEY=VALUE) are appended to the server's environment.
	Env []string `json:"env,omitempty"`
	// Stdin is fed to the command's standard input; the command sees EOF
	// after it, so an empty Stdin behaves like /dev/null.
	Stdin []byte `json:"stdin,omitempty"`
	// LoginEnv runs the command with the environment of a login shell
	// (resolved once per agent process and cached), so toolchains that
	// sandbox profile scripts put on PATH are visible.
	LoginEnv bool `json:"loginEnv,omitempty"`
	// TouchPath, when set, has its mtime refreshed before the command runs;
	// in-sandbox idle watchdogs read it as "last client activity".
	TouchPath string `json:"touchPath,omitempty"`
	// ReadLockFile, when set, is held under a shared flock for the duration
	// of the command, so tree-mutating maintenance (which takes the
	// exclusive lock) never interleaves with running commands.
	ReadLockFile string `json:"readLockFile,omitempty"`
	// HibernationSentinel, when set, aborts the command before it runs
	// (with Hibernated set on the response) if the file exists. The check
	// happens after ReadLockFile is acquired.
	HibernationSentinel string `json:"hibernationSentinel,omitempty"`
}

// ExecResponse carries the buffered result of one ExecRequest.
type ExecResponse struct {
	ID         uint64 `json:"id"`
	Stdout     []byte `json:"stdout,omitempty"`
	Stderr     []byte `json:"stderr,omitempty"`
	ExitStatus int    `json:"exitStatus"`
	// Error is set when the command could not be run at all (e.g. executable
	// not found), as opposed to running and exiting non-zero.
	Error string `json:"error,omitempty"`
	// Hibernated reports the command was not run because HibernationSentinel
	// exists; the client is expected to wake the worktree and retry.
	Hibernated bool `json:"hibernated,omitempty"`
}

// clientMessage is the envelope for client→server frames: exactly one of
// Exec or CancelID is set. A cancel kills the identified command's process
// group; the command's response still arrives afterwards.
type clientMessage struct {
	Exec     *ExecRequest `json:"exec,omitempty"`
	CancelID uint64       `json:"cancelId,omitempty"`
}

// writeFrame writes a 4-byte big-endian length prefix followed by v's JSON.
func writeFrame(w io.Writer, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(payload) > maxFrameSize {
		return fmt.Errorf("frame too large: %d bytes", len(payload))
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

// readFrame reads one length-prefixed JSON frame into v.
func readFrame(r io.Reader, v any) error {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n > maxFrameSize {
		return fmt.Errorf("frame too large: %d bytes", n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return err
	}
	return json.Unmarshal(payload, v)
}
