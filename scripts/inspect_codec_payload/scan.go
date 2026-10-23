package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/segmentio/ksuid"
	commonpb "go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/proto"
)

type scanOptions struct {
	From        string
	To          string
	Contains    string
	SampleEvery int64
	OutCSV      string
	// KeysFile lists codec keys (one per line) to scan instead of a key range.
	KeysFile string
}

type dayStats struct {
	Entries       int64
	Bytes         int64
	Matches       int64
	MatchingBytes int64
}

// scanRange classifies codec entries with keys in [From, To) (or listed in
// KeysFile) by whether their stored bytes contain opts.Contains. KSUID keys
// sort by creation time, so a key range is a time range.
func scanRange(ctx context.Context, db *sql.DB, opts scanOptions) error {
	if opts.Contains == "" {
		return fmt.Errorf("-contains is required with -scan")
	}
	if opts.SampleEvery < 1 {
		opts.SampleEvery = 1
	}

	var out *csv.Writer
	if opts.OutCSV != "" {
		f, err := os.Create(opts.OutCSV)
		if err != nil {
			return err
		}
		defer f.Close()
		out = csv.NewWriter(f)
		defer out.Flush()
		if err := out.Write([]string{"codec_key", "at_utc", "bytes", "matches", "sha256", "content_sha256"}); err != nil {
			return err
		}
	}

	needle := []byte(opts.Contains)
	byDay := map[string]*dayStats{}
	visit := func(key string, value []byte) error {
		size := int64(len(value))
		matches := bytes.Contains(value, needle)
		at := keyTime(key)
		day := at.Format(time.DateOnly)
		s := byDay[day]
		if s == nil {
			s = &dayStats{}
			byDay[day] = s
		}
		s.Entries++
		s.Bytes += size
		if matches {
			s.Matches++
			s.MatchingBytes += size
		}
		if out != nil {
			sum := sha256.Sum256(value)
			contentSum, err := canonicalPayloadHash(value)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			record := []string{key, at.Format(time.DateTime), strconv.FormatInt(size, 10), strconv.FormatBool(matches),
				hex.EncodeToString(sum[:]), hex.EncodeToString(contentSum[:])}
			if err := out.Write(record); err != nil {
				return err
			}
		}
		return nil
	}

	var err error
	if opts.KeysFile != "" {
		err = scanListedKeys(ctx, db, opts.KeysFile, visit)
	} else {
		err = scanKeyRange(ctx, db, opts, visit)
	}
	if err != nil {
		return err
	}

	days := make([]string, 0, len(byDay))
	for day := range byDay {
		days = append(days, day)
	}
	sort.Strings(days)
	fmt.Printf("entries containing %q (sampling 1 in %d by rowid)\n", opts.Contains, opts.SampleEvery)
	fmt.Printf("%-12s %-10s %-14s %-10s %s\n", "day", "entries", "bytes", "matches", "matching bytes")
	var total dayStats
	for _, day := range days {
		s := byDay[day]
		fmt.Printf("%-12s %-10d %-14d %-10d %d\n", day, s.Entries, s.Bytes, s.Matches, s.MatchingBytes)
		total.Entries += s.Entries
		total.Bytes += s.Bytes
		total.Matches += s.Matches
		total.MatchingBytes += s.MatchingBytes
	}
	fmt.Printf("%-12s %-10d %-14d %-10d %d\n", "total", total.Entries, total.Bytes, total.Matches, total.MatchingBytes)
	return nil
}

func scanKeyRange(ctx context.Context, db *sql.DB, opts scanOptions, visit func(key string, value []byte) error) error {
	to := opts.To
	if to == "" {
		to = codecKeyPrefix + "\U0010FFFF"
	}
	rows, err := db.QueryContext(ctx, `
		SELECT key, value
		FROM kv
		WHERE workspace_id = ? AND key >= ? AND key < ? AND rowid % ? = 0
		ORDER BY key`,
		codecWorkspaceID, codecKeyPrefix+strings.TrimPrefix(opts.From, codecKeyPrefix), to, opts.SampleEvery)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			key   string
			value []byte
		)
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		if err := visit(key, value); err != nil {
			return err
		}
	}
	return rows.Err()
}

func scanListedKeys(ctx context.Context, db *sql.DB, keysFile string, visit func(key string, value []byte) error) error {
	raw, err := os.ReadFile(keysFile)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key := strings.TrimSpace(line)
		if key == "" {
			continue
		}
		var value []byte
		err := db.QueryRowContext(ctx, `SELECT value FROM kv WHERE workspace_id = ? AND key = ?`, codecWorkspaceID, key).Scan(&value)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		if err := visit(key, value); err != nil {
			return err
		}
	}
	return nil
}

// canonicalPayloadHash hashes the payload in deterministic protobuf encoding,
// since the stored raw bytes may order metadata map entries differently for
// identical payloads.
func canonicalPayloadHash(raw []byte) ([32]byte, error) {
	p := &commonpb.Payload{}
	if err := proto.Unmarshal(raw, p); err != nil {
		return [32]byte{}, err
	}
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(p)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

func keyTime(key string) time.Time {
	id, err := ksuid.Parse(strings.TrimPrefix(key, codecKeyPrefix))
	if err != nil {
		return time.Time{}
	}
	return id.Time().UTC()
}
