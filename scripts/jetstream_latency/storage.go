package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nats-io/nats-server/v2/server"
)

type diskStats struct {
	FileBytes          int64 `json:"fileBytes"`
	BlockBytes         int64 `json:"blockBytes"`
	ExpandedBlockBytes int64 `json:"expandedBlockBytes"`
	Blocks             int   `json:"blocks"`
	CompressedBlocks   int   `json:"compressedBlocks"`
}

func inspectStore(dir, mode string) (diskStats, error) {
	var stats diskStats
	type block struct {
		path  string
		index uint64
	}
	var blocks []block
	var last uint64
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		stats.FileBytes += info.Size()
		if filepath.Ext(path) != ".blk" || filepath.Base(filepath.Dir(path)) != "msgs" ||
			filepath.Base(filepath.Dir(filepath.Dir(path))) != streamName {
			return nil
		}
		index, err := strconv.ParseUint(strings.TrimSuffix(entry.Name(), ".blk"), 10, 64)
		if err != nil {
			return fmt.Errorf("parse block index %s: %w", path, err)
		}
		blocks = append(blocks, block{path: path, index: index})
		last = max(last, index)
		return nil
	})
	if err != nil {
		return stats, err
	}
	if len(blocks) < 2 {
		return stats, fmt.Errorf("no block rollover: publish more/larger messages (found %d blocks)", len(blocks))
	}
	for _, block := range blocks {
		data, err := os.ReadFile(block.path)
		if err != nil {
			return stats, err
		}
		stats.Blocks++
		stats.BlockBytes += int64(len(data))
		var meta server.CompressionInfo
		offset, err := meta.UnmarshalMetadata(data)
		if err != nil {
			return stats, fmt.Errorf("%s: %w", block.path, err)
		}
		switch meta.Algorithm {
		case server.NoCompression:
			stats.ExpandedBlockBytes += int64(len(data))
		case server.S2Compression:
			if block.index == last {
				return stats, fmt.Errorf("unexpected compressed active block %s", block.path)
			}
			decoded, err := meta.Algorithm.Decompress(data[offset:])
			if err != nil {
				return stats, fmt.Errorf("decode %s: %w", block.path, err)
			}
			if uint64(len(decoded)) != meta.OriginalSize {
				return stats, fmt.Errorf("decoded block size mismatch: %s", block.path)
			}
			stats.ExpandedBlockBytes += int64(len(decoded))
			stats.CompressedBlocks++
		default:
			return stats, fmt.Errorf("unknown compression in %s: %v", block.path, meta.Algorithm)
		}
	}
	switch mode {
	case "s2":
		if stats.CompressedBlocks == 0 {
			return stats, fmt.Errorf("no sealed S2 blocks: compression was not exercised")
		}
	case "none":
		if stats.CompressedBlocks != 0 {
			return stats, fmt.Errorf("uncompressed control contains compressed blocks")
		}
	default:
		return stats, fmt.Errorf("unknown compression %q", mode)
	}
	return stats, nil
}
