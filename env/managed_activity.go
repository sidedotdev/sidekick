package env

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"sidekick/sideagent"
)

// BeginActivity protects work performed over a separately cached channel.
// The returned release must run after cancellation cleanup, not merely when
// the request context is cancelled.
func BeginActivity(ctx context.Context, e Env, onLost func(error)) (func(), error) {
	if _, remote := e.(SSHCapableEnv); !remote {
		return func() {}, ctx.Err()
	}
	return beginActivity(ctx, func(ctx context.Context, req sideagent.ExecRequest) (sideagent.ExecResponse, error) {
		output, err := e.RunCommand(ctx, EnvRunCommandInput{
			activity: &activityRequest{ready: req.OnReady},
		})
		if err == nil && output.ExitStatus != 0 {
			err = fmt.Errorf("activity admission failed (%d): %s", output.ExitStatus, output.Stderr)
		}
		return sideagent.ExecResponse{}, err
	}, onLost)
}

func beginActivity(ctx context.Context, run func(context.Context, sideagent.ExecRequest) (sideagent.ExecResponse, error), onLost func(error)) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	leaseCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	ready, done := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var result error
	stopping, admitted := false, false
	go func() {
		defer close(done)
		resp, err := run(leaseCtx, sideagent.ExecRequest{
			ActivityLease: true,
			TouchPath:     remoteActivityMarker,
			OnReady: func() {
				mu.Lock()
				defer mu.Unlock()
				if !admitted {
					admitted = true
					close(ready)
				}
			},
		})
		if err == nil {
			switch {
			case resp.Sealed:
				err = errors.New(sideagent.SealedMessage)
			case resp.Hibernated:
				err = errors.New("activity worktree is hibernated")
			case resp.Error != "":
				err = fmt.Errorf("activity admission: %s", resp.Error)
			default:
				err = errors.New("activity lease ended before release")
			}
		}
		mu.Lock()
		result = err
		lost := admitted && !stopping
		mu.Unlock()
		if lost && onLost != nil {
			onLost(fmt.Errorf("background activity lease lost: %w", err))
		}
	}()
	select {
	case <-ready:
		var once sync.Once
		return func() {
			once.Do(func() {
				mu.Lock()
				stopping = true
				mu.Unlock()
				cancel()
				<-done
			})
		}, nil
	case <-done:
		cancel()
		return nil, result
	case <-ctx.Done():
		mu.Lock()
		stopping = true
		mu.Unlock()
		cancel()
		<-done
		return nil, ctx.Err()
	}
}

// ManagedBackgroundCommand explicitly opts a transparent SSH subprocess into
// request-based activity accounting. Its callers must hold an activity lease
// whenever they use that subprocess.
func ManagedBackgroundCommand(command string) (string, error) {
	agent, err := agentRemotePath()
	if err != nil {
		return "", err
	}
	return agent + " background sh -c " + shellQuote(command), nil
}

type activityRequest struct {
	ready    func()
	admitted atomic.Bool
}

func (a *activityRequest) admit() {
	if a.admitted.CompareAndSwap(false, true) {
		a.ready()
	}
}
