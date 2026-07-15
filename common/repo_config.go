package common

import "encoding/json"

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

// ModalEnvConfig holds configuration specific to the Modal environment type.
type ModalEnvConfig struct {
	// VM runs the sandbox on Modal's VM runtime (alpha) — a real Linux kernel
	// instead of gVisor — enabling tools that need kernel features (e.g.
	// perf). Memory is statically provisioned on the VM runtime.
	VM bool `toml:"vm,omitempty" json:"vm,omitempty"`
	// Image is the Debian-based, root base image onto which sidekick layers its
	// test and remote-access dependencies. Defaults to the Sidekick Go
	// development image.
	Image string `toml:"image,omitempty" json:"image,omitempty"`
	// CPU is the number of CPU cores to reserve for the sandbox. Unset
	// defaults to 2.
	CPU float64 `toml:"cpu,omitempty" json:"cpu,omitempty"`
	// MemoryMiB is the sandbox memory reservation in MiB. Unset defaults to
	// 2048.
	MemoryMiB int `toml:"memory_mib,omitempty" json:"memoryMiB,omitempty"`
	// IdleSeconds arms the in-sandbox idle watchdog: after this many seconds
	// without activity the sandbox snapshots its filesystem and terminates
	// itself (via the sidekick guard app), stopping billing even when the
	// sidekick host is offline. It is restored from the snapshot on next
	// use. Unset defaults to 30 seconds; negative values are rejected.
	IdleSeconds int `toml:"idle_seconds,omitempty" json:"idleSeconds,omitempty"`
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
