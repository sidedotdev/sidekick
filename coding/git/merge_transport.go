package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"sidekick/env"
)

// mergeTransport only supplies objects; checkout and stash lifecycle decisions
// belong to the coordinator.
type mergeTransport interface {
	targetRepository() mergeRepository
	backupSourceBranch(context.Context, string) error
	copyRef(context.Context, string, bool) error
}

type sameRepositoryMergeTransport struct {
	repository mergeRepository
}

type remoteMergeTransport struct {
	source mergeSourceEnv
	host   mergeRepository
}

func newMergeTransport(ctx context.Context, source mergeRepository) (mergeTransport, error) {
	var remote mergeRepositoryEnv
	switch e := source.envContainer.Env.(type) {
	case *env.ModalEnv:
		remote = remoteMergeRepository{mergeSourceEnv: e, hostDir: e.LocalRepoDir}
	case *env.OpenShellEnv:
		remote = remoteMergeRepository{mergeSourceEnv: e, hostDir: e.LocalRepoDir}
	case mergeRepositoryEnv:
		remote = e
	default:
		return sameRepositoryMergeTransport{repository: source}, nil
	}
	host, err := remote.HostRepository(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to discover host repository: %w", err)
	}
	return remoteMergeTransport{
		source: source.envContainer.Env.(mergeSourceEnv),
		host:   mergeRepository{envContainer: env.EnvContainer{Env: host}},
	}, nil
}

func (t sameRepositoryMergeTransport) targetRepository() mergeRepository {
	return t.repository
}

func (t sameRepositoryMergeTransport) backupSourceBranch(context.Context, string) error {
	return nil
}

func (t sameRepositoryMergeTransport) copyRef(context.Context, string, bool) error {
	return nil
}

func (t remoteMergeTransport) targetRepository() mergeRepository {
	return t.host
}

func (t remoteMergeTransport) backupSourceBranch(ctx context.Context, branch string) error {
	return t.source.SyncFlowBranchToLocal(ctx, branch)
}

func (t remoteMergeTransport) copyRef(ctx context.Context, ref string, toHost bool) error {
	return env.RunWithSSHTransportRecovery(ctx, t.source, func() error {
		sshArgs, err := t.source.SSHArgs(ctx)
		if err != nil {
			return err
		}
		return syncMergeRefOverSSH(ctx, sshArgs, t.source.GetWorkingDirectory(),
			t.host.envContainer.Env.GetWorkingDirectory(), ref, toHost)
	})
}

type mergeSourceEnv interface {
	env.SSHCapableEnv
	env.FlowBranchBackupSyncer
}

type mergeRepositoryEnv interface {
	mergeSourceEnv
	HostRepository(context.Context) (env.Env, error)
}

type remoteMergeRepository struct {
	mergeSourceEnv
	hostDir string
}

func (r remoteMergeRepository) HostRepository(ctx context.Context) (env.Env, error) {
	if r.hostDir == "" {
		return nil, fmt.Errorf("host repository directory is required")
	}
	return env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: r.hostDir})
}

// syncMergeRefOverSSH leaves checkout and stash ownership with the coordinator.
func syncMergeRefOverSSH(ctx context.Context, sshArgs []string, workingDirectory, localRepoDir, ref string, toHost bool) error {
	if !strings.HasPrefix(ref, "refs/sidekick-merge/") {
		return fmt.Errorf("merge transport requires a private merge ref: %q", ref)
	}
	if out, err := exec.CommandContext(ctx, "git", "check-ref-format", ref).CombinedOutput(); err != nil {
		return fmt.Errorf("invalid merge transport ref %q: %w: %s", ref, err, out)
	}
	if workingDirectory == "" || localRepoDir == "" {
		return fmt.Errorf("source and host repository directories are required")
	}

	n := len(sshArgs)
	if n > 0 && sshArgs[n-1] == "--" {
		n--
	}
	if n == 0 || sshArgs[n-1] == "" {
		return fmt.Errorf("could not determine ssh destination from args %v", sshArgs)
	}
	dest, opts := sshArgs[n-1], sshArgs[:n-1]
	gitSSH := "ssh"
	for _, arg := range opts {
		gitSSH += " " + shellQuote(arg)
	}
	args := []string{"-C", localRepoDir}
	if toHost {
		args = append(args, "fetch", "--no-tags", "--no-write-fetch-head")
	} else {
		args = append(args, "push", "--quiet")
	}
	args = append(args, dest+":"+workingDirectory, ref+":"+ref)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+gitSSH, "LC_ALL=C")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git transfer merge ref %s (to host: %t): %w: %s", ref, toHost, err, out)
	}
	return nil
}
