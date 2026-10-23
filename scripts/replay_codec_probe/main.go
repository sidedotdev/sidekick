// replay_codec_probe replays a workflow history through the production data
// converter and reports which payloads the payload codec stores while
// replaying, matched by content to the large payloads already in the history.
// Codec writes go to an in-memory overlay, so the kv database is not modified.
//
// Usage: go run ./scripts/replay_codec_probe [-run-id RUN] [-list N] <workflow-id>
package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/history/v1"
	"go.temporal.io/api/proxy"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"google.golang.org/protobuf/proto"

	"sidekick"
	"sidekick/common"
	"sidekick/utils"
	sidekick_worker "sidekick/worker"
)

// Mirror the unexported constants in common/payload_codec.go.
const (
	codecWorkspaceID = "__temporal_codec"
	codecMetadataKey = "sidekick-payload-codec-key"
)

func main() {
	runID := flag.String("run-id", "", "Workflow run to replay; defaults to the latest run")
	listLimit := flag.Int("list", 30, "Max distinct stored payloads to list individually")
	compareKeys := flag.String("compare", "", "Comma-separated existing codec keys to check for content identical to a replay write")
	dumpDir := flag.String("dump-dir", "", "Directory to write the data of each replay write and each large history payload, for diffing")
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	workflowID := flag.Arg(0)
	ctx := context.Background()

	storage, err := sidekick.GetStorage()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize storage")
	}
	clientOptions, err := common.NewTemporalClientOptions(storage, common.GetTemporalServerHostPort())
	if err != nil {
		log.Fatal().Err(err).Msg("failed to build temporal client options")
	}
	c, err := client.Dial(clientOptions)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to dial temporal")
	}
	defer c.Close()

	hist, err := fetchHistory(ctx, c, workflowID, *runID)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to fetch history")
	}
	index, err := indexLargeHistoryPayloads(ctx, storage, hist)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to index history payloads")
	}

	recording := newRecordingStorage(storage)
	replayClientOptions, err := common.NewTemporalClientOptions(recording, "")
	if err != nil {
		log.Fatal().Err(err).Msg("failed to build replay data converter")
	}
	replayerOptions := utils.TestReplayerOptions()
	replayerOptions.DataConverter = replayClientOptions.DataConverter
	replayer, err := worker.NewWorkflowReplayerWithOptions(replayerOptions)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create replayer")
	}
	sidekick_worker.RegisterWorkflows(replayer)

	start := time.Now()
	replayErr := replayer.ReplayWorkflowHistory(nil, hist)

	fmt.Printf("workflow:  %s (run %s)\n", workflowID, firstNonEmpty(*runID, "latest"))
	fmt.Printf("history:   %d events, %d large payload occurrences (%d distinct), %d codec refs missing from kv\n",
		len(hist.Events), index.occurrences(), len(index.refs), index.missing)
	fmt.Printf("replay:    %s, error: %v\n\n", time.Since(start).Round(time.Millisecond), replayErr)
	writes := recording.Writes()
	printReport(os.Stdout, summarize(writes, index), *listLimit)

	if *compareKeys != "" {
		if err := printComparison(ctx, storage, writes, strings.Split(*compareKeys, ",")); err != nil {
			log.Fatal().Err(err).Msg("failed to compare keys")
		}
	}
	if *dumpDir != "" {
		if err := dumpPayloads(*dumpDir, writes, index); err != nil {
			log.Fatal().Err(err).Msg("failed to dump payloads")
		}
		fmt.Printf("\npayload data written to %s\n", *dumpDir)
	}
}

// printComparison reports, for each existing codec key, which replay writes
// carry identical content.
func printComparison(ctx context.Context, storage common.KeyValueStorage, writes []recordedWrite, keys []string) error {
	writeHashes := make([][32]byte, len(writes))
	for i, w := range writes {
		p := &commonpb.Payload{}
		if err := proto.Unmarshal(w.Raw, p); err != nil {
			return fmt.Errorf("replay write %d: %w", i, err)
		}
		hash, err := contentHash(p)
		if err != nil {
			return err
		}
		writeHashes[i] = hash
	}

	fmt.Printf("\n%-36s %-10s %s\n", "existing key", "bytes", "identical replay writes (by write index)")
	for _, key := range keys {
		key = strings.TrimSpace(key)
		p, err := resolvePayload(ctx, storage, &commonpb.Payload{Metadata: map[string][]byte{codecMetadataKey: []byte(key)}})
		if err != nil {
			fmt.Printf("%-36s %v\n", key, err)
			continue
		}
		hash, err := contentHash(p)
		if err != nil {
			return err
		}
		var matches []string
		for i, h := range writeHashes {
			if h == hash {
				matches = append(matches, fmt.Sprint(i))
			}
		}
		if len(matches) == 0 {
			matches = append(matches, "(none)")
		}
		fmt.Printf("%-36s %-10d %s\n", key, proto.Size(p), strings.Join(matches, ","))
	}
	return nil
}

func dumpPayloads(dir string, writes []recordedWrite, index historyIndex) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for i, w := range writes {
		p := &commonpb.Payload{}
		if err := proto.Unmarshal(w.Raw, p); err != nil {
			return fmt.Errorf("replay write %d: %w", i, err)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("replay_%02d.data", i)), p.GetData(), 0o644); err != nil {
			return err
		}
	}
	for _, refs := range index.refs {
		for _, ref := range refs {
			data := index.data[ref.EventID]
			name := fmt.Sprintf("history_%d_%s.data", ref.EventID, ref.EventType)
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

func fetchHistory(ctx context.Context, c client.Client, workflowID, runID string) (*history.History, error) {
	var hist history.History
	iter := c.GetWorkflowHistory(ctx, workflowID, runID, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		if err != nil {
			return nil, err
		}
		hist.Events = append(hist.Events, event)
	}
	return &hist, nil
}

type eventRef struct {
	EventID   int64
	EventType string
}

type historyIndex struct {
	refs    map[[32]byte][]eventRef
	data    map[int64][]byte
	missing int
}

func (h historyIndex) occurrences() int {
	n := 0
	for _, refs := range h.refs {
		n += len(refs)
	}
	return n
}

// indexLargeHistoryPayloads maps the content of every history payload that
// exceeds the codec threshold to the events carrying it, resolving codec
// references through storage.
func indexLargeHistoryPayloads(ctx context.Context, storage common.KeyValueStorage, hist *history.History) (historyIndex, error) {
	index := historyIndex{refs: map[[32]byte][]eventRef{}, data: map[int64][]byte{}}
	for _, event := range hist.Events {
		ref := eventRef{EventID: event.GetEventId(), EventType: strings.TrimPrefix(event.GetEventType().String(), "EventType")}
		err := proxy.VisitPayloads(ctx, event, proxy.VisitPayloadsOptions{
			SkipSearchAttributes: true,
			Visitor: func(_ *proxy.VisitPayloadsContext, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
				for _, p := range payloads {
					resolved, err := resolvePayload(ctx, storage, p)
					if errors.Is(err, common.ErrCodecPayloadMissing) {
						index.missing++
						continue
					}
					if err != nil {
						return nil, err
					}
					if proto.Size(resolved) <= common.DefaultCodecThreshold {
						continue
					}
					hash, err := contentHash(resolved)
					if err != nil {
						return nil, err
					}
					index.refs[hash] = append(index.refs[hash], ref)
					index.data[ref.EventID] = resolved.GetData()
				}
				return payloads, nil
			},
		})
		if err != nil {
			return index, fmt.Errorf("event %d: %w", event.GetEventId(), err)
		}
	}
	return index, nil
}

func resolvePayload(ctx context.Context, storage common.KeyValueStorage, p *commonpb.Payload) (*commonpb.Payload, error) {
	key, ok := p.GetMetadata()[codecMetadataKey]
	if !ok {
		return p, nil
	}
	values, err := storage.MGet(ctx, codecWorkspaceID, []string{string(key)})
	if err != nil {
		return nil, err
	}
	if values[0] == nil {
		return nil, fmt.Errorf("%w: %s", common.ErrCodecPayloadMissing, key)
	}
	resolved := &commonpb.Payload{}
	if err := proto.Unmarshal(values[0], resolved); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", key, err)
	}
	return resolved, nil
}

// contentHash hashes a deterministic encoding, since payload metadata is a
// map whose default wire order is unspecified.
func contentHash(p *commonpb.Payload) ([32]byte, error) {
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(p)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(raw), nil
}

type recordedWrite struct {
	Key string
	Raw []byte
}

// recordingStorage captures writes in memory while serving reads from the
// overlay first and the wrapped storage otherwise.
type recordingStorage struct {
	common.KeyValueStorage

	mu      sync.Mutex
	overlay map[string]map[string][]byte
	writes  []recordedWrite
}

func newRecordingStorage(backing common.KeyValueStorage) *recordingStorage {
	return &recordingStorage{KeyValueStorage: backing, overlay: map[string]map[string][]byte{}}
}

func (s *recordingStorage) MSetRaw(_ context.Context, workspaceID string, values map[string][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.overlay[workspaceID] == nil {
		s.overlay[workspaceID] = map[string][]byte{}
	}
	for k, v := range values {
		s.overlay[workspaceID][k] = v
		s.writes = append(s.writes, recordedWrite{Key: k, Raw: v})
	}
	return nil
}

func (s *recordingStorage) MSet(context.Context, string, map[string]interface{}) error {
	return errors.New("recordingStorage: MSet is not supported")
}

func (s *recordingStorage) MDelete(context.Context, string, []string) error {
	return errors.New("recordingStorage: MDelete is not supported")
}

func (s *recordingStorage) DeletePrefix(context.Context, string, string) error {
	return errors.New("recordingStorage: DeletePrefix is not supported")
}

func (s *recordingStorage) MGet(ctx context.Context, workspaceID string, keys []string) ([][]byte, error) {
	values, err := s.KeyValueStorage.MGet(ctx, workspaceID, keys)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, k := range keys {
		if v, ok := s.overlay[workspaceID][k]; ok {
			values[i] = v
		}
	}
	return values, nil
}

func (s *recordingStorage) Writes() []recordedWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedWrite(nil), s.writes...)
}

type storedContent struct {
	Bytes  int
	Writes int
	Events []eventRef
}

type eventTypeCoverage struct {
	EventType   string
	Occurrences int
	Restored    int
}

type report struct {
	Writes     int
	Bytes      int64
	Unmatched  int
	Contents   []storedContent
	Coverage   []eventTypeCoverage
	DecodeErrs int
}

// summarize groups replay writes by content and, per history event type,
// counts how many large payload occurrences were stored again.
func summarize(writes []recordedWrite, index historyIndex) report {
	var r report
	byHash := map[[32]byte]*storedContent{}
	var order [][32]byte
	for _, w := range writes {
		r.Writes++
		r.Bytes += int64(len(w.Raw))
		p := &commonpb.Payload{}
		if err := proto.Unmarshal(w.Raw, p); err != nil {
			r.DecodeErrs++
			continue
		}
		hash, err := contentHash(p)
		if err != nil {
			r.DecodeErrs++
			continue
		}
		content := byHash[hash]
		if content == nil {
			content = &storedContent{Bytes: len(w.Raw), Events: index.refs[hash]}
			byHash[hash] = content
			order = append(order, hash)
		}
		content.Writes++
		if len(content.Events) == 0 {
			r.Unmatched++
		}
	}
	for _, hash := range order {
		r.Contents = append(r.Contents, *byHash[hash])
	}

	coverage := map[string]*eventTypeCoverage{}
	for hash, refs := range index.refs {
		_, restored := byHash[hash]
		for _, ref := range refs {
			c := coverage[ref.EventType]
			if c == nil {
				c = &eventTypeCoverage{EventType: ref.EventType}
				coverage[ref.EventType] = c
			}
			c.Occurrences++
			if restored {
				c.Restored++
			}
		}
	}
	for _, c := range coverage {
		r.Coverage = append(r.Coverage, *c)
	}
	sort.Slice(r.Coverage, func(i, j int) bool { return r.Coverage[i].EventType < r.Coverage[j].EventType })
	return r
}

func printReport(out *os.File, r report, listLimit int) {
	fmt.Fprintf(out, "codec writes during replay: %d (%d bytes, %d distinct contents, %d writes match no history payload, %d undecodable)\n\n",
		r.Writes, r.Bytes, len(r.Contents), r.Unmatched, r.DecodeErrs)

	fmt.Fprintf(out, "%-45s %-12s %s\n", "history event type", "large", "stored again during replay")
	for _, c := range r.Coverage {
		fmt.Fprintf(out, "%-45s %-12d %d\n", c.EventType, c.Occurrences, c.Restored)
	}
	fmt.Fprintln(out)

	fmt.Fprintf(out, "%-8s %-10s %s\n", "writes", "bytes", "history events with identical content")
	for i, c := range r.Contents {
		if i == listLimit {
			fmt.Fprintf(out, "... %d more\n", len(r.Contents)-listLimit)
			break
		}
		events := make([]string, 0, len(c.Events))
		for _, e := range c.Events {
			events = append(events, fmt.Sprintf("%d %s", e.EventID, e.EventType))
		}
		if len(events) == 0 {
			events = append(events, "(none)")
		}
		fmt.Fprintf(out, "%-8d %-10d %s\n", c.Writes, c.Bytes, strings.Join(events, ", "))
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
