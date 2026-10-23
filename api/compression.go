package api

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"github.com/rs/zerolog/log"
)

const (
	encodingZstd = "zstd"
	encodingGzip = "gzip"
)

// supportedEncodings is in server preference order: zstd is preferred whenever
// the client accepts it, regardless of relative q-values.
var supportedEncodings = []string{encodingZstd, encodingGzip}

type streamEncoder interface {
	io.WriteCloser
	Flush() error
	Reset(w io.Writer)
}

// Encoders (zstd in particular) are expensive to allocate, so they are pooled
// and reset per response.
var encoderPools = map[string]*sync.Pool{
	encodingZstd: {New: func() any {
		encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
		if err != nil {
			panic(fmt.Sprintf("invalid zstd encoder options: %v", err))
		}
		return encoder
	}},
	encodingGzip: {New: func() any {
		return gzip.NewWriter(io.Discard)
	}},
}

// CompressionMiddleware compresses response bodies with zstd or gzip based on
// the request's Accept-Encoding header.
func CompressionMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !compressionEligible(c.Request) {
			c.Next()
			return
		}
		encoding := negotiateEncoding(c.GetHeader("Accept-Encoding"))
		if encoding == "" {
			c.Next()
			return
		}

		writer := &compressWriter{ResponseWriter: c.Writer, encoding: encoding}
		c.Writer = writer
		defer func() {
			// Restoring the original writer ensures anything written after this
			// middleware returns (eg panic recovery) can't touch a pooled encoder.
			c.Writer = writer.ResponseWriter
			writer.finish()
		}()
		c.Next()
	}
}

func compressionEligible(r *http.Request) bool {
	return r.Method != http.MethodHead &&
		r.Header.Get("Upgrade") == "" &&
		r.Header.Get("Range") == ""
}

// negotiateEncoding returns the preferred supported encoding acceptable to the
// client, or "" if none is acceptable.
func negotiateEncoding(acceptEncoding string) string {
	qualities := parseAcceptEncoding(acceptEncoding)
	for _, encoding := range supportedEncodings {
		q, ok := qualities[encoding]
		if !ok {
			q, ok = qualities["*"]
		}
		if ok && q > 0 {
			return encoding
		}
	}
	return ""
}

func parseAcceptEncoding(acceptEncoding string) map[string]float64 {
	qualities := make(map[string]float64)
	for _, entry := range strings.Split(acceptEncoding, ",") {
		name, params, _ := strings.Cut(entry, ";")
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		q := 1.0
		for _, param := range strings.Split(params, ";") {
			key, value, found := strings.Cut(param, "=")
			if !found || !strings.EqualFold(strings.TrimSpace(key), "q") {
				continue
			}
			parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil {
				parsed = 0
			}
			q = parsed
		}
		qualities[name] = q
	}
	return qualities
}

// compressWriter defers the compression decision until the response headers
// are about to be sent, so handlers that set their own Content-Encoding or
// respond without a body are passed through untouched.
type compressWriter struct {
	gin.ResponseWriter
	encoding string
	started  bool
	encoder  streamEncoder
}

func (w *compressWriter) start(firstChunk []byte) {
	if w.started {
		return
	}
	w.started = true

	header := w.Header()
	if header.Get("Content-Encoding") == "" && bodyAllowedForStatus(w.Status()) {
		// net/http skips content sniffing once Content-Encoding is set, so
		// sniff the uncompressed bytes ourselves.
		if header.Get("Content-Type") == "" && len(firstChunk) > 0 {
			header.Set("Content-Type", http.DetectContentType(firstChunk))
		}
		header.Set("Content-Encoding", w.encoding)
		header.Add("Vary", "Accept-Encoding")
		header.Del("Content-Length")

		encoder := encoderPools[w.encoding].Get().(streamEncoder)
		encoder.Reset(w.ResponseWriter)
		w.encoder = encoder
	}
	w.ResponseWriter.WriteHeaderNow()
}

func (w *compressWriter) Write(data []byte) (int, error) {
	w.start(data)
	if w.encoder == nil {
		return w.ResponseWriter.Write(data)
	}
	return w.encoder.Write(data)
}

func (w *compressWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *compressWriter) WriteHeaderNow() {
	w.start(nil)
}

func (w *compressWriter) Flush() {
	w.start(nil)
	if w.encoder != nil {
		if err := w.encoder.Flush(); err != nil {
			log.Debug().Err(err).Str("encoding", w.encoding).Msg("Failed to flush compressed response")
		}
	}
	w.ResponseWriter.Flush()
}

func (w *compressWriter) finish() {
	if w.encoder == nil {
		return
	}
	if err := w.encoder.Close(); err != nil {
		log.Debug().Err(err).Str("encoding", w.encoding).Msg("Failed to finish compressed response")
	}
	w.encoder.Reset(io.Discard)
	encoderPools[w.encoding].Put(w.encoder)
	w.encoder = nil
}

func bodyAllowedForStatus(status int) bool {
	switch {
	case status >= 100 && status <= 199:
		return false
	case status == http.StatusNoContent, status == http.StatusNotModified:
		return false
	}
	return true
}
