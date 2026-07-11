package env

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"sidekick/coding/unix"

	"github.com/rs/zerolog/log"
)

// autoStartDockerEnvVar controls whether sidekick attempts to start a stopped
// Docker daemon before creating OpenShell/DevPod environments. Enabled by
// default; disable with a falsy value (0, false, no, off).
const autoStartDockerEnvVar = "SIDE_AUTO_START_DOCKER"

// autoRestartDockerEnvVar controls whether sidekick attempts to restart a wedged
// Docker engine (both pre-flight and via the in-operation watchdog). Enabled by
// default; disable with a falsy value (0, false, no, off).
const autoRestartDockerEnvVar = "SIDE_AUTO_RESTART_DOCKER"

func dockerAutoStartEnabled() bool {
	return envFlagEnabled(autoStartDockerEnvVar)
}

func dockerAutoRestartEnabled() bool {
	return envFlagEnabled(autoRestartDockerEnvVar)
}

// envFlagEnabled reports whether a boolean-style environment variable is
// enabled. Unset or unrecognized values are treated as enabled; only explicit
// falsy values (0, false, no, off) disable the flag.
func envFlagEnabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// dockerRunning reports whether the Docker daemon is reachable. It uses the fast
// `docker version` server probe rather than the slower `docker info`.
func dockerRunning(ctx context.Context) bool {
	return checkDockerEngineResponsive(ctx, dockerHealthCheckTimeout)
}

// startDocker and dockerProcessRunning are indirected so tests can exercise
// ensureDockerReady without launching or inspecting a real Docker daemon.
var (
	startDocker          = startDockerPlatform
	dockerProcessRunning = dockerProcessRunningPlatform
)

// ensureDockerReady makes a best-effort attempt to ensure the Docker daemon is
// responsive before an OpenShell/DevPod operation.
//
// An unreachable daemon has two very different causes that need different
// remedies: it was never started (e.g. after a reboot), in which case starting
// it is enough; or its process is alive but wedged, in which case only a restart
// (kill + relaunch) recovers it. We tell them apart by checking whether the
// daemon process is present, so we never waste time "starting" an
// already-running-but-hung daemon.
func ensureDockerReady(ctx context.Context) error {
	if dockerRunning(ctx) {
		return nil
	}

	startEnabled := dockerAutoStartEnabled()
	restartEnabled := dockerAutoRestartEnabled()
	if !startEnabled && !restartEnabled {
		return fmt.Errorf("docker daemon is not responsive (auto-start via %s and auto-restart via %s are both disabled)", autoStartDockerEnvVar, autoRestartDockerEnvVar)
	}

	if dockerProcessRunning(ctx) {
		log.Warn().Msg("docker daemon process is running but unresponsive; treating it as wedged")
	} else if startEnabled {
		log.Warn().Msg("docker daemon not running; attempting to start it")
		if err := startDocker(ctx); err != nil {
			log.Warn().Err(err).Msg("failed to start docker daemon")
		} else if waitForDockerEngine(ctx, dockerReadyPollTimeout) {
			return nil
		}
	}

	if restartEnabled {
		log.Warn().Msg("attempting to restart the docker engine")
		return restartDockerEngineFunc(ctx)
	}

	return fmt.Errorf("docker daemon did not become responsive after auto-start (set %s to allow restarts)", autoRestartDockerEnvVar)
}

// dockerProcessRunningPlatform reports whether a Docker daemon process is
// present, independent of whether it is currently responsive. It is used to
// distinguish a stopped daemon (start it) from a wedged one (restart it).
func dockerProcessRunningPlatform(ctx context.Context) bool {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command = "pgrep"
		args = []string{"-f", "com.docker.backend"}
	case "linux":
		command = "pgrep"
		args = []string{"-x", "dockerd"}
	default:
		return false
	}
	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: ".",
		Command:    command,
		Args:       args,
	})
	return err == nil && output.ExitStatus == 0
}

// startDockerPlatform launches the Docker daemon using a platform-appropriate
// command. Unlike a full restart it does not kill an existing (possibly wedged)
// backend; it is meant to bring up a cleanly stopped daemon.
func startDockerPlatform(ctx context.Context) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command = "open"
		args = []string{"-a", "Docker"}
	case "linux":
		command = "systemctl"
		args = []string{"start", "docker"}
	default:
		return fmt.Errorf("automatic docker start is not supported on %s", runtime.GOOS)
	}

	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: ".",
		Command:    command,
		Args:       args,
	})
	if err != nil {
		return err
	}
	if output.ExitStatus != 0 {
		return fmt.Errorf("%s %s exited with status %d: %s", command, strings.Join(args, " "), output.ExitStatus, output.Stderr)
	}
	return nil
}
