package common

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
	// Keys are use case names (e.g., "requirements", "planning", "coding").
	AgentConfig map[string]AgentUseCaseConfig `toml:"agent_config,omitempty"`

	CommandPermissions CommandPermissionConfig `toml:"command_permissions,omitempty"`
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
	Hints string `toml:"hints"`
	/** Alternatively, specify a path relative to the repo root to load hints from.
	 * If Hints is empty and HintsPath is set, the content of the file will be loaded into Hints. */
	HintsPath string `toml:"hints_path,omitempty"`
}
