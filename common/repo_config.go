package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type RepoConfig struct {
	/** A set of commands to run to check the code for basic issues, eg syntax
	 * err, after an edit to determine if it is a good edit. A failed check
	 * results in rolling back the edit entirely, so is intended for cases where
	 * GenAI is not able to easily self-repair iteratively after a mistake. */
	CheckCommands []CommandConfig `toml:"check_commands,omitempty"`

	/** A set of commands to run to fix the code after applying an edit. This
	 * helps avoid checks reverting code for simple issues. Ideal for things
	 * like auto-importing for example. */
	AutofixCommands []CommandConfig `toml:"autofix_commands,omitempty"`

	/** A set of commands to run to test the code after good/checked edits that
	 * were already fully applied. Typically expected to run a project's unit
	 * tests. Test failure is typically provided as feedback in the next edit
	 * iteration or used to determine whether a given step in a plan is
	 * completa. */
	TestCommands []CommandConfig `toml:"test_commands,omitempty"`

	/** A set of commands to run to test the code after good/checked edits that
	 * were already fully applied. Typically expected to run a project's
	 * integration tests. Test failure is typically provided as feedback in the
	 * next edit iteration or used to determine whether a given step in a plan
	 * is completa. */
	IntegrationTestCommands []CommandConfig `toml:"integration_test_commands,omitempty"`

	/** This is injected into prompts to give the LLM high-level context about
	 * the purpose of your project. This is used especially when defining
	 * requirements */
	Mission string `toml:"mission,omitempty"`

	/** Usage of this flag is NOT RECOMMENDED. This flag is intended to be used
	 * for benchmarking purposes ONLY. Turning this on makes it so a human will
	 * never be asked for input, help/guidance or to review. Human intelligence
	 * and quality control is essential to leverage GenAI effectively. */
	DisableHumanInTheLoop bool `toml:"disable_human_in_the_loop,omitempty"`

	/** The maximum number of iterations that GenAI will run for. This is a
	 * safety measure to prevent infinite loops. Defaults to 17 if unspecified. */
	MaxIterations int `toml:"max_iterations,omitempty"`

	/** The maximum number of planning iterations that GenAI will run for. This is
	 * a safety measure to prevent infinite loops. Defaults to 17 if unspecified. */
	MaxPlanningIterations int `toml:"max_planning_iterations,omitempty"`

	EditCode EditCodeConfig `toml:"edit_code,omitempty"`

	/** A script that will be executed in the working directory of a local git
	 * worktree environment when setting up the dev context. This is useful for
	 * performing any necessary setup steps specific to worktree environments.
	 * The script is executed using /usr/bin/env sh -c and must return a zero
	 * exit code to be considered successful. */
	WorktreeSetup string `toml:"worktree_setup,omitempty"`

	// AgentConfig contains per-use-case configuration for agent loops.
	// Keys are use case names (e.g., "planning", "coding", "coding_and_verification",
	// "step_execution_and_verification").
	AgentConfig map[string]AgentUseCaseConfig `toml:"agent_config,omitempty"`

	CommandPermissions CommandPermissionConfig `toml:"command_permissions,omitempty"`

	// DevRun configures commands for running a dev server or supervisor
	// for pre-approval manual QA in the worktree environment.
	DevRun DevRunConfig `toml:"dev_run,omitempty"`

	// EnvType specifies the default environment type for this repo (e.g., "local", "devpod", "openshell", "modal").
	EnvType string `toml:"env_type,omitempty"`

	// RepoMode specifies the default repo mode for this repo (e.g., "worktree", "in_place").
	RepoMode string `toml:"repo_mode,omitempty"`

	// PortForwards declares host ports to reverse-forward into remote
	// container environments (devpod/openshell) over SSH, making services
	// bound to the host's loopback interface reachable from inside the
	// container.
	PortForwards []PortForwardConfig `toml:"port_forwards,omitempty"`

	DevPodConfig    DevPodEnvConfig    `toml:"devpod,omitempty"`
	OpenShellConfig OpenShellEnvConfig `toml:"openshell,omitempty"`
	ModalConfig     ModalEnvConfig     `toml:"modal,omitempty"`
}

// PortForwardConfig declares a single host port to reverse-forward into a
// remote container environment over SSH, exposing a service listening on the
// host's 127.0.0.1 to the container's 127.0.0.1.
type PortForwardConfig struct {
	// HostPort is the port on the host (bound to 127.0.0.1) to forward.
	HostPort int `toml:"host_port" json:"hostPort"`

	// ContainerPort is the loopback port inside the container on which the
	// forwarded host port is exposed. Defaults to HostPort when zero.
	ContainerPort int `toml:"container_port,omitempty" json:"containerPort,omitempty"`
}

// ContainerPortOrDefault returns ContainerPort, defaulting to HostPort when
// unset.
func (c PortForwardConfig) ContainerPortOrDefault() int {
	if c.ContainerPort != 0 {
		return c.ContainerPort
	}
	return c.HostPort
}

// DevPodEnvConfig holds configuration specific to the DevPod environment type.
type DevPodEnvConfig struct {
	// WorkspaceId overrides the workspace name DevPod derives from the
	// repo directory basename. When empty, the repo directory's basename is
	// used, matching DevPod's default behavior.
	WorkspaceId string `toml:"workspace_id,omitempty"`
}

// OpenShellEnvConfig holds configuration specific to the OpenShell environment type.
type OpenShellEnvConfig struct {
	// PrebuildCommand is a shell command executed locally before sandbox creation.
	PrebuildCommand string `toml:"prebuild_command,omitempty"`
	// From is passed as the --from flag to "openshell sandbox create".
	From string `toml:"from,omitempty"`
}

// ModalVolumeMount attaches a named Modal Volume at an absolute path inside
// the sandbox. Volumes outlive the sandboxes that mount them and are shared by
// every sandbox using the same name, so names should be scoped to the data
// they hold. What a volume contains is entirely up to the repository: it is
// the place for caches that are expensive to rebuild, such as compiler,
// package-manager or test-selection caches.
type ModalVolumeMount struct {
	Name      string `toml:"name" json:"name"`
	MountPath string `toml:"mount_path" json:"mountPath"`
	// ReadOnly mounts the volume without write access, which is how a volume
	// populated elsewhere can be consumed safely by many sandboxes at once.
	ReadOnly bool `toml:"read_only,omitempty" json:"readOnly,omitempty"`
}

// Validate reports whether the mount can be attached to a sandbox. Mount paths
// must be absolute because they are interpreted inside the container, and the
// filesystem root is rejected since mounting over it would hide the image.
func (m ModalVolumeMount) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return errors.New("modal volume mount requires a name")
	}
	mountPath := strings.TrimSpace(m.MountPath)
	if !strings.HasPrefix(mountPath, "/") {
		return fmt.Errorf("modal volume %q requires an absolute mount_path, got %q", m.Name, m.MountPath)
	}
	if filepath.Clean(mountPath) == "/" {
		return fmt.Errorf("modal volume %q cannot be mounted at the filesystem root", m.Name)
	}
	return nil
}

// ModalEnvConfig holds configuration specific to the Modal environment type.
type ModalEnvConfig struct {
	// VM runs the sandbox on Modal's VM runtime (alpha) — a real Linux kernel
	// instead of gVisor — enabling tools that need kernel features (e.g.
	// perf). Memory is statically provisioned from Memory on the VM runtime,
	// so MemoryLimit does not provide burst capacity.
	VM bool `toml:"vm,omitempty" json:"vm,omitempty"`
	// Image is the base container image reference. It must be Debian-based and
	// run as root, since sidekick layers its remote-access dependencies on top.
	Image string `toml:"image,omitempty" json:"image,omitempty"`
	// DockerfilePath selects a single-stage, context-free Dockerfile relative
	// to the repository root. Its FROM supplies the base image, so Image must
	// be unset. COPY, ADD, and BuildKit context mounts are unsupported.
	DockerfilePath string `toml:"dockerfile_path,omitempty" json:"dockerfilePath,omitempty"`
	// CPU is the number of fractional physical CPU cores reserved for the
	// sandbox. Unset defaults to 0.125, Modal's minimum; usage above the
	// request bursts freely unless CPULimit is set.
	CPU float64 `toml:"cpu,omitempty" json:"cpu,omitempty"`
	// CPULimit is a hard cap in fractional physical CPU cores. Zero means no
	// limit.
	CPULimit float64 `toml:"cpu_limit,omitempty" json:"cpuLimit,omitempty"`
	// Memory is the sandbox memory reservation in MiB. Unset defaults to
	// 1024; usage above the request bursts freely unless MemoryLimit is set.
	Memory int `toml:"memory,omitempty" json:"memory,omitempty"`
	// MemoryLimit is a hard memory cap in MiB. Zero means no limit.
	MemoryLimit int `toml:"memory_limit,omitempty" json:"memoryLimit,omitempty"`
	// IdleSeconds arms the in-sandbox idle watchdog: after this many seconds
	// without activity the sandbox snapshots its filesystem and terminates
	// itself (via the sidekick guard app), stopping billing even when the
	// sidekick host is offline. It is restored from the snapshot on next
	// use. Unset defaults to 30 seconds; negative values are rejected.
	IdleSeconds int `toml:"idle_seconds,omitempty" json:"idleSeconds,omitempty"`
	// ActiveSnapshotSeconds sets how often the in-sandbox watchdog takes a
	// best-effort filesystem snapshot while the sandbox is busy, bounding
	// the work lost if the sandbox is forcefully killed before the idle
	// shutdown can run. Unset defaults to 180 seconds; negative values
	// disable active snapshots (idle snapshots are unaffected).
	ActiveSnapshotSeconds int `toml:"active_snapshot_seconds,omitempty" json:"activeSnapshotSeconds,omitempty"`
	// Volumes mounts named, persistent Modal Volumes into the sandbox. Unlike
	// the sandbox filesystem, whose snapshots are tied to a single sandbox
	// lineage, volumes are reachable from any sandbox that mounts them, so
	// data placed there survives sandbox recreation.
	Volumes []ModalVolumeMount `toml:"volumes,omitempty" json:"volumes,omitempty"`
}

// ModalDefaultCPU and ModalDefaultMemoryMiB are the effective resource
// requests applied at sandbox creation when CPU/Memory are unset.
const (
	ModalDefaultCPU       = 0.125
	ModalDefaultMemoryMiB = 1024
)

// NormalizedVolumeMounts validates the configured volume mounts and returns
// copies with cleaned mount paths, rejecting mounts whose paths collide once
// normalized (e.g. "/cache" and "/cache/").
func (c ModalEnvConfig) NormalizedVolumeMounts() ([]ModalVolumeMount, error) {
	if len(c.Volumes) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(c.Volumes))
	mounts := make([]ModalVolumeMount, 0, len(c.Volumes))
	for _, mount := range c.Volumes {
		if err := mount.Validate(); err != nil {
			return nil, err
		}
		mount.MountPath = filepath.Clean(strings.TrimSpace(mount.MountPath))
		if seen[mount.MountPath] {
			return nil, fmt.Errorf("modal volume mount path %s is configured more than once", mount.MountPath)
		}
		seen[mount.MountPath] = true
		mounts = append(mounts, mount)
	}
	return mounts, nil
}

// Validate covers every configuration check that does not require talking to
// Modal, so callers can reject a bad configuration before provisioning — or,
// when reconfiguring a live environment, before its sandbox is destroyed.
func (c ModalEnvConfig) Validate() error {
	if c.Image != "" && c.DockerfilePath != "" {
		return errors.New("modal image and dockerfile_path cannot both be set")
	}
	if c.CPU < 0 {
		return fmt.Errorf("modal cpu must not be negative, got %v", c.CPU)
	}
	if c.CPULimit < 0 {
		return fmt.Errorf("modal cpu_limit must not be negative, got %v", c.CPULimit)
	}
	cpuRequest := c.CPU
	if cpuRequest == 0 {
		cpuRequest = ModalDefaultCPU
	}
	if c.CPULimit > 0 && c.CPULimit < cpuRequest {
		return fmt.Errorf("modal cpu_limit (%v) must not be below the effective cpu request (%v)", c.CPULimit, cpuRequest)
	}
	if c.Memory < 0 {
		return fmt.Errorf("modal memory must not be negative, got %d", c.Memory)
	}
	if c.MemoryLimit < 0 {
		return fmt.Errorf("modal memory_limit must not be negative, got %d", c.MemoryLimit)
	}
	memoryRequest := c.Memory
	if memoryRequest == 0 {
		memoryRequest = ModalDefaultMemoryMiB
	}
	if c.MemoryLimit > 0 && c.MemoryLimit < memoryRequest {
		return fmt.Errorf("modal memory_limit (%d MiB) must not be below the effective memory request (%d MiB)", c.MemoryLimit, memoryRequest)
	}
	if c.IdleSeconds < 0 {
		return fmt.Errorf("modal idle_seconds must not be negative, got %d", c.IdleSeconds)
	}
	_, err := c.NormalizedVolumeMounts()
	return err
}

// GlobalState keys for workflow-specific state
const (
	KeyCurrentTargetBranch = "currentTargetBranch"
)

// DevRunConfig maps command IDs to their configurations.
// Each command ID can only have one instance running at a time,
// but multiple different command IDs can run in parallel.
type DevRunConfig map[string]DevRunCommandConfig

// DevRunCommandConfig configures a single named dev-run command.
type DevRunCommandConfig struct {
	WorkingDir string `toml:"working_dir,omitempty" json:"workingDir,omitempty"`
	Command    string `toml:"command" json:"command"`

	// StopTimeoutSeconds is the time to wait after SIGINT before sending SIGKILL.
	// Defaults to 10 seconds if not specified.
	StopTimeoutSeconds int `toml:"stop_timeout_seconds,omitempty" json:"stopTimeoutSeconds,omitempty"`
}

// UnmarshalJSON supports both the current camelCase keys and the legacy
// PascalCase keys (WorkingDir, Command, StopTimeoutSeconds) that were
// produced before explicit json tags were added.
func (c *DevRunCommandConfig) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if v, ok := raw["workingDir"]; ok {
		_ = json.Unmarshal(v, &c.WorkingDir)
	} else if v, ok := raw["WorkingDir"]; ok {
		_ = json.Unmarshal(v, &c.WorkingDir)
	}

	if v, ok := raw["command"]; ok {
		_ = json.Unmarshal(v, &c.Command)
	} else if v, ok := raw["Command"]; ok {
		_ = json.Unmarshal(v, &c.Command)
	}

	if v, ok := raw["stopTimeoutSeconds"]; ok {
		_ = json.Unmarshal(v, &c.StopTimeoutSeconds)
	} else if v, ok := raw["StopTimeoutSeconds"]; ok {
		_ = json.Unmarshal(v, &c.StopTimeoutSeconds)
	}

	return nil
}

type CommandConfig struct {
	WorkingDir string `toml:"working_dir,omitempty"`
	Command    string `toml:"command"`
}

// AgentUseCaseConfig contains configuration for a specific agent use case.
type AgentUseCaseConfig struct {
	AutoIterations int `toml:"auto_iterations,omitempty"`
}

type EditCodeConfig struct {
	/** This is injected into the edit code prompt in order to provide hints to the LLM
	 * for how to edit code in your particular code base. */
	Hints string `toml:"hints,omitempty"`
	/** Alternatively, specify a path relative to the repo root to load hints from.
	 * If Hints is empty and HintsPath is set, the content of the file will be loaded into Hints. */
	HintsPath string `toml:"hints_path,omitempty"`
}
