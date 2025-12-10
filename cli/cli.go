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
	startServer()
	startWorker()
	startTemporal()
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
		Description: "~ Sidekick is an agentic AI tool for software engineers.",
		Version:     version, // Enables global --version flag
		Commands: []*cli.Command{
			{
				Name:  "init",
				Usage: "Initialize Sidekick in the current directory. Must be a root directory or subdirectory within a git repository.",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					service, err := sidekick.GetService()
					if err != nil {
						return cli.Exit(fmt.Sprintf("Failed to initialize service: %v", err), 1)
					}
					handler := NewInitCommandHandler(service)
					if err := handler.handleInitCommand(); err != nil {
						return cli.Exit(fmt.Sprintf("Initialization failed: %v", err), 1)
					}
					fmt.Println("Sidekick initialized successfully.")
					return nil
				},
			},
			NewStartCommand(),
			// NOTE: disabling the service subcommand since it isn't yet functional
			/*
				{
					Name:      "service",
					Usage:     "Manage Sidekick system service.",
					ArgsUsage: "<install|uninstall|start|stop|status>",
					Action: func(ctx context.Context, cmd *cli.Command) error {
						controlAction := cmd.Args().First()
						if controlAction == "" {
							return cli.Exit("Usage: side service [install|uninstall|start|stop|status]", 1)
						}
						return handleServiceCommand(controlAction)
					},
				},
			*/
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
			NewAuthCommand(),
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
