package srv

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/nats"
	"sidekick/srv/jetstream"
	"sidekick/srv/sqlite"
	"testing"
	"time"

	natspkg "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/suite"
)

type DelegatorTestSuite struct {
	suite.Suite
	server    *nats.Server
	nc        *natspkg.Conn
	streamer  *jetstream.Streamer
	storage   *sqlite.Storage
	delegator *Delegator
	ctx       context.Context
}

func (s *DelegatorTestSuite) SetupSuite() {
	var err error
	s.ctx = context.Background()

	// Create test server with unique domain and port
	s.server, err = nats.NewTestServer(nats.ServerOptions{
		Port:            28867, // Different from other test ports
		JetStreamDomain: "sidekick_delegator_test",
		StoreDir:        s.T().TempDir(),
	})
	s.Require().NoError(err)
	s.Require().NoError(s.server.Start(context.Background()))

	// Connect to server
	s.nc, err = natspkg.Connect(fmt.Sprintf("nats://%s:%d", common.GetNatsServerHost(), 28867))
	s.Require().NoError(err)

	// Create streamer
	s.streamer, err = jetstream.NewStreamer(s.nc)
	s.Require().NoError(err)

	// Create storage
	s.storage = sqlite.NewTestSqliteStorage(s.T(), "delegator_test")

	// Create delegator
	s.delegator = &Delegator{
		storage:  s.storage,
		streamer: s.streamer,
	}
}

func (s *DelegatorTestSuite) TestPersistSubflow() {
	s.T().Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	workspaceId := "test-workspace"
	flowId := "test-flow"
	subflowId := "sf_test"

	// Test case 1: New subflow should generate status change event
	newSubflow := domain.Subflow{
		WorkspaceId: workspaceId,
		Id:          subflowId,
		FlowId:      flowId,
		Status:      domain.SubflowStatusInProgress,
	}

	// Subscribe to flow events
	subscriptionCh := make(chan domain.FlowEventSubscription, 1)
	eventCh, errCh := s.streamer.StreamFlowEvents(ctx, workspaceId, flowId, subscriptionCh)

	go func() {
		subscriptionCh <- domain.FlowEventSubscription{ParentId: subflowId, StreamMessageStartId: ""}
		close(subscriptionCh)
	}()

	err := s.delegator.PersistSubflow(ctx, newSubflow)
	s.Require().NoError(err)

	// Verify subflow was persisted
	persisted, err := s.storage.GetSubflow(ctx, workspaceId, subflowId)
	s.Require().NoError(err)
	s.Equal(newSubflow.Status, persisted.Status)

	// Verify status change event was generated
	select {
	case event := <-eventCh:
		statusEvent, ok := event.(domain.StatusChangeEvent)
		s.Require().True(ok, "Expected StatusChangeEvent")
		s.Equal(domain.StatusChangeEventType, statusEvent.GetEventType())
		s.Equal(subflowId, statusEvent.GetParentId())
		s.Equal(string(domain.SubflowStatusInProgress), statusEvent.Status)
	case err := <-errCh:
		s.Fail("Unexpected error:", err)
	case <-time.After(5 * time.Second):
		s.Fail("Timeout waiting for status change event")
	}

	// Test case 2: Same status should not generate event
	err = s.delegator.PersistSubflow(ctx, newSubflow)
	s.Require().NoError(err)

	// Test case 3: Status change should generate event
	changedSubflow := newSubflow
	changedSubflow.Status = domain.SubflowStatusComplete
	err = s.delegator.PersistSubflow(ctx, changedSubflow)
	s.Require().NoError(err)

	// Verify status was updated
	updated, err := s.storage.GetSubflow(ctx, workspaceId, subflowId)
	s.Require().NoError(err)
	s.Equal(domain.SubflowStatusComplete, updated.Status)

	// Verify status change event was generated
	select {
	case event := <-eventCh:
		statusEvent, ok := event.(domain.StatusChangeEvent)
		s.Require().True(ok, "Expected StatusChangeEvent")
		s.Equal(domain.StatusChangeEventType, statusEvent.GetEventType())
		s.Equal(subflowId, statusEvent.GetParentId())
		s.Equal(string(domain.SubflowStatusComplete), statusEvent.Status)
	case err := <-errCh:
		s.Fail("Unexpected error:", err)
	case <-time.After(5 * time.Second):
		s.Fail("Timeout waiting for status change event")
	}
}

func TestDelegator(t *testing.T) {
	suite.Run(t, new(DelegatorTestSuite))
}
