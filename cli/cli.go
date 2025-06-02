package main

import (
	"fmt"
	"os"
	"sidekick"
	"strconv"

	// Embedding the frontend build files
	_ "embed"

	"github.com/joho/godotenv"
	system_service "github.com/kardianos/service"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// version is the version of the CLI, injected at build time.
var version string

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
	fmt.Println("~ Sidekick (side) is an AI automation tool designed to support software engineers.")
	fmt.Println("\nAvailable Commands:")
	fmt.Println("  init     Initialize Sidekick in the current directory. Must be a root directory or subdirectory within a git repository.")
	fmt.Println("  start    Start services required to use Sidekick. Starts all services by default, but provides sub-commands to run each service individually.")
	fmt.Println("  version  Show version information")
	fmt.Println("  help     Show help information")
	fmt.Println("\nFlags:")
	fmt.Println("  -h, --help     Show help information")
	fmt.Println("  -v, --version  Show version information")
	fmt.Println("\nExamples:")
	fmt.Println("  side init           # Initialize Sidekick")
	fmt.Println("  side start          # Start all Sidekick services")
	fmt.Println("  side version        # Display version")
	fmt.Println("  side help           # Display help")
}

func main() {
	// Check for help flag before other argument processing
	if len(os.Args) == 2 && (os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h") {
		displayHelp()
		osExit(0)
		return
	}

	// Check for version flag before other argument processing
	if len(os.Args) == 2 && (os.Args[1] == "version" || os.Args[1] == "-v" || os.Args[1] == "--version") {
		if version == "" {
			fmt.Println("Sidekick version: MISSING")
		} else {
			fmt.Printf("Sidekick version: %s\n", version)
		}
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

	// set global log level
	logLevel, err := strconv.Atoi(os.Getenv("SIDE_LOG_LEVEL"))
	if err != nil {
		logLevel = int(zerolog.InfoLevel) // default to INFO
	}
	zerolog.SetGlobalLevel(zerolog.Level(logLevel))

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
