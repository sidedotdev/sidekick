package env

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// A hung docker daemon causes every docker/devpod/ssh invocation to block
// indefinitely rather than returning an error. Because the CLI never returns,
// the only reliable signal is that a cheap probe fails to complete within a
// bounded time. The values below control how aggressively that condition is
// detected while an operation is in flight; they are vars (not consts) so tests
// can shrink them.
var (
	// dockerHealthCheckInterval is how often the watchdog probes engine health
	// while an operation runs.
	dockerHealthCheckInterval = 15 * time.Second
	// dockerHealthCheckTimeout bounds a single engine probe. A healthy daemon
	// answers in well under a second; a hung one never answers.
	dockerHealthCheckTimeout = 10 * time.Second
	// dockerHealthUnresponsiveThreshold is the number of consecutive failed
	// probes required before the engine is declared hung. Requiring several in
	// a row avoids false positives from transient slowness during heavy builds.
	dockerHealthUnresponsiveThreshold = 3

	// dockerRestartCommandTimeout bounds each individual restart command so a
	// wedged restart path cannot itself hang forever.
	dockerRestartCommandTimeout = 2 * time.Minute
	// dockerReadyPollTimeout bounds how long we wait for the engine to become
	// responsive again after issuing a restart.
	dockerReadyPollTimeout = 3 * time.Minute
	// dockerReadyPollInterval is how often we re-probe while waiting for the
	// engine to come back.
	dockerReadyPollInterval = 3 * time.Second
)

// Indirected for tests: the watchdog and pre-flight checks call through these
// so behavior can be simulated without a real docker daemon, and restart
// strategies can be replaced so tests never launch or kill a real engine.
var (
	checkDockerEngineResponsive = dockerEngineResponsive
	restartDockerEngineFunc     = RestartDockerEngine
	dockerRestartStrategiesFunc = dockerRestartStrategies
)

// dockerEngineResponsive reports whether the docker daemon answers a cheap
// request within the timeout. It targets the server (not just the CLI) via
// `docker version --format {{.Server.Version}}`, so it returns false both when
// the daemon is down and, crucially, when it is hung: the bounded context kills
// the blocked CLI process and Run returns an error.
func dockerEngineResponsive(ctx context.Context, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	// Ensure Wait returns promptly after the process is killed on timeout,
	// even if a child inherited the pipes.
	cmd.WaitDelay = time.Second
	return cmd.Run() == nil
}

// withDockerEngineWatchdog runs fn while monitoring docker engine health. If the
// engine stops responding for long enough while fn is in flight, it restarts the
// engine and cancels fn so the caller does not block until its activity's
// StartToClose timeout. In that case it returns a retryable error so the
// operation can be re-attempted against the recovered engine. When auto-restart
// is disabled it runs fn without monitoring.
func withDockerEngineWatchdog(ctx context.Context, fn func(ctx context.Context) error) error {
	if !dockerAutoRestartEnabled() {
		return fn(ctx)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		engineRestarted bool
		restartErr      error
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(dockerHealthCheckInterval)
		defer ticker.Stop()
		consecutiveFailures := 0
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				if checkDockerEngineResponsive(runCtx, dockerHealthCheckTimeout) {
					consecutiveFailures = 0
					continue
				}
				// The probe can also fail simply because fn finished and runCtx
				// was canceled; don't count that as an engine hang.
				if runCtx.Err() != nil {
					return
				}
				consecutiveFailures++
				if consecutiveFailures < dockerHealthUnresponsiveThreshold {
					continue
				}
				log.Warn().Int("consecutiveFailures", consecutiveFailures).
					Msg("docker engine unresponsive during operation; killing operation and restarting engine")
				cancel()
				restartErr = restartDockerEngineFunc(ctx)
				engineRestarted = true
				return
			}
		}
	}()

	runErr := fn(runCtx)
	cancel()
	<-done

	if engineRestarted {
		if restartErr != nil {
			return fmt.Errorf("docker engine became unresponsive and could not be restarted: %w", restartErr)
		}
		return errors.New("docker engine became unresponsive and was restarted; operation must be retried")
	}
	return runErr
}

// RestartDockerEngine attempts to restart the docker engine using the first
// platform-appropriate strategy that brings it back to a responsive state. It is
// best-effort: strategies that are not applicable to the host simply fail and
// the next one is tried.
func RestartDockerEngine(ctx context.Context) error {
	log.Warn().Str("os", runtime.GOOS).Msg("attempting to restart docker engine")
	var errs []error
	for _, strategy := range dockerRestartStrategiesFunc() {
		if err := strategy.run(ctx); err != nil {
			log.Warn().Err(err).Str("strategy", strategy.name).Msg("docker restart strategy failed")
			errs = append(errs, fmt.Errorf("%s: %w", strategy.name, err))
			continue
		}
		if waitForDockerEngine(ctx, dockerReadyPollTimeout) {
			log.Info().Str("strategy", strategy.name).Msg("docker engine responsive after restart")
			return nil
		}
		log.Warn().Str("strategy", strategy.name).Msg("docker engine still unresponsive after restart strategy")
		errs = append(errs, fmt.Errorf("%s: engine still unresponsive", strategy.name))
	}
	return fmt.Errorf("docker engine remains unresponsive after all restart strategies: %w", errors.Join(errs...))
}

type dockerRestartStrategy struct {
	name string
	run  func(ctx context.Context) error
}

// dockerRestartStrategies returns ordered, best-effort ways to restart the
// engine for the current OS. The Docker Desktop CLI plugin is tried first
// everywhere it may exist; platform-specific fallbacks follow.
func dockerRestartStrategies() []dockerRestartStrategy {
	desktop := dockerRestartStrategy{
		name: "docker-desktop-restart",
		run:  boundedCommand("docker", "desktop", "restart"),
	}
	switch runtime.GOOS {
	case "darwin":
		return []dockerRestartStrategy{
			desktop,
			{name: "relaunch-docker-desktop-app", run: relaunchDockerDesktopDarwin},
		}
	case "windows":
		return []dockerRestartStrategy{desktop}
	default: // linux and other unixes
		return []dockerRestartStrategy{
			desktop,
			{name: "systemctl-user-restart-docker-desktop", run: boundedCommand("systemctl", "--user", "restart", "docker-desktop")},
			{name: "systemctl-restart-docker", run: boundedCommand("systemctl", "restart", "docker")},
			{name: "sudo-systemctl-restart-docker", run: boundedCommand("sudo", "-n", "systemctl", "restart", "docker")},
		}
	}
}

// relaunchDockerDesktopDarwin force-stops the Docker Desktop backend and
// relaunches the app. Killing the wedged backend is more reliable than a
// graceful quit when the engine is unresponsive.
func relaunchDockerDesktopDarwin(ctx context.Context) error {
	// Best-effort kills: these fail harmlessly when no matching process exists.
	_ = boundedCommand("killall", "-KILL", "com.docker.backend")(ctx)
	_ = boundedCommand("killall", "Docker")(ctx)
	return boundedCommand("open", "-a", "Docker")(ctx)
}

// boundedCommand returns a runnable that executes name with args under a hard
// timeout so a wedged restart path cannot hang indefinitely.
func boundedCommand(name string, args ...string) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, dockerRestartCommandTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.WaitDelay = time.Second
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
}

// waitForDockerEngine polls until the engine responds or the timeout elapses.
func waitForDockerEngine(ctx context.Context, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if checkDockerEngineResponsive(ctx, dockerHealthCheckTimeout) {
			return true
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(dockerReadyPollInterval):
		}
	}
}
