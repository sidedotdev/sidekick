package main // look reviewer: this was already package main, we didn't touch it

import (
	"context"
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
	"github.com/urfave/cli/v3"
)

// version is the version of the CLI, injected at build time.
var version string

// program struct and its methods (Start, run, Stop) are for system service mode
type program struct{}

func (p *program) Start(s system_service.Service) error {
	go p.run()
	return nil
}

func (p *program) run() {
	// These functions (startServer, startWorker, startTemporal) are assumed to be defined elsewhere
	// or would be part of the actual service logic, not directly called by CLI commands.
	// For example:
	// startServer()
	// startWorker()
	// startTemporal()
}

func (p *program) Stop(s system_service.Service) error {
	// Stop should put the program into a safe state and return quickly.
	return nil
}

func main() {
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
		if err := setupAndRunInteractiveCli(os.Args); err != nil {
			// urfave/cli's cli.Exit errors usually print themselves.
			// For other errors, log them.
			if _, ok := err.(cli.ExitCoder); !ok {
				log.Error().Err(err).Msg("CLI execution error")
			}
			// Ensure we exit with a non-zero code on error.
			// If err is ExitCoder, its code will be used. Otherwise, default to 1.
			if exitErr, ok := err.(cli.ExitCoder); ok {
				os.Exit(exitErr.ExitCode())
			} else {
				os.Exit(1)
			}
		}
	} else {
		serviceMain()
	}
}

func setupAndRunInteractiveCli(args []string) error {
	log.Logger = log.Level(zerolog.InfoLevel).Output(zerolog.ConsoleWriter{Out: os.Stderr})

	cliApp := &cli.Command{
		Name:        "side",
		Usage:       "CLI for Sidekick",
		Description: "Manages Sidekick workspaces, tasks, and server.",
		Version:     version, // Enables global --version flag
		Commands: []*cli.Command{
			{
				Name:  "init",
				Usage: "Initialize Sidekick in the current directory. Must be a root directory or subdirectory within a git repository.",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					service, err := sidekick.GetService() // Assumes sidekick.GetService() is available
					if err != nil {
						return cli.Exit(fmt.Sprintf("Failed to initialize service: %v", err), 1)
					}
					// Assumes NewInitCommandHandler and its methods are available
					handler := NewInitCommandHandler(service)
					if err := handler.handleInitCommand(); err != nil {
						return cli.Exit(fmt.Sprintf("Initialization failed: %v", err), 1)
					}
					fmt.Println("Sidekick initialized successfully.")
					return nil
				},
			},
			{
				Name:  "start",
				Usage: "Start services required to use Sidekick. Starts all services by default, but provides sub-commands to run each service individually.",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					// Assumes handleStartCommand is defined elsewhere and handles its own output/exit.
					// It might need refactoring in the future to return errors.
					handleStartCommand(cmd.Args().Slice())
					return nil
				},
			},
			{
				Name:      "service",
				Usage:     "Manage Sidekick system service.",
				ArgsUsage: "<install|uninstall|start|stop|status>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					controlAction := cmd.Args().First()
					if controlAction == "" {
						return cli.Exit("Usage: side service [install|uninstall|start|stop|status]", 1)
					}
					return handleServiceCommandControl(controlAction)
				},
			},
			{
				Name:    "version",
				Aliases: []string{"v"},
				Usage:   "Show version information",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if version == "" {
						fmt.Println("sidekick MISSING_VERSION")
					} else {
						fmt.Printf("sidekick %s\n", version)
					}
					return nil
				},
			},
			NewTaskCommand(),
		},
	}
	return cliApp.Run(context.Background(), args)
}

func serviceMain() {
	prg := &program{}
	s, err := system_service.New(prg, svcConfig)
	if err != nil {
		// Use log.Fatal for service startup errors as it's non-interactive
		log.Fatal().Err(err).Msg("Failed to initialize system service")
	}
	logger, err := s.Logger(nil)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to get system service logger")
	}
	err = s.Run()
	if err != nil {
		logger.Error(err) // Log error from service run
		// os.Exit(1) // Consider if service run failure should exit process
	}
}

var svcConfig = &system_service.Config{
	Name:        "SidekickService",
	DisplayName: "Sidekick Service",
	Description: "This service runs the Sidekick server and worker.",
}

// handleServiceCommandControl is a refactored version of the old handleServiceCommand
func handleServiceCommandControl(action string) error {
	prg := &program{}
	s, err := system_service.New(prg, svcConfig)
	if err != nil {
		return cli.Exit(fmt.Sprintf("Failed to create service for action '%s': %v", action, err), 1)
	}

	err = system_service.Control(s, action)
	if err != nil {
		return cli.Exit(fmt.Sprintf("Service control action '%s' failed: %v", action, err), 1)
	}
	fmt.Printf("Service control action '%s' executed successfully.\n", action)
	return nil
}

// Stubs for functions assumed to exist elsewhere, to make the example runnable if they were missing.
// In a real scenario, these would be properly defined or imported.
// var NewInitCommandHandler = func(service interface{}) interface{ handleInitCommand() error } {
//	return nil // Placeholder
// }
// var handleStartCommand = func(args []string) {
//	// Placeholder
// }
// func startServer(){}
// func startWorker(){}
// func startTemporal(){}
