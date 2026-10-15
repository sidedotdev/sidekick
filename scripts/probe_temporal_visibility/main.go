// Command probe_temporal_visibility measures one visibility page independently
// of history fetching and replay, including through reverse-forwarded proxies.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"sidekick/common"

	"github.com/rs/zerolog/log"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/protobuf/proto"
)

func main() {
	hostPort := flag.String("host-port", common.GetTemporalDiagnosticHostPort(), "Temporal endpoint")
	namespace := flag.String("namespace", client.DefaultNamespace, "Temporal namespace")
	query := flag.String("query", "ExecutionStatus = 'Running'", "visibility query")
	pageSize := flag.Int("page-size", 0, "requested page size (0 uses the server default)")
	timeout := flag.Duration("timeout", time.Minute, "dial and request timeout")
	sdk := flag.Bool("sdk", true, "use SDK ListWorkflow rather than the service client")
	flag.Parse()
	if *pageSize < 0 || int64(*pageSize) > 1<<31-1 || *timeout <= 0 {
		log.Error().Msg("page-size must fit a nonnegative int32 and timeout must be positive")
		os.Exit(2)
	}

	dialCtx, cancel := context.WithTimeout(context.Background(), *timeout)
	c, err := client.DialContext(dialCtx, client.Options{HostPort: *hostPort, Namespace: *namespace})
	cancel()
	if err != nil {
		log.Error().Err(err).Str("endpoint", *hostPort).Msg("dial failed")
		os.Exit(1)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	req := &workflowservice.ListWorkflowExecutionsRequest{
		Namespace: *namespace,
		PageSize:  int32(*pageSize),
		Query:     *query,
	}
	start := time.Now()
	var resp *workflowservice.ListWorkflowExecutionsResponse
	if *sdk {
		resp, err = c.ListWorkflow(ctx, req)
	} else {
		resp, err = c.WorkflowService().ListWorkflowExecutions(ctx, req)
	}
	fmt.Printf("endpoint=%s sdk=%t pageSize=%d timeout=%s elapsed=%s executions=%d bytes=%d hasNextPage=%t\n",
		*hostPort, *sdk, *pageSize, *timeout, time.Since(start).Round(time.Millisecond),
		len(resp.GetExecutions()), proto.Size(resp), len(resp.GetNextPageToken()) > 0)
	if err != nil {
		log.Error().Err(err).Msg("visibility request failed")
		os.Exit(1)
	}
}
