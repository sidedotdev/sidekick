package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sidekick/api"
	"sidekick/worker"
	"sync"
	"syscall"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	temporal_worker "go.temporal.io/sdk/worker"

	uiserver "github.com/temporalio/ui-server/v2/server"
	uiconfig "github.com/temporalio/ui-server/v2/server/config"
	uiserveroptions "github.com/temporalio/ui-server/v2/server/server_options"
	"go.temporal.io/sdk/client"
	"go.temporal.io/server/common/authorization"
	"go.temporal.io/server/common/cluster"
	"go.temporal.io/server/common/config"
	"go.temporal.io/server/common/dynamicconfig"
	temporallog "go.temporal.io/server/common/log"
	"go.temporal.io/server/common/membership/static"
	"go.temporal.io/server/common/metrics"
	sqliteplugin "go.temporal.io/server/common/persistence/sql/sqlplugin/sqlite"
	"go.temporal.io/server/common/primitives"
	sqliteschema "go.temporal.io/server/schema/sqlite"
	"go.temporal.io/server/temporal"
)

type serverConfig struct {
	ip          string
	namespace   string
	clusterName string
	ports       struct {
		frontend int
		history  int
		matching int
		worker   int
		ui       int
		metrics  int
	}
}

func newServerConfig(ip string, basePort int) *serverConfig {
	cfg := &serverConfig{
		ip:          ip,
		namespace:   "default",
		clusterName: "active",
	}

	// Calculate ports
	cfg.ports.frontend = basePort
	cfg.ports.history = basePort + 1
	cfg.ports.matching = basePort + 2
	cfg.ports.worker = basePort + 3
	cfg.ports.ui = basePort + 1000
	cfg.ports.metrics = basePort + 2000

	return cfg
}

func startTemporalUIServer(cfg *serverConfig) error {
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

// cfg := newServerConfig(ip, basePort)
func startTemporal(cfg *serverConfig) temporal.Server {
	go (func() {
		err := startTemporalUIServer(cfg)
		if err != nil {
			log.Fatal().Err(err).Msg("Unable to start the Temporal UI server")
		}
	})()

	return startTemporalServer(cfg)
}

func startTemporalServer(cfg *serverConfig) temporal.Server {
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
							"mode":  "memory",
							"cache": "shared",
						},
						DatabaseName: "temporal",
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
	logger := temporallog.NewNoopLogger().With()
	claimMapper, err := authorization.GetClaimMapperFromConfig(&conf.Global.Authorization, logger)
	if err != nil {
		log.Fatal().Err(err).Msg("Unable to create claim mapper")
	}

	// Setup dynamic config
	dynConf := make(dynamicconfig.StaticClient)
	dynConf[dynamicconfig.ForceSearchAttributesCacheRefreshOnRead.Key()] = true

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

	// FIXME move to caller
	defer server.Stop()

	// FIXME move to caller
	log.Info().Str("component", "Server").Msgf("%v:%v", cfg.ip, cfg.ports.frontend)
	log.Info().Str("component", "UI").Msgf("http://%v:%v", cfg.ip, cfg.ports.ui)
	log.Info().Str("component", "Metrics").Msgf("http://%v:%v/metrics", cfg.ip, cfg.ports.metrics)

	return server
}

func handleStartCommand(args []string) {
	server := false
	worker := false
	temporal := false

	// Parse optional args: `server`, `worker`, `temporal`
	for _, arg := range args {
		switch arg {
		case "server":
			server = true
		case "worker":
			worker = true
		case "temporal":
			temporal = true
		}
	}

	if !server && !worker && !temporal {
		server = true
		worker = true
		temporal = true
		ensureTemporalServerOrExit()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	if temporal {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Info().Msg("Starting temporal server...")
			cfg := newServerConfig("127.0.0.1", 7233)
			temporalServer := startTemporal(cfg)
			defer temporalServer.Stop()

			log.Info().Str("component", "Server").Msgf("%v:%v", cfg.ip, cfg.ports.frontend)
			log.Info().Str("component", "UI").Msgf("http://%v:%v", cfg.ip, cfg.ports.ui)
			log.Info().Str("component", "Metrics").Msgf("http://%v:%v/metrics", cfg.ip, cfg.ports.metrics)

			// Wait for cancellation
			<-ctx.Done()
			log.Info().Msg("Stopping temporal server...")
		}()
	}

	if server {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Info().Msg("Starting server...")
			srv := startServer()

			// Wait for cancellation
			<-ctx.Done()
			log.Info().Msg("Stopping server...")

			if err := srv.Shutdown(ctx); err != nil {
				log.Fatal().Err(err).Msg("Graceful API server shutdown failed")
			}
			log.Info().Msg("Server stopped")
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

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info().Msg("Shutdown signal received...")

	// Signal all goroutines to stop
	cancel()

	// Wait for all processes to complete
	wg.Wait()
	log.Info().Msg("Shutdown complete")
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
