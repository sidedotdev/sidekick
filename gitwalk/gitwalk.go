// Package gitwalk provides a filepath.WalkDir-style API over a git
// commit, served from the object store with parallel content
// prefetching.
//
// Compared to mounting via FUSE, this skips all kernel<->userspace
// FUSE round-trips. The caller gets an interface very close to
// filepath.WalkDir; the only changes are:
//
//   - The DirEntry has an Open() method (no path-based open).
//   - Walk is concurrent under the hood: the caller's callback is
//     invoked serially in path order, but content for files ahead of
//     the cursor is prefetched in parallel.
package gitwalk

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/denormal/go-gitignore"

	"sidekick/gitwalk/catfile"
)

// Walker walks a git commit's tree.
type Walker struct {
	repoPath string
	ref      string
	prefetch int // 0 = no prefetch, N = up to N reads in flight

	rootSHA string
	pool    *catfile.Pool
	shaSize map[string]int64

	sideIgnoreFiles []string
	sideIgnoreTmp   string // tmp dir holding extracted sideignore files
	// sideIgnore is a list of (dir, matcher) pairs sorted deepest-first
	// so the first matching layer wins.
	sideIgnore []sideIgnoreLayer

	overlay Overlay
}

type sideIgnoreLayer struct {
	dir     string // path relative to walk root, "" for root
	matcher gitignore.GitIgnore
}

// Options configures a Walker.
type Options struct {
	// RepoPath is the path to a git repo (with .git or bare).
	RepoPath string
	// Ref is the commit/ref/tree to walk.
	Ref string
	// Prefetch is the number of in-flight content reads. 0 disables
	// prefetching. Defaults to runtime.NumCPU().
	Prefetch int
	// PoolSize is the size of the cat-file --batch pool. Defaults to
	// max(Prefetch, 1).
	PoolSize int
	// SideIgnoreFiles are filenames whose contents (when present in the
	// walked tree) filter the entries the walker emits. Patterns use
	// gitignore syntax; matches in deeper files override shallower ones.
	// Negation (!) patterns can re-include entries hidden by a shallower
	// side-ignore but cannot surface files git doesn't track unless they
	// arrive via Overlay.
	SideIgnoreFiles []string

	// Overlay, if non-nil, presents a working-copy view on top of Ref:
	// added, modified, or deleted paths. Walk merges these with the
	// tracked tree before applying side-ignore, so the consumer sees a
	// single coherent path-ordered stream.
	Overlay Overlay
	// SkipSizes disables the upfront ls-tree -l pass; sizes will be 0
	// until a file is opened.
	SkipSizes bool
}

// Overlay supplies working-copy changes that gitwalk applies on top of
// the walked ref. The walker calls Changes() once at Walk time; the
// caller's per-entry Open callback is invoked when the consumer opens
// an overlay-affected path.
type Overlay interface {
	Changes() []Change
}

// ChangeKind describes what happened to a path relative to the walked ref.
type ChangeKind int

const (
	ChangeAdded ChangeKind = iota + 1
	ChangeModified
	ChangeDeleted
)

// Change is one entry in the overlay.
type Change struct {
	Path string
	Kind ChangeKind
	// Mode is the git-style mode string (e.g. "100644", "100755",
	// "120000" for symlinks, "040000" for added directories). Required
	// for Added; ignored for Deleted; optional for Modified (defaults
	// to whatever the tracked tree had).
	Mode string
	// Size, when known, populates the entry's reported size. Leave 0
	// to defer until Open.
	Size int64
	// Open returns the current content. Required for Added and
	// Modified; ignored for Deleted. The walker calls this lazily —
	// from the consumer's entry.Open() — not during merge.
	Open func() (io.ReadCloser, error)
}

func New(ctx context.Context, opts Options) (*Walker, error) {
	if opts.PoolSize == 0 {
		opts.PoolSize = opts.Prefetch
		if opts.PoolSize < 1 {
			opts.PoolSize = 1
		}
	}
	cmd := exec.CommandContext(ctx, "git", "-C", opts.RepoPath, "rev-parse", opts.Ref+"^{tree}")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("rev-parse %s^{tree}: %w", opts.Ref, err)
	}
	rootSHA := strings.TrimSpace(string(out))

	pool, err := catfile.NewPool(opts.RepoPath, opts.PoolSize)
	if err != nil {
		return nil, err
	}
	w := &Walker{
		repoPath:        opts.RepoPath,
		ref:             opts.Ref,
		prefetch:        opts.Prefetch,
		rootSHA:         rootSHA,
		pool:            pool,
		sideIgnoreFiles: opts.SideIgnoreFiles,
		overlay:         opts.Overlay,
	}
	if !opts.SkipSizes {
		if err := w.populateSizes(ctx); err != nil {
			pool.Close()
			return nil, err
		}
	}
	return w, nil
}

func (w *Walker) Close() error {
	if w.sideIgnoreTmp != "" {
		os.RemoveAll(w.sideIgnoreTmp)
	}
	return w.pool.Close()
}

// DirEntry is what callers receive. Mirrors fs.DirEntry with an extra
// Open() method.
type DirEntry interface {
	fs.DirEntry
	// Path returns the path relative to the walk root, using '/' as
	// separator (consistent across OSes; matches git tree paths).
	Path() string
	// Open returns a reader for a regular file's contents. For
	// directories or symlinks, returns an error.
	Open() (io.ReadCloser, error)
	// Size is the file size in bytes (0 if sizes weren't populated).
	Size() int64
	// SHA is the git blob SHA (lowercase hex).
	SHA() string
}

// WalkFunc is invoked for every entry in path order. Returning
// filepath.SkipDir skips a directory; returning any other error halts
// the walk and is returned from Walk.
type WalkFunc func(d DirEntry) error

// Walk visits every entry in the tree.
func (w *Walker) Walk(ctx context.Context, fn WalkFunc) error {
	// First pass: enumerate all entries with one ls-tree -r call.
	cmd := exec.CommandContext(ctx, "git", "-C", w.repoPath, "ls-tree", "-r", "-t", w.ref)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	type rawEntry struct {
		mode, kind, sha, path string
	}
	var entries []rawEntry
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<24)
	for sc.Scan() {
		line := sc.Text()
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		fields := strings.Fields(line[:tab])
		if len(fields) != 3 {
			continue
		}
		entries = append(entries, rawEntry{
			mode: fields[0], kind: fields[1], sha: fields[2], path: line[tab+1:],
		})
	}
	if err := cmd.Wait(); err != nil {
		return err
	}

	// Synthesize the root entry.
	all := make([]*entry, 0, len(entries)+1)
	mkDone := func() chan struct{} {
		if w.prefetch > 0 {
			return make(chan struct{})
		}
		return nil
	}
	all = append(all, &entry{
		walker: w, path: "", name: "", isDir: true, sha: w.rootSHA, mode: "040000",
		done: mkDone(),
	})
	for _, e := range entries {
		if e.kind == "commit" {
			continue // submodule
		}
		isDir := e.kind == "tree"
		isLink := e.mode == "120000"
		isExec := e.mode == "100755"
		ent := &entry{
			walker: w,
			path:   e.path,
			name:   filepath.Base(e.path),
			isDir:  isDir,
			isLink: isLink,
			isExec: isExec,
			sha:    e.sha,
			mode:   e.mode,
			done:   mkDone(),
		}
		if !isDir {
			ent.size = w.shaSize[e.sha]
		}
		all = append(all, ent)
	}

	// Apply the overlay (working-copy changes) on top of the tracked
	// tree. After this, `all` is the merged path-sorted view that
	// downstream filters (side-ignore) and the walk loop both see.
	if w.overlay != nil {
		all = w.mergeOverlay(all, mkDone)
	}

	// Side-ignore is built lazily here (rather than in New) so it can
	// see overlay-added .sideignore files in addition to tracked ones.
	if len(w.sideIgnoreFiles) > 0 {
		if err := w.buildSideIgnore(all); err != nil {
			return err
		}
	}

	// Prefetch driver. We launch goroutines that pull "next file
	// blob" jobs from a queue and store their content on the entry.
	// The main loop iterates entries in order, blocks until each
	// file's content is ready (if prefetch enabled), and invokes fn.
	skipping := "" // path prefix to skip after SkipDir

	if w.prefetch > 0 {
		return w.runPrefetched(all, fn, &skipping)
	}
	// No prefetch: simple serial walk.
	for _, e := range all {
		if skipping != "" && strings.HasPrefix(e.path, skipping+"/") {
			continue
		}
		skipping = ""
		// Side-ignore applies to leaves only. A directory may have a
		// deeper .sideignore that un-ignores its children, so we
		// can't prune at the directory level.
		if !e.isDir && w.isSideIgnored(e.path, false) {
			continue
		}
		if err := fn(e); err != nil {
			if errors.Is(err, filepath.SkipDir) && e.isDir {
				skipping = e.path
				continue
			}
			return err
		}
	}
	return nil
}

// runPrefetched dispatches blob reads to N workers while preserving
// callback ordering. The consumer's callback is invoked sequentially
// in path order; Open() on an entry blocks on the entry's done channel
// until its prefetch (if any) completes. The dispatch window is bounded
// so memory stays in check.
func (w *Walker) runPrefetched(all []*entry, fn WalkFunc, skipping *string) error {
	window := w.prefetch * 2
	if window < w.prefetch+1 {
		window = w.prefetch + 1
	}

	jobs := make(chan *entry, window)
	stop := make(chan struct{})

	var wg sync.WaitGroup
	for i := 0; i < w.prefetch; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range jobs {
				_ = e.preload()
				close(e.done)
			}
		}()
	}

	// Dispatcher: hand each non-passthrough blob to a worker. For
	// directories and symlinks we just close done immediately —
	// Open() doesn't need a prefetched buffer for those.
	go func() {
		defer close(jobs)
		for _, e := range all {
			if e.isDir || e.isLink {
				close(e.done)
				continue
			}
			select {
			case <-stop:
				// Halting: close done so any blocked Open()s in
				// flight return (they'll error from preload not
				// having run).
				close(e.done)
			case jobs <- e:
			}
		}
	}()

	var walkErr error
	for _, e := range all {
		if *skipping != "" && strings.HasPrefix(e.path, *skipping+"/") {
			continue
		}
		*skipping = ""
		if !e.isDir && w.isSideIgnored(e.path, false) {
			continue
		}
		if err := fn(e); err != nil {
			if errors.Is(err, filepath.SkipDir) && e.isDir {
				// SkipDir only prunes this subtree; the prefetcher
				// keeps running for the rest of the walk.
				*skipping = e.path
				continue
			}
			walkErr = err
			break
		}
		// Release memory once the consumer is done with this entry.
		e.data = nil
	}
	if walkErr != nil {
		close(stop)
	}
	wg.Wait()
	return walkErr
}

// entry implements DirEntry.
type entry struct {
	walker *Walker
	path   string
	name   string
	isDir  bool
	isLink bool
	isExec bool
	sha    string
	mode   string
	size   int64

	// If set, the entry's content comes from the overlay rather than
	// the git object store. Used for added or modified paths supplied
	// via Options.Overlay.
	overlayOpen func() (io.ReadCloser, error)

	mu     sync.Mutex
	data   []byte
	loaded bool
	err    error
	// done is closed by the prefetcher once preload completes for this
	// entry (or immediately if no prefetch was needed). Nil when
	// prefetching is disabled.
	done chan struct{}
}

func (e *entry) Name() string { return e.name }
func (e *entry) IsDir() bool  { return e.isDir }
func (e *entry) Type() fs.FileMode {
	switch {
	case e.isDir:
		return fs.ModeDir
	case e.isLink:
		return fs.ModeSymlink
	}
	return 0
}
func (e *entry) Info() (fs.FileInfo, error) { return &fileInfo{e: e}, nil }
func (e *entry) Path() string               { return e.path }
func (e *entry) Size() int64                { return e.size }
func (e *entry) SHA() string                { return e.sha }

func (e *entry) Open() (io.ReadCloser, error) {
	if e.isDir {
		return nil, errors.New("is a directory")
	}
	// Overlay (working-copy change) wins over everything else.
	if e.overlayOpen != nil {
		return e.overlayOpen()
	}
	if e.isLink {
		// Symlink content is the target.
		if err := e.preload(); err != nil {
			return nil, err
		}
		return io.NopCloser(bytes.NewReader(e.data)), nil
	}
	// If a prefetcher is running, wait for our blob to be ready;
	// otherwise load it inline.
	if e.done != nil {
		<-e.done
		if e.err != nil {
			// Prefetcher saw an error, or the walk was halted before
			// reaching us. Fall back to a direct read.
			if err := e.preload(); err != nil {
				return nil, err
			}
		}
	} else if err := e.preload(); err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(e.data)), nil
}

func (e *entry) preload() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loaded {
		return e.err
	}
	data, err := e.walker.pool.Object(e.sha)
	e.data = data
	e.err = err
	e.loaded = true
	if err == nil {
		e.size = int64(len(data))
	}
	return err
}

type fileInfo struct{ e *entry }

func (i *fileInfo) Name() string { return i.e.name }
func (i *fileInfo) Size() int64  { return i.e.size }
func (i *fileInfo) Mode() fs.FileMode {
	var m fs.FileMode = 0o644
	if i.e.isDir {
		m = fs.ModeDir | 0o755
	} else if i.e.isLink {
		m = fs.ModeSymlink | 0o777
	} else if i.e.isExec {
		m = 0o755
	}
	return m
}
func (i *fileInfo) ModTime() time.Time { return time.Time{} }
func (i *fileInfo) IsDir() bool        { return i.e.isDir }
func (i *fileInfo) Sys() any           { return nil }

func (w *Walker) populateSizes(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "git", "-C", w.repoPath, "ls-tree", "-r", "-l", w.ref)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sizes := map[string]int64{}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<24)
	for sc.Scan() {
		line := sc.Text()
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		fields := strings.Fields(line[:tab])
		if len(fields) < 4 || fields[1] != "blob" {
			continue
		}
		size, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			continue
		}
		sizes[fields[2]] = size
	}
	if err := cmd.Wait(); err != nil {
		return err
	}
	w.shaSize = sizes
	return nil
}

// mergeOverlay folds the overlay's changes into the entry list so the
// merged result reflects the working-copy view. Deletes drop entries
// (and their descendants if a directory was deleted). Modifications
// rewrite the matching entry's content source. Additions append new
// entries (and synthesize any missing intermediate directories).
// Returns the merged list, sorted by path.
func (w *Walker) mergeOverlay(all []*entry, mkDone func() chan struct{}) []*entry {
	changes := w.overlay.Changes()
	if len(changes) == 0 {
		return all
	}

	byPath := make(map[string]Change, len(changes))
	for _, c := range changes {
		byPath[c.Path] = c
	}

	// First pass: drop deletes (and any descendants under a deleted
	// directory). Apply modifications in place.
	deletedDirs := make([]string, 0)
	for _, c := range changes {
		if c.Kind == ChangeDeleted {
			deletedDirs = append(deletedDirs, c.Path)
		}
	}
	keep := all[:0]
	for _, e := range all {
		if c, ok := byPath[e.path]; ok {
			switch c.Kind {
			case ChangeDeleted:
				continue
			case ChangeModified:
				e.overlayOpen = c.Open
				if c.Size > 0 {
					e.size = c.Size
				}
				if c.Mode != "" {
					e.mode = c.Mode
					e.isExec = c.Mode == "100755"
					e.isLink = c.Mode == "120000"
				}
			}
		}
		// Filter out anything under a deleted directory.
		dropped := false
		for _, d := range deletedDirs {
			if e.path == d || strings.HasPrefix(e.path, d+"/") {
				dropped = true
				break
			}
		}
		if !dropped {
			keep = append(keep, e)
		}
	}
	all = keep

	// Second pass: additions. Track which paths already exist so we
	// don't double-insert (an Added change for a path that's also in
	// the tracked tree is treated as Modified).
	existing := make(map[string]bool, len(all))
	for _, e := range all {
		existing[e.path] = true
	}
	for _, c := range changes {
		if c.Kind != ChangeAdded || existing[c.Path] {
			continue
		}
		// Synthesize missing parent directories.
		parts := strings.Split(c.Path, "/")
		for i := 1; i < len(parts); i++ {
			parent := strings.Join(parts[:i], "/")
			if existing[parent] {
				continue
			}
			all = append(all, &entry{
				walker: w, path: parent, name: parts[i-1],
				isDir: true, mode: "040000", done: mkDone(),
			})
			existing[parent] = true
		}
		mode := c.Mode
		if mode == "" {
			mode = "100644"
		}
		isDir := mode == "040000"
		ent := &entry{
			walker: w,
			path:   c.Path,
			name:   parts[len(parts)-1],
			isDir:  isDir,
			isLink: mode == "120000",
			isExec: mode == "100755",
			mode:   mode,
			size:   c.Size,
			done:   mkDone(),
		}
		if !isDir {
			ent.overlayOpen = c.Open
		}
		all = append(all, ent)
		existing[c.Path] = true
	}

	sort.Slice(all, func(i, j int) bool { return all[i].path < all[j].path })
	return all
}

// buildSideIgnore finds entries in `all` whose basename matches one of
// w.sideIgnoreFiles, materializes their content, and constructs a
// denormal/go-gitignore matcher for each. Content comes from the overlay
// for overlay-added/modified entries, and from the object store via
// cat-file for tracked entries.
func (w *Walker) buildSideIgnore(all []*entry) error {
	wanted := make(map[string]bool, len(w.sideIgnoreFiles))
	for _, name := range w.sideIgnoreFiles {
		wanted[name] = true
	}
	var hits []*entry
	for _, e := range all {
		if e.isDir {
			continue
		}
		if wanted[filepath.Base(e.path)] {
			hits = append(hits, e)
		}
	}
	if len(hits) == 0 {
		return nil
	}

	tmp, err := os.MkdirTemp("", "gitwalk-sideignore-")
	if err != nil {
		return err
	}
	w.sideIgnoreTmp = tmp

	for _, e := range hits {
		var content []byte
		if e.overlayOpen != nil {
			rc, err := e.overlayOpen()
			if err != nil {
				return fmt.Errorf("overlay open %s: %w", e.path, err)
			}
			content, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return fmt.Errorf("overlay read %s: %w", e.path, err)
			}
		} else {
			content, err = w.pool.Object(e.sha)
			if err != nil {
				return fmt.Errorf("read %s: %w", e.path, err)
			}
		}
		dest := filepath.Join(tmp, e.path)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, content, 0o644); err != nil {
			return err
		}
		dir := filepath.Dir(e.path)
		if dir == "." {
			dir = ""
		}
		// The denormal library matches paths relative to the file's
		// directory. Constructing the matcher with `dir` as base means
		// it'll treat paths we pass in as repository-relative.
		base := filepath.Join(tmp, dir)
		gi, err := gitignore.NewRepositoryWithFile(base, filepath.Base(e.path))
		if err != nil {
			return err
		}
		w.sideIgnore = append(w.sideIgnore, sideIgnoreLayer{dir: dir, matcher: gi})
	}
	// Deepest first.
	sort.SliceStable(w.sideIgnore, func(i, j int) bool {
		return depth(w.sideIgnore[i].dir) > depth(w.sideIgnore[j].dir)
	})
	return nil
}

func depth(p string) int {
	if p == "" {
		return 0
	}
	return strings.Count(p, "/") + 1
}

// isSideIgnored returns true if the entry should be hidden per the
// side-ignore layers. The deepest layer whose scope covers the path
// decides; layers further up are consulted only if no deeper one had
// an opinion. Directories are passed with isDir=true so dir-only
// patterns work.
func (w *Walker) isSideIgnored(path string, isDir bool) bool {
	if len(w.sideIgnore) == 0 || path == "" {
		return false
	}
	for _, layer := range w.sideIgnore {
		// Layer only applies within its directory.
		rel := path
		if layer.dir != "" {
			if !strings.HasPrefix(path, layer.dir+"/") {
				continue
			}
			rel = path[len(layer.dir)+1:]
		}
		if rel == "" || rel == "." {
			continue
		}
		match := layer.matcher.Relative(rel, isDir)
		if match == nil {
			continue
		}
		return match.Ignore()
	}
	return false
}
