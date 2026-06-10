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

	overlay, err := gitwalk.ChangesFromPorcelainZ(porcelain, func(relPath string) (io.ReadCloser, error) {
		data, err := sshEnv.ReadFile(ctx, path.Join(remoteRoot, relPath))
		if err != nil {
			return nil, err
		}
		return io.NopCloser(bytes.NewReader(data)), nil
	})
	if err != nil {
		return fmt.Errorf("parse remote porcelain: %w", err)
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
		return handleEntry(path.Join(baseDirectory, rel), d.IsDir())
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
