package api

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

func runServer(addr string, allowedOrigins *AllowedOrigins, initializeController func() (Controller, error), initializeTracer func() (func(context.Context) error, error)) (*Server, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	ctrl := newLazyController(initializeController)
	router := DefineRoutes(ctrl, allowedOrigins)
	httpServer := &http.Server{
		Addr:    listener.Addr().String(),
		Handler: router.Handler(),
	}
	go func() {
		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("API server stopped")
		}
	}()

	ctrl.initialization.start()
	return &Server{
		httpServer:     httpServer,
		shutdownTracer: startTracer(initializeTracer),
	}, nil
}

// Local and remote listeners share backend connections and OAuth sessions.
var initializeServerController = sync.OnceValues(NewController)

type controllerInitialization struct {
	once       sync.Once
	ready      chan struct{}
	initialize func() (Controller, error)
	controller Controller
	err        error
}

func newLazyController(initialize func() (Controller, error)) Controller {
	return Controller{
		initialization: &controllerInitialization{
			ready:      make(chan struct{}),
			initialize: initialize,
		},
	}
}

func (i *controllerInitialization) start() {
	i.once.Do(func() {
		go func() {
			i.controller, i.err = i.initialize()
			if i.err != nil {
				log.Error().Err(i.err).Msg("Failed to initialize API controller")
			}
			close(i.ready)
		}()
	})
}

func (i *controllerInitialization) wait(ctx context.Context) (Controller, error) {
	i.start()
	select {
	case <-i.ready:
		return i.controller, i.err
	case <-ctx.Done():
		return Controller{}, ctx.Err()
	}
}

func (ctrl *Controller) initializationMiddleware() gin.HandlerFunc {
	initialization := ctrl.initialization
	allowedOrigins := ctrl.allowedOrigins
	var once sync.Once

	return func(c *gin.Context) {
		if initialization == nil {
			return
		}

		controller, err := initialization.wait(c.Request.Context())
		if err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "API initialization unavailable"})
			return
		}
		once.Do(func() {
			controller.allowedOrigins = allowedOrigins
			*ctrl = controller
		})
	}
}

func startTracer(initialize func() (func(context.Context) error, error)) func(context.Context) error {
	var mu sync.Mutex
	var stopped bool
	var shutdown func(context.Context) error

	go func() {
		stop, err := initialize()
		if err != nil {
			log.Error().Err(err).Msg("Failed to initialize telemetry")
			return
		}

		mu.Lock()
		if !stopped {
			shutdown = stop
			mu.Unlock()
			return
		}
		mu.Unlock()

		// Initialization can finish after the HTTP server has shut down.
		if stop != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := stop(ctx); err != nil {
				log.Error().Err(err).Msg("Error shutting down telemetry")
			}
		}
	}()

	return func(ctx context.Context) error {
		mu.Lock()
		stopped = true
		stop := shutdown
		shutdown = nil
		mu.Unlock()

		if stop != nil {
			return stop(ctx)
		}
		return nil
	}
}
