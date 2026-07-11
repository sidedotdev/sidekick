package temporal

import (
	"context"
	"fmt"
	"net"

	"sidekick/common"

	"github.com/rs/zerolog/log"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// maxHistoryMsgSize matches the Temporal SDK's default gRPC message size
// limit, since workflow history pages can far exceed gRPC's 4MB default.
const maxHistoryMsgSize = 128 * 1024 * 1024

// ReadOnlyProxy exposes a strictly read-only subset of the Temporal
// WorkflowService: just enough to list workflow executions and fetch their
// histories. Unlike the full frontend, it is safe to reverse-forward into
// sandboxed environments (e.g. devpod/openshell containers running
// integration tests), since a sandbox reaching it cannot mutate the host's
// Temporal state.
type ReadOnlyProxy struct {
	server   *grpc.Server
	conn     *grpc.ClientConn
	listener net.Listener
}

// Addr returns the address the proxy is listening on.
func (p *ReadOnlyProxy) Addr() string {
	return p.listener.Addr().String()
}

func (p *ReadOnlyProxy) Stop() {
	// Stop rather than GracefulStop: in-flight long-polling history reads
	// must not block shutdown.
	p.server.Stop()
	_ = p.conn.Close()
}

// readOnlyWorkflowService forwards an explicit allowlist of read-only RPCs to
// the real Temporal frontend. All other WorkflowService RPCs are rejected
// with codes.Unimplemented via the embedded unimplemented server.
type readOnlyWorkflowService struct {
	workflowservice.UnimplementedWorkflowServiceServer
	client workflowservice.WorkflowServiceClient
}

func (s *readOnlyWorkflowService) GetSystemInfo(ctx context.Context, req *workflowservice.GetSystemInfoRequest) (*workflowservice.GetSystemInfoResponse, error) {
	return s.client.GetSystemInfo(ctx, req)
}

func (s *readOnlyWorkflowService) ListWorkflowExecutions(ctx context.Context, req *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	return s.client.ListWorkflowExecutions(ctx, req)
}

func (s *readOnlyWorkflowService) GetWorkflowExecutionHistory(ctx context.Context, req *workflowservice.GetWorkflowExecutionHistoryRequest) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
	return s.client.GetWorkflowExecutionHistory(ctx, req)
}

// StartReadOnlyProxy serves the read-only WorkflowService subset on
// listenHostPort, proxying allowed RPCs to the Temporal frontend at
// temporalHostPort. When payloadStorage is non-nil, payloads that
// common.PayloadCodec offloaded to KV storage are decoded and inlined into
// responses, so consumers without access to this host's KV store (e.g. replay
// tests in sandboxed environments) still receive complete histories.
func StartReadOnlyProxy(listenHostPort, temporalHostPort string, payloadStorage common.KeyValueStorage) (*ReadOnlyProxy, error) {
	dialOptions := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxHistoryMsgSize)),
	}
	if payloadStorage != nil {
		codecInterceptor, err := converter.NewPayloadCodecGRPCClientInterceptor(converter.PayloadCodecGRPCClientInterceptorOptions{
			Codecs: []converter.PayloadCodec{common.NewPayloadCodec(payloadStorage, common.DefaultCodecThreshold)},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create payload codec interceptor for read-only proxy: %w", err)
		}
		dialOptions = append(dialOptions, grpc.WithChainUnaryInterceptor(codecInterceptor))
	}
	conn, err := grpc.NewClient(temporalHostPort, dialOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to set up temporal connection for read-only proxy: %w", err)
	}

	listener, err := net.Listen("tcp", listenHostPort)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read-only temporal proxy failed to listen on %s: %w", listenHostPort, err)
	}

	server := grpc.NewServer(grpc.MaxRecvMsgSize(maxHistoryMsgSize))
	workflowservice.RegisterWorkflowServiceServer(server, &readOnlyWorkflowService{
		client: workflowservice.NewWorkflowServiceClient(conn),
	})

	go func() {
		if err := server.Serve(listener); err != nil {
			log.Error().Err(err).Msg("Read-only temporal proxy server stopped unexpectedly")
		}
	}()

	return &ReadOnlyProxy{server: server, conn: conn, listener: listener}, nil
}
