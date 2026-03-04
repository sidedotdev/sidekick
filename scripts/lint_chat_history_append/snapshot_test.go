package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"sidekick/coding/lsp"
	"sidekick/common"
)

const snapshotPath = "testdata/fixture_snapshot.json"

// Placeholder prefix used in stored snapshots instead of absolute file:// URIs.
const snapshotURIPrefix = "fixture://"

type fixtureSnapshot struct {
	GoplsVersion string                                     `json:"goplsVersion"`
	CodeChecksum string                                     `json:"codeChecksum"`
	Prepare      map[string][]lsp.CallHierarchyItem         `json:"prepare"`
	Incoming     map[string][]lsp.CallHierarchyIncomingCall `json:"incoming"`
}

func itemKey(uri string, line, char int) string {
	return fmt.Sprintf("%s:%d:%d", uri, line, char)
}

func itemKeyFromItem(item lsp.CallHierarchyItem) string {
	return itemKey(item.URI, item.Range.Start.Line, item.Range.Start.Character)
}

// absURIPrefix returns the file:// URI prefix for the fixture root directory.
func absURIPrefix(fixtureRoot string) string {
	return "file://" + fixtureRoot + "/"
}

// decodeFileURI decodes percent-encoded characters in a file:// URI so that
// it can be compared against plain filesystem paths.
func decodeFileURI(uri string) string {
	if !strings.HasPrefix(uri, "file://") {
		return uri
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		return uri
	}
	return "file://" + parsed.Path
}

// normalizeURI replaces the absolute fixture root prefix with the portable placeholder.
func normalizeURI(uri, fixtureRoot string) string {
	decoded := decodeFileURI(uri)
	prefix := absURIPrefix(fixtureRoot)
	if strings.HasPrefix(decoded, prefix) {
		return snapshotURIPrefix + strings.TrimPrefix(decoded, prefix)
	}
	return uri
}

// denormalizeURI replaces the portable placeholder with the current machine's absolute prefix.
func denormalizeURI(uri, fixtureRoot string) string {
	if strings.HasPrefix(uri, snapshotURIPrefix) {
		return absURIPrefix(fixtureRoot) + strings.TrimPrefix(uri, snapshotURIPrefix)
	}
	return uri
}

// splitKeyURIAndSuffix splits a snapshot key "uri:line:char" into the URI and
// the ":line:char" suffix, accounting for colons inside the URI scheme.
func splitKeyURIAndSuffix(key string) (string, string) {
	lastColon := strings.LastIndex(key, ":")
	if lastColon < 0 {
		return key, ""
	}
	secondLastColon := strings.LastIndex(key[:lastColon], ":")
	if secondLastColon < 0 {
		return key, ""
	}
	return key[:secondLastColon], key[secondLastColon:]
}

// normalizeKey normalizes a snapshot key (uri:line:char) by normalizing the URI portion.
func normalizeKey(key, fixtureRoot string) string {
	uri, suffix := splitKeyURIAndSuffix(key)
	return normalizeURI(uri, fixtureRoot) + suffix
}

// denormalizeKey denormalizes a snapshot key by denormalizing the URI portion.
func denormalizeKey(key, fixtureRoot string) string {
	uri, suffix := splitKeyURIAndSuffix(key)
	return denormalizeURI(uri, fixtureRoot) + suffix
}

func normalizeItem(item lsp.CallHierarchyItem, fixtureRoot string) lsp.CallHierarchyItem {
	item.URI = normalizeURI(item.URI, fixtureRoot)
	return item
}

func denormalizeItem(item lsp.CallHierarchyItem, fixtureRoot string) lsp.CallHierarchyItem {
	item.URI = denormalizeURI(item.URI, fixtureRoot)
	return item
}

func normalizeSnapshot(snap *fixtureSnapshot, fixtureRoot string) *fixtureSnapshot {
	out := &fixtureSnapshot{
		GoplsVersion: snap.GoplsVersion,
		CodeChecksum: snap.CodeChecksum,
		Prepare:      make(map[string][]lsp.CallHierarchyItem, len(snap.Prepare)),
		Incoming:     make(map[string][]lsp.CallHierarchyIncomingCall, len(snap.Incoming)),
	}
	for k, items := range snap.Prepare {
		nk := normalizeKey(k, fixtureRoot)
		normalized := make([]lsp.CallHierarchyItem, len(items))
		for i, item := range items {
			normalized[i] = normalizeItem(item, fixtureRoot)
		}
		out.Prepare[nk] = normalized
	}
	for k, calls := range snap.Incoming {
		nk := normalizeKey(k, fixtureRoot)
		normalized := make([]lsp.CallHierarchyIncomingCall, len(calls))
		for i, call := range calls {
			normalized[i] = lsp.CallHierarchyIncomingCall{
				From:       normalizeItem(call.From, fixtureRoot),
				FromRanges: call.FromRanges,
			}
		}
		out.Incoming[nk] = normalized
	}
	return out
}

func denormalizeSnapshot(snap *fixtureSnapshot, fixtureRoot string) *fixtureSnapshot {
	out := &fixtureSnapshot{
		GoplsVersion: snap.GoplsVersion,
		CodeChecksum: snap.CodeChecksum,
		Prepare:      make(map[string][]lsp.CallHierarchyItem, len(snap.Prepare)),
		Incoming:     make(map[string][]lsp.CallHierarchyIncomingCall, len(snap.Incoming)),
	}
	for k, items := range snap.Prepare {
		dk := denormalizeKey(k, fixtureRoot)
		denormalized := make([]lsp.CallHierarchyItem, len(items))
		for i, item := range items {
			denormalized[i] = denormalizeItem(item, fixtureRoot)
		}
		out.Prepare[dk] = denormalized
	}
	for k, calls := range snap.Incoming {
		dk := denormalizeKey(k, fixtureRoot)
		denormalized := make([]lsp.CallHierarchyIncomingCall, len(calls))
		for i, call := range calls {
			denormalized[i] = lsp.CallHierarchyIncomingCall{
				From:       denormalizeItem(call.From, fixtureRoot),
				FromRanges: call.FromRanges,
			}
		}
		out.Incoming[dk] = denormalized
	}
	return out
}

func computeFixtureChecksum(t *testing.T) string {
	t.Helper()
	fixtureDir := filepath.Join("testdata", "fixture")
	var files []string
	err := filepath.WalkDir(fixtureDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking fixture dir: %v", err)
	}
	sort.Strings(files)

	h := sha256.New()
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		fmt.Fprintf(h, "%s\n%s\n", f, data)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func getGoplsVersion(t *testing.T) string {
	t.Helper()
	goplsPath, err := common.FindOrInstallGopls()
	if err != nil {
		t.Fatalf("FindOrInstallGopls: %v", err)
	}
	out, err := exec.Command(goplsPath, "version").Output()
	if err != nil {
		t.Fatalf("gopls version: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func loadSnapshot(t *testing.T, fixtureRoot string) *fixtureSnapshot {
	t.Helper()
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return nil
	}
	var snap fixtureSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil
	}
	return denormalizeSnapshot(&snap, fixtureRoot)
}

func saveSnapshot(t *testing.T, snap *fixtureSnapshot, fixtureRoot string) {
	t.Helper()
	normalized := normalizeSnapshot(snap, fixtureRoot)
	data, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(snapshotPath, data, 0644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
}

// recordingClient wraps a real LSPClient and records call hierarchy responses.
type recordingClient struct {
	lsp.LSPClient
	prepare  map[string][]lsp.CallHierarchyItem
	incoming map[string][]lsp.CallHierarchyIncomingCall
}

func (r *recordingClient) PrepareCallHierarchy(ctx context.Context, uri string, line int, character int) ([]lsp.CallHierarchyItem, error) {
	items, err := r.LSPClient.PrepareCallHierarchy(ctx, uri, line, character)
	if err == nil {
		r.prepare[itemKey(uri, line, character)] = items
	}
	return items, err
}

func (r *recordingClient) CallHierarchyIncomingCalls(ctx context.Context, item lsp.CallHierarchyItem) ([]lsp.CallHierarchyIncomingCall, error) {
	calls, err := r.LSPClient.CallHierarchyIncomingCalls(ctx, item)
	if err == nil {
		r.incoming[itemKeyFromItem(item)] = calls
	}
	return calls, err
}

// initFixtureGoplsClient returns an LSPClient initialized against the fixture module.
// If a valid snapshot exists, it returns a MockLSPClient backed by the snapshot.
// Otherwise it starts a real gopls, records responses, and saves the snapshot.
func initFixtureGoplsClient(t *testing.T) (lsp.LSPClient, string) {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	fixtureRoot := filepath.Join(cwd, "testdata", "fixture")

	checksum := computeFixtureChecksum(t)
	goplsVer := getGoplsVersion(t)

	snap := loadSnapshot(t, fixtureRoot)
	if snap != nil && snap.CodeChecksum == checksum && snap.GoplsVersion == goplsVer {
		t.Log("using cached snapshot")
		return mockFromSnapshot(snap), fixtureRoot
	}

	client := &lsp.Jsonrpc2LSPClient{LanguageName: "golang"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	rootURI := "file://" + fixtureRoot
	params := lsp.InitializeParams{
		ProcessID: os.Getpid(),
		RootURI:   rootURI,
		Capabilities: lsp.ClientCapabilities{
			TextDocument: lsp.TextDocumentClientCapabilities{},
		},
		WorkspaceFolders: &[]lsp.WorkspaceFolder{
			{URI: rootURI, Name: "fixture"},
		},
	}
	_, err = client.Initialize(ctx, params)
	if err != nil {
		t.Fatalf("failed to initialize gopls for fixture: %v", err)
	}

	rec := &recordingClient{
		LSPClient: client,
		prepare:   make(map[string][]lsp.CallHierarchyItem),
		incoming:  make(map[string][]lsp.CallHierarchyIncomingCall),
	}

	containerFile := filepath.Join(fixtureRoot, "chathistory", "container.go")
	pos, err := findMethodInFile(containerFile, "Append", "*ChatHistoryContainer)")
	if err != nil {
		t.Fatalf("findMethodInFile: %v", err)
	}

	uri := "file://" + containerFile
	items, err := rec.PrepareCallHierarchy(ctx, uri, pos.Line, pos.Character)
	if err != nil {
		t.Fatalf("PrepareCallHierarchy: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("PrepareCallHierarchy returned no items for fixture Append")
	}

	calls, err := rec.CallHierarchyIncomingCalls(ctx, items[0])
	if err != nil {
		t.Fatalf("CallHierarchyIncomingCalls: %v", err)
	}
	for _, call := range calls {
		isReachableFromWorkflow(ctx, rec, call.From, fixtureRoot, make(map[string]bool))
	}

	extraItems := []struct {
		file, funcName, hint string
	}{
		{"callers/cycle_a.go", "CycleA", "func CycleA"},
		{"callers/cycle_b.go", "CycleB", "func CycleB"},
		{"callers/activity.go", "ActivityFunc", "func ActivityFunc"},
	}
	for _, extra := range extraItems {
		f := filepath.Join(fixtureRoot, extra.file)
		p, err := findMethodInFile(f, extra.funcName, extra.hint)
		if err != nil {
			t.Fatalf("findMethodInFile(%s): %v", extra.file, err)
		}
		extraURI := "file://" + f
		extraPrepared, err := rec.PrepareCallHierarchy(ctx, extraURI, p.Line, p.Character)
		if err != nil {
			t.Fatalf("PrepareCallHierarchy(%s): %v", extra.file, err)
		}
		for _, ep := range extraPrepared {
			isReachableFromWorkflow(ctx, rec, ep, fixtureRoot, make(map[string]bool))
		}
	}

	newSnap := &fixtureSnapshot{
		GoplsVersion: goplsVer,
		CodeChecksum: checksum,
		Prepare:      rec.prepare,
		Incoming:     rec.incoming,
	}
	saveSnapshot(t, newSnap, fixtureRoot)
	t.Log("saved new snapshot")

	return mockFromSnapshot(denormalizeSnapshot(normalizeSnapshot(newSnap, fixtureRoot), fixtureRoot)), fixtureRoot
}

func mockFromSnapshot(snap *fixtureSnapshot) lsp.MockLSPClient {
	return lsp.MockLSPClient{
		PrepareCallHierarchyFunc: func(ctx context.Context, uri string, line int, character int) ([]lsp.CallHierarchyItem, error) {
			key := itemKey(uri, line, character)
			if items, ok := snap.Prepare[key]; ok {
				return items, nil
			}
			return nil, fmt.Errorf("snapshot miss for PrepareCallHierarchy %s", key)
		},
		CallHierarchyIncomingCallsFunc: func(ctx context.Context, item lsp.CallHierarchyItem) ([]lsp.CallHierarchyIncomingCall, error) {
			key := itemKeyFromItem(item)
			if calls, ok := snap.Incoming[key]; ok {
				return calls, nil
			}
			return nil, fmt.Errorf("snapshot miss for CallHierarchyIncomingCalls %s", key)
		},
	}
}
