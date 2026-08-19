package env

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"path"
	"sidekick/coding/unix"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/sideagent"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// envVarsToInject returns environment variables injected into all commands run
// via Env. GIT_EDITOR=true prevents git from opening an editor for interactive
// commands. common.ActiveEnvTypeEnvVar lets spawned processes (e.g. tests)
// detect which kind of Sidekick environment they run inside, and
// common.PortForwardsEnvVar (set only when forwards are configured) tells them
// the container-local ports at which reverse-forwarded host ports are
// reachable.
func envVarsToInject(envType EnvType, portForwards []common.PortForwardConfig) []string {
	envVars := []string{
		"GIT_EDITOR=true",
		common.ActiveEnvTypeEnvVar + "=" + string(envType),
	}
	if len(portForwards) > 0 {
		envVars = append(envVars, common.PortForwardsEnvVar+"="+common.FormatPortForwards(portForwards))
	}
	return envVars
}

// ErrBranchAlreadyExists is returned when attempting to create a worktree
// with a branch name that already exists
var ErrBranchAlreadyExists = errors.New("branch already exists")

// WorktreeLockReason explains why Sidekick locks its git worktrees. Locking
// prevents "git worktree prune" (including via auto-gc) from deleting a
// worktree's admin directory under .git/worktrees/ when its working tree is not
// visible from the environment running prune (e.g. host vs devcontainer sharing
// the same .git).
const WorktreeLockReason = "Sidekick-managed worktree; locked to prevent git worktree prune from removing it when the working tree is not visible from this environment"

// ErrTypeBranchAlreadyExists is the application error type for branch already exists errors
const ErrTypeBranchAlreadyExists = "BranchAlreadyExists"

type EnvType string

const (
	EnvTypeLocal            EnvType = "local"
	EnvTypeLocalGitWorktree EnvType = "local_git_worktree"
	EnvTypeDevPod           EnvType = "devpod"
	EnvTypeOpenShell        EnvType = "openshell"
	EnvTypeModal            EnvType = "modal"
)

func (e EnvType) IsValid() bool {
	return e == EnvTypeLocal || e == EnvTypeLocalGitWorktree || e == EnvTypeDevPod || e == EnvTypeOpenShell || e == EnvTypeModal
}

type RepoMode string

const (
	RepoModeWorktree RepoMode = "worktree"
	RepoModeInPlace  RepoMode = "in_place"
)

func (r RepoMode) IsValid() bool {
	return r == RepoModeWorktree || r == RepoModeInPlace
}

type Env interface {
	GetType() EnvType
	GetWorkingDirectory() string
	RunCommand(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error)
	// Walk traverses the working directory tree, respecting ignore files
	// whose names are given in ignoreFileNames (ordered by precedence,
	// last = highest). The callback receives absolute paths.
	Walk(ctx context.Context, ignoreFileNames []string, handleEntry func(path string, isDir bool) error) error
	// ReadFile reads the contents of the file at the given path.
	// Relative paths are resolved against the working directory.
	ReadFile(ctx context.Context, path string) ([]byte, error)
	// ReadDir reads the directory at the given path and returns its entries.
	// Relative paths are resolved against the working directory.
	ReadDir(ctx context.Context, path string) ([]fs.DirEntry, error)
	// WriteFile writes data to the file at the given path with the given
	// permission bits, creating it if necessary. Relative paths are resolved
	// against the working directory.
	WriteFile(ctx context.Context, path string, data []byte, perm fs.FileMode) error
	// MkdirAll creates the directory at the given path along with any
	// necessary parents. Relative paths are resolved against the working
	// directory.
	MkdirAll(ctx context.Context, path string, perm fs.FileMode) error
	// Stat returns the FileInfo describing the file at the given path.
	// Relative paths are resolved against the working directory.
	Stat(ctx context.Context, path string) (fs.FileInfo, error)
	// Remove removes the file or (empty) directory at the given path.
	// Relative paths are resolved against the working directory.
	Remove(ctx context.Context, path string) error
	// CreateTemp creates a new temporary file in the directory dir, opens
	// the file for reading and writing, and returns the resulting file's
	// path. An empty dir uses the environment's default temp directory.
	// The pattern follows the same semantics as os.CreateTemp: the last
	// "*" in pattern is replaced with a random string.
	CreateTemp(ctx context.Context, dir, pattern string) (string, error)
	// Hibernate saves the worktree state as patches, removes the git worktree
	// link, and leaves only hibernation artifacts on disk.
	Hibernate(ctx context.Context, branchName string) (HibernationMetadata, error)
	// WakeIfHibernated restores the worktree from hibernation if it is currently
	// hibernated, otherwise it is a no-op.
	WakeIfHibernated(ctx context.Context) error
}

// SnapshottingEnv is implemented by environments that can persist their
// current filesystem state for use by future environments.
type SnapshottingEnv interface {
	Env
	Snapshot(ctx context.Context) (EnvRunCommandOutput, error)
}

// SSHCapableEnv is implemented by environments that support direct SSH access.
type SSHCapableEnv interface {
	Env
	// SSHArgs returns SSH CLI arguments for connecting to this environment.
	// The returned args end with the destination; a remote command string
	// can be appended directly. Reverse port forwards are not included:
	// they are owned by the transport, not by any one invocation.
	SSHArgs(ctx context.Context) ([]string, error)
	// SSHConnConfig describes the same connection in typed form, for
	// transports that dial without the ssh binary. Implementations resolve
	// whatever the ssh binary would have resolved for them, so the config is
	// dialable on its own.
	SSHConnConfig(ctx context.Context) (SSHConnConfig, error)
}

type sshTransportRecoverer interface {
	recoverSSHTransport(ctx context.Context, cause error) (bool, error)
}

// nonRecoveringSSHEnv hides an env's sshTransportRecoverer implementation, for
// callers that own transport recovery themselves and must not have a second
// recovery nested inside each dial attempt.
type nonRecoveringSSHEnv struct {
	SSHCapableEnv
}

// MergeResultSyncer is implemented by environments whose repository is an
// independent clone rather than a bind mount of the host checkout. After a
// successful merge performed inside such an environment, the merged branch
// must be propagated back to the host repository that is the source of truth.
type MergeResultSyncer interface {
	// SyncMergeResultToLocal transfers the given branch's merged state from the
	// environment back to the host repository.
	SyncMergeResultToLocal(ctx context.Context, branch string) error
}

// GitRefSyncer is implemented by environments whose repository is an
// independent clone rather than a bind mount of the host checkout. It
// propagates a single ref created inside the environment (e.g. an archive
// tag) back to the host repository, so that git state survives the
// environment's deletion.
type GitRefSyncer interface {
	// SyncGitRefToLocal transfers the given fully-qualified ref (e.g.
	// "refs/tags/archive/foo") from the environment to the host repository.
	SyncGitRefToLocal(ctx context.Context, ref string) error
}

// FlowBranchBackupSyncer is implemented by environments whose repository is an
// independent clone rather than a bind mount of the host checkout. It backs up
// a flow branch's commits to the host repository, which is the durable home
// for work in progress even while the environment is alive.
type FlowBranchBackupSyncer interface {
	// SyncFlowBranchToLocal force-updates the same-name branch ref in the host
	// repository from the environment's branch. The branch is never checked
	// out locally, so no host working tree is touched.
	SyncFlowBranchToLocal(ctx context.Context, branch string) error
}

// TargetBranchSyncer is implemented by environments whose repository is an
// independent clone rather than a bind mount of the host checkout. Before
// merging into a branch inside such an environment, the branch must be
// refreshed from the host repository (the source of truth), otherwise the
// merge result may diverge from the host branch and fail to fast-forward it
// when synced back.
type TargetBranchSyncer interface {
	// SyncBranchToRemote force-updates the given branch in the environment
	// from the host repository, realigning any environment worktree that has
	// the branch checked out. Branches missing from the host repository are
	// skipped.
	SyncBranchToRemote(ctx context.Context, branch string) error
}

// EnvSeparator returns the path separator string for the env.
func EnvSeparator(e Env) string {
	switch e.GetType() {
	case EnvTypeLocal, EnvTypeLocalGitWorktree:
		return string(filepath.Separator)
	default:
		return "/"
	}
}

// EnvClean returns a cleaned path for the env's filesystem.
func EnvClean(e Env, p string) string {
	switch e.GetType() {
	case EnvTypeLocal, EnvTypeLocalGitWorktree:
		return filepath.Clean(p)
	default:
		return path.Clean(p)
	}
}

// EnvRel returns a relative path from basepath to targpath for the env.
func EnvRel(e Env, basepath, targpath string) (string, error) {
	switch e.GetType() {
	case EnvTypeLocal, EnvTypeLocalGitWorktree:
		return filepath.Rel(basepath, targpath)
	default:
		return posixRel(basepath, targpath)
	}
}

// posixRel computes a relative path from basepath to targpath using POSIX
// path semantics, equivalent to filepath.Rel for forward-slash paths.
func posixRel(basepath, targpath string) (string, error) {
	base := path.Clean(basepath)
	targ := path.Clean(targpath)

	baseIsAbs := len(base) > 0 && base[0] == '/'
	targIsAbs := len(targ) > 0 && targ[0] == '/'
	if baseIsAbs != targIsAbs {
		return "", fmt.Errorf("Rel: can't make %s relative to %s", targpath, basepath)
	}

	baseParts := splitNonEmpty(base, "/")
	targParts := splitNonEmpty(targ, "/")

	commonLen := 0
	for i := 0; i < len(baseParts) && i < len(targParts); i++ {
		if baseParts[i] != targParts[i] {
			break
		}
		commonLen++
	}

	var parts []string
	for i := commonLen; i < len(baseParts); i++ {
		parts = append(parts, "..")
	}
	parts = append(parts, targParts[commonLen:]...)

	if len(parts) == 0 {
		return ".", nil
	}
	return strings.Join(parts, "/"), nil
}

func splitNonEmpty(s, sep string) []string {
	var result []string
	for _, part := range strings.Split(s, sep) {
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

type EnvRunCommandInput struct {
	// the directory relative to the environment's working directory. must not contain ".."
	RelativeWorkingDir string   `json:"relativeWorkingDir"`
	Command            string   `json:"command"`
	Args               []string `json:"args"`
	EnvVars            []string `json:"envVars,omitempty"`
	// SkipWaking suppresses the automatic wake-if-hibernated check in RunCommand.
	// Must be set to true by the wake/hibernate implementations themselves to
	// avoid infinite recursion.
	SkipWaking bool `json:"skipWaking,omitempty"`
}

type EnvRunCommandOutput = unix.RunCommandActivityOutput

type LocalEnv struct {
	WorkingDirectory string
	Hibernated       bool `json:"hibernated,omitempty"`
}

type LocalGitWorktreeEnv struct {
	WorkingDirectory string
	Hibernated       bool `json:"hibernated,omitempty"`
}

type DevPodEnv struct {
	WorkingDirectory string
	WorkspaceName    string
	// LocalRepoDir is the path to the local checkout of the repo whose
	// remote copy lives in this DevPod container. It is used by file-walking
	// to read tracked content from local git objects instead of paying the
	// per-file SSH/sftp cost.
	LocalRepoDir string `json:"localRepoDir,omitempty"`
	// PortForwards are host ports reverse-forwarded into the container over
	// the SSH connection used to run commands.
	PortForwards []common.PortForwardConfig `json:"portForwards,omitempty"`
	Hibernated   bool                       `json:"hibernated,omitempty"`
}

type OpenShellEnv struct {
	WorkingDirectory string
	SandboxName      string
	// LocalRepoDir is the path to the local checkout of the repo whose
	// remote copy lives in this OpenShell sandbox. It is used by file-walking
	// to read tracked content from local git objects instead of paying the
	// per-file SSH/sftp cost.
	LocalRepoDir string `json:"localRepoDir,omitempty"`
	// PortForwards are host ports reverse-forwarded into the container over
	// the SSH connection used to run commands.
	PortForwards []common.PortForwardConfig `json:"portForwards,omitempty"`
	Hibernated   bool                       `json:"hibernated,omitempty"`
}

// ModalEnv is a Modal (https://modal.com) sandbox reachable over SSH through
// a Modal tunnel. Both the default gVisor runtime and the alpha VM runtime
// (real Linux kernel) are supported; which one a sandbox uses is decided at
// creation time via common.ModalEnvConfig.
type ModalEnv struct {
	WorkingDirectory string `json:"workingDirectory"`
	SandboxName      string `json:"sandboxName"`
	// SSHHost and SSHPort are the Modal tunnel endpoint exposing the
	// sandbox's sshd. They are fixed for the sandbox's lifetime.
	SSHHost string `json:"sshHost"`
	SSHPort int    `json:"sshPort"`
	// LocalRepoDir is the path to the local checkout of the repo whose
	// remote copy lives in this Modal sandbox. It is used by file-walking
	// to read tracked content from local git objects instead of paying the
	// per-file SSH/sftp cost.
	LocalRepoDir string `json:"localRepoDir,omitempty"`
	// PortForwards are host ports reverse-forwarded into the sandbox over
	// the SSH connection used to run commands.
	PortForwards []common.PortForwardConfig `json:"portForwards,omitempty"`
	Hibernated   bool                       `json:"hibernated,omitempty"`

	runModalCommand      func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error)
	runModalAPICommand   func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error)
	refreshModalEndpoint func(context.Context, string) (string, int, error)
}

type LocalEnvParams struct {
	RepoDir     string
	StartBranch *string
	// WorktreeBaseDir overrides GetSidekickDataHome() for worktree placement.
	// Used in tests to avoid setting SIDE_DATA_HOME globally.
	WorktreeBaseDir string
}

func NewLocalEnv(ctx context.Context, params LocalEnvParams) (Env, error) {
	if params.StartBranch != nil {
		return nil, fmt.Errorf("start branch is not supported for local environment")
	}
	dir, err := filepath.Abs(params.RepoDir)
	return &LocalEnv{WorkingDirectory: dir}, err
}

func NewLocalGitWorktreeActivity(ctx context.Context, params LocalEnvParams, worktree domain.Worktree) (EnvContainer, error) {
	env, err := NewLocalGitWorktreeEnv(ctx, params, worktree)
	if err != nil {
		if errors.Is(err, ErrBranchAlreadyExists) {
			return EnvContainer{}, temporal.NewNonRetryableApplicationError(
				err.Error(),
				ErrTypeBranchAlreadyExists,
				err,
			)
		}
		return EnvContainer{}, err
	}
	return EnvContainer{Env: env}, nil
}

func NewLocalGitWorktreeEnv(ctx context.Context, params LocalEnvParams, worktree domain.Worktree) (Env, error) {
	var sidekickDataHome string
	if params.WorktreeBaseDir != "" {
		sidekickDataHome = params.WorktreeBaseDir
	} else {
		var err error
		sidekickDataHome, err = common.GetSidekickDataHome()
		if err != nil {
			return nil, fmt.Errorf("failed to get Sidekick data home: %w", err)
		}
	}

	// Create worktree directory
	// dirName combines original repo name and suffix of branch name, for better DX
	repoName := filepath.Base(params.RepoDir)
	branchSuffix := strings.TrimPrefix(worktree.Name, "side/")
	dirName := repoName + "-" + branchSuffix
	workingDir := filepath.Join(sidekickDataHome, "worktrees", worktree.WorkspaceId, dirName)
	if err := os.MkdirAll(workingDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create worktree directory: %w", err)
	}

	// a worktree's name refers to its branch name
	newBranchName := worktree.Name
	worktreeBaseRef := "HEAD"
	if params.StartBranch != nil && *params.StartBranch != "" {
		worktreeBaseRef = *params.StartBranch
	}
	// Add the worktree, creating a new branch based on the target branch. The
	// worktree is created locked so that "git worktree prune" won't remove it
	// from an environment that can't see its working tree.
	addWorktreeInput := unix.RunCommandActivityInput{
		WorkingDir: params.RepoDir,
		Command:    "git",
		Args:       []string{"worktree", "add", "--lock", "--reason", WorktreeLockReason, "-b", newBranchName, workingDir, worktreeBaseRef},
	}
	addWorktreeOutput, err := unix.RunCommandActivity(ctx, addWorktreeInput)
	if err != nil {
		return nil, fmt.Errorf("failed to run git worktree add command: %w", err)
	}

	if addWorktreeOutput.ExitStatus != 0 {
		err := fmt.Errorf("git worktree add command failed with exit status %d: %s", addWorktreeOutput.ExitStatus, addWorktreeOutput.Stderr)
		if strings.Contains(addWorktreeOutput.Stderr, "already exists") {
			return nil, fmt.Errorf("%w: %v", ErrBranchAlreadyExists, err)
		}
		return nil, err
	}

	return &LocalGitWorktreeEnv{WorkingDirectory: workingDir}, nil
}

func (e *LocalEnv) Walk(ctx context.Context, ignoreFileNames []string, handleEntry func(path string, isDir bool) error) error {
	release, err := acquireLocalReadLockWithWake(ctx, e)
	if err != nil {
		return err
	}
	defer release()
	return walkCodeDirectory(ctx, e.WorkingDirectory, ignoreFileNames, handleEntry)
}

func (e *LocalEnv) GetType() EnvType {
	return EnvTypeLocal
}

func (e *LocalEnv) GetWorkingDirectory() string {
	return e.WorkingDirectory
}

func (e *LocalEnv) RunCommand(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
	if !input.SkipWaking {
		release, err := acquireLocalReadLockWithWake(ctx, e)
		if err != nil {
			return EnvRunCommandOutput{}, err
		}
		defer release()
	}
	runCommandInput := unix.RunCommandActivityInput{
		WorkingDir: filepath.Join(e.WorkingDirectory, input.RelativeWorkingDir),
		Command:    input.Command,
		Args:       input.Args,
		EnvVars:    append(input.EnvVars, envVarsToInject(e.GetType(), nil)...),
	}
	return unix.RunCommandActivity(ctx, runCommandInput)
}

func (e *LocalEnv) ReadFile(ctx context.Context, p string) ([]byte, error) {
	release, lockErr := acquireLocalReadLockWithWake(ctx, e)
	if lockErr != nil {
		return nil, lockErr
	}
	defer release()
	if !filepath.IsAbs(p) {
		p = filepath.Join(e.WorkingDirectory, p)
	}
	return os.ReadFile(p)
}

func (e *LocalEnv) ReadDir(ctx context.Context, p string) ([]fs.DirEntry, error) {
	release, lockErr := acquireLocalReadLockWithWake(ctx, e)
	if lockErr != nil {
		return nil, lockErr
	}
	defer release()
	if !filepath.IsAbs(p) {
		p = filepath.Join(e.WorkingDirectory, p)
	}
	return os.ReadDir(p)
}

func (e *LocalEnv) WriteFile(ctx context.Context, p string, data []byte, perm fs.FileMode) error {
	release, lockErr := acquireLocalReadLockWithWake(ctx, e)
	if lockErr != nil {
		return lockErr
	}
	defer release()
	if !filepath.IsAbs(p) {
		p = filepath.Join(e.WorkingDirectory, p)
	}
	return os.WriteFile(p, data, perm)
}

func (e *LocalEnv) MkdirAll(ctx context.Context, p string, perm fs.FileMode) error {
	release, lockErr := acquireLocalReadLockWithWake(ctx, e)
	if lockErr != nil {
		return lockErr
	}
	defer release()
	if !filepath.IsAbs(p) {
		p = filepath.Join(e.WorkingDirectory, p)
	}
	return os.MkdirAll(p, perm)
}

func (e *LocalEnv) Stat(ctx context.Context, p string) (fs.FileInfo, error) {
	release, lockErr := acquireLocalReadLockWithWake(ctx, e)
	if lockErr != nil {
		return nil, lockErr
	}
	defer release()
	if !filepath.IsAbs(p) {
		p = filepath.Join(e.WorkingDirectory, p)
	}
	return os.Stat(p)
}

func (e *LocalEnv) Remove(ctx context.Context, p string) error {
	release, lockErr := acquireLocalReadLockWithWake(ctx, e)
	if lockErr != nil {
		return lockErr
	}
	defer release()
	if !filepath.IsAbs(p) {
		p = filepath.Join(e.WorkingDirectory, p)
	}
	return os.Remove(p)
}

func (e *LocalEnv) CreateTemp(ctx context.Context, dir, pattern string) (string, error) {
	release, lockErr := acquireLocalReadLockWithWake(ctx, e)
	if lockErr != nil {
		return "", lockErr
	}
	defer release()
	if dir != "" && !filepath.IsAbs(dir) {
		dir = filepath.Join(e.WorkingDirectory, dir)
	}
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	name := f.Name()
	if cerr := f.Close(); cerr != nil {
		return name, cerr
	}
	return name, nil
}

func (e *LocalGitWorktreeEnv) Walk(ctx context.Context, ignoreFileNames []string, handleEntry func(path string, isDir bool) error) error {
	release, err := acquireLocalReadLockWithWake(ctx, e)
	if err != nil {
		return err
	}
	defer release()
	return walkCodeDirectory(ctx, e.WorkingDirectory, ignoreFileNames, handleEntry)
}

func (e *LocalGitWorktreeEnv) GetType() EnvType {
	return EnvTypeLocalGitWorktree
}

func (e *LocalGitWorktreeEnv) GetWorkingDirectory() string {
	return e.WorkingDirectory
}

func (e *LocalGitWorktreeEnv) RunCommand(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
	if !input.SkipWaking {
		release, err := acquireLocalReadLockWithWake(ctx, e)
		if err != nil {
			return EnvRunCommandOutput{}, err
		}
		defer release()
	}
	runCommandInput := unix.RunCommandActivityInput{
		WorkingDir: filepath.Join(e.WorkingDirectory, input.RelativeWorkingDir),
		Command:    input.Command,
		Args:       input.Args,
		EnvVars:    append(input.EnvVars, envVarsToInject(e.GetType(), nil)...),
	}
	return unix.RunCommandActivity(ctx, runCommandInput)
}

func (e *LocalGitWorktreeEnv) ReadFile(ctx context.Context, p string) ([]byte, error) {
	release, lockErr := acquireLocalReadLockWithWake(ctx, e)
	if lockErr != nil {
		return nil, lockErr
	}
	defer release()
	if !filepath.IsAbs(p) {
		p = filepath.Join(e.WorkingDirectory, p)
	}
	return os.ReadFile(p)
}

func (e *LocalGitWorktreeEnv) ReadDir(ctx context.Context, p string) ([]fs.DirEntry, error) {
	release, lockErr := acquireLocalReadLockWithWake(ctx, e)
	if lockErr != nil {
		return nil, lockErr
	}
	defer release()
	if !filepath.IsAbs(p) {
		p = filepath.Join(e.WorkingDirectory, p)
	}
	return os.ReadDir(p)
}

func (e *LocalGitWorktreeEnv) WriteFile(ctx context.Context, p string, data []byte, perm fs.FileMode) error {
	release, lockErr := acquireLocalReadLockWithWake(ctx, e)
	if lockErr != nil {
		return lockErr
	}
	defer release()
	if !filepath.IsAbs(p) {
		p = filepath.Join(e.WorkingDirectory, p)
	}
	return os.WriteFile(p, data, perm)
}

func (e *LocalGitWorktreeEnv) MkdirAll(ctx context.Context, p string, perm fs.FileMode) error {
	release, lockErr := acquireLocalReadLockWithWake(ctx, e)
	if lockErr != nil {
		return lockErr
	}
	defer release()
	if !filepath.IsAbs(p) {
		p = filepath.Join(e.WorkingDirectory, p)
	}
	return os.MkdirAll(p, perm)
}

func (e *LocalGitWorktreeEnv) Stat(ctx context.Context, p string) (fs.FileInfo, error) {
	release, lockErr := acquireLocalReadLockWithWake(ctx, e)
	if lockErr != nil {
		return nil, lockErr
	}
	defer release()
	if !filepath.IsAbs(p) {
		p = filepath.Join(e.WorkingDirectory, p)
	}
	return os.Stat(p)
}

func (e *LocalGitWorktreeEnv) Remove(ctx context.Context, p string) error {
	release, lockErr := acquireLocalReadLockWithWake(ctx, e)
	if lockErr != nil {
		return lockErr
	}
	defer release()
	if !filepath.IsAbs(p) {
		p = filepath.Join(e.WorkingDirectory, p)
	}
	return os.Remove(p)
}

func (e *LocalGitWorktreeEnv) CreateTemp(ctx context.Context, dir, pattern string) (string, error) {
	release, lockErr := acquireLocalReadLockWithWake(ctx, e)
	if lockErr != nil {
		return "", lockErr
	}
	defer release()
	if dir != "" && !filepath.IsAbs(dir) {
		dir = filepath.Join(e.WorkingDirectory, dir)
	}
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	name := f.Name()
	if cerr := f.Close(); cerr != nil {
		return name, cerr
	}
	return name, nil
}

func (e *DevPodEnv) Walk(ctx context.Context, ignoreFileNames []string, handleEntry func(path string, isDir bool) error) error {
	return walkCodeDirectorySSH(ctx, e, e.LocalRepoDir, e.WorkingDirectory, ignoreFileNames, handleEntry)
}

func (e *DevPodEnv) GetType() EnvType {
	return EnvTypeDevPod
}

func (e *DevPodEnv) GetWorkingDirectory() string {
	return e.WorkingDirectory
}

// agentExecRequest maps an EnvRunCommandInput onto a structured side-agent
// exec request: argv, cwd and env cross the channel verbatim, so no shell
// command line is ever assembled from user-controlled input.
func agentExecRequest(workingDir string, envType EnvType, portForwards []common.PortForwardConfig, input EnvRunCommandInput) sideagent.ExecRequest {
	return sideagent.ExecRequest{
		Dir:  filepath.Join(workingDir, input.RelativeWorkingDir),
		Argv: append([]string{input.Command}, input.Args...),
		Env:  append(append([]string{}, input.EnvVars...), envVarsToInject(envType, portForwards)...),
	}
}

// withRemoteReadLock makes req run under the per-worktree shared hibernation
// read lock, bailing before execution when the worktree is hibernated so the
// Go side can wake it and retry.
func withRemoteReadLock(req sideagent.ExecRequest, workDir string) sideagent.ExecRequest {
	req.ReadLockFile = hibernationLockFile(workDir)
	req.HibernationSentinel = workDir + "/" + HibernationMetadataFile
	return req
}

// agentExecOutput maps a structured agent response onto command output.
// Hibernation preemption keeps its sentinel exit code, and failures to start
// the command at all (e.g. missing executable or working directory) surface
// like a failed command: non-zero exit with the reason on stderr.
func agentExecOutput(resp sideagent.ExecResponse) EnvRunCommandOutput {
	output := EnvRunCommandOutput{
		Stdout:     string(resp.Stdout),
		Stderr:     string(resp.Stderr),
		ExitStatus: resp.ExitStatus,
	}
	if resp.Hibernated {
		output.ExitStatus = hibernatedRemoteExitCode
		return output
	}
	if resp.Error != "" {
		if output.Stderr != "" && !strings.HasSuffix(output.Stderr, "\n") {
			output.Stderr += "\n"
		}
		output.Stderr += resp.Error + "\n"
		if output.ExitStatus == 0 {
			output.ExitStatus = -1
		}
	}
	return output
}

// reverseForwardArgs returns ssh -R flags exposing the configured host ports
// on the container's loopback interface, so services bound to 127.0.0.1 on the
// host are reachable from inside the container. When commands multiplex onto
// an existing ControlMaster connection, the master recognizes repeated
// identical forward requests as already established, so these flags are safe
// to include on every invocation and forwards self-heal if the master
// connection is ever restarted.
func reverseForwardArgs(forwards []common.PortForwardConfig) []string {
	args := make([]string, 0, len(forwards)*2)
	for _, forward := range forwards {
		args = append(args, "-R", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", forward.ContainerPortOrDefault(), forward.HostPort))
	}
	return args
}

// insertBeforeSSHDestination inserts extra ssh option args before the
// destination. Options placed after the destination are parsed by ssh as the
// remote command, so the destination is located rather than assumed to be
// last: some envs terminate their args with a "--" separator that lets callers
// append a remote command directly.
func insertBeforeSSHDestination(sshArgs []string, extra []string) []string {
	if len(extra) == 0 {
		return sshArgs
	}
	destination := len(sshArgs) - 1
	if destination > 0 && sshArgs[destination] == "--" {
		destination--
	}
	out := make([]string, 0, len(sshArgs)+len(extra))
	out = append(out, sshArgs[:destination]...)
	out = append(out, extra...)
	out = append(out, sshArgs[destination:]...)
	return out
}

func (e *DevPodEnv) RunCommand(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
	if !input.SkipWaking {
		if err := wakeIfHibernatedRemote(ctx, e); err != nil {
			return EnvRunCommandOutput{}, err
		}
	}

	output, err := e.runCommandInner(ctx, input)

	// The read-lock wrapper signals a hibernated worktree via a sentinel exit
	// code; wake and retry when that happens.
	if !input.SkipWaking && err == nil && output.ExitStatus == hibernatedRemoteExitCode {
		if _, wakeErr := WakeHibernatedEnv(ctx, e); wakeErr != nil {
			return EnvRunCommandOutput{}, wakeErr
		}
		return e.runCommandInner(ctx, input)
	}
	return output, err
}

func (e *DevPodEnv) runCommandInner(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
	req := agentExecRequest(e.WorkingDirectory, e.GetType(), e.PortForwards, input)
	if !input.SkipWaking {
		req = withRemoteReadLock(req, e.WorkingDirectory)
	}

	// Commands run over SSH into the container ultimately depend on the docker
	// engine; a hung engine makes them block forever. Guard so such a hang is
	// detected and the engine restarted instead of stalling the activity.
	var output EnvRunCommandOutput
	err := withDockerEngineWatchdog(ctx, func(ctx context.Context) error {
		resp, execErr := runRemoteCommand(ctx, e.sftpConnKey(), e.PortForwards, e, req)
		if execErr != nil {
			return execErr
		}
		output = agentExecOutput(resp)
		return nil
	})
	if err != nil {
		return output, err
	}

	output.Stderr = stripDevPodTunnelError(output.Stderr)
	return output, nil
}

// devpodSSHConnConfig describes the connection as DevPod's own ssh_config
// entry leaves it: the host is an alias, so reachability (real hostname, port,
// identity, any ProxyCommand) is resolved by OpenSSH rather than stated here.
func devpodSSHConnConfig(workspaceName string) SSHConnConfig {
	return SSHConnConfig{
		Host:                   workspaceName + ".devpod",
		BatchMode:              true,
		LogLevel:               "ERROR",
		KeepaliveInterval:      10 * time.Second,
		KeepaliveMaxFailures:   3,
		ControlPath:            devpodSSHControlPath(workspaceName),
		ControlPersist:         time.Hour,
		LegacyCommandSeparator: true,
	}
}

func (e *DevPodEnv) SSHArgs(ctx context.Context) ([]string, error) {
	return devpodSSHConnConfig(e.WorkspaceName).LegacyArgs(), nil
}

// SSHConnConfig resolves the workspace's host alias through OpenSSH, since a
// transport that does not run the ssh binary cannot rely on it to expand the
// entry DevPod wrote.
func (e *DevPodEnv) SSHConnConfig(ctx context.Context) (SSHConnConfig, error) {
	config := devpodSSHConnConfig(e.WorkspaceName)
	resolved, err := resolveSSHConnConfig(ctx, config.Host)
	if err != nil {
		return SSHConnConfig{}, err
	}
	return config.withResolvedReachability(resolved), nil
}

// sharedSFTP returns the process-wide SFTP connection for this workspace, so
// env copies deserialized across activity invocations reuse one session.
func (e *DevPodEnv) sharedSFTP() *sftpConn {
	return sharedSFTPConnFor("devpod:" + e.WorkspaceName)
}

// sftpConnKey returns the stable per-remote identity used to share a pooled
// sftpConn across separately-constructed envs targeting the same DevPod.
func (e *DevPodEnv) sftpConnKey() string {
	return "devpod:" + e.WorkspaceName
}

// transport returns the SSH transport for this env's remote identity.
func (e *DevPodEnv) transport() SSHTransport {
	return sshTransportFor(e.sftpConnKey(), e.PortForwards, e)
}

// EnsureReverseForwards holds this env's forwards on a connection that outlives
// ctx, so a caller spawning its own ssh gets the same routes home that commands
// run through the transport get.
func (e *DevPodEnv) EnsureReverseForwards(ctx context.Context) error {
	return e.transport().EnsureReverseForwards(ctx, e.PortForwards)
}

func (e *DevPodEnv) ReadFile(ctx context.Context, p string) ([]byte, error) {
	if wakeErr := wakeIfHibernatedRemote(ctx, e); wakeErr != nil {
		return nil, wakeErr
	}
	if !strings.HasPrefix(p, "/") {
		p = path.Join(e.WorkingDirectory, p)
	}
	return sftpReadFile(ctx, e.transport(), p)
}

func (e *DevPodEnv) ReadDir(ctx context.Context, p string) ([]fs.DirEntry, error) {
	if wakeErr := wakeIfHibernatedRemote(ctx, e); wakeErr != nil {
		return nil, wakeErr
	}
	if !strings.HasPrefix(p, "/") {
		p = path.Join(e.WorkingDirectory, p)
	}
	return sftpReadDir(ctx, e.transport(), p)
}

func (e *DevPodEnv) WriteFile(ctx context.Context, p string, data []byte, perm fs.FileMode) error {
	if wakeErr := wakeIfHibernatedRemote(ctx, e); wakeErr != nil {
		return wakeErr
	}
	if !strings.HasPrefix(p, "/") {
		p = path.Join(e.WorkingDirectory, p)
	}
	return sftpWriteFile(ctx, e.transport(), p, data, perm)
}

func (e *DevPodEnv) MkdirAll(ctx context.Context, p string, perm fs.FileMode) error {
	if wakeErr := wakeIfHibernatedRemote(ctx, e); wakeErr != nil {
		return wakeErr
	}
	if !strings.HasPrefix(p, "/") {
		p = path.Join(e.WorkingDirectory, p)
	}
	return sftpMkdirAll(ctx, e.transport(), p, perm)
}

func (e *DevPodEnv) Stat(ctx context.Context, p string) (fs.FileInfo, error) {
	if wakeErr := wakeIfHibernatedRemote(ctx, e); wakeErr != nil {
		return nil, wakeErr
	}
	if !strings.HasPrefix(p, "/") {
		p = path.Join(e.WorkingDirectory, p)
	}
	return sftpStat(ctx, e.transport(), p)
}

func (e *DevPodEnv) Remove(ctx context.Context, p string) error {
	if wakeErr := wakeIfHibernatedRemote(ctx, e); wakeErr != nil {
		return wakeErr
	}
	if !strings.HasPrefix(p, "/") {
		p = path.Join(e.WorkingDirectory, p)
	}
	return sftpRemove(ctx, e.transport(), p)
}

func (e *DevPodEnv) CreateTemp(ctx context.Context, dir, pattern string) (string, error) {
	if wakeErr := wakeIfHibernatedRemote(ctx, e); wakeErr != nil {
		return "", wakeErr
	}
	if dir == "" {
		dir = "/tmp"
	} else if !strings.HasPrefix(dir, "/") {
		dir = path.Join(e.WorkingDirectory, dir)
	}
	return sftpCreateTemp(ctx, e.transport(), dir, pattern)
}

func (e *OpenShellEnv) Walk(ctx context.Context, ignoreFileNames []string, handleEntry func(path string, isDir bool) error) error {
	return walkCodeDirectorySSH(ctx, e, e.LocalRepoDir, e.WorkingDirectory, ignoreFileNames, handleEntry)
}

func (e *OpenShellEnv) GetType() EnvType {
	return EnvTypeOpenShell
}

func (e *OpenShellEnv) GetWorkingDirectory() string {
	return e.WorkingDirectory
}

func (e *OpenShellEnv) RunCommand(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
	if !input.SkipWaking {
		if err := wakeIfHibernatedRemote(ctx, e); err != nil {
			return EnvRunCommandOutput{}, err
		}
	}

	output, err := e.runCommandInner(ctx, input)

	// Read-lock prefix detected hibernation (race with concurrent hibernate)
	if !input.SkipWaking && err == nil && output.ExitStatus == hibernatedRemoteExitCode {
		if _, wakeErr := WakeHibernatedEnv(ctx, e); wakeErr != nil {
			return EnvRunCommandOutput{}, wakeErr
		}
		return e.runCommandInner(ctx, input)
	}
	return output, err
}

func (e *OpenShellEnv) runCommandInner(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
	req := agentExecRequest(e.WorkingDirectory, e.GetType(), e.PortForwards, input)
	if !input.SkipWaking {
		req = withRemoteReadLock(req, e.WorkingDirectory)
	}
	resp, err := runRemoteCommand(ctx, e.sftpConnKey(), e.PortForwards, e, req)
	if err != nil {
		return EnvRunCommandOutput{}, err
	}
	return agentExecOutput(resp), nil
}

func (e *OpenShellEnv) SSHArgs(ctx context.Context) ([]string, error) {
	return openShellSSHArgs(ctx, e.SandboxName)
}

func (e *OpenShellEnv) SSHConnConfig(ctx context.Context) (SSHConnConfig, error) {
	return openShellSSHConnConfig(ctx, e.SandboxName)
}

// sharedSFTP returns the process-wide SFTP connection for this sandbox, so
// env copies deserialized across activity invocations reuse one session.
func (e *OpenShellEnv) sharedSFTP() *sftpConn {
	return sharedSFTPConnFor("openshell:" + e.SandboxName)
}

// sftpConnKey returns the stable per-remote identity used to share a pooled
// sftpConn across separately-constructed envs targeting the same sandbox.
func (e *OpenShellEnv) sftpConnKey() string {
	return "openshell:" + e.SandboxName
}

// transport returns the SSH transport for this env's remote identity.
func (e *OpenShellEnv) transport() SSHTransport {
	return sshTransportFor(e.sftpConnKey(), e.PortForwards, e)
}

func (e *OpenShellEnv) EnsureReverseForwards(ctx context.Context) error {
	return e.transport().EnsureReverseForwards(ctx, e.PortForwards)
}

func (e *OpenShellEnv) ReadFile(ctx context.Context, p string) ([]byte, error) {
	if wakeErr := wakeIfHibernatedRemote(ctx, e); wakeErr != nil {
		return nil, wakeErr
	}
	if !strings.HasPrefix(p, "/") {
		p = path.Join(e.WorkingDirectory, p)
	}
	return sftpReadFile(ctx, e.transport(), p)
}

func (e *OpenShellEnv) ReadDir(ctx context.Context, p string) ([]fs.DirEntry, error) {
	if wakeErr := wakeIfHibernatedRemote(ctx, e); wakeErr != nil {
		return nil, wakeErr
	}
	if !strings.HasPrefix(p, "/") {
		p = path.Join(e.WorkingDirectory, p)
	}
	return sftpReadDir(ctx, e.transport(), p)
}

func (e *OpenShellEnv) WriteFile(ctx context.Context, p string, data []byte, perm fs.FileMode) error {
	if wakeErr := wakeIfHibernatedRemote(ctx, e); wakeErr != nil {
		return wakeErr
	}
	if !strings.HasPrefix(p, "/") {
		p = path.Join(e.WorkingDirectory, p)
	}
	return sftpWriteFile(ctx, e.transport(), p, data, perm)
}

func (e *OpenShellEnv) MkdirAll(ctx context.Context, p string, perm fs.FileMode) error {
	if wakeErr := wakeIfHibernatedRemote(ctx, e); wakeErr != nil {
		return wakeErr
	}
	if !strings.HasPrefix(p, "/") {
		p = path.Join(e.WorkingDirectory, p)
	}
	return sftpMkdirAll(ctx, e.transport(), p, perm)
}

func (e *OpenShellEnv) Stat(ctx context.Context, p string) (fs.FileInfo, error) {
	if wakeErr := wakeIfHibernatedRemote(ctx, e); wakeErr != nil {
		return nil, wakeErr
	}
	if !strings.HasPrefix(p, "/") {
		p = path.Join(e.WorkingDirectory, p)
	}
	return sftpStat(ctx, e.transport(), p)
}

func (e *OpenShellEnv) Remove(ctx context.Context, p string) error {
	if wakeErr := wakeIfHibernatedRemote(ctx, e); wakeErr != nil {
		return wakeErr
	}
	if !strings.HasPrefix(p, "/") {
		p = path.Join(e.WorkingDirectory, p)
	}
	return sftpRemove(ctx, e.transport(), p)
}

func (e *OpenShellEnv) CreateTemp(ctx context.Context, dir, pattern string) (string, error) {
	if wakeErr := wakeIfHibernatedRemote(ctx, e); wakeErr != nil {
		return "", wakeErr
	}
	if dir == "" {
		dir = "/tmp"
	} else if !strings.HasPrefix(dir, "/") {
		dir = path.Join(e.WorkingDirectory, dir)
	}
	return sftpCreateTemp(ctx, e.transport(), dir, pattern)
}

// SetLatency injects artificial latency into each SFTP read for benchmarking.
func (e *OpenShellEnv) SetLatency(d time.Duration) {
	sc := getPooledSFTPConn(e.sftpConnKey())
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.latency = d
	// Force reconnect so the latency wrapper takes effect.
	sc.closeLocked()
}

func (e *ModalEnv) Walk(ctx context.Context, ignoreFileNames []string, handleEntry func(path string, isDir bool) error) error {
	return walkCodeDirectorySSH(ctx, e, e.LocalRepoDir, e.WorkingDirectory, ignoreFileNames, handleEntry)
}

func (e *ModalEnv) GetType() EnvType {
	return EnvTypeModal
}

func (e *ModalEnv) GetWorkingDirectory() string {
	return e.WorkingDirectory
}

// RunCommand runs a command in the sandbox. Worktree hibernation is not used
// on Modal (the idle watchdog snapshots and terminates the whole sandbox when
// idle instead), so commands run without hibernation preflights or read-lock
// wrapping.
func (e *ModalEnv) RunCommand(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {

	runCommand := e.runCommandInner
	if e.runModalCommand != nil {
		runCommand = e.runModalCommand
	}
	runAPICommand := e.runAPICommandInner
	if e.runModalAPICommand != nil {
		runAPICommand = e.runModalAPICommand
	}
	refreshEndpoint := refreshModalEndpoint
	if e.refreshModalEndpoint != nil {
		refreshEndpoint = e.refreshModalEndpoint
	}

	output, diagnostics, err := runCommand(ctx, input)
	appendDiagnostics := func() {
		if diagnostics == "" {
			return
		}
		if output.Stderr != "" && !strings.HasSuffix(output.Stderr, "\n") {
			output.Stderr += "\n"
		}
		output.Stderr += diagnostics
	}
	appendDiagnostics()

	// Only SSH client diagnostics kept separate from remote output can prove
	// the command never started, which is what makes re-running it safe.
	transportFailed := func() bool {
		return err == nil && output.ExitStatus == 255 && isModalSSHTransportFailure(diagnostics)
	}
	if !transportFailed() {
		return output, err
	}

	// The stored tunnel endpoint may be stale because the idle watchdog
	// snapshotted and terminated the sandbox; refreshing restores it.
	host, port, refreshErr := refreshEndpoint(ctx, e.SandboxName)
	if refreshErr != nil {
		log.Warn().Err(refreshErr).Str("sandbox", e.SandboxName).Msg("failed to refresh modal sandbox endpoint")
	} else {
		e.SSHHost, e.SSHPort = host, port
		output, diagnostics, err = runCommand(ctx, input)
		appendDiagnostics()
		if !transportFailed() {
			return output, err
		}
	}

	// SSH is preferred for its multiplexed connection, but the tunnel endpoint
	// is only reachable by dialing an ephemeral high port, which some networks
	// forbid (e.g. HTTP proxies that only allow CONNECT to 443). Modal's API
	// stays usable there, at the cost of losing the reverse port forwards that
	// only SSH can provide.
	apiOutput, apiErr := runAPICommand(ctx, input)
	if apiErr != nil {
		log.Warn().Err(apiErr).Str("sandbox", e.SandboxName).Msg("failed to run command via modal API after SSH transport failure")
		return output, err
	}
	log.Info().Str("sandbox", e.SandboxName).Msg("ran command via modal API after SSH transport failure")
	return apiOutput, nil
}

func (e *ModalEnv) Snapshot(ctx context.Context) (EnvRunCommandOutput, error) {
	return e.RunCommand(ctx, EnvRunCommandInput{
		Command: "/usr/local/bin/sidekick-snapshot",
	})
}

func (e *ModalEnv) recoverSSHTransport(ctx context.Context, cause error) (bool, error) {
	if cause == nil || !isModalSSHTransportFailure(cause.Error()) {
		return false, nil
	}
	refreshEndpoint := refreshModalEndpoint
	if e.refreshModalEndpoint != nil {
		refreshEndpoint = e.refreshModalEndpoint
	}
	host, port, err := refreshEndpoint(ctx, e.SandboxName)
	if err != nil {
		return true, err
	}
	e.SSHHost, e.SSHPort = host, port
	return true, nil
}

// isModalSSHTransportFailure reports whether ssh client diagnostics describe a
// failure that happened before the remote command could start. Callers retry
// on it, so every fragment must denote either a dial that never succeeded or a
// connection that died during the pre-authentication identification and banner
// exchange. Connections made through a ProxyCommand only ever produce the
// latter kind: the dial failure happens inside the proxy helper, and ssh sees
// nothing but a closed pipe.
func isModalSSHTransportFailure(diagnostics string) bool {
	diagnostics = strings.ToLower(diagnostics)
	for _, fragment := range []string{
		"connect to address",
		"connect to host",
		"could not resolve hostname",
		"no route to host",
		"exchange_identification",
		"banner exchange",
		// Marker emitted by sshDialTransportError: the ssh client exited 255
		// before the agent protocol answered, sometimes with no stderr at all.
		"transport failure before agent channel established",
	} {
		if strings.Contains(diagnostics, fragment) {
			return true
		}
	}
	return false
}

// buildRemoteShellCommand builds a single POSIX shell command line that runs
// input's command inside workDir. It exports the requested environment
// variables, changes into workDir, and only then runs the command. A failed cd
// (e.g. the directory does not exist in the remote container) aborts with a
// non-zero exit and a clear message on stderr, so callers get an explicit error
// instead of empty output from a command that silently ran in the session's
// default directory. Only the Modal API fallback path needs shell
// serialization; SSH-reachable environments run commands as verbatim argv over
// the side-agent exec channel instead.
func buildRemoteShellCommand(workDir string, envType EnvType, portForwards []common.PortForwardConfig, input EnvRunCommandInput) string {
	allEnvVars := append(input.EnvVars, envVarsToInject(envType, portForwards)...)
	shellParts := make([]string, 0, len(allEnvVars)+3)

	// Detach stdin so remote commands behave like local ones, which get
	// /dev/null as stdin. Otherwise tools like ripgrep read from the (empty)
	// session stream instead of recursing the working directory.
	shellParts = append(shellParts, "exec 0</dev/null")

	for _, envVar := range allEnvVars {
		shellParts = append(shellParts, "export "+shellQuote(envVar))
	}
	cdFailure := shellQuote("cd: " + workDir + ": No such file or directory")
	shellParts = append(shellParts, "cd "+shellQuote(workDir)+" || { echo "+cdFailure+" >&2; exit 1; }")

	cmdStr := shellQuote(input.Command)
	for _, arg := range input.Args {
		cmdStr += " " + shellQuote(arg)
	}
	shellParts = append(shellParts, cmdStr)

	return strings.Join(shellParts, " && ")
}

// remoteCommand returns the shell command to run inside the sandbox, prefixed
// with a refresh of the idle-watchdog activity marker.
func (e *ModalEnv) remoteCommand(input EnvRunCommandInput) string {
	workDir := filepath.Join(e.WorkingDirectory, input.RelativeWorkingDir)
	return "touch " + remoteActivityMarker + " 2>/dev/null; " +
		buildRemoteShellCommand(workDir, e.GetType(), e.PortForwards, input)
}

// runCommandInner executes input over the pooled side-agent exec channel.
// Channel-level SSH transport failures never reached the remote command; they
// are returned as diagnostics with a 255 exit status (matching a failed ssh
// exec) so RunCommand can refresh a stale tunnel endpoint and retry. Remote
// commands that themselves exit 255 arrive as structured responses and are
// therefore never mistaken for transport failures.
func (e *ModalEnv) runCommandInner(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
	req := agentExecRequest(e.WorkingDirectory, e.GetType(), e.PortForwards, input)
	// Commands run under the login environment (sandbox toolchains land on
	// PATH via profile scripts) and refresh the idle-watchdog activity marker.
	req.LoginEnv = true
	req.TouchPath = remoteActivityMarker

	// RunCommand owns endpoint refresh, retry, and the Modal API fallback for
	// the command path; hiding recoverSSHTransport from the dial keeps it the
	// sole owner instead of nesting a second refresh inside every attempt.
	resp, err := runRemoteCommand(ctx, e.sftpConnKey(), e.PortForwards, nonRecoveringSSHEnv{e}, req)
	if err != nil {
		var establishedFailure *agentExecTransportError
		if errors.As(err, &establishedFailure) {
			return EnvRunCommandOutput{}, establishedFailure.Diagnostics(), err
		}
		var dialFailure *sshDialTransportError
		if ctx.Err() == nil && (errors.As(err, &dialFailure) || isModalSSHTransportFailure(err.Error())) {
			return EnvRunCommandOutput{ExitStatus: 255}, err.Error(), nil
		}
		return EnvRunCommandOutput{}, "", err
	}
	return agentExecOutput(resp), "", nil
}

// runAPICommandInner runs the command through Modal's API rather than SSH.
// Reverse port forwards are an SSH-only feature, so host services stay
// unreachable from commands run this way.
func (e *ModalEnv) runAPICommandInner(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
	return modalExecCommand(ctx, e.SandboxName, e.remoteCommand(input))
}

// SSHConnConfig describes how to reach the sandbox's sshd through its Modal
// tunnel endpoint. Nothing is resolved from a user's ssh_config: the endpoint
// and the key are both ours.
func (e *ModalEnv) SSHConnConfig(ctx context.Context) (SSHConnConfig, error) {
	if e.SSHHost == "" || e.SSHPort == 0 {
		return SSHConnConfig{}, fmt.Errorf("modal env for sandbox %s has no SSH endpoint", e.SandboxName)
	}
	keyPath, _, err := ensureModalSSHKey(ctx)
	if err != nil {
		return SSHConnConfig{}, err
	}
	return modalSSHConnConfig(e.SandboxName, e.SSHHost, e.SSHPort, keyPath), nil
}

// SSHArgs returns ssh args (ending with the destination) for reaching the
// sandbox's sshd through its Modal tunnel endpoint.
func (e *ModalEnv) SSHArgs(ctx context.Context) ([]string, error) {
	config, err := e.SSHConnConfig(ctx)
	if err != nil {
		return nil, err
	}
	return config.LegacyArgs(), nil
}

// sftpConnKey returns the stable per-sandbox identity used to share a pooled
// sftpConn across separately-constructed envs. Modal tunnel endpoints can
// change while the sandbox remains alive; transport recovery reconnects the
// pooled entry when its endpoint becomes stale.
func (e *ModalEnv) sftpConnKey() string {
	return "modal:" + e.SandboxName
}

// transport returns the SSH transport for this env's remote identity.
func (e *ModalEnv) transport() SSHTransport {
	return sshTransportFor(e.sftpConnKey(), e.PortForwards, e)
}

func (e *ModalEnv) EnsureReverseForwards(ctx context.Context) error {
	return e.transport().EnsureReverseForwards(ctx, e.PortForwards)
}

func (e *ModalEnv) ReadFile(ctx context.Context, p string) ([]byte, error) {
	if !strings.HasPrefix(p, "/") {
		p = path.Join(e.WorkingDirectory, p)
	}
	return sftpReadFile(ctx, e.transport(), p)
}

func (e *ModalEnv) ReadDir(ctx context.Context, p string) ([]fs.DirEntry, error) {
	if !strings.HasPrefix(p, "/") {
		p = path.Join(e.WorkingDirectory, p)
	}
	return sftpReadDir(ctx, e.transport(), p)
}

func (e *ModalEnv) WriteFile(ctx context.Context, p string, data []byte, perm fs.FileMode) error {
	if !strings.HasPrefix(p, "/") {
		p = path.Join(e.WorkingDirectory, p)
	}
	return sftpWriteFile(ctx, e.transport(), p, data, perm)
}

func (e *ModalEnv) MkdirAll(ctx context.Context, p string, perm fs.FileMode) error {
	if !strings.HasPrefix(p, "/") {
		p = path.Join(e.WorkingDirectory, p)
	}
	return sftpMkdirAll(ctx, e.transport(), p, perm)
}

func (e *ModalEnv) Stat(ctx context.Context, p string) (fs.FileInfo, error) {
	if !strings.HasPrefix(p, "/") {
		p = path.Join(e.WorkingDirectory, p)
	}
	return sftpStat(ctx, e.transport(), p)
}

func (e *ModalEnv) Remove(ctx context.Context, p string) error {
	if !strings.HasPrefix(p, "/") {
		p = path.Join(e.WorkingDirectory, p)
	}
	return sftpRemove(ctx, e.transport(), p)
}

func (e *ModalEnv) CreateTemp(ctx context.Context, dir, pattern string) (string, error) {
	if dir == "" {
		dir = "/tmp"
	} else if !strings.HasPrefix(dir, "/") {
		dir = path.Join(e.WorkingDirectory, dir)
	}
	return sftpCreateTemp(ctx, e.transport(), dir, pattern)
}

// shellQuote wraps a string in single quotes, escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func stripDevPodTunnelError(stderr string) string {
	const tunnelErrSubstring = "Error tunneling to container: wait: remote command exited without exit status or exit signal"
	lines := strings.Split(stderr, "\n")
	var filtered []string
	for _, line := range lines {
		if !strings.Contains(line, tunnelErrSubstring) {
			filtered = append(filtered, line)
		}
	}
	return strings.Join(filtered, "\n")
}

// maxWorkspaceNameLen is the threshold above which we hash the workspace name
// in the SSH control socket path. Keeps the full path well under the 104-byte
// Unix socket limit even on macOS where os.TempDir() can resolve to long
// paths under /private/var/folders/.
const maxWorkspaceNameLen = 20

// devpodSSHControlPath returns a stable socket path for SSH ControlMaster
// keyed by the workspace name. Uses the workspace name directly for
// readability, falling back to a hash for long names to stay within Unix
// socket path length limits.
func devpodSSHControlPath(workspaceName string) string {
	name := workspaceName
	if len(name) > maxWorkspaceNameLen {
		h := sha256.Sum256([]byte(workspaceName))
		name = fmt.Sprintf("%x", h[:8])
	}
	return filepath.Join(os.TempDir(), "devpod-ssh-"+name)
}

// DevPodWorkspaceName returns the DevPod workspace name for a given repo
// directory path. DevPod derives the workspace name from the directory basename.
func DevPodWorkspaceName(repoDir string) string {
	return filepath.Base(repoDir)
}

// CloseDevPodSSHMaster closes any active SSH master connection for the given
// workspace. It is best-effort and safe to call even when no master exists.
func CloseDevPodSSHMaster(workspaceName string) {
	controlPath := devpodSSHControlPath(workspaceName)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "ssh", "-O", "exit", "-S", controlPath, workspaceName+".devpod").Run(); err != nil {
		log.Warn().Err(err).Str("workspace", workspaceName).Msg("Failed to close SSH master connection")
	}
	if err := os.Remove(controlPath); err != nil && !os.IsNotExist(err) {
		log.Warn().Err(err).Str("path", controlPath).Msg("Failed to remove SSH control socket")
	}
}

// EnvContainer is a wrapper for the Env interface that provides custom
// JSON marshaling and unmarshaling.
type EnvContainer struct {
	Env Env
}

// MarshalJSON returns the JSON encoding of the EnvContainer.
func (ec EnvContainer) MarshalJSON() ([]byte, error) {
	if ec.Env == nil {
		return json.Marshal(struct {
			Type string
			Env  Env
		}{
			Type: "",
			Env:  nil,
		})
	}
	// Marshal to type and actual data to handle unmarshaling to specific interface type
	return json.Marshal(struct {
		Type string
		Env  Env
	}{
		Type: string(ec.Env.GetType()),
		Env:  ec.Env,
	})
}

// UnmarshalJSON parses the JSON-encoded data and stores the result in the EnvContainer.
func (ec *EnvContainer) UnmarshalJSON(data []byte) error {
	var v struct {
		Type string
		Env  json.RawMessage
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}

	switch v.Type {
	case string(EnvTypeLocal):
		var le *LocalEnv
		if err := json.Unmarshal(v.Env, &le); err != nil {
			return err
		}
		ec.Env = le
	case string(EnvTypeLocalGitWorktree):
		var lgwe *LocalGitWorktreeEnv
		if err := json.Unmarshal(v.Env, &lgwe); err != nil {
			return err
		}
		ec.Env = lgwe
	case string(EnvTypeDevPod):
		var dpe *DevPodEnv
		if err := json.Unmarshal(v.Env, &dpe); err != nil {
			return err
		}
		ec.Env = dpe
	case string(EnvTypeOpenShell):
		var ose *OpenShellEnv
		if err := json.Unmarshal(v.Env, &ose); err != nil {
			return err
		}
		ec.Env = ose
	case string(EnvTypeModal):
		var me *ModalEnv
		if err := json.Unmarshal(v.Env, &me); err != nil {
			return err
		}
		ec.Env = me
	case "":
		ec.Env = nil
	default:
		return fmt.Errorf("unknown Env type: %s", v.Type)
	}

	return nil
}

type EnvRunCommandActivityInput struct {
	EnvContainer       EnvContainer
	RelativeWorkingDir string   `json:"relativeWorkingDir"`
	Command            string   `json:"command"`
	Args               []string `json:"args"`
	EnvVars            []string `json:"envVars,omitempty"`
	SkipWaking         bool     `json:"skipWaking,omitempty"`
}

type EnvRunCommandActivityOutput = EnvRunCommandOutput

// maxActivityOutputBytes caps individual stdout/stderr fields to stay within
// Temporal's per-event payload size limit (~2MB).
const maxActivityOutputBytes = 2 * 1024 * 1024

type GetEnvironmentInfoInput struct {
	EnvContainer EnvContainer
}

type GetEnvironmentInfoOutput struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

func (o GetEnvironmentInfoOutput) FormatEnvironmentContext() string {
	return fmt.Sprintf("OS: %s, Arch: %s", o.OS, o.Arch)
}

// GetEnvironmentInfoActivity retrieves OS and architecture info from the environment.
func GetEnvironmentInfoActivity(ctx context.Context, input GetEnvironmentInfoInput) (GetEnvironmentInfoOutput, error) {
	out, err := input.EnvContainer.Env.RunCommand(ctx, EnvRunCommandInput{
		Command: "uname",
		Args:    []string{"-sm"},
	})
	if err != nil {
		return GetEnvironmentInfoOutput{}, fmt.Errorf("failed to get environment info: %w", err)
	}
	info := strings.TrimSpace(out.Stdout)
	if info == "" {
		return GetEnvironmentInfoOutput{}, fmt.Errorf("empty environment info from uname")
	}
	parts := strings.Fields(info)
	if len(parts) < 2 {
		return GetEnvironmentInfoOutput{}, fmt.Errorf("unexpected uname output: %s", info)
	}
	return GetEnvironmentInfoOutput{OS: parts[0], Arch: parts[1]}, nil
}

// EnvRunCommandActivity runs a command in the environment contained in the provided EnvContainer.
func EnvRunCommandActivity(ctx context.Context, input EnvRunCommandActivityInput) (EnvRunCommandActivityOutput, error) {
	type result struct {
		output EnvRunCommandActivityOutput
		err    error
	}
	resultCh := make(chan result, 1)

	go func() {
		out, err := input.EnvContainer.Env.RunCommand(ctx, EnvRunCommandInput{
			RelativeWorkingDir: input.RelativeWorkingDir,
			Command:            input.Command,
			Args:               input.Args,
			EnvVars:            input.EnvVars,
			SkipWaking:         input.SkipWaking,
		})
		resultCh <- result{output: out, err: err}
	}()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case res := <-resultCh:
			if activity.IsActivity(ctx) {
				res.output.Stdout = truncateMiddle(res.output.Stdout, maxActivityOutputBytes)
				res.output.Stderr = truncateMiddle(res.output.Stderr, maxActivityOutputBytes)
			}
			return res.output, res.err
		case <-ticker.C:
			if activity.IsActivity(ctx) {
				activity.RecordHeartbeat(ctx, nil)
			}
		case <-ctx.Done():
			return EnvRunCommandActivityOutput{}, ctx.Err()
		}
	}
}

func truncateMiddle(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	removed := len(s) - maxBytes
	marker := "\n\n[... truncated " + strconv.Itoa(removed) + " bytes from the middle ...]\n\n"
	available := maxBytes - 2*len(marker)
	if available <= 0 {
		return s[:maxBytes]
	}
	half := available / 2
	return s[:half] + marker + s[len(s)-half:] + marker
}
