package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sidekick/api"
	"sidekick/db"
	"sidekick/worker"

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
	// Start the server and worker processes
	if err := startServer(); err != nil {
		log.Error().Err(err).Msg("Failed to start server")
	}

	if err := startWorker(); err != nil {
		log.Error().Err(err).Msg("Failed to start worker")
	}
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

	if server {
		go func() {
			fmt.Println("Starting server...")
			if err := startServer(); err != nil {
				log.Fatal().Err(err).Msg("Failed to start server")
			}
		}()
		// TODO gracefully stop the server
		// defer func() {
		// 	stopServer()
		// }()
	}

	if worker {
		go func() {
			fmt.Println("Starting worker...")
			if err := startWorker(); err != nil {
				log.Fatal().Err(err).Msg("Failed to start worker")
			}
		}()
		// TODO gracefully stop the worker
		// defer func() {
		// 	stopWorker()
		// }()
	}

	// Handle signals to gracefully shutdown the server and worker
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
}

func startServer() error {
	if err := api.RunServer(); err != nil {
		return fmt.Errorf("Failed to start server: %w", err)
	}
	return nil
}

func startWorker() error {
	var hostPort string
	var taskQueue string
	flag.StringVar(&hostPort, "hostPort", client.DefaultHostPort, "Host and port for the Temporal server, eg localhost:7233")
	flag.StringVar(&taskQueue, "taskQueue", "default", "Task queue to use, eg default")
	flag.Parse()
	worker.StartWorker(hostPort, taskQueue)

	return nil
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
