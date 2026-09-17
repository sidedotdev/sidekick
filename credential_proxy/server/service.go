package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/rs/zerolog/log"

	"sidekick/common"
)

type Service struct {
	inference         *http.Server
	control           *http.Server
	inferenceListener net.Listener
	controlListener   net.Listener
	url               string
	closeOnce         sync.Once
	closeErr          error
}

func StartService(
	port int,
	socket string,
	loadTrusted func() (map[string]common.CredentialProxyConfig, error),
	allowSideSources func() bool,
) (*Service, error) {
	if loadTrusted == nil || allowSideSources == nil {
		return nil, errors.New("credential proxy authorization callbacks are required")
	}
	dir := filepath.Dir(socket)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("credential proxy control directory must be private")
	}
	inferenceListener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = inferenceListener.Close()
		}
	}()

	controlListener, err := listenControl(socket)
	if err != nil {
		return nil, err
	}
	defer func() {
		if !success {
			_ = controlListener.Close()
		}
	}()
	registry := NewCapabilities()
	service := &Service{
		inference:         newServer(registry),
		control:           newServer(NewControlHandler(registry, loadTrusted, allowSideSources)),
		inferenceListener: inferenceListener,
		controlListener:   controlListener,
		url:               "http://" + inferenceListener.Addr().String(),
	}
	success = true
	go service.serve(service.inference, inferenceListener)
	go service.serve(service.control, controlListener)
	return service, nil
}

func (s *Service) URL() string {
	return s.url
}

func (s *Service) serve(server *http.Server, listener net.Listener) {
	if err := server.Serve(listener); err != nil &&
		!errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		log.Error().Msg("Credential proxy listener stopped unexpectedly")
		_ = s.Close()
	}
}

func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		// Close listeners explicitly even if their Serve goroutines have not
		// started yet. Control listener cleanup retains its ownership lock.
		for _, listener := range []net.Listener{s.controlListener, s.inferenceListener} {
			if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				s.closeErr = errors.Join(s.closeErr, err)
			}
		}
		for _, server := range []*http.Server{s.control, s.inference} {
			if err := server.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				s.closeErr = errors.Join(s.closeErr, err)
			}
		}
	})
	return s.closeErr
}
