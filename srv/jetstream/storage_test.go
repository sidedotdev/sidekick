package jetstream

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"sidekick/domain"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	jsapi "github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"
)

func startStorageTestServer(t *testing.T, dir string) (*server.Server, *nats.Conn) {
	t.Helper()
	s, err := server.NewServer(&server.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: dir,
	})
	require.NoError(t, err)
	s.Start()
	require.True(t, s.ReadyForConnections(5*time.Second))
	t.Cleanup(func() {
		s.Shutdown()
		s.WaitForShutdown()
	})
	nc, err := nats.Connect(s.ClientURL())
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	return s, nc
}

func requireFileConfig(t *testing.T, ctx context.Context, s *Streamer) *jsapi.StreamInfo {
	t.Helper()
	stream, err := s.js.Stream(ctx, EphemeralStreamName)
	require.NoError(t, err)
	info, err := stream.Info(ctx)
	require.NoError(t, err)
	require.Equal(t, jsapi.FileStorage, info.Config.Storage)
	require.Equal(t, jsapi.S2Compression, info.Config.Compression)
	require.Equal(t, jsapi.LimitsPolicy, info.Config.Retention)
	require.Equal(t, jsapi.DiscardOld, info.Config.Discard)
	require.EqualValues(t, 100*1024*1024, info.Config.MaxBytes)
	require.Zero(t, info.Config.MaxAge)
	require.ElementsMatch(t, []string{
		"tasks.changes.*", "flow_actions.changes.*.*", "flow_events.*.*",
	}, info.Config.Subjects)
	return info
}

func TestFileStorageRestartAndJSONRoundTrip(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dir := t.TempDir()
	server1, nc := startStorageTestServer(t, dir)
	s, err := NewStreamer(nc)
	require.NoError(t, err)
	requireFileConfig(t, ctx, s)

	task := domain.Task{WorkspaceId: "workspace", Id: "task", Title: "A task"}
	require.NoError(t, s.AddTaskChange(ctx, task))
	raw, err := json.Marshal(task)
	require.NoError(t, err)
	stream, err := s.js.Stream(ctx, EphemeralStreamName)
	require.NoError(t, err)
	msg, err := stream.GetMsg(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, raw, msg.Data)
	require.Equal(t, "tasks.changes.workspace", msg.Subject)

	nc.Close()
	server1.Shutdown()
	server1.WaitForShutdown()

	_, nc2 := startStorageTestServer(t, dir)
	s, err = NewStreamer(nc2)
	require.NoError(t, err)
	info := requireFileConfig(t, ctx, s)
	require.EqualValues(t, 1, info.State.Msgs)
	require.EqualValues(t, 1, info.State.LastSeq)

	// Repeated startup must retain messages and their replay sequence numbers.
	require.NoError(t, s.Initialize(nc2))
	tasks, errs := s.StreamTaskChanges(ctx, task.WorkspaceId, "0")
	select {
	case received := <-tasks:
		require.Equal(t, task.Id, received.Id)
		require.Equal(t, task.Title, received.Title)
	case err := <-errs:
		t.Fatalf("replay failed: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, s.AddTaskChange(ctx, task))
	select {
	case received := <-tasks:
		require.Equal(t, task.Id, received.Id)
		require.Equal(t, task.Title, received.Title)
	case err := <-errs:
		t.Fatalf("live delivery failed: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	info = requireFileConfig(t, ctx, s)
	require.EqualValues(t, 2, info.State.Msgs)
	require.EqualValues(t, 2, info.State.LastSeq)
}

func TestMemoryStorageMigrationRequiresExplicitAction(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprintf("populated=%t", populated), func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, nc := startStorageTestServer(t, t.TempDir())
			js, err := jsapi.New(nc)
			require.NoError(t, err)
			stream, err := js.CreateStream(ctx, jsapi.StreamConfig{
				Name: EphemeralStreamName, Storage: jsapi.MemoryStorage,
				Subjects: []string{"tasks.changes.*", "flow_actions.changes.*.*", "flow_events.*.*"},
				MaxBytes: 100 * 1024 * 1024, Discard: jsapi.DiscardOld,
			})
			require.NoError(t, err)
			if populated {
				_, err = js.Publish(ctx, "tasks.changes.workspace", []byte(`{"id":"preserve-me"}`))
				require.NoError(t, err)
			}
			_, err = NewStreamer(nc)
			require.ErrorContains(t, err, "can not change storage type")
			info, err := stream.Info(ctx)
			require.NoError(t, err)
			require.Equal(t, jsapi.MemoryStorage, info.Config.Storage)
			if populated {
				require.EqualValues(t, 1, info.State.Msgs)
				msg, err := stream.GetMsg(ctx, 1)
				require.NoError(t, err)
				require.Equal(t, []byte(`{"id":"preserve-me"}`), msg.Data)
			} else {
				require.Zero(t, info.State.Msgs)
			}

			// Explicit deletion is safe here because the test has no other publishers.
			require.NoError(t, js.DeleteStream(ctx, EphemeralStreamName))
			s, err := NewStreamer(nc)
			require.NoError(t, err)
			require.Zero(t, requireFileConfig(t, ctx, s).State.Msgs)
		})
	}
}

func TestExistingFileStorageIsUpgradedWithoutDeletingData(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, nc := startStorageTestServer(t, t.TempDir())
	js, err := jsapi.New(nc)
	require.NoError(t, err)
	_, err = js.CreateStream(ctx, jsapi.StreamConfig{
		Name: EphemeralStreamName, Storage: jsapi.FileStorage,
		Subjects: []string{"tasks.changes.*"},
	})
	require.NoError(t, err)
	_, err = js.Publish(ctx, "tasks.changes.workspace", []byte(`{"id":"keep"}`))
	require.NoError(t, err)
	s, err := NewStreamer(nc)
	require.NoError(t, err)
	info := requireFileConfig(t, ctx, s)
	require.EqualValues(t, 1, info.State.Msgs)
	stream, err := js.Stream(ctx, EphemeralStreamName)
	require.NoError(t, err)
	msg, err := stream.GetMsg(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []byte(`{"id":"keep"}`), msg.Data)
}

func TestFileStorageRetentionAfterRestart(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dir := t.TempDir()
	server1, nc := startStorageTestServer(t, dir)
	s, err := NewStreamer(nc)
	require.NoError(t, err)
	payload := []byte(`{"text":"` + strings.Repeat("x", 512*1024) + `"}`)
	publish := func(s *Streamer, count int) {
		t.Helper()
		for range count {
			_, err := s.js.Publish(ctx, "flow_events.workspace.parent", payload)
			require.NoError(t, err)
		}
	}
	publish(s, 210)
	before := requireFileConfig(t, ctx, s)
	require.LessOrEqual(t, before.State.Bytes, uint64(100*1024*1024))
	require.Greater(t, before.State.FirstSeq, uint64(1))
	require.Less(t, before.State.Msgs, uint64(210))

	nc.Close()
	server1.Shutdown()
	server1.WaitForShutdown()
	_, nc2 := startStorageTestServer(t, dir)
	s, err = NewStreamer(nc2)
	require.NoError(t, err)
	restored := requireFileConfig(t, ctx, s)
	require.Equal(t, before.State.FirstSeq, restored.State.FirstSeq)
	require.Equal(t, before.State.LastSeq, restored.State.LastSeq)
	require.Equal(t, before.State.Msgs, restored.State.Msgs)
	stream, err := s.js.Stream(ctx, EphemeralStreamName)
	require.NoError(t, err)
	msg, err := stream.GetMsg(ctx, restored.State.FirstSeq)
	require.NoError(t, err)
	require.Equal(t, payload, msg.Data)
	publish(s, 10)
	after := requireFileConfig(t, ctx, s)
	require.LessOrEqual(t, after.State.Bytes, uint64(100*1024*1024))
	require.Greater(t, after.State.FirstSeq, before.State.FirstSeq)
	require.Equal(t, before.State.LastSeq+10, after.State.LastSeq)
}

func TestLegacyMemoryServerRestartAllowsFreshFileStream(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dir := t.TempDir()
	oldServer, nc := startStorageTestServer(t, dir)
	js, err := jsapi.New(nc)
	require.NoError(t, err)
	_, err = js.CreateStream(ctx, jsapi.StreamConfig{
		Name: EphemeralStreamName, Storage: jsapi.MemoryStorage,
		Subjects: []string{"tasks.changes.*"},
	})
	require.NoError(t, err)
	_, err = js.Publish(ctx, "tasks.changes.workspace", []byte(`{"id":"old"}`))
	require.NoError(t, err)
	nc.Close()
	oldServer.Shutdown()
	oldServer.WaitForShutdown()

	_, nc = startStorageTestServer(t, dir)
	s, err := NewStreamer(nc)
	require.NoError(t, err)
	info := requireFileConfig(t, ctx, s)
	require.Zero(t, info.State.Msgs)
}
