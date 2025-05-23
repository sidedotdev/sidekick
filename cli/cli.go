package main

import (
	"fmt"
	"os"
	"sidekick"

	// Embedding the frontend build files
	_ "embed"

	"github.com/joho/godotenv"
	system_service "github.com/kardianos/service"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// For testing
var osExit = os.Exit

type program struct{}

func (p *program) Start(s system_service.Service) error {
	go p.run()
	return nil
}

func (p *program) run() {
	startServer()
	startWorker()
	startTemporal()
}

func (p *program) Stop(s system_service.Service) error {
	// Stop should put the program into a safe state and return quickly.
	return nil
}

func displayHelp() {
	fmt.Println("Sidekick - A development tool that helps you manage and monitor your local services")
	fmt.Println("\nAvailable Commands:")
	fmt.Println("  init    Initialize Sidekick in the current directory")
	fmt.Println("  start   Start Sidekick services (server and/or worker)")
	fmt.Println("\nFlags:")
	fmt.Println("  -h, --help   Show help information")
	fmt.Println("\nExamples:")
	fmt.Println("  side init              Initialize Sidekick")
	fmt.Println("  side start             Start all Sidekick services")
	fmt.Println("  side start --server    Start only the Sidekick server")
	fmt.Println("  side start --worker    Start only the Sidekick worker")
}

func main() {
	// Check for help flag before other argument processing
	if len(os.Args) == 2 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
		displayHelp()
		osExit(0)
		return
	}

	if len(os.Args) < 2 {
		fmt.Println("Usage: side init")
		osExit(1)
		return
	}

	// Load .env file if any
	if err := godotenv.Load(); err != nil {
		if !os.IsNotExist(err) {
			log.Warn().Err(err).Msg("Warning: failed to load .env file")
		}
	}

	if system_service.Interactive() {
		interactiveMain()
	} else {
		serviceMain()
	}
}

func interactiveMain() {
	log.Logger = log.Level(zerolog.InfoLevel).Output(zerolog.ConsoleWriter{Out: os.Stderr})

	switch os.Args[1] {
	case "init":
		service, err := sidekick.GetService()
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to initialize service")
		}
		handler := NewInitCommandHandler(service)
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
	s, err := system_service.New(prg, svcConfig)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize system service")
		os.Exit(1)
	}
	logger, err := s.Logger(nil)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to get system service logger")
	}
	err = s.Run()
	if err != nil {
		logger.Error(err)
	}
}

var svcConfig = &system_service.Config{
	Name:        "SidekickService",
	DisplayName: "Sidekick Service",
	Description: "This service runs the Sidekick server and worker.",
}

func handleServiceCommand() {
	fmt.Println("Not yet supported")
	os.Exit(1)
	program := &program{}
	s, err := system_service.New(program, svcConfig)
	if err != nil {
		fmt.Println("Failed to create service:", err)
		os.Exit(1)
	}

	if len(os.Args) < 2 {
		fmt.Println("Usage: side service [install|uninstall|start|stop|status]")
		os.Exit(1)
	}

	err = system_service.Control(s, os.Args[2])
	if err != nil {
		fmt.Println("Service control action failed:", err)
		os.Exit(1)
	}
}
