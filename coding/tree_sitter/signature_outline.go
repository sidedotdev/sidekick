package tree_sitter

import (
	"cmp"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"sync"

	"fmt"

	"sidekick/env"
	"sidekick/logger"
	"sidekick/utils"

	"github.com/cbroglie/mustache"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

type FileOutline struct {
	Path string
	OutlineType
	Content string
}

type OutlineType int

const (
	OutlineTypeFileSignature OutlineType = iota
	OutlineTypeFileSymbol    OutlineType = iota
	OutlineTypeDirNoop       OutlineType = iota
	OutlineTypeFileUnhandled OutlineType = iota
)

// GetFileSignaturesFromBytes returns file signatures from
// pre-read source bytes, avoiding filesystem access.
func GetFileSignaturesFromBytes(languageName string, sourceCode []byte) ([]Signature, error) {
	sitterLanguage, err := getSitterLanguage(languageName)
	if err != nil {
		return nil, err
	}
	parser := tree_sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(sitterLanguage)
	tree := parser.Parse(sourceTransform(languageName, &sourceCode), nil)
	if tree != nil {
		defer tree.Close()
	}
	return getFileSignaturesInternal(languageName, sitterLanguage, tree, &sourceCode, false)
}

// GetFileSignaturesStringFromBytes is like GetFileSignaturesString but operates
// on pre-read source bytes.
func GetFileSignaturesStringFromBytes(languageName string, sourceCode []byte) (string, error) {
	sigs, err := GetFileSignaturesFromBytes(languageName, sourceCode)
	if err != nil {
		return "", err
	}
	return FormatSignatures(sigs), nil
}

func FormatSignatures(signatures []Signature) string {
	var out strings.Builder
	for _, signature := range signatures {
		out.WriteString(signature.Content)
		out.WriteString("\n---\n")
	}
	return out.String()
}

func sourceTransform(languageName string, sourceCode *[]byte) []byte {
	if languageName == "golang" && len(*sourceCode) > 0 && (*sourceCode)[len(*sourceCode)-1] != '\n' {
		*sourceCode = append(*sourceCode, '\n')
	}

	return *sourceCode
}

var checksums = sync.Map{}
var cachedOutlines = sync.Map{}

// Raw outline entries are encoded as a one-byte kind prefix followed by the
// untruncated outline content, so a cached "this file yields no signature
// outline" result is distinguishable from an empty outline.
const (
	rawOutlineSignaturePrefix = "S"
	rawOutlineUnhandled       = "U"
)

// outlineSnapshotEncodingVersion covers snapshot aspects the automatic
// fingerprint below cannot see: entry encoding, source transforms and
// language inference. Bump it when any of those change observably.
const outlineSnapshotEncodingVersion = "1"

// OutlineSnapshotVersion fingerprints the signature-extraction logic and
// snapshot encoding that produce persisted raw-outline snapshots, so
// snapshots from different code become cache misses instead of serving stale
// derived outlines for unchanged git trees indefinitely. It combines the
// manual encoding version with a hash of the embedded signature queries and
// the tree-sitter module versions in the build, invalidating exactly when
// extraction inputs change rather than requiring a manual bump.
var OutlineSnapshotVersion = computeOutlineSnapshotVersion()

func computeOutlineSnapshotVersion() string {
	h := sha256.New()
	h.Write([]byte(outlineSnapshotEncodingVersion))
	h.Write([]byte{0})
	// embedded queries drive what the signature outlines contain
	hashFSFiles(h, signatureQueriesFS)
	// grammar and runtime library versions change the parse trees the
	// queries run against
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if strings.Contains(dep.Path, "tree-sitter") {
				fmt.Fprintf(h, "%s@%s\x00", dep.Path, dep.Version)
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// hashFSFiles writes every file's path and content into h in sorted path
// order, so the digest never depends on the filesystem's traversal order.
func hashFSFiles(h io.Writer, fsys fs.FS) {
	var paths []string
	_ = fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)
	for _, path := range paths {
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			continue
		}
		h.Write([]byte(path))
		h.Write([]byte{0})
		h.Write(data)
		h.Write([]byte{0})
	}
}

// OutlineWalkCache carries raw (untruncated) per-file signature outlines from
// a previously cached repo state so a walk can skip reading and parsing files
// that are known unchanged since that state. Raw contents observed during the
// walk are recorded so callers can persist a complete map for the current
// state.
type OutlineWalkCache struct {
	// Raw maps repo-relative paths to encoded raw outline entries from the
	// cached state.
	Raw map[string]string
	// Affected marks paths whose content may differ from the cached state;
	// they always bypass Raw.
	Affected map[string]bool

	mu      sync.Mutex
	updated map[string]string
	// incomplete marks that at least one file could not be read during the
	// walk; the recorded entries then understate the tree and must never be
	// persisted as a snapshot.
	incomplete bool
}

// lookup returns the cached raw outline content for a path that is unaffected
// since the cached state. handled is false when the cache recorded that the
// file yields no signature outline.
func (c *OutlineWalkCache) lookup(relativePath string) (content string, handled bool, ok bool) {
	if c == nil || c.Affected[relativePath] {
		return "", false, false
	}
	encoded, ok := c.Raw[relativePath]
	if !ok {
		return "", false, false
	}
	if encoded == rawOutlineUnhandled {
		return "", false, true
	}
	return strings.TrimPrefix(encoded, rawOutlineSignaturePrefix), true, true
}

// record collects an encoded raw outline entry observed during the walk. Safe
// for concurrent use and a no-op on a nil cache.
func (c *OutlineWalkCache) record(relativePath, encoded string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.updated == nil {
		c.updated = make(map[string]string)
	}
	c.updated[relativePath] = encoded
	c.mu.Unlock()
}

// markIncomplete flags the recorded entries as understating the tree (eg a
// file read failed mid-walk), which disables UpdatedSnapshot. Safe for
// concurrent use and a no-op on a nil cache.
func (c *OutlineWalkCache) markIncomplete() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.incomplete = true
	c.mu.Unlock()
}

// UpdatedSnapshot returns a copy of the encoded raw outline entries recorded
// by walks using this cache, keyed by repo-relative path. It returns nil when
// the walk was incomplete: persisting a snapshot that silently omits a file
// would keep that file absent from all future exact-tree cache hits.
func (c *OutlineWalkCache) UpdatedSnapshot() map[string]string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.incomplete {
		return nil
	}
	snapshot := make(map[string]string, len(c.updated))
	for k, v := range c.updated {
		snapshot[k] = v
	}
	return snapshot
}

func checksumFromBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// nil for the showPaths means: show all paths
// nil for signaturePaths means: outline signatures for all paths
func GetDirectorySignatureOutlines(ctx context.Context, ec env.EnvContainer, showPaths *map[string]bool, signaturePaths *map[string]int) ([]FileOutline, error) {
	return GetDirectorySignatureOutlinesCached(ctx, ec, showPaths, signaturePaths, nil)
}

// GetDirectorySignatureOutlinesCached is GetDirectorySignatureOutlines with an
// optional walk cache: files known unchanged since the cached repo state are
// served from cached raw outlines without being read or re-parsed.
func GetDirectorySignatureOutlinesCached(ctx context.Context, ec env.EnvContainer, showPaths *map[string]bool, signaturePaths *map[string]int, cache *OutlineWalkCache) ([]FileOutline, error) {
	entries, err := GetDirectoryRawOutlines(ctx, ec, cache)
	if err != nil {
		return nil, err
	}
	return OutlinesFromRawEntries(entries, showPaths, signaturePaths), nil
}

// RawOutlineEntry is one entry from a directory walk with its raw
// (untruncated) signature outline, sufficient to derive any filtered or
// truncated outline view without walking again.
type RawOutlineEntry struct {
	RelativePath string
	IsDir        bool
	// Handled reports whether the file yielded a signature outline.
	Handled bool
	Raw     string
}

// GetDirectoryRawOutlines walks the environment's working directory once and
// returns every entry with its raw signature outline, using the optional
// cache to skip reading and parsing files unchanged since the cached repo
// state. Walking costs a network round trip on SSH-backed environments, so
// callers needing multiple outline views should walk once via this function
// and derive each view with OutlinesFromRawEntries. Entries are returned in
// canonical walk order (depth-first with name-sorted siblings) regardless of
// the underlying environment's native walk order, which differs between
// local and SSH-backed walks; cache reconstruction reproduces the same order.
func GetDirectoryRawOutlines(ctx context.Context, ec env.EnvContainer, cache *OutlineWalkCache) (entries []RawOutlineEntry, err error) {
	baseDirectory := ec.Env.GetWorkingDirectory()
	envType := ec.Env.GetType()
	if envType == env.EnvTypeLocal || envType == env.EnvTypeLocalGitWorktree {
		baseDirectory, err = filepath.Abs(baseDirectory)
		if err != nil {
			return entries, err
		}
	}

	sep := string(filepath.Separator)
	if envType != env.EnvTypeLocal && envType != env.EnvTypeLocalGitWorktree {
		sep = "/"
	}
	if !strings.HasSuffix(baseDirectory, sep) {
		baseDirectory = baseDirectory + sep
	}

	type fileTask struct {
		idx          int
		path         string
		relativePath string
		fileBytes    []byte
	}

	// The entry-based walk sources content on the host for unchanged tracked
	// files of SSH-backed envs, avoiding a per-file remote round trip.
	// Content must be copied inline (entry readers are only valid until the
	// callback returns), so reads happen during the walk while the CPU-bound
	// signature parsing runs in a bounded worker pool; the channel's capacity
	// bounds how many file contents are held in memory at once.
	const maxConcurrency = 15
	taskCh := make(chan fileTask, maxConcurrency)
	rawByIdx := make(map[int]string)
	var resultsMu sync.Mutex
	var wg sync.WaitGroup
	for range maxConcurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range taskCh {
				raw, ok := rawSignatureContentFromBytes(t.path, t.relativePath, t.fileBytes)
				if !ok {
					cache.record(t.relativePath, rawOutlineUnhandled)
					continue
				}
				cache.record(t.relativePath, rawOutlineSignaturePrefix+raw)
				resultsMu.Lock()
				rawByIdx[t.idx] = raw
				resultsMu.Unlock()
			}
		}()
	}

	walkErr := env.WalkCodeDirectoryEntriesViaEnv(ctx, ec, func(entry env.WalkEntryWithContent) error {
		relativePath := strings.Replace(entry.Path, baseDirectory, "", 1)

		idx := len(entries)
		entries = append(entries, RawOutlineEntry{RelativePath: relativePath, IsDir: entry.IsDir})
		if entry.IsDir {
			return nil
		}

		if raw, handled, ok := cache.lookup(relativePath); ok {
			if handled {
				cache.record(relativePath, rawOutlineSignaturePrefix+raw)
				entries[idx].Handled = true
				entries[idx].Raw = raw
			} else {
				cache.record(relativePath, rawOutlineUnhandled)
			}
			return nil
		}

		fileBytes, readErr := readWalkEntry(ctx, entry)
		if readErr != nil {
			cache.markIncomplete()
			l := logger.Get()
			l.Trace().Err(readErr).Msgf("error reading file %s", entry.Path)
			return nil
		}
		taskCh <- fileTask{idx: idx, path: entry.Path, relativePath: relativePath, fileBytes: fileBytes}
		return nil
	})
	close(taskCh)
	wg.Wait()
	if walkErr != nil {
		return entries, walkErr
	}

	for idx, raw := range rawByIdx {
		entries[idx].Handled = true
		entries[idx].Raw = raw
	}
	entries = withoutEmptyDirs(entries)
	sort.SliceStable(entries, func(i, j int) bool {
		return walkOrderLess(entries[i].RelativePath, entries[j].RelativePath)
	})
	return entries, nil
}

// walkOrderLess orders repo-relative paths the way a depth-first walk with
// name-sorted siblings emits them: a directory's whole subtree comes right
// after the directory itself, before any sibling sorting after its name.
// Plain lexical order would instead put "foo.go" before "foo/a.go". It
// defines the canonical entry order shared by cold walks and cache
// reconstruction (a directory entry compares equal to a prefix of its own
// subtree paths and so sorts immediately before them).
func walkOrderLess(a, b string) bool {
	return strings.ReplaceAll(a, "/", "\x00") < strings.ReplaceAll(b, "/", "\x00")
}

// withoutEmptyDirs drops directory entries with no file entries beneath
// them, so walks match cache reconstruction, which derives directories
// purely from file paths.
func withoutEmptyDirs(entries []RawOutlineEntry) []RawOutlineEntry {
	dirsWithFiles := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir {
			continue
		}
		p := entry.RelativePath
		for i := 0; i < len(p); i++ {
			if p[i] == '/' {
				dirsWithFiles[p[:i]] = true
			}
		}
	}
	kept := entries[:0]
	for _, entry := range entries {
		if entry.IsDir && !dirsWithFiles[entry.RelativePath] {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// GetDirectoryRawOutlinesFromCache reconstructs raw outline entries from a
// cached snapshot without walking the directory: the snapshot supplies the
// file list and outlines for unaffected paths, while affected paths (changed,
// added or untracked since the snapshot) are read and re-parsed one by one,
// with paths that no longer exist dropped. ok is false when the cache holds
// no snapshot, in which case the caller must fall back to a full walk.
func GetDirectoryRawOutlinesFromCache(ctx context.Context, ec env.EnvContainer, cache *OutlineWalkCache) (entries []RawOutlineEntry, ok bool, err error) {
	if cache == nil || cache.Raw == nil {
		return nil, false, nil
	}
	pathSet := make(map[string]bool, len(cache.Raw)+len(cache.Affected))
	for p := range cache.Raw {
		pathSet[p] = true
	}
	for p := range cache.Affected {
		pathSet[p] = true
	}
	filePaths := make([]string, 0, len(pathSet))
	for p := range pathSet {
		filePaths = append(filePaths, p)
	}
	sort.Slice(filePaths, func(i, j int) bool {
		return walkOrderLess(filePaths[i], filePaths[j])
	})

	// dir entries don't exist in the snapshot; derive them from file paths,
	// each emitted before its first file like a walk would (empty
	// directories are consistently excluded on both cold and cached paths)
	emittedDirs := map[string]bool{}
	emitDirs := func(relativePath string) {
		for i := 0; i < len(relativePath); i++ {
			if relativePath[i] == '/' {
				dir := relativePath[:i]
				if !emittedDirs[dir] {
					emittedDirs[dir] = true
					entries = append(entries, RawOutlineEntry{RelativePath: dir, IsDir: true})
				}
			}
		}
	}

	baseDirectory := strings.TrimSuffix(ec.Env.GetWorkingDirectory(), "/")
	for _, relativePath := range filePaths {
		entry := RawOutlineEntry{RelativePath: relativePath}
		raw, handled, cached := cache.lookup(relativePath)
		if !cached {
			fileBytes, readErr := ec.Env.ReadFile(ctx, relativePath)
			if readErr != nil {
				if errors.Is(readErr, fs.ErrNotExist) {
					// affected and confirmed missing: deleted since the snapshot
					continue
				}
				// Any other failure (transient transport error, cancellation,
				// permissions) must not silently drop the file: a snapshot
				// persisted without it would keep it absent on future
				// exact-tree cache hits.
				return nil, false, fmt.Errorf("read affected file %s: %w", relativePath, readErr)
			}
			raw, handled = rawSignatureContentFromBytes(baseDirectory+"/"+relativePath, relativePath, fileBytes)
		}
		if handled {
			cache.record(relativePath, rawOutlineSignaturePrefix+raw)
			entry.Handled = true
			entry.Raw = raw
		} else {
			cache.record(relativePath, rawOutlineUnhandled)
		}
		emitDirs(relativePath)
		entries = append(entries, entry)
	}
	return entries, true, nil
}

// OutlinesFromRawEntries derives outlines from raw walk entries, applying the
// given path filters and per-path truncation limits.
// nil for the showPaths means: show all paths
// nil for signaturePaths means: outline signatures for all paths
func OutlinesFromRawEntries(entries []RawOutlineEntry, showPaths *map[string]bool, signaturePaths *map[string]int) []FileOutline {
	var outlines []FileOutline
	for _, entry := range entries {
		if entry.IsDir {
			if showPaths == nil || (*showPaths)[entry.RelativePath] {
				outlines = append(outlines, FileOutline{
					Path:        entry.RelativePath,
					OutlineType: OutlineTypeDirNoop,
				})
			}
			continue
		}

		if signaturePaths != nil && (*signaturePaths)[entry.RelativePath] == 0 {
			// just show path without an actual signature outline
			if showPaths == nil || (*showPaths)[entry.RelativePath] {
				outlines = append(outlines, FileOutline{
					Path:        entry.RelativePath,
					OutlineType: OutlineTypeFileUnhandled,
				})
			}
			continue
		}

		if !entry.Handled {
			outlines = append(outlines, FileOutline{
				Path:        entry.RelativePath,
				OutlineType: OutlineTypeFileUnhandled,
			})
			continue
		}
		outlines = append(outlines, finishSignatureOutline(entry.RelativePath, entry.RelativePath, entry.Raw, signaturePaths))
	}
	return outlines
}

// readWalkEntry copies the entry's content inline; entry readers are only
// valid until the walk callback returns.
func readWalkEntry(ctx context.Context, entry env.WalkEntryWithContent) ([]byte, error) {
	rc, err := entry.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// rawSignatureContentFromBytes extracts the untruncated signature outline for
// a single file's content, reusing the process-level checksum cache. ok is
// false when signature extraction fails, in which case the caller keeps its
// unhandled placeholder for the file.
func rawSignatureContentFromBytes(path, relativePath string, fileBytes []byte) (_ string, ok bool) {
	checksum := checksumFromBytes(fileBytes)

	if val, ok := checksums.Load(path); ok && val == checksum {
		if val, ok := cachedOutlines.Load(path); ok {
			return val.(string), true
		}
	}

	langName := utils.InferLanguageNameFromFilePath(path)
	content, sigErr := GetFileSignaturesStringFromBytes(langName, fileBytes)
	if sigErr != nil {
		l := logger.Get()
		if strings.Contains(sigErr.Error(), path) {
			l.Trace().Err(sigErr).Msg("error getting signatures")
		} else {
			l.Trace().Err(sigErr).Msg(fmt.Sprintf("error getting signatures for file %s", relativePath))
		}
		return "", false
	}
	cachedOutlines.Store(path, content)
	checksums.Store(path, checksum)
	return content, true
}

// finishSignatureOutline applies the per-call truncation limits to a raw
// signature outline.
func finishSignatureOutline(path, relativePath, outlineContent string, signaturePaths *map[string]int) FileOutline {
	// NOTE max embed size is 8192 tokens, this character limit is trying to
	// avoid hitting that with a decent margin of error
	maxContentLength := 30000
	if signaturePaths != nil {
		maxContentLength = min((*signaturePaths)[relativePath], maxContentLength)
	}
	if len(outlineContent) > maxContentLength {
		languageName := utils.InferLanguageNameFromFilePath(path)
		sourceCode := SourceCode{
			Content:              outlineContent,
			LanguageName:         languageName,
			OriginalLanguageName: languageName + "-signatures",
		}
		_, newSourceCode := removeComments(sourceCode)
		outlineContent = newSourceCode.Content
	}
	if len(outlineContent) > maxContentLength {
		truncatedPart := outlineContent[maxContentLength:]
		outlineContent = outlineContent[:maxContentLength]
		// Only show truncation message if we truncated more than just whitespace
		if strings.TrimSpace(truncatedPart) != "" {
			outlineContent += fmt.Sprintf("\n... [truncated %d characters]", len(truncatedPart))
		}
	}

	return FileOutline{
		Path:        relativePath,
		OutlineType: OutlineTypeFileSignature,
		Content:     outlineContent,
	}
}

func GetDirectorySignatureOutlinesString(ctx context.Context, ec env.EnvContainer) (string, error) {
	outlines, err := GetDirectorySignatureOutlines(ctx, ec, nil, nil)
	if err != nil {
		return "", err
	}
	return GetFileOutlinesString(outlines)
}

func GetFileOutlinesString(outlines []FileOutline) (string, error) {
	var sb strings.Builder
	charCount := 0
	charThreshold := 2000
	for _, outline := range outlines {
		indentLevel := countDirectories(outline.Path) - 1
		indent := strings.Repeat("\t", indentLevel)

		// when we hit the threshold, we re-print the parent directories to
		// prevent the LLM from forgetting them
		if charCount >= charThreshold {
			parentDirs := []string{}
			remainder := filepath.Dir(outline.Path)
			for remainder != "." {
				parentDirs = append(parentDirs, filepath.Base(remainder))
				remainder = filepath.Dir(remainder)
			}
			if len(parentDirs) > 0 {
				slices.Reverse(parentDirs)
				sb.WriteString(filepath.Join(parentDirs...))
				sb.WriteRune(filepath.Separator)
				sb.WriteString("\n")
			}
			charCount = 0
		}

		if outline.OutlineType == OutlineTypeDirNoop {
			childDir := fmt.Sprintf("%s%s/\n", indent, filepath.Base(outline.Path))
			sb.WriteString(childDir)
			charCount += len(childDir)
			continue
		}

		line := fmt.Sprintf("%s%s\n", indent, filepath.Base(outline.Path))
		sb.WriteString(line)
		charCount += len(line)
		if outline.Content != "" {
			indentation := strings.Repeat("\t", indentLevel+1)
			indentedContent := indentation + strings.ReplaceAll(outline.Content, "\n", "\n"+indentation)
			indentedContent = strings.TrimSuffix(indentedContent, indentation)
			indentedContent = strings.TrimSuffix(indentedContent, "\n"+indentation+"---\n")
			indentedContent = strings.ReplaceAll(indentedContent, indentation+"---\n", "")
			sb.WriteString(indentedContent)
			sb.WriteString("\n")
			charCount += len(indentedContent) + 1
		}
	}
	return sb.String(), nil
}

type Signature struct {
	Content    string
	StartPoint tree_sitter.Point
	EndPoint   tree_sitter.Point
}

func getFileSignaturesInternal(languageName string, sitterLanguage *tree_sitter.Language, tree *tree_sitter.Tree, sourceCode *[]byte, showComplete bool) ([]Signature, error) {
	queryString, err := getSignatureQuery(languageName, showComplete)
	if err != nil {
		return []Signature{}, fmt.Errorf("error rendering symbol definition query: %w", err)
	}

	q, qErr := tree_sitter.NewQuery(sitterLanguage, queryString)
	if qErr != nil {
		return []Signature{}, fmt.Errorf("error creating sitter symbol definition query: %s", qErr.Message)
	}
	defer q.Close()

	var signatures []Signature
	qc := tree_sitter.NewQueryCursor()
	defer qc.Close()
	matches := qc.Matches(q, tree.RootNode(), []byte(*sourceCode))
	// Iterate over query results
	for match := matches.Next(); match != nil; match = matches.Next() {
		sigWriter := strings.Builder{}

		startPoint := tree_sitter.Point{Row: ^uint(0), Column: ^uint(0)}
		endPoint := tree_sitter.Point{Row: 0, Column: 0}
		for _, c := range match.Captures {
			name := q.CaptureNames()[c.Index]
			writeSignatureCapture(languageName, &sigWriter, sourceCode, c, name)
			if shouldExtendSignatureRange(languageName, name) {
				if c.Node.StartPosition().Row < startPoint.Row || (c.Node.StartPosition().Row == startPoint.Row && c.Node.StartPosition().Column < startPoint.Column) {
					startPoint = c.Node.StartPosition()
				}
				if c.Node.EndPosition().Row > endPoint.Row || (c.Node.EndPosition().Row == endPoint.Row && c.Node.EndPosition().Column > endPoint.Column) {
					endPoint = c.Node.EndPosition()
				}
			}
		}
		if sigWriter.Len() > 0 {
			signature := Signature{
				Content:    strings.Trim(sigWriter.String(), " \n"),
				StartPoint: startPoint,
				EndPoint:   endPoint,
			}
			if slices.Index(signatures, signature) == -1 {
				signatures = append(signatures, signature)
			}
		}
	}

	embeddedSignatures, err := getEmbeddedLanguageSignatures(languageName, tree, sourceCode)
	if err != nil {
		return nil, fmt.Errorf("error getting embedded language file map: %w", err)
	}
	signatures = append(signatures, embeddedSignatures...)

	// Sort signatures by start point
	slices.SortFunc(signatures, func(i, j Signature) int {
		c := cmp.Compare(i.StartPoint.Row, j.StartPoint.Row)
		if c == 0 {
			c = cmp.Compare(i.StartPoint.Column, j.StartPoint.Column)
		}
		return c
	})

	return signatures, nil
}

func shouldExtendSignatureRange(languageName, captureName string) bool {
	if !strings.HasSuffix(captureName, ".declaration") && !strings.HasSuffix(captureName, ".body") && !strings.HasPrefix(captureName, "parent.") {
		// all non-declaration/non-body captures that aren't explicitly parents should extend the range
		// FIXME /gen/req this is probably broken in the case where we capture
		// methods within a class capture for example. we could rely on
		// hierarchy captured in naming convention for captures, where "."
		// denotes a parent-child relationship - more than 1 dot can be excluded
		// (return false here).
		return true
	}
	switch languageName {
	case "golang":
		switch captureName {
		case "type.declaration", "const.declaration":
			return true
		}
	case "kotlin":
		switch captureName {
		case "property.declaration", "function.declaration":
			return true
		}
	case "vue":
		{
			// extend the range for <template>, <script>, and <style>
			if captureName == "template" || captureName == "script" || captureName == "style" {
				return true
			}
		}
	}
	return false // declaration is inclusive of the body so usually shouldn't extend
}

func getEmbeddedLanguageSignatures(languageName string, tree *tree_sitter.Tree, sourceCode *[]byte) ([]Signature, error) {
	switch languageName {
	case "vue":
		{
			return getVueEmbeddedLanguageSignatures(tree, sourceCode)
		}
	}
	return []Signature{}, nil
}

func writeSignatureCapture(languageName string, out *strings.Builder, sourceCode *[]byte, c tree_sitter.QueryCapture, name string) {
	//out.WriteString(name + "\n")
	//out.WriteString(c.Node.Type() + "\n")
	switch languageName {
	case "golang":
		{
			writeGolangSignatureCapture(out, sourceCode, c, name)
		}
	case "typescript":
		{
			writeTypescriptSignatureCapture(out, sourceCode, c, name)
		}
	case "tsx":
		{
			writeTsxSignatureCapture(out, sourceCode, c, name)
		}
	case "vue":
		{
			writeVueSignatureCapture(out, sourceCode, c, name)
		}
	case "python":
		{
			writePythonSignatureCapture(out, sourceCode, c, name)
		}
	case "java":
		{
			writeJavaSignatureCapture(out, sourceCode, c, name)
		}
	case "kotlin":
		{
			writeKotlinSignatureCapture(out, sourceCode, c, name)
		}
	case "javascript", "js", "jsx", "mjs", "cjs":
		{
			writeJavascriptSignatureCapture(out, sourceCode, c, name)
		}
	case "markdown":
		{
			writeMarkdownSignatureCapture(out, sourceCode, c, name)
		}
	default:
		{
			// NOTE this is expected to provide quite bad output until tweaked per language
			out.WriteString(c.Node.Utf8Text(*sourceCode))
		}
	}
}

//go:embed signature_queries/*
var signatureQueriesFS embed.FS

func getSignatureQuery(languageName string, showComplete bool) (string, error) {
	queryLang := normalizeLanguageForQueryFile(languageName)
	queryPath := fmt.Sprintf("signature_queries/signature_%s.scm.mustache", queryLang)
	queryBytes, err := signatureQueriesFS.ReadFile(queryPath)
	if err != nil {
		return "", fmt.Errorf("error reading signature query template file: %w", err)
	}

	// Render the template with showComplete variable
	rendered, err := mustache.Render(string(queryBytes), map[string]interface{}{
		"showComplete": showComplete,
	})
	if err != nil {
		return "", fmt.Errorf("error rendering signature query template: %w", err)
	}

	return rendered, nil
}

func countDirectories(path string) int {
	// Normalize the path
	cleanPath := filepath.Clean(path)

	// Handle OS-specific path separators
	separator := string(filepath.Separator)
	pathElements := strings.Split(cleanPath, separator)

	// Count the directories, exclude the file if present
	count := 0
	for _, element := range pathElements {
		if element != "" && element != "." {
			count++
		}
	}

	return count
}
