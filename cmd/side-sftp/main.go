// side-sftp is a standalone SFTP server that serves the local filesystem
// over stdin/stdout. It is designed to be cross-compiled and uploaded to
// remote environments (devpod, openshell) where the full sidekick binary
// is not available.
//
// Usage: side-sftp
//
// The binary reads SFTP protocol messages from stdin and writes responses
// to stdout, allowing an SSH-tunneled sftp.Client to read remote files.
package main

import (
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/pkg/sftp"
)

type stdioReadWriteCloser struct {
	io.Reader
	io.WriteCloser
}

// run serves SFTP over rw until the connection closes. A closed stdin/pipe
// surfaces as io.EOF, which is treated as a clean shutdown so a dropped SSH
// connection reaps this process. Signals also trigger a clean shutdown in case
// EOF is never delivered.
func run(rw io.ReadWriteCloser) error {
	server, err := sftp.NewServer(rw)
	if err != nil {
		return err
	}
	defer server.Close()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGPIPE)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		_ = rw.Close()
		_ = server.Close()
	}()

	if err := server.Serve(); err != nil && err != io.EOF {
		return err
	}
	return nil
}

func main() {
	rw := &stdioReadWriteCloser{os.Stdin, os.Stdout}
	if err := run(rw); err != nil {
		log.Fatal("sftp server error: ", err)
	}
}
