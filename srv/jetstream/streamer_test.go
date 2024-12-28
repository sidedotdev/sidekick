package jetstream

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/nats"
	"testing"
	"time"

	natspkg "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/suite"
)

type StreamerTestSuite struct {
	suite.Suite
	server   *nats.Server
	nc       *natspkg.Conn
	streamer *Streamer
}

const TestNatsServerPort = 28866

func (s *StreamerTestSuite) SetupSuite() {
	var err error

	// Create test server with unique domain and port
	s.server, err = nats.NewTestServer(nats.ServerOptions{
		Port:            TestNatsServerPort,
		JetStreamDomain: "sidekick_test",
		StoreDir:        s.T().TempDir(),
	})
	s.Require().NoError(err)
	s.Require().NoError(s.server.Start(context.Background()))

	// Connect to server
	s.nc, err = natspkg.Connect(fmt.Sprintf("nats://%s:%d", common.GetNatsServerHost(), TestNatsServerPort))
	s.Require().NoError(err)

	// Create streamer
	s.streamer, err = NewStreamer(s.nc)
	s.Require().NoError(err)
}

func (s *StreamerTestSuite) TearDownSuite() {
	if s.nc != nil {
		s.nc.Close()
	}
	// The Lame Duck mode stop seems to hang the test suite, so we'll skip it for now
	// if s.server != nil {
	// 	s.server.Stop()
	// }
}

func (s *StreamerTestSuite) TestTaskStreaming() {
	ctx := context.Background()
	workspaceId := "test-workspace"

	// Create test task
	task := domain.Task{
		WorkspaceId: workspaceId,
		Id:          "test-task-1",
		Title:       "Test Task",
		Description: "Test Description",
		Status:      domain.TaskStatusToDo,
		AgentType:   domain.AgentTypeLLM,
		FlowType:    domain.FlowTypeBasicDev,
		Created:     time.Now(),
		Updated:     time.Now(),
		FlowOptions: map[string]interface{}{"test": "value"},
	}

	// Test adding task change
	err := s.streamer.AddTaskChange(ctx, task)
	s.Require().NoError(err)

	// Test getting task changes
	tasks, lastId, err := s.streamer.GetTaskChanges(ctx, workspaceId, "", 10, time.Second)
	s.Require().NoError(err)
	s.Require().Len(tasks, 1)
	s.Equal(task.Id, tasks[0].Id)
	s.Equal(task.Title, tasks[0].Title)
	s.Equal(task.Description, tasks[0].Description)
	s.Equal(task.Status, tasks[0].Status)
	s.Equal(task.AgentType, tasks[0].AgentType)
	s.Equal(task.FlowType, tasks[0].FlowType)
	s.Equal(task.FlowOptions, tasks[0].FlowOptions)
	s.NotEmpty(lastId)
	fmt.Printf("Last ID: %s\n", lastId)

	// Test getting changes with no new messages
	tasks, newLastId, err := s.streamer.GetTaskChanges(ctx, workspaceId, lastId, 10, time.Millisecond)
	s.Require().NoError(err)
	s.Empty(tasks)
	s.Equal(lastId, newLastId)

	// Test getting changes with no new messages and no wait
	tasks, newLastId, err = s.streamer.GetTaskChanges(ctx, workspaceId, lastId, 10, 0)
	s.Require().NoError(err)
	s.Empty(tasks)
	s.Equal(lastId, newLastId)
}

func TestStreamerSuite(t *testing.T) {
	suite.Run(t, new(StreamerTestSuite))
}
