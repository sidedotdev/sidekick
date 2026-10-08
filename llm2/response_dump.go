package llm2

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// ResponseDumpDirEnvVar names a directory into which every raw LLM provider
// HTTP response (status, headers and body, e.g. the full SSE stream) is
// written verbatim, one file per response. Dumping is disabled when unset.
// Useful for diagnosing stream decoding failures, e.g. alongside
// scripts/run_activity.
const ResponseDumpDirEnvVar = "SIDEKICK_LLM_RESPONSE_DUMP_DIR"

// withResponseDump enables raw response dumping on the client when
// ResponseDumpDirEnvVar is set.
func withResponseDump(client *http.Client) *http.Client {
	return withResponseDumpDir(client, os.Getenv(ResponseDumpDirEnvVar))
}

func withResponseDumpDir(client *http.Client, dir string) *http.Client {
	if dir == "" {
		return client
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = &responseDumpTransport{base: base, dir: dir}
	return client
}

type responseDumpTransport struct {
	base http.RoundTripper
	dir  string
}

func (t *responseDumpTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp.Body == nil {
		return resp, err
	}

	file, dumpErr := t.createDumpFile()
	if dumpErr != nil {
		log.Warn().Err(dumpErr).Str("dir", t.dir).Msg("failed to create llm response dump file")
		return resp, nil
	}

	// Request headers and cookies are deliberately omitted since they carry
	// credentials.
	fmt.Fprintf(file, "%s %s\n%s %s\n", req.Method, req.URL.Redacted(), resp.Proto, resp.Status)
	if err := resp.Header.WriteSubset(file, map[string]bool{"Set-Cookie": true}); err != nil {
		log.Warn().Err(err).Str("path", file.Name()).Msg("failed to write llm response dump headers")
	}
	fmt.Fprint(file, "\n")

	log.Info().Str("path", file.Name()).Str("url", req.URL.Redacted()).Msg("dumping raw llm response")
	resp.Body = &dumpingBody{body: resp.Body, file: file}
	return resp, nil
}

func (t *responseDumpTransport) createDumpFile() (*os.File, error) {
	if err := os.MkdirAll(t.dir, 0o755); err != nil {
		return nil, err
	}
	return os.CreateTemp(t.dir, time.Now().Format("20060102T150405.000000000")+"-*.txt")
}

// dumpingBody copies everything read from the response body into the dump
// file, recording the read error that ended the stream, if any.
type dumpingBody struct {
	body io.ReadCloser
	mu   sync.Mutex
	file *os.File
}

func (b *dumpingBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.file != nil {
		if n > 0 {
			_, _ = b.file.Write(p[:n])
		}
		if err != nil && err != io.EOF {
			fmt.Fprintf(b.file, "\n[sidekick: response body read error: %v]\n", err)
		}
	}
	return n, err
}

func (b *dumpingBody) Close() error {
	b.mu.Lock()
	if b.file != nil {
		_ = b.file.Close()
		b.file = nil
	}
	b.mu.Unlock()
	return b.body.Close()
}
