package sideagent

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

func testSeal(t *testing.T) *Seal {
	t.Helper()
	dir := t.TempDir()
	return NewSeal(filepath.Join(dir, "lock"), filepath.Join(dir, "sealed"))
}

// mustSeal asserts the shutdown can take the sandbox, i.e. no work is
// outstanding.
func mustSeal(t *testing.T, seal *Seal, because string) {
	t.Helper()
	sealed, err := seal.TrySeal(5 * time.Second)
	if err != nil {
		t.Fatalf("TrySeal: %v", err)
	}
	if !sealed {
		t.Fatalf("shutdown was blocked but %s", because)
	}
}

// mustNotSeal asserts the shutdown is held off because work is outstanding.
// A shutdown that went ahead here would snapshot without that work and then
// terminate, discarding it.
func mustNotSeal(t *testing.T, seal *Seal, because string) {
	t.Helper()
	sealed, err := seal.TrySeal(200 * time.Millisecond)
	if err != nil {
		t.Fatalf("TrySeal: %v", err)
	}
	if sealed {
		t.Fatalf("shutdown proceeded while %s", because)
	}
}

type pipeConn struct {
	io.Reader
	io.WriteCloser
}

// guardedSFTP runs a real SFTP server behind the guard and returns a real
// client speaking to it, so the barriers exercise whole filesystem
// operations rather than hand-built packets.
func guardedSFTP(t *testing.T, seal *Seal) *sftp.Client {
	t.Helper()
	toServer, fromClient := io.Pipe()
	toClient, fromServer := io.Pipe()

	guard := newSFTPGuard(&pipeConn{Reader: toServer, WriteCloser: fromServer}, seal)
	server, err := sftp.NewServer(guard)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = server.Serve()
		// mirrors ServeSFTP: the session is over and its handlers have
		// drained, so admission is released and the peer is unblocked
		guard.finish()
		_ = server.Close()
		_ = fromServer.Close()
	}()

	client, err := sftp.NewClientPipe(toClient, fromClient)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() {
		_ = fromClient.Close()
		select {
		case <-served:
		case <-time.After(10 * time.Second):
			t.Error("sftp server did not stop after the client disconnected")
		}
		// Both ends are closed before the blocking client shutdown, so a
		// stuck session fails the test rather than hanging it.
		_ = toClient.Close()
		_ = toServer.Close()
		_ = client.Close()
	})
	return client
}

// TestIdleSessionDoesNotBlockShutdown: pooled connections idle for up to an
// hour, so a shutdown that waited on a connected-but-quiet session would
// never happen and the sandbox would bill forever.
func TestIdleSessionDoesNotBlockShutdown(t *testing.T) {
	t.Parallel()
	seal := testSeal(t)
	guardedSFTP(t, seal)
	mustSeal(t, seal, "the session was merely connected and idle")
}

// TestWriteInProgressBlocksShutdown drives a real multi-chunk write and
// proves the file is neither truncated nor half written by a shutdown.
func TestWriteInProgressBlocksShutdown(t *testing.T) {
	t.Parallel()
	seal := testSeal(t)
	client := guardedSFTP(t, seal)

	target := filepath.Join(t.TempDir(), "payload")
	// larger than the 32KiB maximum packet, so the body spans several
	// WRITE packets with boundaries a seal could fall between
	payload := bytes.Repeat([]byte("sidekick"), 32<<10)

	f, err := client.Create(target)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	mustNotSeal(t, seal, "a file was open for writing")

	if _, err := f.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	mustNotSeal(t, seal, "a written file had not been closed")

	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	mustSeal(t, seal, "the write had completed")

	written, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(written, payload) {
		t.Fatalf("file was left incomplete: got %d bytes, want %d", len(written), len(payload))
	}
}

// TestSealedSessionRefusesFurtherWork: work arriving after the sandbox has
// committed to shutting down must fail rather than be acknowledged and then
// discarded with the sandbox.
func TestSealedSessionRefusesFurtherWork(t *testing.T) {
	t.Parallel()
	seal := testSeal(t)
	client := guardedSFTP(t, seal)
	dir := t.TempDir()

	mustSeal(t, seal, "the session was idle")

	if err := client.Mkdir(filepath.Join(dir, "after-seal")); err == nil {
		t.Fatal("a sealed sandbox accepted work")
	}
	if _, err := os.Stat(filepath.Join(dir, "after-seal")); !os.IsNotExist(err) {
		t.Fatalf("a refused request still changed the filesystem: %v", err)
	}
}

// TestRequestArrivingAfterSealIsRefused covers the ordering that matters
// most: the session is already blocked waiting for a request when the seal
// lands, so a check made before that wait would wrongly admit it.
func TestRequestArrivingAfterSealIsRefused(t *testing.T) {
	t.Parallel()
	seal := testSeal(t)
	client := guardedSFTP(t, seal)
	dir := t.TempDir()

	// the client is connected and quiet, so the server sits blocked in its
	// read exactly as a pooled connection does
	mustSeal(t, seal, "the session was idle")

	done := make(chan error, 1)
	go func() { done <- client.Mkdir(filepath.Join(dir, "late")) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a request arriving after the seal was served")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a sealed session must end rather than hang")
	}
}

// TestConcurrentWorkReleasesAdmissionExactly pipelines overlapping
// operations, since the guard must account for responses that can complete
// in any order without releasing admission early or leaking it.
func TestConcurrentWorkReleasesAdmissionExactly(t *testing.T) {
	t.Parallel()
	seal := testSeal(t)
	client := guardedSFTP(t, seal)
	dir := t.TempDir()

	const workers = 16
	payload := []byte("payload")
	var wg sync.WaitGroup
	failures := make(chan error, workers)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := filepath.Join(dir, "concurrent-"+strconv.Itoa(i))
			f, err := client.Create(name)
			if err != nil {
				failures <- fmt.Errorf("create %s: %w", name, err)
				return
			}
			if _, err := f.Write(payload); err != nil {
				failures <- fmt.Errorf("write %s: %w", name, err)
				return
			}
			if err := f.Close(); err != nil {
				failures <- fmt.Errorf("close %s: %w", name, err)
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}

	mustSeal(t, seal, "all concurrent work had finished")

	for i := range workers {
		name := filepath.Join(dir, "concurrent-"+strconv.Itoa(i))
		written, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if !bytes.Equal(written, payload) {
			t.Fatalf("%s holds %q, want %q", name, written, payload)
		}
	}
}

// TestTransportFailureDoesNotStrandAdmission: a dropped connection with work
// outstanding must not leave the sandbox permanently unable to shut down.
func TestTransportFailureDoesNotStrandAdmission(t *testing.T) {
	t.Parallel()
	seal := testSeal(t)
	client := guardedSFTP(t, seal)

	f, err := client.Create(filepath.Join(t.TempDir(), "interrupted"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	mustNotSeal(t, seal, "a file was open")

	// the host vanishes mid-operation, without closing the handle
	if err := client.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}
	_ = f

	// The session's process exits on a dropped connection, and the kernel
	// drops its locks with it; within one process the guard must reach the
	// same state rather than hold admission forever.
	sealed, err := seal.TrySeal(2 * time.Second)
	if err != nil {
		t.Fatalf("TrySeal: %v", err)
	}
	if !sealed {
		t.Fatal("a dropped connection stranded admission, so the sandbox could never shut down")
	}
}

// TestCloseOfUnknownHandleReleasesNothing exercises the accounting directly,
// because a well-behaved client will not send a close twice. Well-formed
// packets are used so only the guard's bookkeeping is under test.
func TestCloseOfUnknownHandleReleasesNothing(t *testing.T) {
	t.Parallel()
	seal := testSeal(t)
	guard := newSFTPGuard(&pipeConn{Reader: bytes.NewReader(nil), WriteCloser: nopWriteCloser{io.Discard}}, seal)
	// the session outlives the assertions, exactly as a served one does
	defer guard.finish()

	if !guard.admit(request(sftpTypeOpen, 1)) {
		t.Fatal("open was refused")
	}
	guard.complete(handleResponse(1, "real"))
	mustNotSeal(t, seal, "a file was open")

	if !guard.admit(closeRequest(2, "bogus")) {
		t.Fatal("close was refused")
	}
	guard.complete(statusResponse(2, statusOK))
	mustNotSeal(t, seal, "closing an unopened handle released a different file")

	if !guard.admit(closeRequest(3, "real")) {
		t.Fatal("close was refused")
	}
	guard.complete(statusResponse(3, statusOK))
	mustSeal(t, seal, "the open file was closed")
}

// TestDuplicateRequestIDIsRefused: two requests sharing an id cannot both be
// tracked, and the first response would then release admission while the
// other was still running.
func TestDuplicateRequestIDIsRefused(t *testing.T) {
	t.Parallel()
	seal := testSeal(t)
	guard := newSFTPGuard(&pipeConn{Reader: bytes.NewReader(nil), WriteCloser: nopWriteCloser{io.Discard}}, seal)
	defer guard.finish()

	if !guard.admit(request(sftpTypeOpen, 7)) {
		t.Fatal("first request was refused")
	}
	if guard.admit(request(sftpTypeOpen, 7)) {
		t.Fatal("a duplicate request id was admitted")
	}
}

// TestFailedCloseKeepsFileOpen: a close that errors leaves the file open, so
// releasing admission on it would let a shutdown run over live work.
func TestFailedCloseKeepsFileOpen(t *testing.T) {
	t.Parallel()
	seal := testSeal(t)
	guard := newSFTPGuard(&pipeConn{Reader: bytes.NewReader(nil), WriteCloser: nopWriteCloser{io.Discard}}, seal)
	defer guard.finish()

	if !guard.admit(request(sftpTypeOpen, 1)) {
		t.Fatal("open was refused")
	}
	guard.complete(handleResponse(1, "h"))
	if !guard.admit(closeRequest(2, "h")) {
		t.Fatal("close was refused")
	}
	guard.complete(statusResponse(2, 4 /* SSH_FX_FAILURE */))

	mustNotSeal(t, seal, "a failed close left the file open")
}

// TestAdmissionSurvivesGCWhileSessionLive: a file can stay open far longer
// than the collector's view of any one object, so a live session's admission
// must not depend on collection timing.
func TestAdmissionSurvivesGCWhileSessionLive(t *testing.T) {
	t.Parallel()
	seal := testSeal(t)
	client := guardedSFTP(t, seal)

	f, err := client.Create(filepath.Join(t.TempDir(), "held"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()

	for range 3 {
		runtime.GC()
	}
	mustNotSeal(t, seal, "a file was open across a garbage collection")
}

// TestSealWaitsForAnotherProcess proves admission holds across processes,
// which is the case that matters: the watchdog seals from outside the agent.
func TestSealWaitsForAnotherProcess(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_TEST_SEAL_LOCK") != "" {
		t.Skip("running as the lock-holding helper")
	}
	seal := testSeal(t)

	helper := exec.Command(os.Args[0], "-test.run=TestSealHelperHoldsSharedLock", "-test.v")
	helper.Env = append(os.Environ(), "SIDE_TEST_SEAL_LOCK="+seal.lockPath)
	holding, err := helper.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout: %v", err)
	}
	if err := helper.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() { _ = helper.Process.Kill(); _ = helper.Wait() })

	// wait for the helper to report it holds the lock, rather than sleeping
	if err := awaitMarker(holding, "holding"); err != nil {
		t.Fatalf("helper never took the lock: %v", err)
	}
	mustNotSeal(t, seal, "another process held admission")
}

func TestSealHelperHoldsSharedLock(t *testing.T) {
	path := os.Getenv("SIDE_TEST_SEAL_LOCK")
	if path == "" {
		t.Skip("only runs as a helper process")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		t.Fatalf("open lock: %v", err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		t.Fatalf("flock: %v", err)
	}
	os.Stdout.WriteString("holding\n")
	time.Sleep(10 * time.Second)
}

func awaitMarker(r io.Reader, marker string) error {
	buf := make([]byte, 0, 512)
	chunk := make([]byte, 128)
	for {
		n, err := r.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if bytes.Contains(buf, []byte(marker)) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// TestExecSealedRefusesWithoutRunning: a refusal reported in the protocol is
// a promise the command did not run, which is what makes retrying it against
// the restored sandbox safe.
func TestExecSealedRefusesWithoutRunning(t *testing.T) {
	t.Parallel()
	seal := testSeal(t)
	client := startTestChannelWithSeal(t, seal)
	witness := filepath.Join(t.TempDir(), "ran")

	mustSeal(t, seal, "no command was running")

	resp, err := client.Exec(context.Background(), ExecRequest{Argv: []string{"touch", witness}})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !resp.Sealed {
		t.Fatalf("a sealed sandbox ran the command, exit %d", resp.ExitStatus)
	}
	if _, err := os.Stat(witness); !os.IsNotExist(err) {
		t.Fatalf("a command reported as refused had in fact run: %v", err)
	}
}

// TestExecRunningCommandBlocksShutdown: a command holds admission for its
// whole run, so a shutdown cannot snapshot the filesystem halfway through it.
func TestExecRunningCommandBlocksShutdown(t *testing.T) {
	t.Parallel()
	seal := testSeal(t)
	client := startTestChannelWithSeal(t, seal)
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	release := filepath.Join(dir, "release")

	done := make(chan ExecResponse, 1)
	go func() {
		resp, err := client.Exec(context.Background(), ExecRequest{Argv: []string{"sh", "-c",
			fmt.Sprintf("touch '%s'; while [ ! -e '%s' ]; do sleep 0.05; done", started, release)}})
		if err != nil {
			t.Errorf("exec: %v", err)
		}
		done <- resp
	}()

	awaitFile(t, started, "command never started")
	mustNotSeal(t, seal, "a command was still running")

	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatalf("release: %v", err)
	}
	select {
	case resp := <-done:
		if resp.ExitStatus != 0 {
			t.Fatalf("command exited %d: %s", resp.ExitStatus, resp.Stderr)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("command never finished")
	}
	mustSeal(t, seal, "the command had finished")
}

func awaitFile(t *testing.T, path, message string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(message)
}

// TestResponseScannerReportsEachPacketOnce covers the chunking a transport
// can impose: a response split across writes, several coalesced into one, and
// writes ending exactly on a packet boundary. Missing the last completion
// leaves work accounted as outstanding forever.
func TestResponseScannerReportsEachPacketOnce(t *testing.T) {
	t.Parallel()
	first := statusResponse(1, statusOK)
	second := handleResponse(2, "h")
	stream := append(append([]byte{}, first...), second...)

	for name, chunks := range map[string][][]byte{
		"whole stream":      {stream},
		"exact boundaries":  {first, second},
		"split mid header":  {stream[:2], stream[2:]},
		"split mid payload": {stream[:len(first)-2], stream[len(first)-2:]},
		"byte at a time":    splitBytes(stream),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var scanner responseScanner
			var got [][]byte
			for _, chunk := range chunks {
				got = append(got, scanner.scan(chunk)...)
			}
			if len(got) != 2 {
				t.Fatalf("reported %d responses, want 2", len(got))
			}
			if view := viewPacket(got[0]); view.kind != sftpTypeStatus || view.id != 1 {
				t.Errorf("first response = kind %d id %d, want status id 1", view.kind, view.id)
			}
			if view := viewPacket(got[1]); view.kind != sftpTypeHandle || view.id != 2 {
				t.Errorf("second response = kind %d id %d, want handle id 2", view.kind, view.id)
			}
		})
	}
}

func splitBytes(b []byte) [][]byte {
	chunks := make([][]byte, 0, len(b))
	for i := range b {
		chunks = append(chunks, b[i:i+1])
	}
	return chunks
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func framed(body []byte) []byte {
	out := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(out, uint32(len(body)))
	copy(out[4:], body)
	return out
}

func request(kind byte, id uint32) []byte {
	body := make([]byte, 5)
	body[0] = kind
	binary.BigEndian.PutUint32(body[1:], id)
	return framed(body)
}

func closeRequest(id uint32, handle string) []byte {
	body := make([]byte, 5, 9+len(handle))
	body[0] = sftpTypeClose
	binary.BigEndian.PutUint32(body[1:], id)
	body = binary.BigEndian.AppendUint32(body, uint32(len(handle)))
	return framed(append(body, handle...))
}

func handleResponse(id uint32, handle string) []byte {
	body := make([]byte, 5, 9+len(handle))
	body[0] = sftpTypeHandle
	binary.BigEndian.PutUint32(body[1:], id)
	body = binary.BigEndian.AppendUint32(body, uint32(len(handle)))
	return framed(append(body, handle...))
}

func statusResponse(id uint32, code uint32) []byte {
	body := make([]byte, 5)
	body[0] = sftpTypeStatus
	binary.BigEndian.PutUint32(body[1:], id)
	return framed(binary.BigEndian.AppendUint32(body, code))
}

var _ atomic.Bool
