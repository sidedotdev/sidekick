package main

import (
	"context"
	"flag"
	"fmt"
	temporal_worker "go.temporal.io/sdk/worker"
	"net/http"
	"os"
	"os/signal"
	"sidekick/api"
	"sidekick/db"
	"sidekick/worker"
	"sync"
	"syscall"

	// Embedding the frontend build files
	_ "embed"

	"github.com/joho/godotenv"
	"github.com/kardianos/service"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/client"
)

var (
	dbAccessor db.DatabaseAccessor
)

func init() {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379" // default address if environment variable is not set
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	dbAccessor = &db.RedisDatabase{Client: redisClient}
}

type program struct{}

func (p *program) Start(s service.Service) error {
	go p.run()
	return nil
}

func (p *program) run() {
	startServer()
	startWorker()
}

func (p *program) Stop(s service.Service) error {
	// Stop should put the program into a safe state and return quickly.
	return nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: side init")
		os.Exit(1)
	}

	// Load .env file if any
	if err := godotenv.Load(); err != nil {
		log.Debug().Err(err).Msg("Error loading .env file")
	}

	if service.Interactive() {
		interactiveMain()
	} else {
		serviceMain()
	}
}

func interactiveMain() {
	switch os.Args[1] {
	case "init":
		handler := NewInitCommandHandler(dbAccessor)
		if err := handler.handleInitCommand(); err != nil {
			fmt.Println("Initialization failed:", err)
			os.Exit(1)
		}
	case "start":
		handleStartCommand(os.Args[2:])
	case "service":
		handleServiceCommand()
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
	}
}

func serviceMain() {
	prg := &program{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize service")
		os.Exit(1)
	}
	logger, err := s.Logger(nil)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to get service logger")
	}
	err = s.Run()
	if err != nil {
		logger.Error(err)
	}
}

var svcConfig = &service.Config{
	Name:        "SidekickService",
	DisplayName: "Sidekick Service",
	Description: "This service runs the Sidekick server and worker.",
}

func handleServiceCommand() {
	fmt.Println("Not yet supported")
	os.Exit(1)
	program := &program{}
	s, err := service.New(program, svcConfig)
	if err != nil {
		fmt.Println("Failed to create service:", err)
		os.Exit(1)
	}

	if len(os.Args) < 2 {
		fmt.Println("Usage: side service [install|uninstall|start|stop|status]")
		os.Exit(1)
	}

	err = service.Control(s, os.Args[2])
	if err != nil {
		fmt.Println("Service control action failed:", err)
		os.Exit(1)
	}
}

func handleStartCommand(args []string) {
	server := false
	worker := false

	// Parse optional args: `server`, `worker`
	for _, arg := range args {
		switch arg {
		case "server":
			server = true
		case "worker":
			worker = true
		}
	}

	if !server && !worker {
		server = true
		worker = true
		ensureTemporalServerOrExit()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	if server {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fmt.Println("Starting server...")
			srv := startServer()

			// Wait for cancellation
			<-ctx.Done()
			fmt.Println("Stopping server...")

			if err := srv.Shutdown(ctx); err != nil {
				panic(fmt.Sprintf("Graceful API server shutdown failed: %s", err))
			}
			fmt.Println("Server stopped")
		}()
	}

	if worker {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fmt.Println("Starting worker...")
			w := startWorker()

			// Wait for cancellation
			<-ctx.Done()
			fmt.Println("Stopping worker...")
			w.Stop()
		}()
	}

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info().Msg("Shutdown Server ...")

	// Signal all goroutines to stop
	cancel()

	// Wait for all processes to complete
	wg.Wait()
	fmt.Println("Shutdown complete")
}

func startServer() *http.Server {
	return api.RunServer()
}

func startWorker() temporal_worker.Worker {
	var hostPort string
	var taskQueue string
	flag.StringVar(&hostPort, "hostPort", client.DefaultHostPort, "Host and port for the Temporal server, eg localhost:7233")
	flag.StringVar(&taskQueue, "taskQueue", "default", "Task queue to use, eg default")
	flag.Parse()
	return worker.StartWorker(hostPort, taskQueue)
}

func isTemporalServerRunning() bool {
	resp, err := http.Get("http://localhost:8233")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

func ensureTemporalServerOrExit() {
	if !isTemporalServerRunning() {
		fmt.Println("Temporal server is not running. Please start it before running starting sidekick server")
		fmt.Println("To install the Temporal CLI, visit: https://docs.temporal.io/cli#install")
		fmt.Println("To run the Temporal server, use the command:\n\n\ttemporal server start-dev --dynamic-config-value frontend.enableUpdateWorkflowExecution=true --dynamic-config-value frontend.enableUpdateWorkflowExecutionAsyncAccepted=true --db-filename local-temporal-db")
		os.Exit(1)
	}
}
