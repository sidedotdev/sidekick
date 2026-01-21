package temporal

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sidekick/common"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.temporal.io/api/enums/v1"
	namespacepb "go.temporal.io/api/namespace/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/protobuf/types/known/durationpb"
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

type serverConfig struct {
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

func newServerConfig(ip string, basePort int) (*serverConfig, error) {
	sidekickDataDir, err := common.GetSidekickDataHome()
	if err != nil {
		return nil, fmt.Errorf("failed to ensure Sidekick XDG data home: %w", err)
	}

	cfg := &serverConfig{
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

func startUIServer(cfg *serverConfig) error {
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

// Start initializes and starts the Temporal server and UI.
// Returns the temporal.Server which can be stopped via its Stop() method.
func Start() temporal.Server {
	cfg, err := newServerConfig(common.GetTemporalServerHost(), common.GetTemporalServerPort())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create Temporal server config")
	}

	go (func() {
		err := startUIServer(cfg)
		if err != nil {
			log.Fatal().Err(err).Msg("Unable to start the Temporal UI server")
		}
	})()

	server := startServer(cfg)

	log.Info().Str("component", "Temporal Server").Msgf("%v:%v", cfg.ip, cfg.ports.frontend)
	log.Info().Str("component", "Temporal UI").Msgf("http://%v:%v", cfg.ip, cfg.ports.ui)
	log.Info().Str("component", "Temporal Metrics").Msgf("http://%v:%v/metrics", cfg.ip, cfg.ports.metrics)

	return server
}

func startServer(cfg *serverConfig) temporal.Server {
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
	nsConfig, err := sqliteschema.NewNamespaceConfig(cfg.clusterName, cfg.namespace, false, nil)
	if err != nil {
		log.Fatal().Err(err).Msg("Unable to create namespace config")
	}
	if err := sqliteschema.CreateNamespaces(conf.Persistence.DataStores["sqlite-default"].SQL, nsConfig); err != nil {
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

	err = ensureNamespaceRetention(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to ensure namespace retention")
	}

	err = setupSearchAttributes(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Setting up sidekick temporal search attributes failed")
	}

	return server
}

func ensureNamespaceRetention(cfg *serverConfig) error {
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
	workflowService := cl.WorkflowService()

	describeReq := &workflowservice.DescribeNamespaceRequest{
		Namespace: cfg.namespace,
	}
	describeResp, err := workflowService.DescribeNamespace(ctx, describeReq)
	if err != nil {
		return fmt.Errorf("failed to describe namespace: %w", err)
	}

	targetDays := common.GetTemporalRetentionDays()
	targetRetention := time.Duration(targetDays) * 24 * time.Hour

	currentRetention := time.Duration(0)
	if describeResp.Config != nil && describeResp.Config.WorkflowExecutionRetentionTtl != nil {
		currentRetention = describeResp.Config.WorkflowExecutionRetentionTtl.AsDuration()
	}

	if currentRetention >= targetRetention {
		log.Info().
			Dur("current_retention", currentRetention).
			Dur("target_retention", targetRetention).
			Int("target_days", targetDays).
			Msg("Namespace retention already meets or exceeds target, no update needed")
		return nil
	}

	log.Info().
		Dur("current_retention", currentRetention).
		Dur("target_retention", targetRetention).
		Int("target_days", targetDays).
		Msg("Updating namespace retention")

	updateReq := &workflowservice.UpdateNamespaceRequest{
		Namespace: cfg.namespace,
		Config: &namespacepb.NamespaceConfig{
			WorkflowExecutionRetentionTtl: durationpb.New(targetRetention),
		},
	}

	_, err = workflowService.UpdateNamespace(ctx, updateReq)
	if err != nil {
		return fmt.Errorf("failed to update namespace retention: %w", err)
	}

	log.Info().
		Int("retention_days", targetDays).
		Msg("Successfully updated namespace retention")

	return nil
}

func setupSearchAttributes(cfg *serverConfig) error {
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
