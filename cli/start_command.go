package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sidekick/api"
	"sidekick/common"
	"sidekick/nats"
	temporalsrv "sidekick/temporal"
	"sidekick/worker"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v3"
	temporal_worker "go.temporal.io/sdk/worker"
)

// openURL opens the specified URL in the default browser of the user.
func openURL(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	case "darwin":
		cmd = "open"
		args = []string{url}
	default: // "linux", "freebsd", "openbsd", "netbsd"
		// Check if running under WSL
		if isWSL() {
			// Use 'cmd.exe /c start' to open the URL in the default Windows browser
			cmd = "cmd.exe"
			args = []string{"/c", "start", url}
		} else {
			// Use xdg-open on native Linux environments
			cmd = "xdg-open"
			args = []string{url}
		}
	}
	if len(args) > 1 {
		// args[0] is used for 'start' command argument, to prevent issues with URLs starting with a quote
		args = append(args[:1], append([]string{""}, args[1:]...)...)
	}
	return exec.Command(cmd, args...).Start()
}

// isWSL checks if the Go program is running inside Windows Subsystem for Linux
func isWSL() bool {
	releaseData, err := exec.Command("uname", "-r").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(releaseData)), "microsoft")
}

// waitForServer attempts to connect to the server until it responds or times out
func waitForServer(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if checkServerStatus() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func NewStartCommand() *cli.Command {
	return &cli.Command{
		Name:  "start",
		Usage: "Start services required to use Sidekick",
		Description: "Starts the Sidekick services. By default, all services are started. " +
			"Use flags to enable specific services if you want to run only a subset.",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "server",
				Aliases: []string{"s"},
				Usage:   "Enable the server component",
			},
			&cli.BoolFlag{
				Name:    "worker",
				Aliases: []string{"w"},
				Usage:   "Enable the worker component",
			},
			&cli.BoolFlag{
				Name:    "temporal",
				Aliases: []string{"t"},
				Usage:   "Enable the temporal component",
			},
			&cli.BoolFlag{
				Name:    "nats",
				Aliases: []string{"n"},
				Usage:   "Enable the NATS server component",
			},
			&cli.BoolFlag{
				Name:    "disable-auto-open",
				Aliases: []string{"x"},
				Usage:   "Disable automatic browser opening",
			},
		},
		Action: handleStartCommand,
	}
}

func handleStartCommand(cliCtx context.Context, cmd *cli.Command) error {
	server := cmd.Bool("server")
	worker := cmd.Bool("worker")
	temporal := cmd.Bool("temporal")
	natsServer := cmd.Bool("nats")
	disableAutoOpen := cmd.Bool("disable-auto-open")

	// If no services specified, enable all by default
	if !server && !worker && !temporal && !natsServer {
		server = true
		worker = true
		temporal = true
		natsServer = true
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	if temporal {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Info().Msg("Starting temporal...")

			temporalServer := temporalsrv.Start()

			// Wait for cancellation
			<-ctx.Done()
			log.Info().Msg("Stopping temporal...")
			temporalServer.Stop()
		}()
	}

	if server {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Info().Msg("Starting server...")
			srv := startServer()

			// Wait for server to be ready
			if waitForServer(5 * time.Second) {
				fmt.Printf(`
  ______  __       __          __       __          __       
 /      \|  \     |  \        |  \     |  \        |  \      
|  ▓▓▓▓▓▓\\▓▓ ____| ▓▓ ______ | ▓▓   __ \▓▓ _______| ▓▓   __ 
| ▓▓___\▓▓  \/      ▓▓/      \| ▓▓  /  \  \/       \ ▓▓  /  \
 \▓▓    \| ▓▓  ▓▓▓▓▓▓▓  ▓▓▓▓▓▓\ ▓▓_/  ▓▓ ▓▓  ▓▓▓▓▓▓▓ ▓▓_/  ▓▓
 _\▓▓▓▓▓▓\ ▓▓ ▓▓  | ▓▓ ▓▓    ▓▓ ▓▓   ▓▓| ▓▓ ▓▓     | ▓▓   ▓▓ 
|  \__| ▓▓ ▓▓ ▓▓__| ▓▓ ▓▓▓▓▓▓▓▓ ▓▓▓▓▓▓\| ▓▓ ▓▓_____| ▓▓▓▓▓▓\ 
 \▓▓    ▓▓ ▓▓\▓▓    ▓▓\▓▓     \ ▓▓  \▓▓\ ▓▓\▓▓     \ ▓▓  \▓▓\
  \▓▓▓▓▓▓ \▓▓ \▓▓▓▓▓▓▓ \▓▓▓▓▓▓▓\▓▓   \▓▓\▓▓ \▓▓▓▓▓▓▓\▓▓   \▓▓  v%s

`, version)
				// If auto-open is enabled and server is started successfully, try to open the URL
				if !disableAutoOpen {
					url := fmt.Sprintf("http://localhost:%d", common.GetServerPort())
					fmt.Printf("Opening Sidekick UI at %s\n\n", url)
					log.Info().Msgf("Opening %s in default browser...", url)
					if err := openURL(url); err != nil {
						log.Error().Err(err).Msg("Failed to open URL in browser")
					}
				}
			} else if !disableAutoOpen {
				log.Error().Msg("Server did not become ready in time to open URL")
			}

			// Wait for cancellation
			<-ctx.Done()
			log.Info().Msg("Stopping server...")

			if err := srv.Shutdown(context.Background()); err != nil {
				log.Fatal().Err(err).Msg("Graceful API server shutdown failed")
			}
		}()
	}

	if worker {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Info().Msg("Starting worker...")
			w := startWorker()

			// Wait for cancellation
			<-ctx.Done()
			log.Info().Msg("Stopping worker...")
			w.Stop()
		}()
	}

	if natsServer {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Info().Msg("Starting NATS server...")

			nats, err := nats.GetOrNewServer()
			if err != nil {
				log.Fatal().Err(err).Msg("Failed to create NATS server")
			}

			if err := nats.Start(ctx); err != nil {
				log.Fatal().Err(err).Msg("Failed to start NATS server")
			}

			// Wait for cancellation
			<-ctx.Done()
			log.Info().Msg("Stopping NATS server...")

			if err := nats.Stop(); err != nil {
				log.Error().Err(err).Msg("Error stopping NATS server")
			}
		}()
	}

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info().Msg("Shutdown signal received...")

	// Signal all goroutines to stop
	cancel()

	// Wait for all processes to complete
	wg.Wait()
	log.Info().Msg("Shut down gracefully")
	return nil
}

func startServer() *api.Server {
	return api.RunServer()
}

func startWorker() temporal_worker.Worker {
	return worker.StartWorker(common.GetTemporalServerHostPort(), common.GetTemporalTaskQueue())
}
