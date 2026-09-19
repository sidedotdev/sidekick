package sideagent

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"
)

// A sealed sandbox has committed to shutting down: its filesystem is about to
// be snapshotted and the sandbox terminated. Work acknowledged after that
// snapshot would be reported to the host as durable and then discarded with
// the sandbox, so a sealed agent accepts none.
//
// Admission and sealing are mutually exclusive through one flock. Work holds
// it shared from the moment a request arrives until that work is finished;
// the shutdown holds it exclusively. Taking it exclusively is therefore the
// drain, since it cannot be acquired while work is outstanding, and the seal
// is only ever recorded under it while admission only ever reads it under the
// shared lock. Work is thus never admitted into a sealed sandbox, and never
// interrupted by one.
//
// Crucially the lock is taken only once a request has arrived, never around
// the wait for one: pooled connections idle for up to an hour, and a
// shutdown that waited on them would never happen.
const (
	SealPath     = "/tmp/.sidekick-sealed"
	SealLockPath = "/tmp/.sidekick-seal.lock"
)

// SealedMessage is what a sealed agent reports before ending a session. Hosts
// match it to tell "this sandbox is going away; wait for it and retry against
// its restored incarnation" apart from an ordinary transport failure, which
// is retried against the same incarnation.
const SealedMessage = "side-agent: sandbox is sealed for shutdown"

// Seal is the admission gate between in-flight work and shutdown.
type Seal struct {
	lockPath string
	flagPath string
}

func NewSeal(lockPath, flagPath string) *Seal {
	return &Seal{lockPath: lockPath, flagPath: flagPath}
}

// DefaultSeal is the gate the agent and the in-sandbox watchdog share.
func DefaultSeal() *Seal { return NewSeal(SealLockPath, SealPath) }

// Sealed reports whether the sandbox has committed to shutting down. Callers
// admitting work must read it through admit instead, so the answer cannot go
// stale between the check and the work.
func (s *Seal) Sealed() bool {
	_, err := os.Stat(s.flagPath)
	if err == nil {
		return true
	}
	// Anything other than a definite "absent" counts as sealed: refusing
	// work costs a retry, admitting it into a dying sandbox loses it.
	return !os.IsNotExist(err)
}

// admit takes the shared lock and reports whether work may proceed. The
// returned release must be called once the admitted work is finished.
func (s *Seal) admit() (release func(), sealed bool, err error) {
	f, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		f.Close()
		return nil, false, err
	}
	// Ownership is explicit: whoever admits work releases it when that work
	// finishes, and finish tears down whatever a session leaves behind. The
	// holder stays reachable for as long as the work runs.
	release = sync.OnceFunc(func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	})
	if s.Sealed() {
		release()
		return nil, true, nil
	}
	return release, false, nil
}

// TrySeal waits up to timeout for outstanding work to finish and then records
// the seal, reporting whether it succeeded. Failing to seal means work is
// still in flight: the caller must abandon the shutdown rather than proceed,
// since snapshotting or terminating now would discard that work.
func (s *Seal) TrySeal(timeout time.Duration) (bool, error) {
	f, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		return false, err
	}
	defer f.Close()
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return false, err
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if err := os.WriteFile(s.flagPath, nil, 0o666); err != nil {
		return false, err
	}
	return true, nil
}

// Lift reopens the sandbox to work. It is only sound before a termination has
// been requested: once one is dispatched it may still arrive, so work
// accepted afterwards could be destroyed by it.
func (s *Seal) Lift() error {
	if err := os.Remove(s.flagPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SFTP packet types the guard interprets. A file write spans OPEN, WRITE and
// CLOSE, so admission has to cover the open handle rather than each packet,
// or a seal between them would leave the file truncated and half written.
const (
	sftpTypeInit    = 1
	sftpTypeVersion = 2
	sftpTypeOpen    = 3
	sftpTypeClose   = 4
	sftpTypeOpendir = 11
	sftpTypeStatus  = 101
	sftpTypeHandle  = 102
)

// statusOK is SSH_FX_OK; only a successful close releases a handle.
const statusOK = 0

// maxSFTPPacket mirrors the protocol's maximum message length, bounding what
// a peer can make the guard allocate.
const maxSFTPPacket = 256 << 10

// responsePrefixMax is how much of a response is retained for parsing: the
// header, type, request id and a handle string all fall well inside it.
const responsePrefixMax = 64

// GuardSFTPTransport wraps an SFTP transport so work is admitted before the
// server sees it and admission is held until that work is finished.
func GuardSFTPTransport(rw io.ReadWriteCloser, seal *Seal) io.ReadWriteCloser {
	return newSFTPGuard(rw, seal)
}

func newSFTPGuard(rw io.ReadWriteCloser, seal *Seal) *sftpGuard {
	return &sftpGuard{
		src:         rw,
		dst:         rw,
		seal:        seal,
		pending:     map[uint32]pendingRequest{},
		openHandles: map[string]struct{}{},
	}
}

// pendingRequest is a request the server has been given but not yet answered.
type pendingRequest struct {
	kind byte
	// handle is the file a close request names, so its success releases that
	// file rather than whichever one happens to be open.
	handle string
}

type sftpGuard struct {
	src  io.Reader
	dst  io.WriteCloser
	seal *Seal

	undelivered []byte
	ended       bool

	mu              sync.Mutex
	release         func()
	pending         map[uint32]pendingRequest
	awaitingVersion bool
	openHandles     map[string]struct{}

	response responseScanner
}

func (g *sftpGuard) Read(p []byte) (int, error) {
	if len(g.undelivered) == 0 {
		if g.ended {
			return 0, io.EOF
		}
		// Blocks with no admission held, so idling here cannot delay a
		// shutdown, and the seal is consulted only once a request exists.
		packet, err := readFramedPacket(g.src)
		if err != nil {
			return 0, err
		}
		if !g.admit(packet) {
			g.ended = true
			return 0, io.EOF
		}
		g.undelivered = packet
	}
	n := copy(p, g.undelivered)
	g.undelivered = g.undelivered[n:]
	return n, nil
}

func (g *sftpGuard) Write(p []byte) (int, error) {
	for _, finished := range g.response.scan(p) {
		g.complete(finished)
	}
	return g.dst.Write(p)
}

func (g *sftpGuard) Close() error {
	g.mu.Lock()
	// Only what is provably finished is released here: sftp.Server.Close can
	// run while handlers are still active. Anything still outstanding stays
	// admitted until it completes, or until the process exits and the kernel
	// drops the lock with its file descriptors.
	g.releaseIfQuiescentLocked()
	g.mu.Unlock()
	return g.dst.Close()
}

// admit reports whether the request may reach the server, taking the shared
// lock for the first outstanding request and holding it until the last one
// finishes.
func (g *sftpGuard) admit(packet []byte) bool {
	view := viewPacket(packet)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.release == nil {
		release, sealed, err := g.seal.admit()
		if err != nil || sealed {
			return false
		}
		g.release = release
	}
	if view.kind == sftpTypeInit {
		g.awaitingVersion = true
		return true
	}
	if _, duplicate := g.pending[view.id]; duplicate {
		// Overwriting would lose one of the two, and the response to the
		// survivor would release admission while the other still runs.
		return false
	}
	request := pendingRequest{kind: view.kind}
	if view.kind == sftpTypeClose {
		request.handle, _ = sftpString(view.rest)
	}
	g.pending[view.id] = request
	return true
}

// complete accounts for one response, matched to its request by the id both
// carry, so the accounting does not depend on the server answering in order.
func (g *sftpGuard) complete(response []byte) {
	view := viewPacket(response)
	g.mu.Lock()
	defer g.mu.Unlock()
	if view.kind == sftpTypeVersion {
		g.awaitingVersion = false
		g.releaseIfQuiescentLocked()
		return
	}
	request, known := g.pending[view.id]
	delete(g.pending, view.id)
	switch {
	case known && (request.kind == sftpTypeOpen || request.kind == sftpTypeOpendir):
		if view.kind == sftpTypeHandle {
			handle, ok := sftpString(view.rest)
			if !ok {
				// An unreadable handle can never be matched to its close, so
				// it holds admission until the session ends rather than
				// releasing it over a file that is still open.
				handle = fmt.Sprintf("\x00unreadable-%d", view.id)
			}
			g.openHandles[handle] = struct{}{}
		}
	case known && request.kind == sftpTypeClose:
		// A failed close leaves the file open, and one naming a handle that
		// is not open releases nothing.
		if view.kind == sftpTypeStatus && statusCode(view.rest) == statusOK {
			delete(g.openHandles, request.handle)
		}
	}
	g.releaseIfQuiescentLocked()
}

// finish releases admission once the session is over. sftp.Server.Serve
// returns only after its workers have drained and its still-open files have
// been closed, so nothing admitted can still be running, and a connection
// dropped mid-operation cannot strand the sandbox unable to shut down.
func (g *sftpGuard) finish() {
	g.mu.Lock()
	defer g.mu.Unlock()
	clear(g.pending)
	clear(g.openHandles)
	g.awaitingVersion = false
	if g.release != nil {
		g.release()
		g.release = nil
	}
}

func (g *sftpGuard) releaseIfQuiescentLocked() {
	if g.release == nil || g.awaitingVersion || len(g.pending) > 0 || len(g.openHandles) > 0 {
		return
	}
	g.release()
	g.release = nil
}

// responseScanner tracks packet boundaries in the server's output stream
// without holding any of it back, retaining enough of each response to match
// it to its request.
type responseScanner struct {
	header    [4]byte
	headerPos int
	remaining int64
	prefix    []byte
}

func (s *responseScanner) scan(p []byte) [][]byte {
	var completed [][]byte
	for len(p) > 0 {
		if s.headerPos < len(s.header) {
			n := copy(s.header[s.headerPos:], p)
			s.headerPos += n
			p = p[n:]
			if s.headerPos == len(s.header) {
				s.remaining = int64(binary.BigEndian.Uint32(s.header[:]))
				s.prefix = append(s.prefix[:0], s.header[:]...)
			}
			continue
		}
		if s.remaining == 0 {
			completed = append(completed, s.take())
			continue
		}
		n := int64(len(p))
		if n > s.remaining {
			n = s.remaining
		}
		if room := responsePrefixMax - len(s.prefix); room > 0 {
			take := int(n)
			if take > room {
				take = room
			}
			s.prefix = append(s.prefix, p[:take]...)
		}
		s.remaining -= n
		p = p[n:]
		// Completed here rather than on the next call, which may never come:
		// a response is usually the last thing in its write.
		if s.remaining == 0 {
			completed = append(completed, s.take())
		}
	}
	return completed
}

// take yields the finished packet's retained prefix and readies the scanner
// for the next one.
func (s *responseScanner) take() []byte {
	finished := append([]byte(nil), s.prefix...)
	s.headerPos = 0
	s.prefix = s.prefix[:0]
	return finished
}

func readFramedPacket(src io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(src, header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length > maxSFTPPacket {
		return nil, fmt.Errorf("sftp packet of %d bytes exceeds the %d byte maximum", length, maxSFTPPacket)
	}
	packet := make([]byte, len(header)+int(length))
	copy(packet, header[:])
	if _, err := io.ReadFull(src, packet[len(header):]); err != nil {
		return nil, err
	}
	return packet, nil
}

// packetView is the part of a framed packet the guard reasons about.
type packetView struct {
	kind byte
	id   uint32
	rest []byte
}

func viewPacket(packet []byte) packetView {
	if len(packet) < 5 {
		return packetView{}
	}
	body := packet[4:]
	view := packetView{kind: body[0]}
	if len(body) >= 5 {
		view.id = binary.BigEndian.Uint32(body[1:5])
		view.rest = body[5:]
	}
	return view
}

// sftpString decodes a length-prefixed protocol string.
func sftpString(b []byte) (string, bool) {
	if len(b) < 4 {
		return "", false
	}
	length := binary.BigEndian.Uint32(b)
	if uint32(len(b)-4) < length {
		return "", false
	}
	return string(b[4 : 4+length]), true
}

func statusCode(b []byte) uint32 {
	if len(b) < 4 {
		return ^uint32(0)
	}
	return binary.BigEndian.Uint32(b)
}
