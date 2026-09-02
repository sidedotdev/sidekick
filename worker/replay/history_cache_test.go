package main

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog/log"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/history/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/proto"
)

const (
	historyCacheDirName = "replay_history_cache"
	historyCacheFileExt = ".histcache"

	// historyPageTimeout bounds a single history page RPC; pages are at most
	// a few MB, so a healthy connection finishes far sooner.
	historyPageTimeout = 60 * time.Second

	// The cache directory may live on a volume shared across sandboxes and
	// branches, so entries for workflows no longer in anyone's replay set
	// must eventually go away: first by age, then by LRU size cap (cache
	// hits refresh mtimes). A full 20-workflow replay set has measured
	// around 300MB, so 1GiB comfortably fits several hosts' sets.
	historyCacheMaxAge   = 14 * 24 * time.Hour
	historyCacheMaxBytes = int64(1) << 30
)

// cachedHistory is the persisted resume state for one workflow run: all
// events except the last fetched page, plus the page token that re-fetches
// that last page. A run's history is append-only, so resuming from that token
// re-reads at most one page and then only events appended since.
type cachedHistory struct {
	events      []*history.HistoryEvent
	resumeToken []byte
}

var errHistoryResumeMismatch = errors.New("cached history prefix does not line up with resumed page")

type historyPageFetcher func(pageToken []byte) (*workflowservice.GetWorkflowExecutionHistoryResponse, error)

// fetchHistoryWithResume drains a run's history pages, starting from cached
// resume state when provided. It returns the full history along with the
// resume state to persist for the next fetch.
func fetchHistoryWithResume(fetch historyPageFetcher, cached *cachedHistory) (*history.History, *cachedHistory, error) {
	var all []*history.HistoryEvent
	var token []byte
	resuming := false
	if cached != nil {
		all = append(all, cached.events...)
		token = cached.resumeToken
		resuming = len(cached.events) > 0
	}
	for {
		resp, err := fetch(token)
		if err != nil {
			return nil, nil, err
		}
		events := resp.GetHistory().GetEvents()
		if resuming {
			wantFirst := all[len(all)-1].GetEventId() + 1
			if len(events) == 0 || events[0].GetEventId() != wantFirst {
				return nil, nil, errHistoryResumeMismatch
			}
			resuming = false
		}
		pageStart := len(all)
		all = append(all, events...)
		if len(resp.GetNextPageToken()) == 0 {
			return &history.History{Events: all},
				&cachedHistory{events: all[:pageStart], resumeToken: token},
				nil
		}
		token = resp.GetNextPageToken()
	}
}

// fetchWorkflowHistoryCached fetches the full history of one workflow run,
// resuming from persisted state when available so unchanged history prefixes
// (the bulk of the data: a full replay set fetches hundreds of MB from
// scratch) are not re-transferred through the reverse-forwarded proxy on
// every run. It reports whether the fetch resumed from cache.
func fetchWorkflowHistoryCached(ctx context.Context, svc workflowservice.WorkflowServiceClient, namespace, cacheDir, workflowID, runID string) (*history.History, bool, error) {
	fetch := func(pageToken []byte) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
		rctx, cancel := context.WithTimeout(ctx, historyPageTimeout)
		defer cancel()
		return svc.GetWorkflowExecutionHistory(rctx, &workflowservice.GetWorkflowExecutionHistoryRequest{
			Namespace:              namespace,
			Execution:              &commonpb.WorkflowExecution{WorkflowId: workflowID, RunId: runID},
			NextPageToken:          pageToken,
			HistoryEventFilterType: enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT,
		})
	}

	cached := loadCachedHistory(cacheDir, workflowID, runID)
	resumed := cached != nil && len(cached.events) > 0
	hist, updated, err := fetchHistoryWithResume(fetch, cached)
	if err != nil && resumed && !isTransientHistoryFetchErr(err) {
		// Rejected resume state (stale token after a server upgrade, a reset
		// run, ...) is recoverable: retry from scratch. Transient transport
		// errors are left to the caller's retry policy instead, since a full
		// refetch would fail the same way but cost far more.
		resumed = false
		hist, updated, err = fetchHistoryWithResume(fetch, nil)
	}
	if err != nil {
		return nil, false, err
	}
	if saveErr := saveCachedHistory(cacheDir, workflowID, runID, updated); saveErr != nil {
		log.Warn().Err(saveErr).Str("workflowId", workflowID).Msg("failed to persist replay history cache")
	}
	return hist, resumed, nil
}

func sanitizeCacheComponent(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, s)
}

func historyCacheFilePath(dir, workflowID, runID string) string {
	return filepath.Join(dir, sanitizeCacheComponent(workflowID)+"__"+sanitizeCacheComponent(runID)+historyCacheFileExt)
}

// loadCachedHistory returns nil when caching is disabled (empty dir) or the
// cache entry is absent or unreadable. The file layout is a 4-byte big-endian
// resume token length, the token, then a proto-marshaled history.History.
func loadCachedHistory(dir, workflowID, runID string) *cachedHistory {
	if dir == "" {
		return nil
	}
	path := historyCacheFilePath(dir, workflowID, runID)
	data, err := os.ReadFile(path)
	if err != nil || len(data) < 4 {
		return nil
	}
	tokenLen := int(binary.BigEndian.Uint32(data[:4]))
	if tokenLen > len(data)-4 {
		return nil
	}
	var hist history.History
	if err := proto.Unmarshal(data[4+tokenLen:], &hist); err != nil {
		return nil
	}
	// Refresh mtime so LRU pruning keeps entries that are actually in use.
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	var token []byte
	if tokenLen > 0 {
		token = append([]byte(nil), data[4:4+tokenLen]...)
	}
	return &cachedHistory{events: hist.Events, resumeToken: token}
}

// saveCachedHistory atomically persists resume state and prunes entries for
// other runs of the same workflow, which are dead weight once the workflow is
// on a new run. Cache files may live on a volume shared across sandboxes, so
// writes go through a temp file plus rename.
func saveCachedHistory(dir, workflowID, runID string, cached *cachedHistory) error {
	if dir == "" || cached == nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	histBytes, err := proto.Marshal(&history.History{Events: cached.events})
	if err != nil {
		return err
	}
	data := make([]byte, 4, 4+len(cached.resumeToken)+len(histBytes))
	binary.BigEndian.PutUint32(data[:4], uint32(len(cached.resumeToken)))
	data = append(data, cached.resumeToken...)
	data = append(data, histBytes...)

	target := historyCacheFilePath(dir, workflowID, runID)
	tmp, err := os.CreateTemp(dir, filepath.Base(target)+".tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	// A concurrent run in another sandbox may have already cached a longer
	// prefix of this append-only run; a longer prefix always serializes
	// larger, so never replace a bigger file with a smaller one. The
	// stat-then-rename race is benign: both files are valid resume states.
	if info, err := os.Stat(target); err == nil && info.Size() > int64(len(data)) {
		_ = os.Remove(tmp.Name())
		return nil
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}

	prefix := sanitizeCacheComponent(workflowID) + "__"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	base := filepath.Base(target)
	for _, entry := range entries {
		name := entry.Name()
		if name != base && strings.HasPrefix(name, prefix) && strings.HasSuffix(name, historyCacheFileExt) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
	return nil
}

// pruneHistoryCache removes entries older than maxAge, then evicts
// least-recently-used entries (by mtime, which cache hits refresh) until the
// remaining total size fits within maxTotalBytes. Removal errors are ignored:
// concurrent runs may prune the same files.
func pruneHistoryCache(dir string, maxTotalBytes int64, maxAge time.Duration) {
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type cacheFileInfo struct {
		path    string
		size    int64
		modTime time.Time
	}
	var files []cacheFileInfo
	now := time.Now()
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), historyCacheFileExt) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if now.Sub(info.ModTime()) > maxAge {
			_ = os.Remove(path)
			continue
		}
		files = append(files, cacheFileInfo{path: path, size: info.Size(), modTime: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.After(files[j].modTime) })
	var total int64
	for _, f := range files {
		total += f.size
		if total > maxTotalBytes {
			_ = os.Remove(f.path)
		}
	}
}

type fakeHistoryPage struct {
	firstEventID int64
	count        int
	next         string
}

func fakePageFetcher(pages map[string]fakeHistoryPage, calls *int) historyPageFetcher {
	return func(token []byte) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
		if calls != nil {
			*calls++
		}
		page, ok := pages[string(token)]
		if !ok {
			return nil, errors.New("unknown page token")
		}
		events := make([]*history.HistoryEvent, page.count)
		for i := range events {
			events[i] = &history.HistoryEvent{EventId: page.firstEventID + int64(i)}
		}
		return &workflowservice.GetWorkflowExecutionHistoryResponse{
			History:       &history.History{Events: events},
			NextPageToken: []byte(page.next),
		}, nil
	}
}

func eventIDs(events []*history.HistoryEvent) []int64 {
	ids := make([]int64, len(events))
	for i, e := range events {
		ids[i] = e.GetEventId()
	}
	return ids
}

func assertEventIDs(t *testing.T, events []*history.HistoryEvent, want ...int64) {
	t.Helper()
	got := eventIDs(events)
	if len(got) != len(want) {
		t.Fatalf("event ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event ids = %v, want %v", got, want)
		}
	}
}

func makeEvents(ids ...int64) []*history.HistoryEvent {
	events := make([]*history.HistoryEvent, len(ids))
	for i, id := range ids {
		events[i] = &history.HistoryEvent{EventId: id}
	}
	return events
}

func TestFetchHistoryWithResume(t *testing.T) {
	t.Parallel()

	t.Run("full fetch caches all but the last page", func(t *testing.T) {
		t.Parallel()
		pages := map[string]fakeHistoryPage{
			"":   {firstEventID: 1, count: 2, next: "t1"},
			"t1": {firstEventID: 3, count: 2, next: "t2"},
			"t2": {firstEventID: 5, count: 1, next: ""},
		}
		hist, updated, err := fetchHistoryWithResume(fakePageFetcher(pages, nil), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertEventIDs(t, hist.Events, 1, 2, 3, 4, 5)
		assertEventIDs(t, updated.events, 1, 2, 3, 4)
		if string(updated.resumeToken) != "t2" {
			t.Errorf("resumeToken = %q, want %q", updated.resumeToken, "t2")
		}
	})

	t.Run("resume fetches only from the last cached page", func(t *testing.T) {
		t.Parallel()
		pages := map[string]fakeHistoryPage{
			"t2": {firstEventID: 5, count: 2, next: "t3"},
			"t3": {firstEventID: 7, count: 1, next: ""},
		}
		calls := 0
		cached := &cachedHistory{events: makeEvents(1, 2, 3, 4), resumeToken: []byte("t2")}
		hist, updated, err := fetchHistoryWithResume(fakePageFetcher(pages, &calls), cached)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertEventIDs(t, hist.Events, 1, 2, 3, 4, 5, 6, 7)
		assertEventIDs(t, updated.events, 1, 2, 3, 4, 5, 6)
		if string(updated.resumeToken) != "t3" {
			t.Errorf("resumeToken = %q, want %q", updated.resumeToken, "t3")
		}
		if calls != 2 {
			t.Errorf("fetch calls = %d, want 2 (must not refetch cached prefix)", calls)
		}
	})

	t.Run("mismatched resume state is rejected", func(t *testing.T) {
		t.Parallel()
		pages := map[string]fakeHistoryPage{
			"t2": {firstEventID: 7, count: 1, next: ""},
		}
		cached := &cachedHistory{events: makeEvents(1, 2, 3, 4), resumeToken: []byte("t2")}
		_, _, err := fetchHistoryWithResume(fakePageFetcher(pages, nil), cached)
		if !errors.Is(err, errHistoryResumeMismatch) {
			t.Fatalf("expected errHistoryResumeMismatch, got %v", err)
		}
	})

	t.Run("empty resumed page is rejected", func(t *testing.T) {
		t.Parallel()
		pages := map[string]fakeHistoryPage{
			"t2": {firstEventID: 5, count: 0, next: ""},
		}
		cached := &cachedHistory{events: makeEvents(1, 2, 3, 4), resumeToken: []byte("t2")}
		_, _, err := fetchHistoryWithResume(fakePageFetcher(pages, nil), cached)
		if !errors.Is(err, errHistoryResumeMismatch) {
			t.Fatalf("expected errHistoryResumeMismatch, got %v", err)
		}
	})

	t.Run("fetch error propagates", func(t *testing.T) {
		t.Parallel()
		_, _, err := fetchHistoryWithResume(fakePageFetcher(map[string]fakeHistoryPage{}, nil), nil)
		if err == nil {
			t.Fatal("expected error for unknown page token")
		}
	})
}

func TestHistoryCacheRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cached := &cachedHistory{events: makeEvents(1, 2, 3), resumeToken: []byte("tok")}
	if err := saveCachedHistory(dir, "wf1", "run1", cached); err != nil {
		t.Fatalf("save: %v", err)
	}

	got := loadCachedHistory(dir, "wf1", "run1")
	if got == nil {
		t.Fatal("expected cache hit")
	}
	assertEventIDs(t, got.events, 1, 2, 3)
	if string(got.resumeToken) != "tok" {
		t.Errorf("resumeToken = %q, want %q", got.resumeToken, "tok")
	}

	if loadCachedHistory(dir, "wf1", "other-run") != nil {
		t.Error("different run must miss the cache")
	}
	if loadCachedHistory(dir, "wf2", "run1") != nil {
		t.Error("different workflow must miss the cache")
	}

	// A new run of the same workflow replaces (prunes) the old run's entry.
	if err := saveCachedHistory(dir, "wf1", "run2", &cachedHistory{events: makeEvents(1)}); err != nil {
		t.Fatalf("save new run: %v", err)
	}
	if loadCachedHistory(dir, "wf1", "run1") != nil {
		t.Error("old run's cache entry should have been pruned")
	}
	if loadCachedHistory(dir, "wf1", "run2") == nil {
		t.Error("new run's cache entry should load")
	}

	// Corrupt files degrade to a cache miss.
	if err := os.WriteFile(historyCacheFilePath(dir, "wf3", "run1"), []byte{0xff, 0xff}, 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
	if loadCachedHistory(dir, "wf3", "run1") != nil {
		t.Error("corrupt cache file must be treated as a miss")
	}

	// Empty dir disables caching entirely.
	if loadCachedHistory("", "wf1", "run1") != nil {
		t.Error("empty dir must disable cache reads")
	}
	if err := saveCachedHistory("", "wf1", "run1", cached); err != nil {
		t.Errorf("empty dir save must be a no-op, got %v", err)
	}
}

func TestSaveCachedHistoryNeverShrinksEntry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	longer := &cachedHistory{events: makeEvents(1, 2, 3, 4, 5), resumeToken: []byte("t3")}
	if err := saveCachedHistory(dir, "wf1", "run1", longer); err != nil {
		t.Fatalf("save longer: %v", err)
	}
	shorter := &cachedHistory{events: makeEvents(1, 2), resumeToken: []byte("t1")}
	if err := saveCachedHistory(dir, "wf1", "run1", shorter); err != nil {
		t.Fatalf("save shorter: %v", err)
	}

	got := loadCachedHistory(dir, "wf1", "run1")
	if got == nil {
		t.Fatal("expected cache hit")
	}
	assertEventIDs(t, got.events, 1, 2, 3, 4, 5)
	if string(got.resumeToken) != "t3" {
		t.Errorf("resumeToken = %q, want %q", got.resumeToken, "t3")
	}
}

func TestPruneHistoryCache(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	writeEntry := func(name string, size int, age time.Duration) string {
		t.Helper()
		path := filepath.Join(dir, name+historyCacheFileExt)
		if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		mtime := time.Now().Add(-age)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return path
	}

	tooOld := writeEntry("wf-old__run1", 10, 48*time.Hour)
	newest := writeEntry("wf-a__run1", 60, time.Minute)
	middle := writeEntry("wf-b__run1", 60, time.Hour)
	oldest := writeEntry("wf-c__run1", 60, 2*time.Hour)
	unrelated := filepath.Join(dir, "not-a-cache-file.txt")
	if err := os.WriteFile(unrelated, make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}

	// Age cap removes tooOld; the 100-byte cap then keeps only the newest
	// entry, since newest+middle already exceeds it.
	pruneHistoryCache(dir, 100, 24*time.Hour)

	for _, gone := range []string{tooOld, middle, oldest} {
		if _, err := os.Stat(gone); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("expected %s to be pruned, stat err=%v", filepath.Base(gone), err)
		}
	}
	for _, kept := range []string{newest, unrelated} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("expected %s to survive pruning: %v", filepath.Base(kept), err)
		}
	}

	// No-ops must not fail.
	pruneHistoryCache("", 100, time.Hour)
	pruneHistoryCache(filepath.Join(dir, "missing"), 100, time.Hour)
}
