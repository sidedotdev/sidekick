package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	if !pe.Packages["sidekick/foo"].hasHash("hf") {
		t.Errorf("foo hash: %+v", pe.Packages["sidekick/foo"])
	}
	if passes := pe.Packages["sidekick/bar"].Passes; len(passes) != 1 || passes[0].PassedAt != now.Format(time.RFC3339) {
		t.Errorf("bar timestamp: %+v", pe.Packages["sidekick/bar"])
	}

	// Re-apply with only foo passing at a later time at a NEW hash; both the
	// new hash and the previously-passing hash for foo must be remembered,
	// and bar's untouched entry must remain.
	later := now.Add(time.Hour)
	applyPasses(c, "prof1", "", []string{"sidekick/foo"}, map[string]string{"sidekick/foo": "hf2"}, later)
	foo := c.Profiles["prof1"].Packages["sidekick/foo"]
	if !foo.hasHash("hf") || !foo.hasHash("hf2") {
		t.Errorf("foo should remember both passing hashes, got %+v", foo)
	}
	if got := c.Profiles["prof1"].Packages["sidekick/bar"]; !got.hasHash("hb") {
		t.Errorf("bar should remain unchanged: %+v", got)
	}
	// Skipping empty hashes: should not insert an entry.
	applyPasses(c, "prof1", "", []string{"sidekick/baz"}, map[string]string{}, later)
	if _, ok := c.Profiles["prof1"].Packages["sidekick/baz"]; ok {
		t.Errorf("baz should not have been cached without a hash")
	}
}

func TestApplyPassesOscillation(t *testing.T) {
	t.Parallel()
	c := &cacheFile{Profiles: map[string]*profileEntry{}}
	t0 := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	applyPasses(c, "prof", "", []string{"pkg"}, map[string]string{"pkg": "A"}, t0)
	applyPasses(c, "prof", "", []string{"pkg"}, map[string]string{"pkg": "B"}, t0.Add(time.Hour))
	applyPasses(c, "prof", "", []string{"pkg"}, map[string]string{"pkg": "A"}, t0.Add(2*time.Hour))

	pe := c.Profiles["prof"].Packages["pkg"]
	if !pe.hasHash("A") || !pe.hasHash("B") {
		t.Fatalf("expected both A and B remembered, got %+v", pe)
	}
	// Re-recording A should refresh, not duplicate, its entry.
	if len(pe.Passes) != 2 {
		t.Errorf("expected 2 distinct passes after oscillation, got %d: %+v", len(pe.Passes), pe.Passes)
	}
	if pe.Passes[len(pe.Passes)-1].Hash != "A" {
		t.Errorf("re-recorded A should be newest, got %+v", pe.Passes)
	}
}

func TestApplyPassesCapEvictsOldest(t *testing.T) {
	t.Parallel()
	c := &cacheFile{Profiles: map[string]*profileEntry{}}
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	total := maxPassesPerPackage + 5
	for i := 0; i < total; i++ {
		h := fmt.Sprintf("h%04d", i)
		applyPasses(c, "prof", "", []string{"pkg"}, map[string]string{"pkg": h}, base.Add(time.Duration(i)*time.Second))
	}
	pe := c.Profiles["prof"].Packages["pkg"]
	if len(pe.Passes) != maxPassesPerPackage {
		t.Fatalf("expected cap of %d entries, got %d", maxPassesPerPackage, len(pe.Passes))
	}
	if pe.Passes[0].Hash != fmt.Sprintf("h%04d", total-maxPassesPerPackage) {
		t.Errorf("oldest entry not evicted, oldest hash = %q", pe.Passes[0].Hash)
	}
	if pe.Passes[len(pe.Passes)-1].Hash != fmt.Sprintf("h%04d", total-1) {
		t.Errorf("newest entry wrong, got %q", pe.Passes[len(pe.Passes)-1].Hash)
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
		// Individual test-level skip must not be mistaken for a package result.
		`{"Action":"skip","Package":"sidekick/qux","Test":"TestSkipped","Elapsed":0.0}`,
		// Package-level skip (no tests / all tests skipped) is cacheable.
		`{"Action":"skip","Package":"sidekick/baz","Elapsed":0.0}`,
		`not json`,
		``,
	}
	for _, l := range lines {
		tr.observeLine([]byte(l))
	}
	passed := tr.passed()
	if !reflect.DeepEqual(passed, []string{"sidekick/baz", "sidekick/foo"}) {
		t.Errorf("passed = %v, want [sidekick/baz sidekick/foo]", passed)
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
	if !loaded.Profiles["prof"].Packages["a"].hasHash("ha") {
		t.Errorf("round-trip mismatch: %+v", loaded.Profiles["prof"].Packages)
	}
}

func TestReadLegacyCacheMigrates(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cache.json")
	legacy := `{
  "profiles": {
    "prof": {
      "description": "go test",
      "packages": {
        "sidekick/foo": {"hash": "hf", "passedAt": "2024-01-02T03:04:05Z"}
      }
    }
  }
}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := readCacheFromPath(path)
	if err != nil {
		t.Fatalf("legacy cache must read without error, got %v", err)
	}
	pe := c.Profiles["prof"].Packages["sidekick/foo"]
	if !pe.hasHash("hf") {
		t.Errorf("legacy hash not migrated into Passes: %+v", pe)
	}
	if pe.LegacyHash != "" || pe.LegacyPassedAt != "" {
		t.Errorf("legacy fields not cleared after migration: %+v", pe)
	}
	if len(pe.Passes) != 1 || pe.Passes[0].PassedAt != "2024-01-02T03:04:05Z" {
		t.Errorf("expected single migrated pass entry preserving timestamp, got %+v", pe.Passes)
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
			"sidekick/foo": {Passes: []passEntry{{Hash: "hf-old"}, {Hash: "hf-older"}}},
			"sidekick/bar": {Passes: []passEntry{{Hash: "hb"}}},
			// baz is absent: should run.
		}},
	}}
	prof := cache.Profiles["prof"]
	var toRun, toSkip []string
	for p, h := range hashes {
		if e, ok := prof.Packages[p]; ok && e.hasHash(h) {
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
	if prof == nil || !prof.Packages["sidekick/foo"].hasHash("hf") {
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
		if !entry.hasHash(j.hash) {
			t.Errorf("%s: passes = %+v, want hash %q", j.pkg, entry.Passes, j.hash)
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

// TestActivityFunctionReferenceCapturedInClosure confirms that activities
// invoked by function reference (workflow.ExecuteActivity(ctx, Activity, ...))
// still create a captured dependency edge. sidekick/dev calls
// env.EnvRunCommandActivity this way, so sidekick/env must appear in dev's
// hashed dependency closure; otherwise changes to that activity could be
// skipped (a recall gap).
func TestActivityFunctionReferenceCapturedInClosure(t *testing.T) {
	t.Parallel()
	const testPkg = "sidekick/dev"
	const activityPkg = "sidekick/env"

	listing, err := goListDepsTest([]string{testPkg})
	if err != nil {
		t.Fatalf("goListDepsTest(%q): %v", testPkg, err)
	}
	closure := packageClosure(testPkg, listing)
	if !closure[activityPkg] {
		t.Fatalf("activity package %q not in hashed closure of %q; function-reference activity edge not captured", activityPkg, testPkg)
	}
}

// TestReplayClosureIncludesRegisteredWorkflowPackages confirms that the
// worker/replay test package's hashed closure includes the packages that define
// workflows/activities registered via RegisterWorkflows in worker/worker.go.
// Changes to any of those (or their transitive deps) must force the replay
// tests to re-run.
func TestReplayClosureIncludesRegisteredWorkflowPackages(t *testing.T) {
	t.Parallel()
	const replayPkg = "sidekick/worker/replay"

	listing, err := goListDepsTest([]string{replayPkg})
	if err != nil {
		t.Fatalf("goListDepsTest(%q): %v", replayPkg, err)
	}
	closure := packageClosure(replayPkg, listing)

	for _, want := range []string{
		"sidekick/dev",
		"sidekick/poll_failures",
		"sidekick/persisted_ai",
		"sidekick/srv",
		"sidekick/common",
	} {
		if !closure[want] {
			t.Errorf("replay closure missing registered workflow package %q; changes there would not re-run replay tests", want)
		}
	}
}

// TestReplayClosureExcludesApiPackage locks in that the replay tests are not
// coupled to sidekick/api. There is no legitimate reason for the api package to
// be part of the replay closure, so its presence is treated as a defect rather
// than tolerable over-broadness.
func TestReplayClosureExcludesApiPackage(t *testing.T) {
	t.Parallel()
	const replayPkg = "sidekick/worker/replay"
	const apiPkg = "sidekick/api"

	listing, err := goListDepsTest([]string{replayPkg})
	if err != nil {
		t.Fatalf("goListDepsTest(%q): %v", replayPkg, err)
	}
	closure := packageClosure(replayPkg, listing)
	if closure[apiPkg] {
		t.Errorf("replay closure unexpectedly includes %q; replay tests should not depend on the api package", apiPkg)
	}
}

func packageHasHash(t *testing.T, profile, pkg, hash string) bool {
	t.Helper()
	path, err := cachePath()
	if err != nil {
		t.Fatalf("cachePath: %v", err)
	}
	c, err := readCacheFromPath(path)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	prof := c.Profiles[profile]
	if prof == nil {
		return false
	}
	return prof.Packages[pkg].hasHash(hash)
}

func TestCacheWriterFlushesIncrementallyAndCoalesces(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", dir)

	hashes := map[string]string{"sidekick/a": "ha", "sidekick/b": "hb", "sidekick/c": "hc"}
	tick := make(chan time.Time)
	var flushCalls int
	flushed := make(chan struct{}, 8)
	flushFn := func(sig, desc string, passed []string, h map[string]string) error {
		flushCalls++
		err := updateCachePasses(sig, desc, passed, h)
		flushed <- struct{}{}
		return err
	}
	w := newCacheWriter("prof", "go test", hashes, tick, flushFn, func(err error) {
		t.Errorf("unexpected cache write failure: %v", err)
	})
	tracker := newPackageTracker()
	tracker.onPass = w.enqueue

	// Multiple package completions before a tick must coalesce into a single
	// cache write; packages without a computed hash are ineligible, and
	// test-level or fail events must not be persisted.
	tracker.observeLine([]byte(`{"Action":"pass","Package":"sidekick/a"}`))
	tracker.observeLine([]byte(`{"Action":"skip","Package":"sidekick/b"}`))
	tracker.observeLine([]byte(`{"Action":"pass","Package":"sidekick/unhashed"}`))
	tracker.observeLine([]byte(`{"Action":"pass","Package":"sidekick/c","Test":"TestX"}`))
	tick <- time.Time{}
	<-flushed

	// Mid-run persistence: a and b are on disk while c is still outstanding.
	if !packageHasHash(t, "prof", "sidekick/a", "ha") || !packageHasHash(t, "prof", "sidekick/b", "hb") {
		t.Error("expected a and b cached after debounce flush")
	}
	if packageHasHash(t, "prof", "sidekick/c", "hc") {
		t.Error("c should not be cached before it passes")
	}
	if packageHasHash(t, "prof", "sidekick/unhashed", "") {
		t.Error("unhashed package must not be cached")
	}
	if flushCalls != 1 {
		t.Errorf("flushCalls = %d, want 1 (completions must coalesce)", flushCalls)
	}

	// The final flush on close persists remaining pending passes.
	tracker.observeLine([]byte(`{"Action":"pass","Package":"sidekick/c"}`))
	w.close()
	if !packageHasHash(t, "prof", "sidekick/c", "hc") {
		t.Error("expected c cached after final flush")
	}
	if flushCalls != 2 {
		t.Errorf("flushCalls = %d, want 2", flushCalls)
	}
}

func TestCacheWriterNoWriteWhenNothingPending(t *testing.T) {
	t.Parallel()
	tick := make(chan time.Time)
	var flushCalls int
	flushFn := func(string, string, []string, map[string]string) error {
		flushCalls++
		return nil
	}
	w := newCacheWriter("prof", "go test", map[string]string{"sidekick/a": "ha"}, tick, flushFn, func(err error) {
		t.Errorf("unexpected warn: %v", err)
	})
	tick <- time.Time{}
	w.close()
	if flushCalls != 0 {
		t.Errorf("flushCalls = %d, want 0 when nothing is pending", flushCalls)
	}
}

func TestCacheWriterRetriesFailedBatchOnFinalFlush(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", dir)

	tick := make(chan time.Time)
	var flushCalls, warns int
	flushFn := func(sig, desc string, passed []string, h map[string]string) error {
		flushCalls++
		if flushCalls == 1 {
			return fmt.Errorf("transient failure")
		}
		return updateCachePasses(sig, desc, passed, h)
	}
	w := newCacheWriter("prof", "go test", map[string]string{"sidekick/a": "ha"}, tick, flushFn, func(error) {
		warns++
	})
	w.enqueue("sidekick/a")
	tick <- time.Time{}
	w.close()

	if flushCalls != 2 {
		t.Errorf("flushCalls = %d, want 2 (failed batch must be retried)", flushCalls)
	}
	if warns != 1 {
		t.Errorf("warns = %d, want 1", warns)
	}
	if !packageHasHash(t, "prof", "sidekick/a", "ha") {
		t.Error("expected pass persisted by final flush after transient incremental failure")
	}
}

func TestCacheWriterWarnsOnFlushFailure(t *testing.T) {
	t.Parallel()
	var warned error
	w := newCacheWriter("prof", "go test", map[string]string{"sidekick/a": "ha"}, make(chan time.Time), func(string, string, []string, map[string]string) error {
		return fmt.Errorf("disk full")
	}, func(err error) {
		warned = err
	})
	w.enqueue("sidekick/a")
	w.close()
	if warned == nil || !strings.Contains(warned.Error(), "disk full") {
		t.Errorf("expected warn callback with flush error, got %v", warned)
	}
}

// writeModuleManifest writes the dependency manifest of a throwaway module so
// hash inputs can be exercised without touching the repo's own manifest.
func writeModuleManifest(t *testing.T, dir, goMod, goSum string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if goSum == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte(goSum), 0o644); err != nil {
		t.Fatal(err)
	}
}

const manifestGoMod = `module example.com/m

go 1.24

require example.com/used v1.0.0
`

const manifestGoSum = `example.com/used v1.0.0 h1:aaa=
example.com/used v1.0.0/go.mod h1:bbb=
`

// TestCommonHashBaseIsManifestIndependent pins the invalidation granularity of
// the shared hash base. Dependency manifest churn (added or bumped requires,
// // indirect markers, checksums, replacements of modules nobody imports) must
// only invalidate the packages consuming the changed module, which their own
// dependency sources and resolved module identity already cover. Hashing the
// manifest wholesale invalidates every cached package on any dependency edit.
func TestCommonHashBaseIsManifestIndependent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeModuleManifest(t, dir, manifestGoMod, manifestGoSum)
	before := string(commonHashBase("prof"))

	writeModuleManifest(t, dir, `module example.com/m

go 1.24

require (
	example.com/other v1.2.3 // indirect
	example.com/used v1.0.0
)

replace example.com/unimported => example.com/forked v1.0.0
`, manifestGoSum+`example.com/other v1.2.3 h1:ccc=
example.com/other v1.2.3/go.mod h1:ddd=
`)

	if after := string(commonHashBase("prof")); after != before {
		t.Errorf("hash base changed with the dependency manifest:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if other := string(commonHashBase("other-profile")); other == before {
		t.Error("hash base must still separate profiles")
	}
}

// TestComputePackageHashTracksDependencyModuleIdentity is the safety half of
// per-package dependency hashing: since the manifest is no longer folded into
// every hash wholesale, the resolved identity of each dependency module must
// be an input, so a version, language version or checksum change is never
// masked by byte-identical sources.
func TestComputePackageHashTracksDependencyModuleIdentity(t *testing.T) {
	t.Parallel()

	mainDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(mainDir, "p.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	depDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(depDir, "dep.go"), []byte("package dep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	const pkg = "example.com/m/p"
	const depPkg = "example.com/dep"
	listingFor := func(depModule goListModule) *pkgListing {
		return &pkgListing{
			byPath: map[string]*goListPackage{
				pkg: {
					Dir:        mainDir,
					ImportPath: pkg,
					GoFiles:    []string{"p.go"},
					Deps:       []string{depPkg},
					Module:     &goListModule{Path: "example.com/m", Main: true, GoVersion: "1.24"},
				},
				depPkg: {
					Dir:        depDir,
					ImportPath: depPkg,
					GoFiles:    []string{"dep.go"},
					Module:     &depModule,
				},
			},
			testBinaries: map[string]*goListPackage{},
			testVariants: map[string][]*goListPackage{},
		}
	}

	resolved := goListModule{
		Path:      depPkg,
		Version:   "v1.0.0",
		GoVersion: "1.21",
		Sum:       "h1:aaa=",
		GoModSum:  "h1:bbb=",
	}
	reference, err := computePackageHash(pkg, listingFor(resolved), []byte("base"))
	if err != nil {
		t.Fatalf("computePackageHash: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(m *goListModule)
	}{
		{name: "version bump", mutate: func(m *goListModule) { m.Version = "v1.0.1" }},
		{name: "module language version", mutate: func(m *goListModule) { m.GoVersion = "1.22" }},
		{name: "module checksum", mutate: func(m *goListModule) { m.Sum = "h1:ccc=" }},
		{name: "go.mod checksum", mutate: func(m *goListModule) { m.GoModSum = "h1:ddd=" }},
		{name: "replacement", mutate: func(m *goListModule) {
			m.Replace = &goListModule{Path: "example.com/fork", Version: "v0.0.1"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mutated := resolved
			tc.mutate(&mutated)
			got, err := computePackageHash(pkg, listingFor(mutated), []byte("base"))
			if err != nil {
				t.Fatalf("computePackageHash: %v", err)
			}
			if got == reference {
				t.Errorf("hash unchanged after %s: dependency module identity is not hashed", tc.name)
			}
		})
	}
}

// TestComputePackageHashTracksTestBinaryGODEBUG covers godebug settings, which
// change how a test binary behaves at runtime without touching any source
// file. go list resolves them onto the package's test binary entry.
func TestComputePackageHashTracksTestBinaryGODEBUG(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const pkg = "example.com/m/p"
	listingFor := func(godebug string) *pkgListing {
		return &pkgListing{
			byPath: map[string]*goListPackage{
				pkg: {
					Dir:        dir,
					ImportPath: pkg,
					GoFiles:    []string{"p.go"},
					Module:     &goListModule{Path: "example.com/m", Main: true, GoVersion: "1.24"},
				},
			},
			testBinaries: map[string]*goListPackage{
				pkg: {
					ImportPath:     pkg + ".test",
					Deps:           []string{pkg},
					DefaultGODEBUG: godebug,
				},
			},
			testVariants: map[string][]*goListPackage{},
		}
	}

	before, err := computePackageHash(pkg, listingFor("asynctimerchan=1"), []byte("base"))
	if err != nil {
		t.Fatalf("computePackageHash: %v", err)
	}
	after, err := computePackageHash(pkg, listingFor("asynctimerchan=0"), []byte("base"))
	if err != nil {
		t.Fatalf("computePackageHash: %v", err)
	}
	if before == after {
		t.Error("hash unchanged after the test binary's default GODEBUG changed")
	}
}

// TestMergedDependencyEditsStillHitCache reproduces the post-merge symptom: a
// branch merged into the base adds a module (with its requires, checksums and
// a replacement) that this package does not import, and the pass recorded
// before the merge must still apply instead of re-running the suite.
func TestMergedDependencyEditsStillHitCache(t *testing.T) {
	pkgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pkgDir, "p.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	depDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(depDir, "dep.go"), []byte("package dep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unrelatedDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(unrelatedDir, "u.go"), []byte("package u\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	const pkg = "example.com/m/p"
	const depPkg = "example.com/dep"
	const unrelatedPkg = "example.com/unrelated"
	listingWith := func(extra ...*goListPackage) *pkgListing {
		listing := &pkgListing{
			byPath: map[string]*goListPackage{
				pkg: {
					Dir:        pkgDir,
					ImportPath: pkg,
					GoFiles:    []string{"p.go"},
					Deps:       []string{depPkg},
					Module:     &goListModule{Path: "example.com/m", Main: true, GoVersion: "1.24"},
				},
				depPkg: {
					Dir:        depDir,
					ImportPath: depPkg,
					GoFiles:    []string{"dep.go"},
					Module: &goListModule{
						Path:     depPkg,
						Version:  "v1.0.0",
						Sum:      "h1:aaa=",
						GoModSum: "h1:bbb=",
					},
				},
			},
			testBinaries: map[string]*goListPackage{},
			testVariants: map[string][]*goListPackage{},
		}
		for _, p := range extra {
			listing.byPath[p.ImportPath] = p
		}
		return listing
	}

	// Each state is hashed from inside its own module checkout, so the
	// manifest of the merged state is as visible to hashing as it is in a real
	// run on the base branch after the merge.
	hashAt := func(goMod, goSum string, extra ...*goListPackage) string {
		t.Helper()
		moduleDir := t.TempDir()
		writeModuleManifest(t, moduleDir, goMod, goSum)
		t.Chdir(moduleDir)
		h, err := computePackageHash(pkg, listingWith(extra...), commonHashBase("prof"))
		if err != nil {
			t.Fatalf("computePackageHash: %v", err)
		}
		return h
	}

	branchHash := hashAt(`module example.com/m

go 1.24

require example.com/dep v1.0.0
`, `example.com/dep v1.0.0 h1:aaa=
example.com/dep v1.0.0/go.mod h1:bbb=
`)

	mergedHash := hashAt(`module example.com/m

go 1.24

require (
	example.com/dep v1.0.0
	example.com/unrelated v2.0.0
)

replace example.com/unrelated => example.com/forked v2.0.1
`, `example.com/dep v1.0.0 h1:aaa=
example.com/dep v1.0.0/go.mod h1:bbb=
example.com/forked v2.0.1 h1:ccc=
example.com/forked v2.0.1/go.mod h1:ddd=
`, &goListPackage{
		Dir:        unrelatedDir,
		ImportPath: unrelatedPkg,
		GoFiles:    []string{"u.go"},
		Module: &goListModule{
			Path:     unrelatedPkg,
			Version:  "v2.0.0",
			Sum:      "h1:ccc=",
			GoModSum: "h1:ddd=",
			Replace:  &goListModule{Path: "example.com/forked", Version: "v2.0.1"},
		},
	})

	cache := &cacheFile{Profiles: map[string]*profileEntry{}}
	applyPasses(cache, "prof", "go test ./...", []string{pkg}, map[string]string{pkg: branchHash}, time.Now())
	if !cache.Profiles["prof"].Packages[pkg].hasHash(mergedHash) {
		t.Errorf("package %s lost its cached pass after an unrelated dependency was merged", pkg)
	}
}

// TestGoListDepsTestDecodesModuleMetadata proves the module identity fields
// hashing depends on are really the ones go list emits: a JSON shape mismatch
// would silently drop dependency version and checksum changes from every hash.
func TestGoListDepsTestDecodesModuleMetadata(t *testing.T) {
	t.Parallel()
	const pkg = "sidekick/coding/tree_sitter"
	listing, err := goListDepsTest([]string{pkg})
	if err != nil {
		t.Fatalf("goListDepsTest(%q): %v", pkg, err)
	}

	var identityDecoded, replacementDecoded, replacementPresent bool
	for _, p := range listing.byPath {
		m := p.Module
		if m == nil || m.Main {
			continue
		}
		if m.Version != "" && m.Sum != "" && m.GoModSum != "" && m.GoVersion != "" {
			identityDecoded = true
		}
		if m.Replace == nil {
			continue
		}
		replacementPresent = true
		if m.Replace.Path != "" && m.Replace.Version != "" && m.Replace.Sum != "" && m.Replace.GoModSum != "" {
			replacementDecoded = true
		}
	}
	if !identityDecoded {
		t.Errorf("no dependency of %s decoded a module version with checksums and language version", pkg)
	}
	if !replacementPresent {
		t.Errorf("no replaced module in the closure of %s; point this test at a package consuming a replace directive", pkg)
	} else if !replacementDecoded {
		t.Errorf("replacement module metadata did not decode for any dependency of %s", pkg)
	}
}

// TestGoListDepsTestDecodesTestBinaryGODEBUG proves the same for the test
// binary's effective godebug settings, which change test behaviour without
// changing any source file.
func TestGoListDepsTestDecodesTestBinaryGODEBUG(t *testing.T) {
	dir := t.TempDir()
	writeModuleManifest(t, dir, `module example.com/gd

go 1.24

godebug default=go1.21
`, "")
	pkgDir := filepath.Join(dir, "a")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "a.go"), []byte("package a\n\nfunc F() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "a_test.go"), []byte("package a\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { _ = F() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	listing, err := goListDepsTest([]string{"./..."})
	if err != nil {
		t.Fatalf("goListDepsTest: %v", err)
	}
	tb := listing.testBinaries["example.com/gd/a"]
	if tb == nil {
		t.Fatal("no test binary entry listed for example.com/gd/a")
	}
	if tb.DefaultGODEBUG == "" {
		t.Error("test binary DefaultGODEBUG did not decode despite a godebug directive in go.mod")
	}
}
