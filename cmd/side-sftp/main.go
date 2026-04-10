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

	"github.com/pkg/sftp"
)

type stdioReadWriteCloser struct {
	io.Reader
	io.WriteCloser
}

func main() {
	server, err := sftp.NewServer(
		&stdioReadWriteCloser{os.Stdin, os.Stdout},
	)
	if err != nil {
		log.Fatal(err)
	}
	if err := server.Serve(); err == io.EOF {
		server.Close()
	} else if err != nil {
		log.Fatal("sftp server error: ", err)
	}
}
