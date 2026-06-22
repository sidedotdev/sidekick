// Package catfile wraps a long-lived `git cat-file --batch` subprocess
// to read git objects much faster than re-invoking git per object or
// using go-git's packfile reader. Safe for concurrent callers via an
// internal mutex.
package catfile

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

type Reader struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
	mu  sync.Mutex
}

// New starts `git -C repoPath cat-file --batch`. Close to stop.
func New(repoPath string) (*Reader, error) {
	cmd := exec.Command("git", "-C", repoPath, "cat-file", "--batch")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Reader{
		cmd: cmd,
		in:  stdin,
		out: bufio.NewReaderSize(stdout, 1<<16),
	}, nil
}

// Object returns the bytes of the object with the given SHA.
// Returns nil, error if the object is missing.
func (r *Reader) Object(sha string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := fmt.Fprintf(r.in, "%s\n", sha); err != nil {
		return nil, err
	}
	header, err := r.out.ReadString('\n')
	if err != nil {
		return nil, err
	}
	header = strings.TrimRight(header, "\n")
	// header forms:
	//   "<sha> missing"
	//   "<sha> <type> <size>"
	parts := strings.SplitN(header, " ", 3)
	if len(parts) < 2 {
		return nil, fmt.Errorf("bad cat-file header: %q", header)
	}
	if parts[1] == "missing" {
		return nil, fmt.Errorf("object %s missing", sha)
	}
	if len(parts) != 3 {
		return nil, fmt.Errorf("bad cat-file header: %q", header)
	}
	size, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("bad size in header %q: %w", header, err)
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(r.out, buf); err != nil {
		return nil, err
	}
	// Trailing newline after content.
	if _, err := r.out.ReadByte(); err != nil {
		return nil, err
	}
	return buf, nil
}

// Size returns the size of an object without reading its content. Uses
// `cat-file --batch-check` semantics — we run it lazily via a second
// pipe if requested. For now, callers that just need size during a walk
// can fold size+content into one batch by always calling Object.
//
// (Not implemented as a separate pipe yet; callers can request the full
// object and use len(). Kept here as a placeholder for future
// optimization.)
type SizeReader struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
	mu  sync.Mutex
}

// NewSizeReader starts `git -C repoPath cat-file --batch-check` for
// size-only queries (much cheaper — no zlib decompression).
func NewSizeReader(repoPath string) (*SizeReader, error) {
	cmd := exec.Command("git", "-C", repoPath, "cat-file", "--batch-check")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &SizeReader{
		cmd: cmd,
		in:  stdin,
		out: bufio.NewReaderSize(stdout, 1<<16),
	}, nil
}

func (s *SizeReader) Size(sha string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := fmt.Fprintf(s.in, "%s\n", sha); err != nil {
		return 0, err
	}
	header, err := s.out.ReadString('\n')
	if err != nil {
		return 0, err
	}
	header = strings.TrimRight(header, "\n")
	parts := strings.SplitN(header, " ", 3)
	if len(parts) < 2 {
		return 0, fmt.Errorf("bad header: %q", header)
	}
	if parts[1] == "missing" {
		return 0, fmt.Errorf("missing %s", sha)
	}
	return strconv.ParseInt(parts[2], 10, 64)
}

func (s *SizeReader) Close() error {
	s.in.Close()
	return s.cmd.Wait()
}

func (r *Reader) Close() error {
	r.in.Close()
	return r.cmd.Wait()
}
