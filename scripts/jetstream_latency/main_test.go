package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/require"
)

func TestLatencySummary(t *testing.T) {
	t.Parallel()
	values := make([]time.Duration, 100)
	for i := range values {
		values[i] = time.Duration(100-i) * time.Millisecond
	}
	stats, err := summarizeLatencies(values)
	require.NoError(t, err)
	require.Equal(t, 100, stats.Samples)
	require.Equal(t, 50.0, stats.P50Ms)
	require.Equal(t, 95.0, stats.P95Ms)
	require.Equal(t, 99.0, stats.P99Ms)
	require.Equal(t, 100.0, stats.MaxMs)
	_, err = summarizeLatencies(nil)
	require.Error(t, err)
	_, err = summarizeLatencies([]time.Duration{-time.Second})
	require.Error(t, err)
}

func TestInspectStore(t *testing.T) {
	for _, mode := range []string{"none", "s2"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			msgDir := filepath.Join(dir, "jetstream", "$G", "streams", streamName, "msgs")
			require.NoError(t, os.MkdirAll(msgDir, 0700))
			raw := []byte(strings.Repeat("a log line\n", 1000))
			block := raw
			if mode == "s2" {
				compressed, err := server.S2Compression.Compress(raw)
				require.NoError(t, err)
				meta := server.CompressionInfo{
					Algorithm: server.S2Compression, OriginalSize: uint64(len(raw)),
				}
				block = append(meta.MarshalMetadata(), compressed...)
			}
			path := filepath.Join(msgDir, "1.blk")
			require.NoError(t, os.WriteFile(path, block, 0600))
			require.NoError(t, os.WriteFile(filepath.Join(msgDir, "2.blk"), raw, 0600))
			stats, err := inspectStore(dir, mode)
			require.NoError(t, err)
			require.Equal(t, 2, stats.Blocks)
			require.EqualValues(t, 2*len(raw), stats.ExpandedBlockBytes)
			require.EqualValues(t, len(raw)+len(block), stats.BlockBytes)
			if mode == "s2" {
				require.Equal(t, 1, stats.CompressedBlocks)
				require.Less(t, stats.BlockBytes, stats.ExpandedBlockBytes)
				require.NoError(t, os.WriteFile(path, block[:len(block)/2], 0600))
				_, err = inspectStore(dir, mode)
				require.Error(t, err)
			} else {
				require.Zero(t, stats.CompressedBlocks)
				_, err = inspectStore(dir, "s2")
				require.ErrorContains(t, err, "no sealed S2 blocks")
			}
		})
	}
}

func TestProbeRoundTripAndNativeCompression(t *testing.T) {
	for _, mode := range []string{"none", "s2"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cfg := probeConfig{
				Messages: 2048, Warmup: 16, BodyBytes: 16384,
				Workers: 8, BurstSize: 128,
			}
			report, err := runTrial(ctx, cfg, mode, "burst", t.TempDir())
			require.NoError(t, err)
			require.Equal(t, cfg.Messages, report.PublishAck.Samples)
			require.Equal(t, cfg.Messages, report.Delivery.Samples)
			require.EqualValues(t, cfg.Messages+cfg.Warmup, report.RetainedMessages)
			require.Greater(t, report.Disk.Blocks, 1)
			if mode == "s2" {
				require.Positive(t, report.Disk.CompressedBlocks)
				require.Less(t, report.Disk.BlockBytes, report.Disk.ExpandedBlockBytes)
			} else {
				require.Zero(t, report.Disk.CompressedBlocks)
			}
		})
	}
}

func TestProbeRejectsInsufficientRollover(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := runTrial(ctx, probeConfig{
		Messages: 1, BodyBytes: 100, Workers: 1, BurstSize: 1,
	}, "s2", "burst", t.TempDir())
	require.ErrorContains(t, err, "rollover")
}
