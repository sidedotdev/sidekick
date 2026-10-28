package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"golang.org/x/sync/errgroup"
)

const streamName = "SIDEKICK_EPHEMERAL_V2"

type trialReport struct {
	Run              int          `json:"run"`
	Compression      string       `json:"compression"`
	Traffic          string       `json:"traffic"`
	Config           probeConfig  `json:"config"`
	ServerVersion    string       `json:"serverVersion"`
	PublishAck       latencyStats `json:"publishAck"`
	Delivery         latencyStats `json:"delivery"`
	ElapsedSeconds   float64      `json:"elapsedSeconds"`
	MessagesPerSec   float64      `json:"messagesPerSec"`
	RetainedMessages uint64       `json:"retainedMessages"`
	LogicalBytes     uint64       `json:"logicalBytes"`
	Disk             diskStats    `json:"disk"`
	Memory           memoryStats  `json:"memory"`
}

type probeMessage struct {
	ID   int    `json:"id"`
	Body string `json:"body"`
}

func runTrial(ctx context.Context, cfg probeConfig, mode, traffic, dir string) (trialReport, error) {
	result := trialReport{Compression: mode, Traffic: traffic, Config: cfg, ServerVersion: server.VERSION}
	if err := cfg.validate(); err != nil {
		return result, err
	}
	compression := jetstream.NoCompression
	switch mode {
	case "none":
	case "s2":
		compression = jetstream.S2Compression
	default:
		return result, fmt.Errorf("unknown compression %q", mode)
	}
	if traffic != "steady" && traffic != "burst" {
		return result, fmt.Errorf("unknown traffic %q", traffic)
	}
	srv, err := server.NewServer(&server.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: dir,
		JetStreamMaxMemory: 1024 * 1024 * 1024, JetStreamMaxStore: 1024 * 1024 * 1024,
	})
	if err != nil {
		return result, err
	}
	srv.Start()
	defer func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	}()
	if !srv.ReadyForConnections(5 * time.Second) {
		return result, fmt.Errorf("isolated server did not become ready")
	}
	nc, err := nats.Connect(srv.ClientURL(), nats.NoReconnect())
	if err != nil {
		return result, err
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return result, err
	}
	stream, err := js.CreateStream(ctx, jetstream.StreamConfig{
		Name: streamName, Subjects: []string{"tasks.changes.*", "flow_actions.changes.*.*", "flow_events.*.*"},
		Storage: jetstream.FileStorage, Compression: compression,
		MaxBytes: 100 * 1024 * 1024, Discard: jetstream.DiscardOld, Retention: jetstream.LimitsPolicy,
	})
	if err != nil {
		return result, err
	}
	consumer, err := js.OrderedConsumer(ctx, streamName, jetstream.OrderedConsumerConfig{
		FilterSubjects: []string{"tasks.changes.bench"}, DeliverPolicy: jetstream.DeliverAllPolicy,
		InactiveThreshold: time.Minute,
	})
	if err != nil {
		return result, err
	}

	total := cfg.Warmup + cfg.Messages
	starts := make([]time.Time, total)
	acks := make([]time.Duration, total)
	deliveries := make([]time.Duration, total)
	seen := make([]bool, total)
	line := "level=info step=compile file=worker.go status=ok elapsed=12ms\n"
	body := strings.Repeat(line, cfg.BodyBytes/len(line)+1)[:cfg.BodyBytes]
	var mu sync.Mutex
	received := 0
	warm := make(chan struct{})
	if cfg.Warmup == 0 {
		close(warm)
	}
	complete := make(chan struct{})
	failures := make(chan error, 1)
	fail := func(err error) {
		select {
		case failures <- err:
		default:
		}
	}
	subscription, err := consumer.Consume(func(msg jetstream.Msg) {
		arrived := time.Now()
		var payload probeMessage
		if err := json.Unmarshal(msg.Data(), &payload); err != nil {
			fail(fmt.Errorf("decode delivered JSON: %w", err))
			return
		}
		mu.Lock()
		defer mu.Unlock()
		id := payload.ID
		if id < 0 || id >= total || payload.Body != body {
			fail(fmt.Errorf("unexpected delivered payload id=%d", id))
			return
		}
		if seen[id] || starts[id].IsZero() {
			fail(fmt.Errorf("duplicate or unstarted message id=%d", id))
			return
		}
		seen[id] = true
		deliveries[id] = arrived.Sub(starts[id])
		received++
		if cfg.Warmup > 0 && received == cfg.Warmup {
			close(warm)
		}
		if received == total {
			close(complete)
		}
	}, jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, err error) {
		fail(fmt.Errorf("consumer: %w", err))
	}))
	if err != nil {
		return result, err
	}
	defer subscription.Stop()

	publish := func(id int) error {
		data, err := json.Marshal(probeMessage{ID: id, Body: body})
		if err != nil {
			return err
		}
		mu.Lock()
		starts[id] = time.Now()
		start := starts[id]
		mu.Unlock()
		_, err = js.Publish(ctx, "tasks.changes.bench", data)
		acks[id] = time.Since(start)
		return err
	}
	await := func(done <-chan struct{}) error {
		select {
		case err := <-failures:
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			select {
			case err := <-failures:
				return err
			default:
				return nil
			}
		}
	}

	stopMemory := monitorMemory()
	defer stopMemory()
	for id := 0; id < cfg.Warmup; id++ {
		if err := publish(id); err != nil {
			return result, err
		}
	}
	if err := await(warm); err != nil {
		return result, err
	}

	started := time.Now()
	if traffic == "steady" {
		for id := cfg.Warmup; id < total; id++ {
			if err := waitUntil(ctx, started.Add(time.Duration(id-cfg.Warmup)*cfg.Interval)); err != nil {
				return result, err
			}
			if err := publish(id); err != nil {
				return result, err
			}
		}
	} else {
		for first := cfg.Warmup; first < total; first += cfg.BurstSize {
			var group errgroup.Group
			group.SetLimit(cfg.Workers)
			for id := first; id < min(first+cfg.BurstSize, total); id++ {
				group.Go(func() error { return publish(id) })
			}
			if err := group.Wait(); err != nil {
				return result, err
			}
			if first+cfg.BurstSize < total {
				if err := waitUntil(ctx, time.Now().Add(cfg.BurstPause)); err != nil {
					return result, err
				}
			}
		}
	}
	if err := await(complete); err != nil {
		return result, fmt.Errorf("delivery incomplete: %w", err)
	}
	result.ElapsedSeconds = time.Since(started).Seconds()
	result.MessagesPerSec = float64(cfg.Messages) / result.ElapsedSeconds
	result.Memory = stopMemory()
	result.PublishAck, err = summarizeLatencies(acks[cfg.Warmup:])
	if err != nil {
		return result, err
	}
	mu.Lock()
	result.Delivery, err = summarizeLatencies(deliveries[cfg.Warmup:])
	mu.Unlock()
	if err != nil {
		return result, err
	}
	info, err := stream.Info(ctx)
	if err != nil {
		return result, err
	}
	result.RetainedMessages = info.State.Msgs
	result.LogicalBytes = info.State.Bytes
	subscription.Stop()
	nc.Close()
	srv.Shutdown()
	srv.WaitForShutdown()
	result.Disk, err = inspectStore(dir, mode)
	return result, err
}

func waitUntil(ctx context.Context, deadline time.Time) error {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
