// affected_tests is a drop-in replacement for the gotestreport CLI that
// additionally skips Go test packages whose transitive inputs are unchanged
// since the last recorded passing run for the same test profile.
//
// Affected detection is best-effort: any failure during enumeration, hashing,
// or cache access falls back to running every requested package. A cold cache
// always runs everything.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"sidekick/common"
	"sidekick/gotestreport"
)

// hashedEnvVars are the environment variables that, when set, affect what
// Go tests do at runtime and therefore must be folded into the per-profile
// signature so unit vs integration vs e2e profiles never share cache entries.
var hashedEnvVars = []string{
	"SIDE_INTEGRATION_TEST",
	"SIDE_E2E_TEST",
}

func main() {
	code, err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(code)
}

func run(args []string, stdout, stderr io.Writer) (int, error) {
	force := os.Getenv("AFFECTED_TESTS_FORCE") != ""
	cleaned := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--all", "--force":
			force = true
		default:
			cleaned = append(cleaned, a)
		}
	}
	flags, patterns := splitArgs(cleaned)
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}

	var (
		toRun      []string
		skipped    []string
		pkgHashes  map[string]string
		profileSig string
	)

	// Hashing is independent of cache loading so a corrupt or unreadable
	// cache file does not lose us the ability to record current passes.
	sig, hashes, selected, hashErr := computeHashes(flags, patterns)
	if hashErr != nil {
		fmt.Fprintf(stderr, "affected-tests: cannot determine affected set, running all packages: %v\n", hashErr)
	} else {
		profileSig = sig
		pkgHashes = hashes
	}

	switch {
	case force:
		// Bypass: run everything that matches, but still record passes if
		// hashing succeeded so subsequent non-force invocations benefit.
		toRun = fallbackRunList(patterns, selected)
	case hashErr != nil:
		toRun = fallbackRunList(patterns, nil)
	default:
		runList, skipList, cacheErr := selectAffected(sig, selected, hashes)
		if cacheErr != nil {
			fmt.Fprintf(stderr, "affected-tests: cache read failed, running all packages (will refresh cache after run): %v\n", cacheErr)
			toRun = append([]string(nil), selected...)
		} else {
			toRun = runList
			skipped = skipList
		}
	}

	if !force && hashErr == nil && len(toRun) == 0 {
		fmt.Fprintf(stdout, "affected-tests: all %d package(s) skipped (cached pass)\n", len(skipped))
		return 0, nil
	}
	if !force && len(skipped) > 0 {
		fmt.Fprintf(stderr, "affected-tests: skipping %d cached-pass package(s), running %d\n", len(skipped), len(toRun))
	}

	tracker := newPackageTracker()
	// Persist passes incrementally while go test runs, so a killed run (CI
	// timeout, Ctrl-C) still caches the packages that already passed. Only
	// eligible when hashing succeeded, mirroring the pre-existing end-of-run
	// caching condition.
	var writer *cacheWriter
	if profileSig != "" && len(pkgHashes) > 0 {
		writer = newCacheWriter(profileSig, describeProfile(flags), pkgHashes, nil, updateCachePasses, func(err error) {
			fmt.Fprintf(stderr, "affected-tests: failed to update cache: %v\n", err)
		})
		tracker.onPass = writer.enqueue
	}
	exitCode, err := runGoTest(flags, toRun, stdout, stderr, tracker)
	if writer != nil {
		writer.close()
	}
	if err != nil {
		return exitCode, err
	}

	return exitCode, nil
}

// fallbackRunList returns the package list to run when we cannot consult the
// affected-set cache. Prefer concrete import paths from go list (so cache
// updates can key by them); fall back to the raw patterns if go list failed.
func fallbackRunList(patterns, resolved []string) []string {
	if len(resolved) > 0 {
		return append([]string(nil), resolved...)
	}
	out, err := goListPackagesMatching(patterns)
	if err != nil || len(out) == 0 {
		return append([]string(nil), patterns...)
	}
	return out
}

// valueTakingGoTestFlags is the allowlist of go test / go build flags that
// consume a following positional argument as their value when used in the
// "-flag value" form (as opposed to "-flag=value"). Anything not listed is
// treated as a boolean flag, so a bare import-path argument like
// "sidekick/env" that follows e.g. "-v" is correctly identified as a package
// pattern rather than swallowed as a flag value.
var valueTakingGoTestFlags = map[string]bool{
	"run": true, "skip": true, "count": true, "timeout": true,
	"bench": true, "benchtime": true, "cpu": true, "parallel": true,
	"shuffle": true, "fuzz": true, "fuzztime": true, "fuzzminimizetime": true,
	"fuzzcachedir": true, "list": true,
	"coverpkg": true, "coverprofile": true, "covermode": true,
	"cpuprofile": true, "blockprofile": true, "blockprofilerate": true,
	"memprofile": true, "memprofilerate": true, "mutexprofile": true,
	"mutexprofilefraction": true, "outputdir": true, "trace": true,
	"tags": true, "gcflags": true, "ldflags": true, "asmflags": true,
	"gccgoflags": true, "mod": true, "modfile": true, "pgo": true,
	"installsuffix": true, "toolexec": true, "overlay": true, "pkgdir": true,
	"exec": true, "o": true, "p": true, "buildmode": true, "compiler": true,
}

func flagTakesValue(flag string) bool {
	name := strings.TrimLeft(flag, "-")
	name = strings.TrimPrefix(name, "test.")
	return valueTakingGoTestFlags[name]
}

// splitArgs separates wrapper-forwarded args into go-test flags (with their
// values) and package patterns. Flags and patterns may be interleaved in any
// order; an explicit "--" forces everything that follows to be treated as a
// pattern. A flag that does not appear in valueTakingGoTestFlags is assumed
// boolean, so e.g. "./... -v" parses as one pattern + one bool flag.
func splitArgs(args []string) (flags, patterns []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			patterns = append(patterns, args[i+1:]...)
			return
		}
		if !strings.HasPrefix(a, "-") {
			patterns = append(patterns, a)
			continue
		}
		flags = append(flags, a)
		if strings.Contains(a, "=") {
			continue
		}
		if !flagTakesValue(a) {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return
}

// looksLikePackagePattern is a best-effort recognizer for package-pattern
// arguments. It is retained for documentation/test purposes; splitArgs no
// longer relies on it to disambiguate flag values from package patterns.
func looksLikePackagePattern(s string) bool {
	if s == "all" || s == "..." {
		return true
	}
	if strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") {
		return true
	}
	if strings.Contains(s, "/...") || strings.HasSuffix(s, "/...") {
		return true
	}
	return false
}

func describeProfile(flags []string) string {
	parts := []string{"go test " + strings.Join(flags, " ")}
	for _, e := range hashedEnvVars {
		if v := os.Getenv(e); v != "" {
			parts = append(parts, e+"="+v)
		}
	}
	return strings.Join(parts, " | ")
}

func computeProfileSignature(flags []string, envLookup func(string) string) string {
	h := sha256.New()
	for _, f := range flags {
		fmt.Fprintf(h, "FLAG %s\n", f)
	}
	envs := append([]string(nil), hashedEnvVars...)
	if extra := envLookup("AFFECTED_TESTS_HASH_ENV"); extra != "" {
		for _, e := range strings.Split(extra, ",") {
			if e = strings.TrimSpace(e); e != "" {
				envs = append(envs, e)
			}
		}
	}
	sort.Strings(envs)
	seen := map[string]bool{}
	for _, e := range envs {
		if seen[e] {
			continue
		}
		seen[e] = true
		fmt.Fprintf(h, "ENV %s=%s\n", e, envLookup(e))
	}
	return hex.EncodeToString(h.Sum(nil))
}

type goListPackage struct {
	Dir             string
	ImportPath      string
	Standard        bool
	Module          *goListModule
	GoFiles         []string
	CgoFiles        []string
	EmbedFiles      []string
	TestGoFiles     []string
	XTestGoFiles    []string
	TestEmbedFiles  []string
	XTestEmbedFiles []string
	Imports         []string
	TestImports     []string
	XTestImports    []string
	Deps            []string
	ForTest         string
	DefaultGODEBUG  string
	Error           *goListError
}

type goListModule struct {
	Path      string
	Version   string
	Main      bool
	GoVersion string
	Sum       string
	GoModSum  string
	Replace   *goListModule
}

type goListError struct {
	Err string
}

type pkgListing struct {
	byPath       map[string]*goListPackage
	testBinaries map[string]*goListPackage // keyed by base import path (without .test)
	// testVariants maps the base import path of a tested package to all
	// go-list entries built specifically for that test binary (the in-test
	// variant of the package itself and any synthetic external test package
	// like "P_test [P.test]"). Their ImportPaths carry build-context
	// brackets and so wouldn't be discovered via a plain Deps walk; this
	// index lets the hasher pick them up by base path directly.
	testVariants map[string][]*goListPackage
}

func goListPackagesMatching(patterns []string) ([]string, error) {
	args := append([]string{"list", "-e", "-f", "{{.ImportPath}}"}, patterns...)
	out, err := exec.Command("go", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("go list %v: %w", patterns, err)
	}
	var pkgs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pkgs = append(pkgs, line)
	}
	return pkgs, nil
}

func goListDepsTest(patterns []string) (*pkgListing, error) {
	args := append([]string{"list", "-e", "-json", "-deps", "-test"}, patterns...)
	out, err := exec.Command("go", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("go list -deps -test: %w", err)
	}
	listing := &pkgListing{
		byPath:       map[string]*goListPackage{},
		testBinaries: map[string]*goListPackage{},
		testVariants: map[string][]*goListPackage{},
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var p goListPackage
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("decode go list output: %w", err)
		}
		// Fail safe: any per-package Error from go list (broken imports,
		// missing modules, etc.) means our dependency graph and therefore the
		// computed hashes are not trustworthy. Surface the error so the caller
		// runs the requested packages instead of risking incorrect skips.
		if p.Error != nil && strings.TrimSpace(p.Error.Err) != "" {
			return nil, fmt.Errorf("go list reported error for %s: %s", p.ImportPath, p.Error.Err)
		}
		pp := p
		switch {
		case strings.HasSuffix(pp.ImportPath, ".test"):
			base := strings.TrimSuffix(pp.ImportPath, ".test")
			listing.testBinaries[base] = &pp
		case pp.ForTest != "":
			if _, ok := listing.byPath[pp.ImportPath]; !ok {
				listing.byPath[pp.ImportPath] = &pp
			}
			listing.testVariants[pp.ForTest] = append(listing.testVariants[pp.ForTest], &pp)
		default:
			listing.byPath[pp.ImportPath] = &pp
		}
	}
	return listing, nil
}

func readModulePath() (string, error) {
	out, err := exec.Command("go", "list", "-m").Output()
	if err != nil {
		return "", fmt.Errorf("go list -m: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// hashFormatVersion is folded into every package hash so that changing what a
// hash covers retires the passes recorded under the previous meaning instead
// of letting them match.
const hashFormatVersion = 2

// commonHashBase covers the inputs shared by every package: the test profile
// and the toolchain. The dependency manifest is deliberately absent, because
// manifest churn only changes the build of packages importing the affected
// module, which every package hash already covers through the resolved module
// identity of its dependencies. Hashing go.mod and go.sum wholesale instead
// invalidates every cached package on any dependency edit.
func commonHashBase(profileSig string) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "FORMAT %d\n", hashFormatVersion)
	fmt.Fprintf(&buf, "PROFILE %s\n", profileSig)
	fmt.Fprintf(&buf, "GOVERSION %s\n", runtime.Version())
	return buf.Bytes()
}

// packageClosure returns the set of import paths whose source file changes are
// treated as inputs to pkg's hash: pkg itself plus its transitive build and
// test dependencies. It mirrors exactly what computePackageHash folds into the
// hash, so callers can reason about which dependency edges are captured.
func packageClosure(pkg string, listing *pkgListing) map[string]bool {
	closure := map[string]bool{pkg: true}
	if tb := listing.testBinaries[pkg]; tb != nil {
		for _, d := range tb.Deps {
			closure[d] = true
		}
	} else if bp := listing.byPath[pkg]; bp != nil {
		for _, d := range bp.Deps {
			closure[d] = true
		}
		for _, d := range bp.TestImports {
			closure[d] = true
		}
		for _, d := range bp.XTestImports {
			closure[d] = true
		}
	}
	return closure
}

func computePackageHash(pkg string, listing *pkgListing, base []byte) (string, error) {
	h := sha256.New()
	h.Write(base)

	// godebug settings (go.mod directives, //go:debug lines, the language
	// version defaults) change how the test binary behaves at runtime without
	// changing any source file, and go list resolves them for us.
	if tb := listing.testBinaries[pkg]; tb != nil && tb.DefaultGODEBUG != "" {
		fmt.Fprintf(h, "GODEBUG %s\n", tb.DefaultGODEBUG)
	}

	closure := packageClosure(pkg, listing)

	sorted := make([]string, 0, len(closure))
	for k := range closure {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	for _, dep := range sorted {
		dp := listing.byPath[dep]
		if dp == nil {
			// Fail safe: if a dep referenced by the closure has no
			// corresponding go-list entry we cannot hash its inputs, so
			// returning an error here makes the caller fall back to running
			// every requested package rather than risking an incorrect skip.
			return "", fmt.Errorf("no go list entry for dependency %q of %q", dep, pkg)
		}
		if dp.Standard {
			// Standard library content is covered by the Go toolchain version
			// already mixed into the base hash; skip per-file hashing for speed.
			continue
		}
		hashModuleIdentity(h, dp.Module)
		if err := hashPackageFiles(h, dp, dep == pkg); err != nil {
			return "", err
		}
	}

	// Also fold in any go-list test variants targeting the selected package
	// (e.g. a synthetic "P_test [P.test]" external test package) whose
	// bracketed ImportPaths may not appear in the Deps closure above.
	variants := listing.testVariants[pkg]
	if len(variants) > 0 {
		sortedVariants := make([]*goListPackage, len(variants))
		copy(sortedVariants, variants)
		sort.Slice(sortedVariants, func(i, j int) bool {
			return sortedVariants[i].ImportPath < sortedVariants[j].ImportPath
		})
		for _, v := range sortedVariants {
			if v.Standard {
				continue
			}
			if err := hashPackageFiles(h, v, true); err != nil {
				return "", err
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashModuleIdentity mixes the resolved module of a dependency into h,
// following replacements to the module actually built. Version, language
// version and go.sum checksums decide which source the build consumes, so they
// must be inputs even when the files we hashed are byte-identical.
func hashModuleIdentity(h hash.Hash, m *goListModule) {
	for ; m != nil; m = m.Replace {
		fmt.Fprintf(h, "MOD %s@%s go=%s sum=%s gomodsum=%s\n", m.Path, m.Version, m.GoVersion, m.Sum, m.GoModSum)
	}
}

// hashPackageFiles mixes the content hashes of a package's source/embed files
// into h. When includeTests is true, the package's own *_test.go and test
// embed files are also included; we intentionally do NOT include test files
// for transitive dependencies because Go forbids _test.go in one package from
// being imported by another package, so a dependency's test files cannot
// influence a consumer's test results.
func hashPackageFiles(h hash.Hash, p *goListPackage, includeTests bool) error {
	files := append([]string(nil), p.GoFiles...)
	files = append(files, p.CgoFiles...)
	files = append(files, p.EmbedFiles...)
	if includeTests {
		files = append(files, p.TestGoFiles...)
		files = append(files, p.XTestGoFiles...)
		files = append(files, p.TestEmbedFiles...)
		files = append(files, p.XTestEmbedFiles...)
	}
	sort.Strings(files)
	fmt.Fprintf(h, "PKG %s\n", p.ImportPath)
	for _, f := range files {
		full := f
		if !filepath.IsAbs(f) {
			full = filepath.Join(p.Dir, f)
		}
		data, err := os.ReadFile(full)
		if err != nil {
			// Fail safe: if go list reported a file we cannot read, we cannot
			// produce a trustworthy hash and the caller must fall back to
			// running everything rather than risk an incorrect skip.
			return fmt.Errorf("hash %s: %w", full, err)
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(h, "FILE %s %s\n", f, hex.EncodeToString(sum[:]))
	}
	return nil
}

// computeHashes resolves the requested patterns to concrete packages and
// computes a content hash per package. It is deliberately independent of the
// cache so a corrupt/unreadable cache cannot prevent recording passes from
// this run.
func computeHashes(flags, patterns []string) (string, map[string]string, []string, error) {
	profileSig := computeProfileSignature(flags, os.Getenv)

	selected, err := goListPackagesMatching(patterns)
	if err != nil {
		return "", nil, nil, err
	}
	if len(selected) == 0 {
		return profileSig, map[string]string{}, nil, nil
	}

	listing, err := goListDepsTest(patterns)
	if err != nil {
		return "", nil, nil, err
	}

	base := commonHashBase(profileSig)

	hashes := make(map[string]string, len(selected))
	for _, p := range selected {
		h, err := computePackageHash(p, listing, base)
		if err != nil {
			return "", nil, nil, err
		}
		hashes[p] = h
	}

	return profileSig, hashes, selected, nil
}

// selectAffected partitions selected into packages that must run vs packages
// whose recorded hash matches the cache. If the cache cannot be loaded the
// caller should run everything and still attempt to record passes.
func selectAffected(profileSig string, selected []string, hashes map[string]string) ([]string, []string, error) {
	cache, err := loadCache()
	if err != nil {
		return nil, nil, err
	}
	prof := cache.Profiles[profileSig]

	var toRun, toSkip []string
	for _, p := range selected {
		if prof != nil {
			if e, ok := prof.Packages[p]; ok && e.hasHash(hashes[p]) {
				toSkip = append(toSkip, p)
				continue
			}
		}
		toRun = append(toRun, p)
	}
	sort.Strings(toRun)
	sort.Strings(toSkip)
	return toRun, toSkip, nil
}

// maxPassesPerPackage bounds the per-package set of remembered passing hashes
// so the on-disk cache cannot grow unboundedly as a working tree oscillates
// across many distinct contents. Oldest entries (by PassedAt) are evicted
// first when the cap is exceeded.
const maxPassesPerPackage = 500

type cacheFile struct {
	Profiles map[string]*profileEntry `json:"profiles"`
}

type profileEntry struct {
	Description string                  `json:"description,omitempty"`
	Packages    map[string]packageEntry `json:"packages"`
}

// packageEntry remembers every closure-hash at which a package was last seen
// passing under a given profile so that oscillating the working tree between
// previously-passing contents still hits the cache.
//
// LegacyHash / LegacyPassedAt accept the pre-multi-hash on-disk format so
// existing caches read cleanly; readCacheFromPath migrates them into Passes
// and clears them, so they are never written back out.
type packageEntry struct {
	Passes         []passEntry `json:"passes,omitempty"`
	LegacyHash     string      `json:"hash,omitempty"`
	LegacyPassedAt string      `json:"passedAt,omitempty"`
}

type passEntry struct {
	Hash     string `json:"hash"`
	PassedAt string `json:"passedAt"`
}

func (pe packageEntry) hasHash(h string) bool {
	if h == "" {
		return false
	}
	for _, p := range pe.Passes {
		if p.Hash == h {
			return true
		}
	}
	return false
}

func cachePath() (string, error) {
	home, err := common.GetSidekickCacheHome()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "affected_tests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	module, err := readModulePath()
	if err != nil {
		return "", err
	}
	safe := sanitizeModulePath(module)
	return filepath.Join(dir, safe+".json"), nil
}

func sanitizeModulePath(m string) string {
	m = strings.TrimSpace(m)
	if m == "" {
		m = "unknown_module"
	}
	repl := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return repl.Replace(m)
}

func readCacheFromPath(path string) (*cacheFile, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &cacheFile{Profiles: map[string]*profileEntry{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var c cacheFile
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse cache: %w", err)
	}
	if c.Profiles == nil {
		c.Profiles = map[string]*profileEntry{}
	}
	for _, prof := range c.Profiles {
		if prof == nil {
			continue
		}
		if prof.Packages == nil {
			prof.Packages = map[string]packageEntry{}
			continue
		}
		for k, pe := range prof.Packages {
			if len(pe.Passes) == 0 && pe.LegacyHash != "" {
				pe.Passes = []passEntry{{Hash: pe.LegacyHash, PassedAt: pe.LegacyPassedAt}}
			}
			pe.LegacyHash = ""
			pe.LegacyPassedAt = ""
			prof.Packages[k] = pe
		}
	}
	return &c, nil
}

func loadCache() (*cacheFile, error) {
	path, err := cachePath()
	if err != nil {
		return nil, err
	}
	var c *cacheFile
	// Reads use a shared lock so we never observe a partial write from a
	// concurrent updater. Writers still use exclusive locking + atomic rename.
	if err := withFileLockShared(path+".lock", func() error {
		cc, err := readCacheFromPath(path)
		if err != nil {
			return err
		}
		c = cc
		return nil
	}); err != nil {
		return nil, err
	}
	return c, nil
}

// withFileLock acquires an exclusive flock on a sentinel file alongside the
// cache so concurrent updates from different worktrees serialize and the
// read-modify-write below cannot lose entries.
func withFileLock(lockPath string, fn func() error) error {
	return withFileLockMode(lockPath, syscall.LOCK_EX, fn)
}

// withFileLockShared acquires a shared (read) flock; multiple readers can hold
// it concurrently but it blocks against an exclusive writer.
func withFileLockShared(lockPath string, fn func() error) error {
	return withFileLockMode(lockPath, syscall.LOCK_SH, fn)
}

func withFileLockMode(lockPath string, mode int, fn func() error) error {
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), mode); err != nil {
		return err
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	return fn()
}

func updateCachePasses(profileSig, description string, passed []string, hashes map[string]string) error {
	path, err := cachePath()
	if err != nil {
		return err
	}
	return withFileLock(path+".lock", func() error {
		c, err := readCacheFromPath(path)
		if err != nil {
			// The on-disk cache is corrupt/unreadable. Quarantine it (so a
			// human can inspect it later if needed) and continue with a fresh
			// cache: this lets the affected-set mechanism self-heal after a
			// corruption rather than getting stuck in permanent fallback.
			quarantine := fmt.Sprintf("%s.corrupt.%d", path, time.Now().UnixNano())
			if renameErr := os.Rename(path, quarantine); renameErr != nil && !errors.Is(renameErr, os.ErrNotExist) {
				return fmt.Errorf("read cache for update: %w (quarantine failed: %v)", err, renameErr)
			}
			c = &cacheFile{Profiles: map[string]*profileEntry{}}
		}
		applyPasses(c, profileSig, description, passed, hashes, time.Now().UTC())
		data, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			return err
		}
		// Use a unique temp file per writer so we cannot collide with another
		// process that crashed mid-write (or an unrelated stale .tmp file).
		f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp.*")
		if err != nil {
			return err
		}
		tmpName := f.Name()
		if _, err := f.Write(data); err != nil {
			f.Close()
			os.Remove(tmpName)
			return err
		}
		if err := f.Close(); err != nil {
			os.Remove(tmpName)
			return err
		}
		if err := os.Rename(tmpName, path); err != nil {
			os.Remove(tmpName)
			return err
		}
		return nil
	})
}

func applyPasses(c *cacheFile, profileSig, description string, passed []string, hashes map[string]string, now time.Time) {
	if c.Profiles == nil {
		c.Profiles = map[string]*profileEntry{}
	}
	pe := c.Profiles[profileSig]
	if pe == nil {
		pe = &profileEntry{Packages: map[string]packageEntry{}}
		c.Profiles[profileSig] = pe
	}
	if pe.Packages == nil {
		pe.Packages = map[string]packageEntry{}
	}
	if description != "" {
		pe.Description = description
	}
	ts := now.Format(time.RFC3339)
	for _, p := range passed {
		h := hashes[p]
		if h == "" {
			continue
		}
		entry := pe.Packages[p]
		// If this hash was already recorded as passing, refresh its timestamp
		// by moving it to the newest position so it survives future evictions.
		for i, pp := range entry.Passes {
			if pp.Hash == h {
				entry.Passes = append(entry.Passes[:i], entry.Passes[i+1:]...)
				break
			}
		}
		entry.Passes = append(entry.Passes, passEntry{Hash: h, PassedAt: ts})
		if len(entry.Passes) > maxPassesPerPackage {
			entry.Passes = entry.Passes[len(entry.Passes)-maxPassesPerPackage:]
		}
		pe.Packages[p] = entry
	}
}

// packageTracker observes go test -json package-level events and remembers
// the final per-package action so the caller can record non-failing packages
// in the affected-tests cache.
type packageTracker struct {
	mu      sync.Mutex
	results map[string]string // package -> "pass" | "fail" | "skip"
	// onPass, when set, is invoked (from the output-streaming goroutine) for
	// each package-level pass/skip event; it must be cheap and non-blocking.
	onPass func(pkg string)
}

func newPackageTracker() *packageTracker {
	return &packageTracker{results: map[string]string{}}
}

func (t *packageTracker) observeLine(line []byte) {
	if len(line) == 0 || line[0] != '{' {
		return
	}
	var ev struct {
		Action  string `json:"Action"`
		Package string `json:"Package"`
		Test    string `json:"Test"`
	}
	if err := json.Unmarshal(line, &ev); err != nil {
		return
	}
	if ev.Package == "" || ev.Test != "" {
		return
	}
	switch ev.Action {
	case "pass", "fail", "skip":
		t.mu.Lock()
		t.results[ev.Package] = ev.Action
		t.mu.Unlock()
		if t.onPass != nil && ev.Action != "fail" {
			t.onPass(ev.Package)
		}
	}
}

// passed returns packages whose final package-level action was "pass" OR
// "skip". A package-level skip means either the package has no test files or
// every test was skipped (e.g. build tags excluded them); in both cases the
// run did not fail and re-running at the same closure hash would produce the
// same outcome, so it is safe (and necessary, to avoid forever re-running
// no-test packages) to record them as cacheable.
func (t *packageTracker) passed() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.results))
	for p, r := range t.results {
		if r == "pass" || r == "skip" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// cacheFlushInterval bounds how often mid-run pass results are written to the
// affected-tests cache: each write re-reads and re-marshals the whole cache
// file, so batching amortizes that cost across package completions.
const cacheFlushInterval = 2 * time.Second

// cacheWriter persists package pass results to the affected-tests cache
// incrementally on a dedicated goroutine, so a run killed mid-way still
// benefits future runs. enqueue is cheap and never blocks on flock
// acquisition or disk I/O, keeping the go test output-streaming path
// unstalled.
type cacheWriter struct {
	profileSig  string
	description string
	hashes      map[string]string
	flushFn     func(profileSig, description string, passed []string, hashes map[string]string) error
	warn        func(error)

	mu       sync.Mutex
	pending  []string
	enqueued map[string]bool

	stop chan struct{}
	done chan struct{}
}

// newCacheWriter starts the flush goroutine, which writes pending passes at
// most once per tick. tick may be injected by tests; when nil, a ticker at
// cacheFlushInterval is used. close must be called to stop the goroutine and
// persist any remaining pending passes.
func newCacheWriter(profileSig, description string, hashes map[string]string, tick <-chan time.Time, flushFn func(string, string, []string, map[string]string) error, warn func(error)) *cacheWriter {
	w := &cacheWriter{
		profileSig:  profileSig,
		description: description,
		hashes:      hashes,
		flushFn:     flushFn,
		warn:        warn,
		enqueued:    map[string]bool{},
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
	go func() {
		defer close(w.done)
		if tick == nil {
			t := time.NewTicker(cacheFlushInterval)
			defer t.Stop()
			tick = t.C
		}
		for {
			select {
			case <-tick:
				w.flushPending()
			case <-w.stop:
				w.flushPending()
				return
			}
		}
	}()
	return w
}

// enqueue records a package pass for eventual persistence. Packages without a
// computed hash are ignored (same eligibility filter as the end-of-run logic)
// and each package is persisted at most once.
func (w *cacheWriter) enqueue(pkg string) {
	if _, ok := w.hashes[pkg]; !ok {
		return
	}
	w.mu.Lock()
	if !w.enqueued[pkg] {
		w.enqueued[pkg] = true
		w.pending = append(w.pending, pkg)
	}
	w.mu.Unlock()
}

func (w *cacheWriter) flushPending() {
	w.mu.Lock()
	batch := w.pending
	w.pending = nil
	w.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	if err := w.flushFn(w.profileSig, w.description, batch, w.hashes); err != nil {
		w.warn(err)
		// Requeue so a later tick or the final flush retries the batch,
		// merging with anything enqueued while the write was in flight.
		w.mu.Lock()
		w.pending = append(batch, w.pending...)
		w.mu.Unlock()
	}
}

// close signals the flush goroutine to perform a final flush of anything
// still pending and waits for it to finish, so no observed pass is lost.
func (w *cacheWriter) close() {
	close(w.stop)
	<-w.done
}

func runGoTest(flags, pkgs []string, stdout, stderr io.Writer, tracker *packageTracker) (int, error) {
	testArgs := append([]string{"test", "-json"}, flags...)
	testArgs = append(testArgs, pkgs...)
	cmd := exec.Command("go", testArgs...)
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return 1, fmt.Errorf("stdout pipe: %w", err)
	}
	streamer := gotestreport.NewStreamer(stdout)
	if err := cmd.Start(); err != nil {
		return 1, fmt.Errorf("start go test: %w", err)
	}

	// Tee child stdout through a line-buffering observer so that the same
	// reads that drive gotestreport.ProcessReader also feed per-package
	// pass/fail tracking. Using io.TeeReader (rather than an io.Pipe + writer
	// goroutine) means we never have an independent producer that could
	// block if ProcessReader returns before fully draining the stream.
	observer := newLineObserver(tracker.observeLine)
	teed := io.TeeReader(pipe, observer)
	readErr := streamer.ProcessReader(teed)
	// Ensure the child can finish writing even if ProcessReader stopped
	// early; otherwise cmd.Wait would block on a full OS pipe buffer. The
	// drained bytes still flow through the observer via the tee, keeping
	// tracker state consistent with what the child emitted.
	if _, drainErr := io.Copy(io.Discard, teed); drainErr != nil && readErr == nil {
		readErr = drainErr
	}
	observer.flush()
	cmdErr := cmd.Wait()

	if readErr != nil {
		fmt.Fprintf(stderr, "error reading test output: %v\n", readErr)
	}
	if stderrBuf.Len() > 0 && (cmdErr != nil || streamer.HasFailures() || readErr != nil) {
		fmt.Fprint(stderr, stderrBuf.String())
	}
	fmt.Fprint(stdout, streamer.Summary())

	if cmdErr != nil || streamer.HasFailures() || readErr != nil {
		return 1, nil
	}
	return 0, nil
}

// lineObserver is an io.Writer that buffers incoming bytes and invokes cb
// once per newline-terminated line. Bytes after the last newline are kept
// in the buffer until either another newline arrives or flush is called.
type lineObserver struct {
	buf bytes.Buffer
	cb  func([]byte)
}

func newLineObserver(cb func([]byte)) *lineObserver {
	return &lineObserver{cb: cb}
}

func (lo *lineObserver) Write(p []byte) (int, error) {
	lo.buf.Write(p)
	for {
		data := lo.buf.Bytes()
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			break
		}
		// Copy the line out before advancing the buffer, since cb may
		// retain (or unmarshal from) the slice asynchronously.
		line := make([]byte, idx)
		copy(line, data[:idx])
		lo.cb(line)
		lo.buf.Next(idx + 1)
	}
	return len(p), nil
}

func (lo *lineObserver) flush() {
	if lo.buf.Len() == 0 {
		return
	}
	tail := make([]byte, lo.buf.Len())
	copy(tail, lo.buf.Bytes())
	lo.buf.Reset()
	lo.cb(tail)
}
