package env

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	sftpMsgTypeRead   = 5
	sftpMsgTypeStatus = 101
	sftpMsgTypeData   = 103
)

// sftpReadPipelineStats counts serial rounds: READ requests issued while no
// other READ is outstanding. Each such round costs a full network round trip,
// so a size-independent read keeps them constant while one-chunk-at-a-time
// reading grows them with file size.
type sftpReadPipelineStats struct {
	mu           sync.Mutex
	outstanding  map[uint32]struct{}
	serialRounds int
}

func (s *sftpReadPipelineStats) onRequest(typ byte, id uint32) {
	if typ != sftpMsgTypeRead {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.outstanding) == 0 {
		s.serialRounds++
	}
	s.outstanding[id] = struct{}{}
}

func (s *sftpReadPipelineStats) onResponse(typ byte, id uint32) {
	if typ != sftpMsgTypeStatus && typ != sftpMsgTypeData {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.outstanding, id)
}

func (s *sftpReadPipelineStats) serialReadRounds() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.serialRounds
}

// pumpSFTPFrames forwards whole SFTP packets from src to dst, invoking onFrame
// with each packet's type and request id, and sleeping delay per packet to
// emulate network latency (so responses can't win races against pipelined
// request dispatch). dst is closed when src reaches EOF so connection teardown
// propagates through the tap.
func pumpSFTPFrames(dst io.WriteCloser, src io.Reader, delay time.Duration, onFrame func(typ byte, id uint32)) {
	defer dst.Close()
	var lenBuf [4]byte
	for {
		if _, err := io.ReadFull(src, lenBuf[:]); err != nil {
			return
		}
		payload := make([]byte, binary.BigEndian.Uint32(lenBuf[:]))
		if _, err := io.ReadFull(src, payload); err != nil {
			return
		}
		if delay > 0 {
			time.Sleep(delay)
		}
		if len(payload) >= 5 {
			onFrame(payload[0], binary.BigEndian.Uint32(payload[1:5]))
		}
		if _, err := dst.Write(lenBuf[:]); err != nil {
			return
		}
		if _, err := dst.Write(payload); err != nil {
			return
		}
	}
}

// newFrameCountingSFTPClient wires a real sftp.Client to an in-process server
// through taps that observe every request and response packet.
func newFrameCountingSFTPClient(t *testing.T) (*sftp.Client, *sftpReadPipelineStats) {
	t.Helper()
	stats := &sftpReadPipelineStats{outstanding: map[uint32]struct{}{}}

	clientToTap, clientWrites := io.Pipe()
	serverReads, tapToServer := io.Pipe()
	serverToTap, serverWrites := io.Pipe()
	clientReads, tapToClient := io.Pipe()

	go pumpSFTPFrames(tapToServer, clientToTap, 0, stats.onRequest)
	go pumpSFTPFrames(tapToClient, serverToTap, 2*time.Millisecond, stats.onResponse)

	server, err := sftp.NewServer(pipeRWC{serverReads, serverWrites})
	require.NoError(t, err)
	go func() {
		_ = server.Serve()
		_ = serverWrites.Close()
	}()
	t.Cleanup(func() { _ = server.Close() })

	client, err := sftp.NewClientPipe(clientReads, clientWrites)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client, stats
}

// Reading a multi-chunk file must pipeline its chunk requests: over
// high-latency links (e.g. Modal sandboxes) one synchronous round trip per
// chunk makes read cost scale with file size, which made activities reading
// dozens of real source files prohibitively slow.
func TestDoSFTPReadPipelinesMultiChunkReads(t *testing.T) {
	t.Parallel()

	client, stats := newFrameCountingSFTPClient(t)

	dir := t.TempDir()
	content := bytes.Repeat([]byte("0123456789abcdef"), 16*1024) // 256KiB, 8 chunks at the 32KiB default packet size
	path := filepath.Join(dir, "big.bin")
	require.NoError(t, os.WriteFile(path, content, 0644))

	data, err := doSFTPRead(client, path)
	require.NoError(t, err)
	require.True(t, bytes.Equal(content, data), "read content must match the file")

	assert.LessOrEqual(t, stats.serialReadRounds(), 2,
		"reading a file must pipeline chunk requests instead of paying one full round trip per (growing) chunk")
}
