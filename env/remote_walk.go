package env

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"sidekick/common"
	"sidekick/gitwalk"
)

// WalkEntry represents a single entry discovered during a directory walk.
type WalkEntry struct {
	// Path is the full path on the target environment.
	Path string
	// IsDir indicates whether the entry is a directory.
	IsDir bool
}

// WalkEntryWithContent is an entry from an entry-based walk. Open is nil
// for directories; for files it returns the entry's content as observed
// by the walker. The reader is only guaranteed to be valid until the
// next entry callback returns; callers wanting to defer the read should
// copy the bytes inline.
type WalkEntryWithContent struct {
	Path  string
	IsDir bool
	Open  func(ctx context.Context) (io.ReadCloser, error)
}

// RemoteWalkContentMode selects how WalkCodeDirectoryEntriesViaEnv reads
// content for entries on an SSH-backed env. See the package-level
// constants for the available values.
type RemoteWalkContentMode string

const (
	// RemoteWalkContentModeSFTP reads every file's content via the env's
	// SFTP transport. This is the default — simpler and lower-latency for
	// small numbers of file reads.
	RemoteWalkContentModeSFTP RemoteWalkContentMode = "sftp"
	// RemoteWalkContentModeSnapshot reads unchanged tracked files from
	// local git objects (via gitwalk's cat-file pool) and falls back to
	// SFTP only for overlay-changed or untracked files.
	RemoteWalkContentModeSnapshot RemoteWalkContentMode = "snapshot"
)

// remoteWalkContentModeEnv lets operators flip the default content mode
// without recompiling. Unset / unrecognized values resolve to the SFTP
// default.
const remoteWalkContentModeEnv = "SIDEKICK_REMOTE_WALK_CONTENT_MODE"

func remoteWalkContentMode() RemoteWalkContentMode {
	switch RemoteWalkContentMode(os.Getenv(remoteWalkContentModeEnv)) {
	case RemoteWalkContentModeSnapshot:
		return RemoteWalkContentModeSnapshot
	default:
		return RemoteWalkContentModeSFTP
	}
}

// WalkCodeDirectoryViaEnv walks the environment's working directory using the
// sidekick ignore file set. It delegates to the Env.Walk method, which handles
// both local and remote environments transparently.
func WalkCodeDirectoryViaEnv(
	ctx context.Context,
	ec EnvContainer,
	handleEntry func(path string, isDir bool) error,
) error {
	return ec.Env.Walk(ctx, common.SidekickIgnoreFileNames, handleEntry)
}

// WalkCodeDirectoryEntriesViaEnv walks the environment's working directory
// like WalkCodeDirectoryViaEnv but yields entries carrying an Open callback
// for file content. For SSH-backed envs the content source is chosen by the
// active RemoteWalkContentMode.
func WalkCodeDirectoryEntriesViaEnv(
	ctx context.Context,
	ec EnvContainer,
	handleEntry func(WalkEntryWithContent) error,
) error {
	switch env := ec.Env.(type) {
	case *LocalEnv:
		return walkCodeDirectoryEntries(ctx, env.WorkingDirectory, common.SidekickIgnoreFileNames, handleEntry)
	case *LocalGitWorktreeEnv:
		return walkCodeDirectoryEntries(ctx, env.WorkingDirectory, common.SidekickIgnoreFileNames, handleEntry)
	case *DevPodEnv:
		return walkCodeDirectorySSHEntries(ctx, env, env.LocalRepoDir, env.WorkingDirectory,
			common.SidekickIgnoreFileNames, remoteWalkContentMode(), handleEntry)
	case *OpenShellEnv:
		return walkCodeDirectorySSHEntries(ctx, env, env.LocalRepoDir, env.WorkingDirectory,
			common.SidekickIgnoreFileNames, remoteWalkContentMode(), handleEntry)
	default:
		return fmt.Errorf("unsupported env type %s for entry walk", ec.Env.GetType())
	}
}

// remoteWalkInfoSeparator delimits the three sections of the single remote
// info script's stdout (toplevel, HEAD, porcelain status). The exact byte
// sequence is highly unlikely to appear in any tracked or untracked path.
const remoteWalkInfoSeparator = "---SIDEKICK-WALK-SEP---"

// The script takes the base directory as its first positional argument so
// it runs against the requested working directory regardless of the env's
// default cwd. Using `git -C "$1"` avoids relying on a writable cd.
const remoteWalkInfoScript = `set -e
git -C "$1" rev-parse --show-toplevel
printf '%s\n' '` + remoteWalkInfoSeparator + `'
git -C "$1" rev-parse HEAD
printf '%s\n' '` + remoteWalkInfoSeparator + `'
git -C "$1" status --porcelain=v1 -uall -z
`

// walkCodeDirectorySSH walks an SSH-reachable env's working directory by
// driving gitwalk against a local clone of the same repository. The remote
// is queried once for repo root, HEAD, and `git status --porcelain=v1 -uall -z`,
// the missing commit (if any) is fetched into the local repo, then gitwalk
// emits entries with overlay content read back from the remote via sftp.
// Callbacks receive remote-absolute paths anchored at baseDirectory.
func walkCodeDirectorySSH(
	ctx context.Context,
	sshEnv SSHCapableEnv,
	localRepoDir string,
	baseDirectory string,
	ignoreFileNames []string,
	handleEntry func(path string, isDir bool) error,
) error {
	return walkCodeDirectorySSHEntries(ctx, sshEnv, localRepoDir, baseDirectory, ignoreFileNames,
		remoteWalkContentMode(), func(e WalkEntryWithContent) error {
			return handleEntry(e.Path, e.IsDir)
		})
}

// walkCodeDirectorySSHEntries is the entry-callback core for SSH-backed
// envs. mode selects how file content is opened: RemoteWalkContentModeSFTP
// always reads via the env's SFTP transport; RemoteWalkContentModeSnapshot
// reads unchanged tracked files from local git objects via the gitwalk
// cat-file pool and falls back to SFTP only for overlay (modified, added,
// renamed-to, untracked) entries.
func walkCodeDirectorySSHEntries(
	ctx context.Context,
	sshEnv SSHCapableEnv,
	localRepoDir string,
	baseDirectory string,
	ignoreFileNames []string,
	mode RemoteWalkContentMode,
	handleEntry func(WalkEntryWithContent) error,
) error {
	if localRepoDir == "" {
		return fmt.Errorf("env has no LocalRepoDir set; cannot walk %s via local git", baseDirectory)
	}

	info, err := sshEnv.RunCommand(ctx, EnvRunCommandInput{
		Command: "sh",
		Args:    []string{"-c", remoteWalkInfoScript, "sidekick-walk", baseDirectory},
	})
	if err != nil {
		return fmt.Errorf("collect remote git info: %w", err)
	}
	if info.ExitStatus != 0 {
		return fmt.Errorf("collect remote git info exited %d: %s", info.ExitStatus, info.Stderr)
	}

	remoteRoot, remoteHead, porcelain, err := parseRemoteGitInfo(info.Stdout)
	if err != nil {
		return fmt.Errorf("parse remote git info: %w", err)
	}

	if err := ensureLocalHasCommit(ctx, sshEnv, localRepoDir, remoteHead, remoteRoot); err != nil {
		return err
	}

	readRemoteRel := func(relPath string) (io.ReadCloser, error) {
		data, err := sshEnv.ReadFile(ctx, path.Join(remoteRoot, relPath))
		if err != nil {
			return nil, err
		}
		return io.NopCloser(bytes.NewReader(data)), nil
	}

	overlay, err := gitwalk.ChangesFromPorcelainZ(porcelain, readRemoteRel)
	if err != nil {
		return fmt.Errorf("parse remote porcelain: %w", err)
	}

	// overlayPaths tracks every path touched by the working-copy overlay
	// (added/modified/renamed-to/untracked) so snapshot mode can route
	// those reads through SFTP instead of stale local blobs.
	overlayPaths := make(map[string]struct{}, len(overlay))
	for _, c := range overlay {
		if c.Kind != gitwalk.ChangeDeleted {
			overlayPaths[c.Path] = struct{}{}
		}
	}

	w, err := gitwalk.New(ctx, gitwalk.Options{
		RepoPath:        localRepoDir,
		Ref:             remoteHead,
		Overlay:         sliceOverlay(overlay),
		SideIgnoreFiles: ignoreFilesWithGitignore(ignoreFileNames),
	})
	if err != nil {
		return err
	}
	defer w.Close()

	subPrefix, ok := remoteSubPath(remoteRoot, baseDirectory)
	if !ok {
		return fmt.Errorf("base directory %q is not under remote repo root %q", baseDirectory, remoteRoot)
	}

	return w.Walk(ctx, func(d gitwalk.DirEntry) error {
		p := d.Path()
		if p == "" {
			return nil
		}
		var rel string
		if subPrefix == "" {
			rel = p
		} else {
			switch {
			case p == subPrefix:
				return nil
			case strings.HasPrefix(p, subPrefix+"/"):
				rel = strings.TrimPrefix(p, subPrefix+"/")
			case d.IsDir() && strings.HasPrefix(subPrefix+"/", p+"/"):
				return nil
			default:
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		entry := WalkEntryWithContent{
			Path:  path.Join(baseDirectory, rel),
			IsDir: d.IsDir(),
		}
		if !d.IsDir() {
			repoRelPath := p
			_, isOverlay := overlayPaths[repoRelPath]
			useSnapshot := mode == RemoteWalkContentModeSnapshot && !isOverlay
			if useSnapshot {
				snapEntry := d
				entry.Open = func(_ context.Context) (io.ReadCloser, error) {
					return snapEntry.Open()
				}
			} else {
				entry.Open = func(_ context.Context) (io.ReadCloser, error) {
					return readRemoteRel(repoRelPath)
				}
			}
		}
		return handleEntry(entry)
	})
}

func parseRemoteGitInfo(stdout string) (toplevel, head string, porcelain []byte, err error) {
	sep := []byte("\n" + remoteWalkInfoSeparator + "\n")
	data := []byte(stdout)
	i := bytes.Index(data, sep)
	if i < 0 {
		return "", "", nil, errors.New("missing toplevel/HEAD separator")
	}
	toplevel = strings.TrimSpace(string(data[:i]))
	rest := data[i+len(sep):]
	j := bytes.Index(rest, sep)
	if j < 0 {
		return "", "", nil, errors.New("missing HEAD/porcelain separator")
	}
	head = strings.TrimSpace(string(rest[:j]))
	porcelain = rest[j+len(sep):]
	if toplevel == "" || head == "" {
		return "", "", nil, errors.New("empty toplevel or HEAD")
	}
	return toplevel, head, porcelain, nil
}

// sshCommitFetcher is an optional interface that SSHCapableEnvs may implement
// to provide their own way of fetching a commit into the local mirror repo.
// When not implemented, the default ssh-based fetch is used. Tests use this
// hook to substitute a file:// fetch so they don't need a real ssh server.
type sshCommitFetcher interface {
	FetchCommitForWalk(ctx context.Context, localRepo, sha, remoteRoot string) error
}

func ensureLocalHasCommit(ctx context.Context, sshEnv SSHCapableEnv, localRepo, sha, remoteRoot string) error {
	if exec.CommandContext(ctx, "git", "-C", localRepo, "cat-file", "-e", sha+"^{commit}").Run() == nil {
		return nil
	}
	if f, ok := sshEnv.(sshCommitFetcher); ok {
		return f.FetchCommitForWalk(ctx, localRepo, sha, remoteRoot)
	}
	return sshFetchCommitDefault(ctx, sshEnv, localRepo, sha, remoteRoot)
}

func sshFetchCommitDefault(ctx context.Context, sshEnv SSHCapableEnv, localRepo, sha, remoteRoot string) error {
	sshArgs, err := sshEnv.SSHArgs(ctx)
	if err != nil {
		return fmt.Errorf("ssh args: %w", err)
	}
	dest, opts := splitSSHDestination(sshArgs)
	if dest == "" {
		return fmt.Errorf("could not determine ssh destination from args %v", sshArgs)
	}
	gitSSH := "ssh"
	for _, a := range opts {
		gitSSH += " " + shellQuote(a)
	}
	fetchURL := dest + ":" + remoteRoot
	cmd := exec.CommandContext(ctx, "git", "-C", localRepo,
		"fetch", "--no-tags", "--no-write-fetch-head", fetchURL, sha)
	cmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+gitSSH)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch %s %s: %w: %s", fetchURL, sha, err, string(out))
	}
	return nil
}

// splitSSHDestination returns the trailing destination token and the
// preceding option args, ignoring a final "--" separator if present.
func splitSSHDestination(args []string) (string, []string) {
	n := len(args)
	if n == 0 {
		return "", nil
	}
	if args[n-1] == "--" {
		n--
	}
	if n == 0 {
		return "", nil
	}
	return args[n-1], args[:n-1]
}

// remoteSubPath returns the slash-separated path of workingDir underneath
// repoRoot, or "" when they refer to the same directory. The second return
// value is false when workingDir is outside repoRoot.
func remoteSubPath(repoRoot, workingDir string) (string, bool) {
	root := path.Clean(repoRoot)
	wd := path.Clean(workingDir)
	if root == wd {
		return "", true
	}
	if !strings.HasPrefix(wd, root+"/") {
		return "", false
	}
	return strings.TrimPrefix(wd, root+"/"), true
}
