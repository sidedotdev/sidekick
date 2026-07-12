package main

import (
	"bytes"
	"testing"
	"time"
)

type nopReadWriteCloser struct {
	*bytes.Reader
}

func (nopReadWriteCloser) Write(p []byte) (int, error) { return len(p), nil }

func (nopReadWriteCloser) Close() error { return nil }

// TestRunExitsOnEOF ensures the server exits promptly (without error) when its
// input is already at EOF, so a dropped SSH connection reaps the remote leaf.
func TestRunExitsOnEOF(t *testing.T) {
	t.Parallel()

	rw := nopReadWriteCloser{bytes.NewReader(nil)}

	done := make(chan error, 1)
	go func() {
		done <- run(rw)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return promptly on EOF")
	}
}
