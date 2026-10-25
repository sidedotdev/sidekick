package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sidekick/frontend"

	"github.com/stretchr/testify/require"
)

func TestServerListensDuringInitialization(t *testing.T) {
	t.Parallel()

	for _, dependency := range []string{"controller", "telemetry"} {
		t.Run(dependency, func(t *testing.T) {
			t.Parallel()

			ctrl := NewMockController(t)
			frontendPath, frontendContent := embeddedFrontendFile(t)
			started := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			var controllerCalls atomic.Int32
			var tracerCalls atomic.Int32
			var tracerShutdownCalls atomic.Int32

			initializeController := func() (Controller, error) {
				controllerCalls.Add(1)
				if dependency == "controller" {
					close(started)
					<-release
				}
				return ctrl, nil
			}
			initializeTracer := func() (func(context.Context) error, error) {
				tracerCalls.Add(1)
				if dependency == "telemetry" {
					close(started)
					<-release
				}
				return func(context.Context) error {
					tracerShutdownCalls.Add(1)
					return nil
				}, nil
			}

			type startResult struct {
				server *Server
				err    error
			}
			results := make(chan startResult, 1)
			go func() {
				server, err := runServer("127.0.0.1:0", TestAllowedOrigins(), initializeController, initializeTracer)
				results <- startResult{server: server, err: err}
			}()

			var server *Server
			t.Cleanup(func() {
				unblock()
				if server == nil {
					select {
					case result := <-results:
						require.NoError(t, result.err)
						server = result.server
					case <-time.After(5 * time.Second):
						t.Error("server startup did not finish after initialization was released")
						return
					}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				require.NoError(t, server.Shutdown(ctx))
				require.Eventually(t, func() bool { return tracerShutdownCalls.Load() == 1 }, time.Second, time.Millisecond)
			})

			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("initialization did not start proactively")
			}

			select {
			case result := <-results:
				require.NoError(t, result.err)
				server = result.server
			case <-time.After(time.Second):
				t.Fatal("HTTP startup waited for dependency initialization")
			}

			httpClient := &http.Client{Timeout: time.Second}
			response, err := httpClient.Get("http://" + server.httpServer.Addr + frontendPath)
			require.NoError(t, err)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, response.Body.Close())
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, response.StatusCode)
			require.Equal(t, frontendContent, string(body))

			const requestCount = 16
			apiResults := make(chan error, requestCount)
			for range requestCount {
				go func() {
					response, err := httpClient.Get("http://" + server.httpServer.Addr + "/api/v1/workspaces")
					if err == nil {
						err = response.Body.Close()
						if response.StatusCode != http.StatusOK {
							err = fmt.Errorf("unexpected API status: %d", response.StatusCode)
						}
					}
					apiResults <- err
				}()
			}

			if dependency == "controller" {
				select {
				case err := <-apiResults:
					t.Fatalf("API request completed before controller initialization: %v", err)
				case <-time.After(50 * time.Millisecond):
				}

				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				request := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil).WithContext(ctx)
				canceledResult := make(chan struct{})
				go func() {
					server.httpServer.Handler.ServeHTTP(httptest.NewRecorder(), request)
					close(canceledResult)
				}()
				select {
				case <-canceledResult:
				case <-time.After(time.Second):
					t.Fatal("canceled request waited for initialization")
				}
				unblock()
			}

			for range requestCount {
				select {
				case err := <-apiResults:
					require.NoError(t, err)
				case <-time.After(2 * time.Second):
					t.Fatal("API request did not finish")
				}
			}
			if dependency == "telemetry" {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				require.NoError(t, server.Shutdown(ctx))
				require.Zero(t, tracerShutdownCalls.Load())
			}
			unblock()
			require.EqualValues(t, 1, controllerCalls.Load())
			require.Eventually(t, func() bool { return tracerCalls.Load() == 1 }, time.Second, time.Millisecond)
		})
	}
}

func TestServerInitializationFailure(t *testing.T) {
	t.Parallel()

	for _, dependency := range []string{"controller", "telemetry"} {
		t.Run(dependency, func(t *testing.T) {
			t.Parallel()

			ctrl := NewMockController(t)
			frontendPath, _ := embeddedFrontendFile(t)
			var controllerCalls atomic.Int32
			var tracerCalls atomic.Int32
			server, err := runServer("127.0.0.1:0", TestAllowedOrigins(), func() (Controller, error) {
				controllerCalls.Add(1)
				if dependency == "controller" {
					return Controller{}, errors.New("backend unavailable")
				}
				return ctrl, nil
			}, func() (func(context.Context) error, error) {
				tracerCalls.Add(1)
				if dependency == "telemetry" {
					return nil, errors.New("telemetry unavailable")
				}
				return func(context.Context) error { return nil }, nil
			})
			require.NoError(t, err)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				require.NoError(t, server.Shutdown(ctx))
			})

			httpClient := &http.Client{Timeout: time.Second}
			for _, path := range []string{frontendPath, "/api/v1/workspaces", "/api/v1/workspaces", "/ws/v1/workspaces/test/task_changes"} {
				response, err := httpClient.Get("http://" + server.httpServer.Addr + path)
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())

				if path == frontendPath {
					require.Equal(t, http.StatusOK, response.StatusCode)
				} else if dependency == "controller" {
					require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
				} else if path == "/api/v1/workspaces" {
					require.Equal(t, http.StatusOK, response.StatusCode)
				}
			}
			require.EqualValues(t, 1, controllerCalls.Load())
			require.Eventually(t, func() bool { return tracerCalls.Load() == 1 }, time.Second, time.Millisecond)
		})
	}
}

func TestServerBindFailureDoesNotInitializeDependencies(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	var initializationCalls atomic.Int32
	server, err := runServer(listener.Addr().String(), TestAllowedOrigins(), func() (Controller, error) {
		initializationCalls.Add(1)
		return NewMockController(t), nil
	}, func() (func(context.Context) error, error) {
		initializationCalls.Add(1)
		return func(context.Context) error { return nil }, nil
	})
	require.Error(t, err)
	require.Nil(t, server)
	require.Zero(t, initializationCalls.Load())
}

func embeddedFrontendFile(t *testing.T) (string, string) {
	t.Helper()

	if content, err := frontend.DistFs.ReadFile("dist/index.html"); err == nil {
		return "/", string(content)
	}

	// Go-only test runs may embed placeholders instead of a built frontend.
	files, err := frontend.DistFs.ReadDir("dist")
	require.NoError(t, err)
	for _, file := range files {
		if !file.IsDir() {
			content, err := frontend.DistFs.ReadFile("dist/" + file.Name())
			require.NoError(t, err)
			return "/" + file.Name(), string(content)
		}
	}
	t.Fatal("no embedded frontend files")
	return "", ""
}
