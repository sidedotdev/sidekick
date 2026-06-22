package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func newSha() hash.Hash { return sha256.New() }

func hexSum(h hash.Hash) string { return hex.EncodeToString(h.Sum(nil)) }

func TestSplitArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		args         []string
		wantFlags    []string
		wantPatterns []string
	}{
		{
			name:         "no args",
			args:         nil,
			wantFlags:    nil,
			wantPatterns: nil,
		},
		{
			name:         "just patterns",
			args:         []string{"./...", "./env/..."},
			wantPatterns: []string{"./...", "./env/..."},
		},
		{
			name:         "timeout and pattern",
			args:         []string{"-test.timeout", "60s", "./..."},
			wantFlags:    []string{"-test.timeout", "60s"},
			wantPatterns: []string{"./..."},
		},
		{
			name:         "run and pattern",
			args:         []string{"-test.timeout", "120s", "-run", "TestX", "./env/..."},
			wantFlags:    []string{"-test.timeout", "120s", "-run", "TestX"},
			wantPatterns: []string{"./env/..."},
		},
		{
			name:         "equals form",
			args:         []string{"-run=TestX", "./..."},
			wantFlags:    []string{"-run=TestX"},
			wantPatterns: []string{"./..."},
		},
		{
			name:         "bool flag without value",
			args:         []string{"-v", "./..."},
			wantFlags:    []string{"-v"},
			wantPatterns: []string{"./..."},
		},
		{
			name:         "bool flag followed by bare import path",
			args:         []string{"-v", "sidekick/env"},
			wantFlags:    []string{"-v"},
			wantPatterns: []string{"sidekick/env"},
		},
		{
			name:         "multiple bool flags then patterns",
			args:         []string{"-v", "-race", "sidekick/env", "sidekick/dev"},
			wantFlags:    []string{"-v", "-race"},
			wantPatterns: []string{"sidekick/env", "sidekick/dev"},
		},
		{
			name:         "value flag still consumes next arg",
			args:         []string{"-run", "TestX", "sidekick/env"},
			wantFlags:    []string{"-run", "TestX"},
			wantPatterns: []string{"sidekick/env"},
		},
		{
			name:         "double-dash separator forces remaining to patterns",
			args:         []string{"-v", "--", "-not-a-flag", "sidekick/foo"},
			wantFlags:    []string{"-v"},
			wantPatterns: []string{"-not-a-flag", "sidekick/foo"},
		},
		{
			name:         "flag trailing after pattern",
			args:         []string{"./...", "-v"},
			wantFlags:    []string{"-v"},
			wantPatterns: []string{"./..."},
		},
		{
			name:         "patterns and flags interleaved",
			args:         []string{"-run", "TestX", "./env/...", "-v", "./dev/..."},
			wantFlags:    []string{"-run", "TestX", "-v"},
			wantPatterns: []string{"./env/...", "./dev/..."},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotF, gotP := splitArgs(tc.args)
			if !reflect.DeepEqual(gotF, tc.wantFlags) {
				t.Errorf("flags: got %v, want %v", gotF, tc.wantFlags)
			}
			if !reflect.DeepEqual(gotP, tc.wantPatterns) {
				t.Errorf("patterns: got %v, want %v", gotP, tc.wantPatterns)
			}
		})
	}
}

func TestLooksLikePackagePattern(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"./...":         true,
		"./env/...":     true,
		"../foo":        true,
		"sidekick/dev":  false, // bare import paths are disambiguated by splitArgs via the value-flag allowlist, not this helper
		"foo/...":       true,
		"all":           true,
		"60s":           false,
		"TestSomething": false,
	}
	for s, want := range cases {
		if got := looksLikePackagePattern(s); got != want {
			t.Errorf("looksLikePackagePattern(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestComputeProfileSignature(t *testing.T) {
	t.Parallel()
	env1 := func(k string) string {
		switch k {
		case "SIDE_INTEGRATION_TEST":
			return "true"
		}
		return ""
	}
	env2 := func(k string) string { return "" }

	sig1 := computeProfileSignature([]string{"-test.timeout", "60s"}, env1)
	sig1Again := computeProfileSignature([]string{"-test.timeout", "60s"}, env1)
	sig2 := computeProfileSignature([]string{"-test.timeout", "60s"}, env2)
	sig3 := computeProfileSignature([]string{"-test.timeout", "120s"}, env1)

	if sig1 != sig1Again {
		t.Fatalf("signature not stable: %s vs %s", sig1, sig1Again)
	}
	if sig1 == sig2 {
		t.Fatalf("signature should differ when env var differs")
	}
	if sig1 == sig3 {
		t.Fatalf("signature should differ when flags differ")
	}
}

func TestApplyPasses(t *testing.T) {
	t.Parallel()
	c := &cacheFile{Profiles: map[string]*profileEntry{}}
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	applyPasses(c, "prof1", "go test ./...", []string{"sidekick/foo", "sidekick/bar"}, map[string]string{
		"sidekick/foo": "hf",
		"sidekick/bar": "hb",
	}, now)
	pe := c.Profiles["prof1"]
	if pe == nil {
		t.Fatal("missing profile entry")
	}
	if pe.Description != "go test ./..." {
		t.Errorf("description not set: %q", pe.Description)
	}
	if pe.Packages["sidekick/foo"].Hash != "hf" {
		t.Errorf("foo hash: %+v", pe.Packages["sidekick/foo"])
	}
	if pe.Packages["sidekick/bar"].PassedAt != now.Format(time.RFC3339) {
		t.Errorf("bar timestamp: %+v", pe.Packages["sidekick/bar"])
	}

	// Re-apply with only foo passing at a later time; bar's previous entry must remain.
	later := now.Add(time.Hour)
	applyPasses(c, "prof1", "", []string{"sidekick/foo"}, map[string]string{"sidekick/foo": "hf2"}, later)
	if got := c.Profiles["prof1"].Packages["sidekick/foo"].Hash; got != "hf2" {
		t.Errorf("foo not updated: %q", got)
	}
	if got := c.Profiles["prof1"].Packages["sidekick/bar"].Hash; got != "hb" {
		t.Errorf("bar should remain unchanged: %q", got)
	}
	// Skipping empty hashes: should not insert an entry.
	applyPasses(c, "prof1", "", []string{"sidekick/baz"}, map[string]string{}, later)
	if _, ok := c.Profiles["prof1"].Packages["sidekick/baz"]; ok {
		t.Errorf("baz should not have been cached without a hash")
	}
}

func TestPackageTrackerObserveLine(t *testing.T) {
	t.Parallel()
	tr := newPackageTracker()
	lines := []string{
		`{"Action":"run","Package":"sidekick/foo","Test":"TestX"}`,
		`{"Action":"pass","Package":"sidekick/foo","Test":"TestX","Elapsed":0.1}`,
		`{"Action":"pass","Package":"sidekick/foo","Elapsed":0.2}`,
		`{"Action":"fail","Package":"sidekick/bar","Elapsed":0.3}`,
		`{"Action":"skip","Package":"sidekick/baz","Elapsed":0.0}`,
		`not json`,
		``,
	}
	for _, l := range lines {
		tr.observeLine([]byte(l))
	}
	passed := tr.passed()
	if !reflect.DeepEqual(passed, []string{"sidekick/foo"}) {
		t.Errorf("passed = %v, want [sidekick/foo]", passed)
	}
}

func TestCacheRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	c := &cacheFile{Profiles: map[string]*profileEntry{}}
	applyPasses(c, "prof", "desc", []string{"a", "b"}, map[string]string{"a": "ha", "b": "hb"}, time.Unix(0, 0).UTC())
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := readCacheFromPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Profiles["prof"].Packages["a"].Hash != "ha" {
		t.Errorf("round-trip mismatch: %+v", loaded.Profiles["prof"].Packages)
	}
}

func TestReadCacheMissing(t *testing.T) {
	t.Parallel()
	c, err := readCacheFromPath(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("missing cache should not error, got %v", err)
	}
	if c == nil || c.Profiles == nil {
		t.Fatal("expected empty cache, got nil")
	}
}

func TestReadCacheCorrupt(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cache.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readCacheFromPath(path); err == nil {
		t.Fatal("expected error parsing corrupt cache")
	}
}

func TestSelectAffected(t *testing.T) {
	t.Parallel()
	// Simulate the cache layer of computeAffected: given known per-package
	// hashes and a cache, ensure run/skip selection works correctly.
	hashes := map[string]string{
		"sidekick/foo": "hf-new",
		"sidekick/bar": "hb",
		"sidekick/baz": "hz",
	}
	cache := &cacheFile{Profiles: map[string]*profileEntry{
		"prof": {Packages: map[string]packageEntry{
			"sidekick/foo": {Hash: "hf-old"},
			"sidekick/bar": {Hash: "hb"},
			// baz is absent: should run.
		}},
	}}
	prof := cache.Profiles["prof"]
	var toRun, toSkip []string
	for p, h := range hashes {
		if e, ok := prof.Packages[p]; ok && e.Hash == h {
			toSkip = append(toSkip, p)
		} else {
			toRun = append(toRun, p)
		}
	}
	sort.Strings(toRun)
	sort.Strings(toSkip)
	if !reflect.DeepEqual(toRun, []string{"sidekick/baz", "sidekick/foo"}) {
		t.Errorf("toRun = %v", toRun)
	}
	if !reflect.DeepEqual(toSkip, []string{"sidekick/bar"}) {
		t.Errorf("toSkip = %v", toSkip)
	}
}

func TestSanitizeModulePath(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"sidekick":           "sidekick",
		"github.com/foo/bar": "github.com_foo_bar",
		"":                   "unknown_module",
		"a b/c":              "a_b_c",
	}
	for in, want := range cases {
		if got := sanitizeModulePath(in); got != want {
			t.Errorf("sanitizeModulePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHashPackageFilesStable(t *testing.T) {
	t.Parallel()
	// Set up two equivalent packages with the same file contents and make sure
	// the contributed hash bytes are identical, then change a file and ensure
	// the result differs.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package p\n// b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &goListPackage{
		Dir:        dir,
		ImportPath: "example/p",
		GoFiles:    []string{"a.go", "b.go"},
	}
	h1 := newSha()
	if err := hashPackageFiles(h1, p, false); err != nil {
		t.Fatal(err)
	}
	h2 := newSha()
	if err := hashPackageFiles(h2, p, false); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(hexSum(h1), hexSum(h2)) {
		t.Errorf("hashPackageFiles not stable: %s vs %s", hexSum(h1), hexSum(h2))
	}

	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package p\n// changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h3 := newSha()
	if err := hashPackageFiles(h3, p, false); err != nil {
		t.Fatal(err)
	}
	if hexSum(h1) == hexSum(h3) {
		t.Errorf("hash should have changed after file edit")
	}
}

func TestHashPackageFilesMissingFileFailsSafe(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := &goListPackage{
		Dir:        dir,
		ImportPath: "example/p",
		GoFiles:    []string{"gone.go"},
	}
	if err := hashPackageFiles(newSha(), p, false); err == nil {
		t.Fatalf("expected error for missing file, got nil")
	}
}

func TestUpdateCachePassesQuarantinesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", dir)

	path, err := cachePath()
	if err != nil {
		t.Fatalf("cachePath: %v", err)
	}
	const corruptBody = "{not valid json"
	if err := os.WriteFile(path, []byte(corruptBody), 0o644); err != nil {
		t.Fatalf("seed corrupt cache: %v", err)
	}

	err = updateCachePasses("prof", "go test", []string{"sidekick/foo"}, map[string]string{"sidekick/foo": "hf"})
	if err != nil {
		t.Fatalf("update should self-heal after quarantining corrupt cache, got: %v", err)
	}

	// The fresh cache should contain only the just-recorded pass so subsequent
	// runs can benefit instead of being stuck in permanent fallback.
	c, err := readCacheFromPath(path)
	if err != nil {
		t.Fatalf("read rewritten cache: %v", err)
	}
	prof := c.Profiles["prof"]
	if prof == nil || prof.Packages["sidekick/foo"].Hash != "hf" {
		t.Errorf("expected fresh cache to contain recorded pass, got %+v", c)
	}

	// The corrupted contents should be preserved alongside the cache under a
	// .corrupt.* sibling so a human can inspect them.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var found bool
	prefix := filepath.Base(path) + ".corrupt."
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			data, readErr := os.ReadFile(filepath.Join(filepath.Dir(path), e.Name()))
			if readErr != nil {
				t.Fatalf("read quarantine: %v", readErr)
			}
			if string(data) != corruptBody {
				t.Errorf("quarantined file body = %q, want %q", string(data), corruptBody)
			}
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a %s* file alongside cache, got entries: %v", prefix, entries)
	}
}

func TestUpdateCachePassesAtomicAndConcurrent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", dir)

	const writers = 8
	type job struct {
		pkg  string
		hash string
	}
	jobs := make([]job, writers)
	for i := range jobs {
		jobs[i] = job{pkg: "sidekick/p" + string(rune('a'+i)), hash: "h" + string(rune('a'+i))}
	}

	errCh := make(chan error, writers)
	doneCh := make(chan struct{})
	var started int
	syncCh := make(chan struct{})
	for _, j := range jobs {
		j := j
		go func() {
			<-syncCh
			errCh <- updateCachePasses("prof", "go test", []string{j.pkg}, map[string]string{j.pkg: j.hash})
		}()
		started++
	}
	go func() {
		close(syncCh)
		for i := 0; i < writers; i++ {
			if err := <-errCh; err != nil {
				t.Errorf("writer %d: %v", i, err)
			}
		}
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent writers did not finish in time")
	}

	c, err := loadCache()
	if err != nil {
		t.Fatalf("loadCache: %v", err)
	}
	prof := c.Profiles["prof"]
	if prof == nil || len(prof.Packages) != writers {
		t.Fatalf("expected %d packages recorded, got prof=%+v", writers, prof)
	}
	for _, j := range jobs {
		entry, ok := prof.Packages[j.pkg]
		if !ok {
			t.Errorf("missing package %s", j.pkg)
			continue
		}
		if entry.Hash != j.hash {
			t.Errorf("%s: hash = %q, want %q", j.pkg, entry.Hash, j.hash)
		}
	}
}

func TestLineObserverInvokesPerLine(t *testing.T) {
	t.Parallel()
	var got []string
	lo := newLineObserver(func(line []byte) {
		got = append(got, string(line))
	})

	// Write a chunk that contains a complete line and a partial trailing line.
	if _, err := lo.Write([]byte("alpha\nbeta\npar")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if want := []string{"alpha", "beta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after first write got %v want %v", got, want)
	}

	// A second write completes the previously partial line and starts another.
	if _, err := lo.Write([]byte("tial\nfinal")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if want := []string{"alpha", "beta", "partial"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after second write got %v want %v", got, want)
	}

	// flush surfaces the unterminated tail.
	lo.flush()
	if want := []string{"alpha", "beta", "partial", "final"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after flush got %v want %v", got, want)
	}

	// flush on an empty buffer is a no-op.
	lo.flush()
	if want := []string{"alpha", "beta", "partial", "final"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after second flush got %v want %v", got, want)
	}
}

func TestComputePackageHashFailsSafeOnUnresolvedDep(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg := "example/p"
	listing := &pkgListing{
		byPath: map[string]*goListPackage{
			pkg: {
				Dir:        dir,
				ImportPath: pkg,
				GoFiles:    []string{"p.go"},
				Deps:       []string{"example/missing"},
			},
		},
		testBinaries: map[string]*goListPackage{},
		testVariants: map[string][]*goListPackage{},
	}
	_, err := computePackageHash(pkg, listing, []byte("base"))
	if err == nil {
		t.Fatalf("expected error for unresolved dep, got nil")
	}
	if !strings.Contains(err.Error(), "example/missing") {
		t.Errorf("error should mention unresolved dep, got %v", err)
	}
}

func TestComputePackageHashIncludesTestVariantFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p_external_test.go"), []byte("package p_test\nfunc x(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg := "example/p"
	base := &goListPackage{
		Dir:        dir,
		ImportPath: pkg,
		GoFiles:    []string{"p.go"},
	}
	xtestVariant := &goListPackage{
		Dir:          dir,
		ImportPath:   "example/p_test [example/p.test]",
		ForTest:      pkg,
		XTestGoFiles: []string{"p_external_test.go"},
	}
	listing := &pkgListing{
		byPath: map[string]*goListPackage{
			pkg:                     base,
			xtestVariant.ImportPath: xtestVariant,
		},
		testBinaries: map[string]*goListPackage{},
		testVariants: map[string][]*goListPackage{
			pkg: {xtestVariant},
		},
	}

	h1, err := computePackageHash(pkg, listing, []byte("base"))
	if err != nil {
		t.Fatalf("computePackageHash: %v", err)
	}

	// Editing the external test file must change the hash.
	if err := os.WriteFile(filepath.Join(dir, "p_external_test.go"), []byte("package p_test\nfunc y(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h2, err := computePackageHash(pkg, listing, []byte("base"))
	if err != nil {
		t.Fatalf("computePackageHash: %v", err)
	}
	if h1 == h2 {
		t.Errorf("hash did not change after editing external test file; xtest variant inputs are not being hashed")
	}
}
