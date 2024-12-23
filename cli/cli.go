package main

import (
	"fmt"
	"os"
	"sidekick/db"

	// Embedding the frontend build files
	_ "embed"

	"github.com/joho/godotenv"
	"github.com/kardianos/service"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	dbAccessor db.Service
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
		if !os.IsNotExist(err) {
			log.Warn().Err(err).Msg("Warning: failed to load .env file")
		}
	}

	if service.Interactive() {
		interactiveMain()
	} else {
		serviceMain()
	}
}

func interactiveMain() {
	log.Logger = log.Level(zerolog.InfoLevel).Output(zerolog.ConsoleWriter{Out: os.Stderr})

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
