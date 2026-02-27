package common

import (
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"strconv"

	"github.com/rs/zerolog/log"
)

const defaultPprofPort = 6060

func GetPprofPort() int {
	port := os.Getenv("SIDE_PPROF_PORT")
	if port == "" {
		return defaultPprofPort
	}
	intPort, err := strconv.Atoi(port)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse pprof port: %s", port))
	}
	return intPort
}

// StartPprofServer starts a pprof HTTP server on the configured port if
// SIDE_APP_ENV is "development". It is non-blocking and returns immediately.
// If no explicit port is set via SIDE_PPROF_PORT and the default port is
// unavailable, an ephemeral port is selected automatically.
func StartPprofServer() {
	if os.Getenv("SIDE_APP_ENV") != "development" {
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	explicitPort := os.Getenv("SIDE_PPROF_PORT") != ""
	addr := fmt.Sprintf("127.0.0.1:%d", GetPprofPort())

	ln, err := net.Listen("tcp", addr)
	if err != nil && !explicitPort {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		log.Error().Err(err).Msg("Failed to start pprof server")
		return
	}

	log.Info().Str("addr", ln.Addr().String()).Msg("Starting pprof server")

	go func() {
		if err := http.Serve(ln, mux); err != nil {
			log.Error().Err(err).Msg("pprof server error")
		}
	}()
}
