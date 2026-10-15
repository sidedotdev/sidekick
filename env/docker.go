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

func dockerAutoStartEnabled() bool {
	return envFlagEnabled(autoStartDockerEnvVar)
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
// A stopped daemon may be started automatically. An unresponsive running
// daemon requires explicit recovery confirmation because restarting it
// interrupts every workspace on the engine.
func ensureDockerReady(ctx context.Context) error {
	if dockerRunning(ctx) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	var startErr error
	if dockerProcessRunning(ctx) {
		log.Warn().Msg("docker daemon process is running but unresponsive")
	} else if dockerAutoStartEnabled() {
		log.Warn().Msg("docker daemon not running; attempting to start it")
		startErr = startDocker(ctx)
		if startErr != nil {
			log.Warn().Err(startErr).Msg("failed to start docker daemon")
		} else if waitForDockerEngine(ctx, dockerReadyPollTimeout) {
			return nil
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return dockerEngineUnavailable(startErr)
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
