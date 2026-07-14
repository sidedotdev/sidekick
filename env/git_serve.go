package env

import (
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// startLocalGitUploadPackServer serves read-only fetches of exactly one
// repository on an ephemeral loopback port, so sandboxes can clone/fetch
// from the host through an ssh reverse forward. Each connection's git
// daemon request header is parsed and discarded (the requested path is
// ignored, and only upload-pack is served, so writes are impossible) before
// `git upload-pack` is spawned wired to the socket. Unlike a real git
// daemon there is no separate process to manage or clean up: the listener
// dies with the returned stop func or with this process.
func startLocalGitUploadPackServer(ctx context.Context, repoDir string) (int, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, nil, fmt.Errorf("failed to listen for git upload-pack serving: %w", err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return // listener closed
			}
			go serveGitUploadPackConn(ctx, conn, repoDir)
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port, func() { listener.Close() }, nil
}

func serveGitUploadPackConn(ctx context.Context, conn net.Conn, repoDir string) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	service, err := readGitDaemonRequest(conn)
	if err != nil {
		log.Debug().Err(err).Msg("rejected git upload-pack serving connection")
		return
	}
	if service != "git-upload-pack" {
		log.Debug().Str("service", service).Msg("rejected git serving connection: unsupported service")
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	cmd := exec.CommandContext(ctx, "git", "upload-pack", repoDir)
	cmd.Stdin = conn
	cmd.Stdout = conn
	if err := cmd.Run(); err != nil && ctx.Err() == nil {
		log.Debug().Err(err).Msg("git upload-pack serving connection ended with error")
	}
}

// readGitDaemonRequest reads the single pkt-line request that opens the git
// daemon protocol ("<4-hex-len>git-upload-pack <path>\0host=<host>\0...")
// and returns the requested service. The path and host are deliberately
// ignored: the server serves exactly one preconfigured repository.
func readGitDaemonRequest(conn net.Conn) (string, error) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return "", fmt.Errorf("reading pkt-line length: %w", err)
	}
	n, err := strconv.ParseUint(string(hdr), 16, 32)
	if err != nil {
		return "", fmt.Errorf("invalid pkt-line length %q: %w", hdr, err)
	}
	if n < 5 || n > 65520 {
		return "", fmt.Errorf("pkt-line length %d out of range", n)
	}
	payload := make([]byte, n-4)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return "", fmt.Errorf("reading pkt-line payload: %w", err)
	}
	service, _, _ := strings.Cut(string(payload), " ")
	return strings.TrimRight(service, "\x00\n"), nil
}
