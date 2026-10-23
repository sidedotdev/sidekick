// inspect_codec_payload decodes payloads that common.PayloadCodec offloaded to
// kv and summarizes where their bytes are, to trace which workflow code
// produced them.
//
// Usage: go run ./scripts/inspect_codec_payload [-top N] [-preview N] codec/<ksuid> ...
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rs/zerolog/log"
	"github.com/segmentio/ksuid"
	commonpb "go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/proto"

	"sidekick/common"
	"sidekick/srv/sqlite"

	_ "modernc.org/sqlite"
)

const (
	codecWorkspaceID = "__temporal_codec"
	codecKeyPrefix   = "codec/"
)

func main() {
	dataHome, err := common.GetSidekickDataHome()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to resolve sidekick data home")
	}
	flag.StringVar(&dataHome, "data-home", dataHome, "Sidekick data home containing the kv database")
	top := flag.Int("top", 15, "Number of JSON paths to show, largest first")
	previewChars := flag.Int("preview", 300, "Characters of sample value and raw data to show")
	scan := flag.Bool("scan", false, "Classify codec entries in a key range instead of inspecting individual keys")
	var opts scanOptions
	flag.StringVar(&opts.From, "from", "", "With -scan: first codec key (inclusive) of the range")
	flag.StringVar(&opts.To, "to", "", "With -scan: codec key ending the range (exclusive); defaults to the last key")
	flag.StringVar(&opts.Contains, "contains", "", "With -scan: byte string to look for in each stored payload")
	flag.Int64Var(&opts.SampleEvery, "sample-every", 1, "With -scan: only examine rows whose rowid is a multiple of this")
	flag.StringVar(&opts.OutCSV, "out", "", "With -scan: also write per-entry results to this CSV")
	flag.StringVar(&opts.KeysFile, "keys-file", "", "With -scan: scan the codec keys listed one per line in this file instead of a key range")
	flag.Parse()
	if !*scan && flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}

	path := filepath.Join(dataHome, sqlite.KVDatabaseFileName)
	db, err := sql.Open("sqlite", "file:"+(&url.URL{Path: path}).EscapedPath()+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		log.Fatal().Err(err).Str("path", path).Msg("failed to open kv database")
	}
	defer db.Close()

	if *scan {
		if err := scanRange(context.Background(), db, opts); err != nil {
			log.Fatal().Err(err).Msg("scan failed")
		}
		return
	}

	failed := false
	for _, key := range flag.Args() {
		if !strings.HasPrefix(key, codecKeyPrefix) {
			key = codecKeyPrefix + key
		}
		if err := inspect(context.Background(), db, key, *top, *previewChars); err != nil {
			log.Error().Err(err).Str("key", key).Msg("inspect failed")
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func inspect(ctx context.Context, db *sql.DB, key string, top, previewChars int) error {
	var raw []byte
	err := db.QueryRowContext(ctx, `SELECT value FROM kv WHERE workspace_id = ? AND key = ?`, codecWorkspaceID, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("not found in kv")
	}
	if err != nil {
		return err
	}
	payload := &commonpb.Payload{}
	if err := proto.Unmarshal(raw, payload); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	fmt.Printf("== %s\n", key)
	if id, err := ksuid.Parse(strings.TrimPrefix(key, codecKeyPrefix)); err == nil {
		fmt.Printf("stored at:  %s\n", id.Time().UTC().Format(time.RFC3339))
	}
	fmt.Printf("kv bytes:   %d\ndata bytes: %d\n", len(raw), len(payload.GetData()))
	metaKeys := make([]string, 0, len(payload.GetMetadata()))
	for k := range payload.GetMetadata() {
		metaKeys = append(metaKeys, k)
	}
	sort.Strings(metaKeys)
	for _, k := range metaKeys {
		fmt.Printf("metadata:   %s=%s\n", k, truncate(string(payload.GetMetadata()[k]), previewChars))
	}

	paths, err := summarizeJSON(payload.GetData())
	if err != nil {
		fmt.Printf("not JSON (%v); raw prefix:\n%s\n\n", err, truncate(string(payload.GetData()), previewChars))
		return nil
	}
	fmt.Printf("%-12s %-8s %s\n", "bytes", "count", "path (sample)")
	for i, p := range paths {
		if i == top {
			fmt.Printf("... %d more paths\n", len(paths)-top)
			break
		}
		fmt.Printf("%-12d %-8d %s  %s\n", p.Bytes, p.Count, p.Path, truncate(p.Sample, previewChars))
	}
	fmt.Println()
	return nil
}

type pathSize struct {
	Path   string
	Bytes  int64
	Count  int64
	Sample string
}

// summarizeJSON totals the encoded size of leaf values per path, with array
// indices collapsed to "[]" so repeated elements aggregate. The sample is the
// largest value seen at each path. Paths are returned largest first.
func summarizeJSON(data []byte) ([]pathSize, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return nil, err
	}

	byPath := map[string]*pathSize{}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, child := range v {
				walk(path+"."+k, child)
			}
			return
		case []any:
			for _, child := range v {
				walk(path+"[]", child)
			}
			return
		}
		encoded, err := json.Marshal(v)
		if err != nil {
			panic(fmt.Sprintf("re-marshal of decoded JSON value at %s: %v", path, err))
		}
		size := int64(len(encoded))
		p := byPath[path]
		if p == nil {
			p = &pathSize{Path: path}
			byPath[path] = p
		}
		p.Bytes += size
		p.Count++
		if s, ok := v.(string); ok && int64(len(s)) >= int64(len(p.Sample)) {
			p.Sample = s
		} else if !ok && p.Sample == "" {
			p.Sample = string(encoded)
		}
	}
	walk("$", root)

	paths := make([]pathSize, 0, len(byPath))
	for _, p := range byPath {
		paths = append(paths, *p)
	}
	sort.Slice(paths, func(i, j int) bool {
		if paths[i].Bytes != paths[j].Bytes {
			return paths[i].Bytes > paths[j].Bytes
		}
		return paths[i].Path < paths[j].Path
	})
	return paths, nil
}

func truncate(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("… (+%d bytes)", len(s)-cut)
}
