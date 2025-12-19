package common

// ConfigOverrides allows overriding configuration parameters from RepoConfig
// and LocalConfig on a per-run basis. Pointer fields distinguish between
// unset (nil) and explicitly set values (including empty slices/strings).
type ConfigOverrides struct {
	// RepoConfig overrides
	Mission                 *string          `json:"mission,omitempty"`
	DisableHumanInTheLoop   *bool            `json:"disableHumanInTheLoop,omitempty"`
	MaxIterations           *int             `json:"maxIterations,omitempty"`
	MaxPlanningIterations   *int             `json:"maxPlanningIterations,omitempty"`
	CheckCommands           *[]CommandConfig `json:"checkCommands,omitempty"`
	AutofixCommands         *[]CommandConfig `json:"autofixCommands,omitempty"`
	TestCommands            *[]CommandConfig `json:"testCommands,omitempty"`
	IntegrationTestCommands *[]CommandConfig `json:"integrationTestCommands,omitempty"`
	WorktreeSetup           *string          `json:"worktreeSetup,omitempty"`

	AgentConfig map[string]*AgentUseCaseConfig `json:"agentConfig,omitempty"`

	CommandPermissions *CommandPermissionConfig `json:"commandPermissions,omitempty"`

	// LocalConfig overrides
	LLM       *LLMConfig                   `json:"llm,omitempty"`
	Embedding *EmbeddingConfig             `json:"embedding,omitempty"`
	Providers *[]ModelProviderPublicConfig `json:"providers,omitempty"`
}

// ApplyToRepoConfig updates the provided RepoConfig with any non-nil override values.
func (o ConfigOverrides) ApplyToRepoConfig(c *RepoConfig) {
	if o.Mission != nil {
		c.Mission = *o.Mission
	}
	if o.DisableHumanInTheLoop != nil {
		c.DisableHumanInTheLoop = *o.DisableHumanInTheLoop
	}
	if o.MaxIterations != nil {
		c.MaxIterations = *o.MaxIterations
	}
	if o.MaxPlanningIterations != nil {
		c.MaxPlanningIterations = *o.MaxPlanningIterations
	}
	if o.CheckCommands != nil {
		c.CheckCommands = *o.CheckCommands
	}
	if o.AutofixCommands != nil {
		c.AutofixCommands = *o.AutofixCommands
	}
	if o.TestCommands != nil {
		c.TestCommands = *o.TestCommands
	}
	if o.IntegrationTestCommands != nil {
		c.IntegrationTestCommands = *o.IntegrationTestCommands
	}
	if o.WorktreeSetup != nil {
		c.WorktreeSetup = *o.WorktreeSetup
	}
	if o.AgentConfig != nil {
		if c.AgentConfig == nil {
			c.AgentConfig = make(map[string]AgentUseCaseConfig)
		}
		for key, val := range o.AgentConfig {
			if val != nil {
				c.AgentConfig[key] = *val
			}
		}
	}
	if o.CommandPermissions != nil {
		c.CommandPermissions = *o.CommandPermissions
	}
}
