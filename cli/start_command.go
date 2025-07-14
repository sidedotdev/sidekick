package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sidekick/api"
	"sidekick/common"
	"sidekick/nats"
	"sidekick/worker"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v3"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/sdk/client"
	temporal_worker "go.temporal.io/sdk/worker"
	zerologadapter "logur.dev/adapter/zerolog"
	"logur.dev/logur"

	uiserver "github.com/temporalio/ui-server/v2/server"
	uiconfig "github.com/temporalio/ui-server/v2/server/config"
	uiserveroptions "github.com/temporalio/ui-server/v2/server/server_options"
	"go.temporal.io/server/common/authorization"
	"go.temporal.io/server/common/cluster"
	"go.temporal.io/server/common/config"
	"go.temporal.io/server/common/dynamicconfig"
	"go.temporal.io/server/common/membership/static"
	"go.temporal.io/server/common/metrics"
	sqliteplugin "go.temporal.io/server/common/persistence/sql/sqlplugin/sqlite"
	"go.temporal.io/server/common/primitives"
	sqliteschema "go.temporal.io/server/schema/sqlite"
	"go.temporal.io/server/temporal"
)

type temporalServerConfig struct {
	ip          string
	namespace   string
	clusterName string
	dbFilePath  string
	ports       struct {
		frontend int
		history  int
		matching int
		worker   int
		ui       int
		metrics  int
	}
}

func newTemporalServerConfig(ip string, basePort int) (*temporalServerConfig, error) {
	sidekickDataDir, err := common.GetSidekickDataHome()
	if err != nil {
		return nil, fmt.Errorf("failed to ensure Sidekick XDG data home: %w", err)
	}

	cfg := &temporalServerConfig{
		ip:          ip,
		namespace:   common.GetTemporalNamespace(),
		clusterName: "active",
		dbFilePath:  filepath.Join(sidekickDataDir, "temporal.db"),
	}

	// Calculate ports
	cfg.ports.frontend = basePort
	cfg.ports.history = basePort + 1
	cfg.ports.matching = basePort + 2
	cfg.ports.worker = basePort + 3
	cfg.ports.ui = basePort + 1000
	cfg.ports.metrics = basePort + 2000

	return cfg, nil
}

func startTemporalUIServer(cfg *temporalServerConfig) error {
	ui := uiserver.NewServer(uiserveroptions.WithConfigProvider(&uiconfig.Config{
		TemporalGRPCAddress: fmt.Sprintf("%s:%d", cfg.ip, cfg.ports.frontend),
		Host:                cfg.ip,
		Port:                cfg.ports.ui,
		EnableUI:            true,
		CORS:                uiconfig.CORS{CookieInsecure: true},
		HideLogs:            true,
	}))

	if err := ui.Start(); err != nil && err != http.ErrServerClosed {
		return err
	}

	return nil
}

func startTemporal() temporal.Server {
	cfg, err := newTemporalServerConfig(common.GetTemporalServerHost(), common.GetTemporalServerPort())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create Temporal server config")
	}

	go (func() {
		err := startTemporalUIServer(cfg)
		if err != nil {
			log.Fatal().Err(err).Msg("Unable to start the Temporal UI server")
		}
	})()

	server := startTemporalServer(cfg)

	log.Info().Str("component", "Temporal Server").Msgf("%v:%v", cfg.ip, cfg.ports.frontend)
	log.Info().Str("component", "Temporal UI").Msgf("http://%v:%v", cfg.ip, cfg.ports.ui)
	log.Info().Str("component", "Temporal Metrics").Msgf("http://%v:%v/metrics", cfg.ip, cfg.ports.metrics)

	return server
}

func startTemporalServer(cfg *temporalServerConfig) temporal.Server {
	// Create temporal server config
	conf := &config.Config{
		Global: config.Global{
			Metrics: &metrics.Config{
				Prometheus: &metrics.PrometheusConfig{
					ListenAddress: fmt.Sprintf("%s:%d", cfg.ip, cfg.ports.metrics),
					HandlerPath:   "/metrics",
				},
			},
		},
		Persistence: config.Persistence{
			DefaultStore:     "sqlite-default",
			VisibilityStore:  "sqlite-default",
			NumHistoryShards: 1,
			DataStores: map[string]config.DataStore{
				"sqlite-default": {
					SQL: &config.SQL{
						PluginName: sqliteplugin.PluginName,
						ConnectAttributes: map[string]string{
							"mode":  "rwc",
							"setup": "true",
						},
						DatabaseName: cfg.dbFilePath,
					},
				},
			},
		},
		ClusterMetadata: &cluster.Config{
			EnableGlobalNamespace:    false,
			FailoverVersionIncrement: 10,
			MasterClusterName:        cfg.clusterName,
			CurrentClusterName:       cfg.clusterName,
			ClusterInformation: map[string]cluster.ClusterInformation{
				cfg.clusterName: {
					Enabled:                true,
					InitialFailoverVersion: int64(1),
					RPCAddress:             fmt.Sprintf("%s:%d", cfg.ip, cfg.ports.frontend),
					ClusterID:              uuid.NewString(),
				},
			},
		},
		DCRedirectionPolicy: config.DCRedirectionPolicy{
			Policy: "noop",
		},
		Services: map[string]config.Service{
			"frontend": {
				RPC: config.RPC{
					GRPCPort: cfg.ports.frontend,
					BindOnIP: cfg.ip,
				},
			},
			"history": {
				RPC: config.RPC{
					GRPCPort: cfg.ports.history,
					BindOnIP: cfg.ip,
				},
			},
			"matching": {
				RPC: config.RPC{
					GRPCPort: cfg.ports.matching,
					BindOnIP: cfg.ip,
				},
			},
			"worker": {
				RPC: config.RPC{
					GRPCPort: cfg.ports.worker,
					BindOnIP: cfg.ip,
				},
			},
		},
		Archival: config.Archival{
			History: config.HistoryArchival{
				State: "disabled",
			},
			Visibility: config.VisibilityArchival{
				State: "disabled",
			},
		},
		NamespaceDefaults: config.NamespaceDefaults{
			Archival: config.ArchivalNamespaceDefaults{
				History: config.HistoryArchivalNamespaceDefaults{
					State: "disabled",
				},
				Visibility: config.VisibilityArchivalNamespaceDefaults{
					State: "disabled",
				},
			},
		},
		PublicClient: config.PublicClient{
			HostPort: fmt.Sprintf("%s:%d", cfg.ip, cfg.ports.frontend),
		},
	}

	// Setup namespace
	if err := sqliteschema.CreateNamespaces(conf.Persistence.DataStores["sqlite-default"].SQL,
		sqliteschema.NewNamespaceConfig(cfg.clusterName, cfg.namespace, false)); err != nil {
		log.Fatal().Err(err).Msg("Unable to create namespace")
	}

	// Setup authorization
	authorizer, err := authorization.GetAuthorizerFromConfig(&conf.Global.Authorization)
	if err != nil {
		log.Fatal().Err(err).Msg("Unable to create authorizer")
	}

	logger := common.NewZerologLogger(log.Logger.Level(zerolog.WarnLevel))
	claimMapper, err := authorization.GetClaimMapperFromConfig(&conf.Global.Authorization, logger)
	if err != nil {
		log.Fatal().Err(err).Msg("Unable to create claim mapper")
	}

	// Setup dynamic config
	dynConf := make(dynamicconfig.StaticClient)

	// NOTE: these are now the defaults, so no longer needed. we'll keep this
	// around as an example of how to set dynamic config options
	dynConf[dynamicconfig.FrontendEnableUpdateWorkflowExecution.Key()] = true
	dynConf[dynamicconfig.FrontendEnableUpdateWorkflowExecutionAsyncAccepted.Key()] = true

	// we bump up against the default 10mb warning limit often
	dynConf[dynamicconfig.HistorySizeLimitWarn.Key()] = 50 * 1024 * 1024
	dynConf[dynamicconfig.HistorySizeLimitError.Key()] = 100 * 1024 * 1024

	// Create and start temporal server
	server, err := temporal.NewServer(
		temporal.WithConfig(conf),
		temporal.ForServices(temporal.DefaultServices),
		temporal.WithStaticHosts(map[primitives.ServiceName]static.Hosts{
			primitives.FrontendService: static.SingleLocalHost(fmt.Sprintf("%s:%d", cfg.ip, cfg.ports.frontend)),
			primitives.HistoryService:  static.SingleLocalHost(fmt.Sprintf("%s:%d", cfg.ip, cfg.ports.history)),
			primitives.MatchingService: static.SingleLocalHost(fmt.Sprintf("%s:%d", cfg.ip, cfg.ports.matching)),
			primitives.WorkerService:   static.SingleLocalHost(fmt.Sprintf("%s:%d", cfg.ip, cfg.ports.worker)),
		}),
		temporal.WithLogger(logger),
		temporal.WithAuthorizer(authorizer),
		temporal.WithClaimMapper(func(*config.Config) authorization.ClaimMapper { return claimMapper }),
		temporal.WithDynamicConfigClient(dynConf),
	)
	if err != nil {
		log.Fatal().Err(err).Msg("Unable to create server")
	}

	if err := server.Start(); err != nil {
		log.Fatal().Err(err).Msg("Unable to start server")
	}

	err = setupSidekickTemporalSearchAttributes(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Setting up sidekick temporal search attributes failed")
	}

	return server
}

func setupSidekickTemporalSearchAttributes(cfg *temporalServerConfig) error {
	logger := logur.LoggerToKV(zerologadapter.New(log.Logger))
	clientOptions := client.Options{
		Logger:   logger,
		HostPort: fmt.Sprintf("%s:%d", cfg.ip, cfg.ports.frontend),
	}
	cl, err := client.NewLazyClient(clientOptions)
	if err != nil {
		return fmt.Errorf("failed to create Temporal client: %w", err)
	}
	defer cl.Close()

	ctx := context.Background()
	operator := cl.OperatorService()

	// List existing search attributes
	listReq := &operatorservice.ListSearchAttributesRequest{
		Namespace: cfg.namespace,
	}
	existingAttrs, err := operator.ListSearchAttributes(ctx, listReq)
	if err != nil {
		return fmt.Errorf("failed to list search attributes: %w", err)
	}

	// Check if WorkspaceId already exists and verify its type
	workspaceIdAttr, exists := existingAttrs.CustomAttributes["WorkspaceId"]
	if exists {
		if workspaceIdAttr != enums.INDEXED_VALUE_TYPE_KEYWORD {
			return fmt.Errorf("WorkspaceId search attribute already exists with a different type: %v", workspaceIdAttr)
		}
		// If it exists with the correct type, we don't need to add it again
		log.Debug().Msg("WorkspaceId search attribute already exists and has the correct type")
		return nil
	}

	// Add WorkspaceId search attribute
	log.Info().Msg("Adding WorkspaceId search attribute to Temporal server")
	addReq := &operatorservice.AddSearchAttributesRequest{
		SearchAttributes: map[string]enums.IndexedValueType{
			"WorkspaceId": enums.INDEXED_VALUE_TYPE_KEYWORD,
		},
		Namespace: cfg.namespace,
	}

	_, err = operator.AddSearchAttributes(ctx, addReq)
	if err != nil {
		return fmt.Errorf("failed to add WorkspaceId search attribute: %w", err)
	}

	return nil
}

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

			temporalServer := startTemporal()

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

			if err := srv.Shutdown(ctx); err != nil {
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

func startServer() *http.Server {
	return api.RunServer()
}

func startWorker() temporal_worker.Worker {
	return worker.StartWorker(common.GetTemporalServerHostPort(), common.GetTemporalTaskQueue())
}
