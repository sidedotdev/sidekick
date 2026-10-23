package git

import (
	"context"
	"fmt"
	"sync"
	"time"

	"sidekick/env"
	"sidekick/sideagent"

	"github.com/rs/zerolog/log"
)

const (
	gitSyncInterval = 15 * time.Second
	gitSyncTimeout  = 2 * time.Minute
)

type gitSyncRequest struct {
	ctx      context.Context
	run      func(context.Context, bool) error
	attempts int
}

type gitSyncJob struct {
	started time.Time
	done    chan struct{}
	running bool
	timer   *time.Timer
	pending map[string]gitSyncRequest
	order   []string
}

type gitSyncCoordinator struct {
	mu        sync.Mutex
	jobs      map[string]*gitSyncJob
	nextSweep time.Time
	interval  time.Duration
}

func (c *gitSyncCoordinator) delay() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return gitSyncInterval
}

func (c *gitSyncCoordinator) jobLocked(key string) *gitSyncJob {
	now := time.Now()
	if c.jobs == nil {
		c.jobs = make(map[string]*gitSyncJob)
	}
	if now.After(c.nextSweep) {
		for key, job := range c.jobs {
			if !job.running && job.timer == nil && len(job.pending) == 0 && now.Sub(job.started) > time.Hour {
				delete(c.jobs, key)
			}
		}
		c.nextSweep = now.Add(time.Minute)
	}
	if c.jobs[key] == nil {
		c.jobs[key] = &gitSyncJob{pending: make(map[string]gitSyncRequest)}
	}
	return c.jobs[key]
}

func (c *gitSyncCoordinator) finish(key string, job *gitSyncJob, worktree string, request gitSyncRequest, err error, background bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if background && err != nil && request.attempts < 3 {
		if _, newer := job.pending[worktree]; !newer {
			request.attempts++
			job.pending[worktree] = request
			job.order = append(job.order, worktree)
		}
	}
	job.running = false
	close(job.done)
	c.scheduleLocked(key, job)
}

func (c *gitSyncCoordinator) background(ctx context.Context, key, worktree string, run func(context.Context, bool) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	job := c.jobLocked(key)
	if _, exists := job.pending[worktree]; !exists {
		job.order = append(job.order, worktree)
	}
	job.pending[worktree] = gitSyncRequest{ctx: context.WithoutCancel(ctx), run: run, attempts: 1}
	c.scheduleLocked(key, job)
}

func (c *gitSyncCoordinator) checkpoint(ctx context.Context, key, worktree string, run func(context.Context, bool) error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.mu.Lock()
		job := c.jobLocked(key)
		if job.running {
			done := job.done
			c.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if job.timer != nil {
			job.timer.Stop()
			job.timer = nil
		}
		delete(job.pending, worktree)
		for i, queued := range job.order {
			if queued == worktree {
				job.order = append(job.order[:i], job.order[i+1:]...)
				break
			}
		}
		job.started, job.running, job.done = time.Now(), true, make(chan struct{})
		c.mu.Unlock()
		request := gitSyncRequest{ctx: ctx, run: run}
		err := run(ctx, true)
		c.finish(key, job, worktree, request, err, false)
		return err
	}
}

var repositoryGitSync gitSyncCoordinator

// remoteSyncEnv is a sandbox environment whose repository is an independent
// clone of a worker-side repository, reachable over SSH.
type remoteSyncEnv interface {
	env.SSHCapableEnv
	// WorkerRepository returns the worker-side repository directory the
	// sandbox clone was seeded from and the sandbox's stable identity.
	WorkerRepository() (localRepoDir, sandboxName string)
}

func gitSyncOperation(e env.Env) (string, func(context.Context, bool) error) {
	var remote env.SSHCapableEnv
	var local, sandbox string
	switch e := e.(type) {
	case *env.ModalEnv:
		copy := *e
		remote, local, sandbox = &copy, e.LocalRepoDir, e.SandboxName
	case *env.OpenShellEnv:
		copy := *e
		remote, local, sandbox = &copy, e.LocalRepoDir, e.SandboxName
	case remoteSyncEnv:
		remote = e
		local, sandbox = e.WorkerRepository()
	default:
		return "", nil
	}
	if local == "" || remote.GetWorkingDirectory() == "" {
		return "", nil
	}
	key := fmt.Sprintf("%s:%s:%s", remote.GetType(), sandbox, local)
	return key, func(ctx context.Context, checkpoint bool) error {
		operation := func() error {
			args, err := remote.SSHArgs(ctx)
			if err != nil {
				return err
			}
			lock := ""
			if checkpoint {
				lock = sideagent.SealLockPath
			}
			return syncGitRepositoriesWithLock(ctx, args, local, remote.GetWorkingDirectory(), lock)
		}
		if checkpoint {
			return env.RunWithSSHTransportRecovery(ctx, remote, operation)
		}
		return operation()
	}
}

func SupportsGitSync(e env.Env) bool {
	key, _ := gitSyncOperation(e)
	return key != ""
}

// RemoteSyncObserver backs up sandbox commits to the worker repository shortly
// after each command, so that work survives a lost sandbox. Observed
// environments never get woken or recovered for this: a sandbox that is gone
// has nothing new to back up.
type RemoteSyncObserver struct{}

var _ env.CommandObserver = RemoteSyncObserver{}

func (RemoteSyncObserver) CommandFinished(ctx context.Context, envContainer env.EnvContainer) {
	key, run := gitSyncOperation(envContainer.Env)
	if run != nil {
		repositoryGitSync.background(ctx, key, envContainer.Env.GetWorkingDirectory(), run)
	}
}

type SyncGitInput struct {
	EnvContainer env.EnvContainer `json:"envContainer"`
}

type SyncGitOutput struct{}

func SyncGitActivity(ctx context.Context, input SyncGitInput) (SyncGitOutput, error) {
	key, run := gitSyncOperation(input.EnvContainer.Env)
	if run == nil {
		return SyncGitOutput{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, gitSyncTimeout)
	defer cancel()
	return SyncGitOutput{}, repositoryGitSync.checkpoint(ctx, key, input.EnvContainer.Env.GetWorkingDirectory(), run)
}

func (c *gitSyncCoordinator) scheduleLocked(key string, job *gitSyncJob) {
	if job.running || job.timer != nil || len(job.order) == 0 {
		return
	}
	delay := max(time.Until(job.started.Add(c.delay())), 0)
	var timer *time.Timer
	timer = time.AfterFunc(delay, func() {
		c.mu.Lock()
		if job.timer != timer {
			c.mu.Unlock()
			return
		}
		job.timer = nil
		worktree := job.order[0]
		job.order = job.order[1:]
		request := job.pending[worktree]
		delete(job.pending, worktree)
		job.started, job.running, job.done = time.Now(), true, make(chan struct{})
		c.mu.Unlock()

		ctx, cancel := context.WithTimeout(request.ctx, gitSyncTimeout)
		defer cancel()
		err := request.run(ctx, false)
		if err != nil {
			log.Warn().Err(err).Str("repository", key).Str("worktree", worktree).
				Int("attempt", request.attempts).Msg("background git sync failed")
		}
		c.finish(key, job, worktree, request, err, true)
	})
	job.timer = timer
}
