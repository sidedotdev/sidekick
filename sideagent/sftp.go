package sideagent

import (
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/pkg/sftp"
)

// ServeSFTP serves the SFTP protocol over rw until the connection closes. A
// closed stdin/pipe surfaces as io.EOF, which is treated as a clean shutdown
// so a dropped SSH connection reaps this process. Signals also trigger a
// clean shutdown in case EOF is never delivered.
func ServeSFTP(rw io.ReadWriteCloser) error {
	server, err := sftp.NewServer(rw)
	if err != nil {
		return err
	}
	defer server.Close()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGPIPE)
	defer signal.Stop(sigCh)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-sigCh:
			_ = rw.Close()
			_ = server.Close()
		case <-done:
		}
	}()

	if err := server.Serve(); err != nil && err != io.EOF {
		return err
	}
	return nil
}
