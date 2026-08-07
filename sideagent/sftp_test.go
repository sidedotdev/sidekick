package sideagent

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type nopReadWriteCloser struct {
	*bytes.Reader
}

func (nopReadWriteCloser) Write(p []byte) (int, error) { return len(p), nil }

func (nopReadWriteCloser) Close() error { return nil }

// TestServeSFTPExitsOnEOF ensures the SFTP mode exits promptly (without
// error) when its input is already at EOF, so a dropped SSH connection reaps
// the remote leaf.
func TestServeSFTPExitsOnEOF(t *testing.T) {
	t.Parallel()

	done := make(chan error, 1)
	go func() {
		done <- ServeSFTP(nopReadWriteCloser{bytes.NewReader(nil)})
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("ServeSFTP did not exit on EOF")
	}
}
