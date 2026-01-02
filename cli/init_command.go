package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/llm"
	"sidekick/srv"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/huh"
	"github.com/erikgeiser/promptkit/selection"
	"github.com/goccy/go-yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"github.com/segmentio/ksuid"
	"github.com/zalando/go-keyring"
)

type InitCommandHandler struct {
	storage srv.Storage
}

func NewInitCommandHandler(storage srv.Storage) *InitCommandHandler {
	return &InitCommandHandler{
		storage: storage,
	}
}

func (h *InitCommandHandler) handleInitCommand() error {
	fmt.Println("Initializing new workspace...")

	baseDir, err := getGitBaseDirectory()
	if err != nil {
		return fmt.Errorf("error determining Git base directory: %w", err)
	}

	isRepo, err := isGitRepo(baseDir)
	if err != nil {
		return fmt.Errorf("error checking if directory is a git repository: %w", err)
	}
	if !isRepo {
		return fmt.Errorf("side init must be run within a git repository")
	}

	err = checkLanguageSpecificTools(baseDir)
	if err != nil {
		return err
	}

	sideignorePath := filepath.Join(baseDir, ".sideignore")
	if _, err := os.Stat(sideignorePath); errors.Is(err, os.ErrNotExist) {
		err := createSideignore(sideignorePath)
		if err != nil {
			return fmt.Errorf("error creating .sideignore: %w", err)
		}
		fmt.Println("✔ Created .sideignore file (commit this)")
	} else if err != nil {
		return fmt.Errorf("error checking .sideignore: %w", err)
	}

	// Check for existing local provider configuration
	localConfig, err := common.LoadSidekickConfig(common.GetSidekickConfigPath())
	if err != nil {
		return fmt.Errorf("error loading local config: %w", err)
	}

	// Handle embedding provider setup
	embeddingProvider, err := selectEmbeddingProvider(localConfig)
	if err != nil {
		return fmt.Errorf("error selecting embedding provider: %w", err)
	}

	// Handle LLM provider setup
	llmProvider, err := selectLLMProvider(localConfig)
	if err != nil {
		return fmt.Errorf("error selecting LLM provider: %w", err)
	}

	config, configCheck, err := checkConfig(baseDir)
	if err != nil {
		return fmt.Errorf("error during config check: %w", err)
	}

	if !configCheck.hasTestCommands {
		err = ensureTestCommands(&config, configCheck.filePath)
		if err != nil {
			return fmt.Errorf("error prompting for test command: %w", err)
		}
		if len(config.TestCommands) > 0 {
			fmt.Println("✔ Your test command has been saved in side.yml (commit this)")
		} else {
			fmt.Println("ℹ Skipping test command configuration. You can add test commands to side.yml later for best results.")
		}
	} else {
		fmt.Println("✔ Found valid test commands in repo config")
	}

	ctx := context.Background()

	// check if redis is running
	err = h.storage.CheckConnection(ctx)
	if err != nil {
		return fmt.Errorf("Redis isn't running, please install and run it: https://redis.io/docs/install/install-redis/")
	}

	existingWorkspace, err := h.getWorkspaceByRepoDir(ctx, baseDir)
	if err != nil {
		return fmt.Errorf("error retrieving existing workspace: %w", err)
	}
	dirName := filepath.Base(baseDir)
	workspaceName := dirName
	repoName, err := getRepoName(baseDir)
	if err != nil && repoName != dirName && existingWorkspace == nil {
		workspaceNameSelection := selection.New("Which workspace name do you prefer?", []string{dirName, repoName})
		selectedWorkspaceName, err := workspaceNameSelection.RunPrompt()
		if err != nil {
			return fmt.Errorf("workspace name selection failed: %w", err)
		}
		workspaceName = selectedWorkspaceName
	}

	workspace, err := h.findOrCreateWorkspace(ctx, workspaceName, baseDir)
	if err != nil {
		return fmt.Errorf("error finding or creating workspace: %w", err)
	}
	fmt.Printf("✔ Workspace found or created successfully: %v\n", workspace.Id)

	existingConfig, err := h.storage.GetWorkspaceConfig(ctx, workspace.Id)
	if err != nil && !errors.Is(err, srv.ErrNotFound) {
		return fmt.Errorf("error retrieving workspace configuration: %w", err)
	}

	err = h.ensureWorkspaceConfig(ctx, workspace.Id, &existingConfig, llmProvider, embeddingProvider)
	if err != nil {
		return fmt.Errorf("error ensuring workspace configuration: %w", err)
	}
	fmt.Println("✔ Workspace configuration has been set up")

	if checkServerStatus() {
		fmt.Printf("✔ Sidekick server is running. Go to http://localhost:%d\n", common.GetServerPort())
	} else {
		fmt.Println("ℹ Sidekick server is not running")

		startServer := true // default to "Yes"
		err := huh.NewConfirm().
			Title("Would you like to start the server now?").
			Value(&startServer).
			Affirmative("Yes").
			Negative("No").
			Run()

		if err != nil {
			return fmt.Errorf("error prompting to start server: %w", err)
		}

		if startServer {
			cmd := NewStartCommand()
			if err := handleStartCommand(context.Background(), cmd); err != nil {
				return fmt.Errorf("error starting server: %w", err)
			}
		} else {
			fmt.Println("Please run 'side start' to start the server when you're ready")
		}
	}

	return nil
}

func createSideignore(sideignorePath string) error {
	f, err := os.Create(sideignorePath)
	if err != nil {
		return fmt.Errorf("error creating .sideignore: %w", err)
	}
	defer f.Close()
	content := `
# To avoid giving your LLM irrelevant context, this .sideignore file can be used
# to exclude any code that isn't part of what is normally edited.  The format is
# the same as .gitignore. Note that any patterns listed here are ignored *in
# addition* to to what is ignored via .sideignore.
#
# The following is a list of common vendored dependencies or known paths that
# should not be edited, in case they are not already in a .gitignore file.
# Adjust by:
#
# 1. Removing paths for languages/frameworks not relevant to you
# 2. Add any paths specific to your project that get auto-generated but not
#    normally imported, such as mocks etc.

# General vendored dependencies
vendor/
third_party/
extern/
deps/

# Node
node_modules/

# Python virtual environments (less common names)
.venv/
env/

# Go vendored dependencies
vendor/

# Ruby
.bundle/
vendor/bundle/

# PHP
vendor/

# Java / Kotlin
.gradle/

# C / C++
deps/

# Swift / Dart / Flutter
.dart_tool/
Carthage/

# Elixir / Erlang
_build/
deps/
`
	_, err = f.WriteString(strings.TrimSpace(content))
	return err
}

func (h *InitCommandHandler) ensureWorkspaceConfig(ctx context.Context, workspaceID string, currentConfig *domain.WorkspaceConfig, llmProvider, embeddingProvider string) error {
	if currentConfig == nil {
		currentConfig = &domain.WorkspaceConfig{}
	}

	// Set up LLM configuration
	if llmProvider != "" {
		currentConfig.LLM.Defaults = []common.ModelConfig{{Provider: llmProvider}}
	} else {
		currentConfig.LLM.Defaults = []common.ModelConfig{}
	}

	// Set up Embedding configuration
	if embeddingProvider != "" {
		currentConfig.Embedding.Defaults = []common.ModelConfig{{Provider: embeddingProvider}}
	} else {
		currentConfig.Embedding.Defaults = []common.ModelConfig{}
	}

	// Persist the updated configuration
	err := h.storage.PersistWorkspaceConfig(ctx, workspaceID, *currentConfig)
	if err != nil {
		return fmt.Errorf("failed to persist workspace config: %w", err)
	}

	return nil
}

// FIXME make a call to an API instead of directly using a database. this may
// require running the server locally as a daemon if not already running or
// configured to be remote
func (h *InitCommandHandler) findOrCreateWorkspace(ctx context.Context, workspaceName, repoDir string) (*domain.Workspace, error) {
	existingWorkspace, err := h.getWorkspaceByRepoDir(ctx, repoDir)
	if err != nil {
		return nil, fmt.Errorf("error retrieving workspace by repo dir: %w", err)
	}
	if existingWorkspace != nil {
		return existingWorkspace, err
	}

	workspace := domain.Workspace{
		Name:         workspaceName,
		Id:           "ws_" + ksuid.New().String(),
		LocalRepoDir: repoDir,
		Created:      time.Now(),
		Updated:      time.Now(),
	}

	if err := h.storage.PersistWorkspace(ctx, workspace); err != nil {
		return nil, fmt.Errorf("error persisting workspace: %w", err)
	}

	return &workspace, nil
}

func (h *InitCommandHandler) getWorkspaceByRepoDir(ctx context.Context, repoDir string) (*domain.Workspace, error) {
	workspaces, err := h.storage.GetAllWorkspaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("error retrieving workspaces: %w", err)
	}

	for _, workspace := range workspaces {
		if workspace.LocalRepoDir == repoDir {
			return &workspace, nil
		}
	}
	return nil, nil
}

func isGitRepo(baseDir string) (bool, error) {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = baseDir
	err := cmd.Run()

	if err != nil {
		exitError, ok := err.(*exec.ExitError)
		if !ok {
			return false, err
		}
		if status, ok := exitError.Sys().(syscall.WaitStatus); ok && status.ExitStatus() != 0 {
			return false, nil // Not a git repository
		}
	}

	return true, nil
}

func getGitBaseDirectory() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func getRepoName(baseDir string) (string, error) {
	cmd := exec.Command("git", "-C", baseDir, "remote", "get-url", "origin")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	url := strings.TrimSpace(string(output))
	parts := strings.Split(url, "/")
	if len(parts) == 0 {
		return "", fmt.Errorf("invalid repository URL")
	}

	repoName := parts[len(parts)-1]
	repoName = strings.TrimSuffix(repoName, ".git")
	return repoName, nil
}

type configCheckResult struct {
	filePath        string
	hasTestCommands bool
}

var repoConfigCandidates = []string{"side.yml", "side.yaml", "side.toml", "side.json"}

func checkConfig(baseDir string) (common.RepoConfig, configCheckResult, error) {
	var config common.RepoConfig
	var result configCheckResult

	discovery := common.DiscoverConfigFile(baseDir, repoConfigCandidates)
	if discovery.ChosenPath == "" {
		result.filePath = filepath.Join(baseDir, "side.yml")
		return config, result, nil
	}

	result.filePath = discovery.ChosenPath

	k := koanf.New(".")
	parser := common.GetParserForExtension(discovery.ChosenPath)
	if parser == nil {
		return config, result, fmt.Errorf("unsupported config file format: %s", discovery.ChosenPath)
	}

	if err := k.Load(file.Provider(discovery.ChosenPath), parser); err != nil {
		return config, result, fmt.Errorf("error loading config file: %w", err)
	}

	if err := k.UnmarshalWithConf("", &config, koanf.UnmarshalConf{Tag: "toml"}); err != nil {
		return config, result, fmt.Errorf("error decoding config file: %w", err)
	}

	if len(config.TestCommands) > 0 {
		result.hasTestCommands = true
	}

	return config, result, nil
}

func saveConfig(filePath string, config common.RepoConfig) error {
	ext := strings.ToLower(filepath.Ext(filePath))

	var data []byte
	var err error

	switch ext {
	case ".yml", ".yaml":
		data, err = yaml.Marshal(config)
	case ".toml":
		var buf bytes.Buffer
		err = toml.NewEncoder(&buf).Encode(config)
		data = buf.Bytes()
	case ".json":
		data, err = json.MarshalIndent(config, "", "  ")
	default:
		return fmt.Errorf("unsupported config file format: %s", ext)
	}

	if err != nil {
		return fmt.Errorf("error encoding config: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("error writing config file: %w", err)
	}

	return nil
}

func ensureTestCommands(config *common.RepoConfig, filePath string) error {
	fmt.Println("\nPlease enter the command you use to run your tests (or type 'skip' to skip)")
	fmt.Println("Examples: pytest, jest, go test ./...")
	fmt.Print("Test command: ")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	testCommand := strings.TrimSpace(scanner.Text())

	if testCommand == "" {
		return fmt.Errorf("no command entered, exiting early")
	}

	if strings.EqualFold(testCommand, "skip") {
		// Write a commented placeholder example to the config file
		if err := saveConfig(filePath, *config); err != nil {
			return err
		}
		// Append commented example after the encoded config (JSON doesn't support comments)
		ext := strings.ToLower(filepath.Ext(filePath))
		if ext != ".json" {
			f, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY, 0644)
			if err != nil {
				return fmt.Errorf("error opening config file: %w", err)
			}
			defer f.Close()
			var comment string
			if ext == ".toml" {
				comment = "\n# Uncomment and configure test commands for best results:\n# [[test_commands]]\n# command = \"pytest\"\n"
			} else {
				comment = "\n# Uncomment and configure test commands for best results:\n# test_commands:\n#   - command: pytest\n"
			}
			if _, err := f.WriteString(comment); err != nil {
				return fmt.Errorf("error writing comment to config file: %w", err)
			}
		}
		return nil
	}

	config.TestCommands = []common.CommandConfig{
		{Command: testCommand},
	}

	if err := saveConfig(filePath, *config); err != nil {
		return err
	}

	return nil
}

func getConfiguredBuiltinLLMProviders() []string {
	var providers []string

	// Check OpenAI
	if key, err := keyring.Get(keyringService, llm.OpenaiApiKeySecretName); err == nil && key != "" {
		providers = append(providers, "openai")
	}

	// Check Google
	if key, err := keyring.Get(keyringService, llm.GoogleApiKeySecretName); err == nil && key != "" {
		providers = append(providers, "google")
	}

	// Check Anthropic (either API key or OAuth)
	hasAnthropicKey := false
	if key, err := keyring.Get(keyringService, llm.AnthropicApiKeySecretName); err == nil && key != "" {
		hasAnthropicKey = true
	}
	if creds, err := keyring.Get(keyringService, AnthropicOAuthSecretName); err == nil && creds != "" {
		hasAnthropicKey = true
	}
	if hasAnthropicKey {
		providers = append(providers, "anthropic")
	}

	return providers
}

func selectLLMProvider(localConfig common.LocalConfig) (string, error) {
	// Check if LLM is already configured
	if len(localConfig.LLM) > 0 {
		useExisting := true
		err := huh.NewConfirm().
			Title("Found existing LLM configuration. Use existing?").
			Value(&useExisting).
			Affirmative("Use existing").
			Negative("Customize").
			Run()
		if err != nil {
			return "", fmt.Errorf("error prompting for LLM configuration: %w", err)
		}
		if useExisting {
			return "", nil
		}
	}

	// Build selection list
	var options []string

	// Add configured built-in providers
	configuredBuiltins := getConfiguredBuiltinLLMProviders()
	for _, p := range configuredBuiltins {
		options = append(options, strings.Title(p))
	}

	// Add custom providers from local config
	for _, provider := range localConfig.Providers {
		options = append(options, provider.Name)
	}

	// If no providers configured at all, go directly to auth flow
	if len(options) == 0 {
		fmt.Println("No LLM providers configured. Let's set one up.")
		if err := handleAuthCommand(); err != nil {
			return "", err
		}
		// After auth, determine which provider was added
		newProviders := getConfiguredBuiltinLLMProviders()
		if len(newProviders) > 0 {
			return newProviders[len(newProviders)-1], nil
		}
		return "", fmt.Errorf("no provider was configured")
	}

	// Add "Add new provider" option
	options = append(options, "Add new provider")

	providerSelection := selection.New("Select your LLM provider", options)
	selected, err := providerSelection.RunPrompt()
	if err != nil {
		return "", fmt.Errorf("provider selection failed: %w", err)
	}

	if selected == "Add new provider" {
		if err := handleAuthCommand(); err != nil {
			return "", err
		}
		// After auth, determine which provider was added
		newProviders := getConfiguredBuiltinLLMProviders()
		if len(newProviders) > 0 {
			return newProviders[len(newProviders)-1], nil
		}
		return "", fmt.Errorf("no provider was configured")
	}

	return strings.ToLower(selected), nil
}

func getConfiguredBuiltinEmbeddingProviders() []string {
	var providers []string

	// Check OpenAI
	if key, err := keyring.Get(keyringService, llm.OpenaiApiKeySecretName); err == nil && key != "" {
		providers = append(providers, "openai")
	}

	// Check Google
	if key, err := keyring.Get(keyringService, llm.GoogleApiKeySecretName); err == nil && key != "" {
		providers = append(providers, "google")
	}

	return providers
}

func selectEmbeddingProvider(localConfig common.LocalConfig) (string, error) {
	// Check if embedding is already configured
	if len(localConfig.Embedding) > 0 {
		useExisting := true
		err := huh.NewConfirm().
			Title("Found existing embedding configuration. Use existing?").
			Value(&useExisting).
			Affirmative("Use existing").
			Negative("Customize").
			Run()
		if err != nil {
			return "", fmt.Errorf("error prompting for embedding configuration: %w", err)
		}
		if useExisting {
			return "", nil
		}
	}

	// Build selection list
	var options []string

	// Add configured built-in embedding providers (OpenAI and Google only)
	configuredBuiltins := getConfiguredBuiltinEmbeddingProviders()
	for _, p := range configuredBuiltins {
		options = append(options, strings.Title(p))
	}

	// Add custom providers that support embeddings (openai, google, or openai_compatible types)
	for _, provider := range localConfig.Providers {
		if provider.Type == "openai" || provider.Type == "google" || provider.Type == "openai_compatible" {
			options = append(options, provider.Name)
		}
	}

	// If no providers configured at all, show OpenAI/Google selection and run auth
	if len(options) == 0 {
		fmt.Println("No embedding providers configured. Let's set one up.")
		return selectAndAuthEmbeddingProvider()
	}

	// Add "Add new provider" option
	options = append(options, "Add new provider")

	providerSelection := selection.New("Select your embedding provider", options)
	selected, err := providerSelection.RunPrompt()
	if err != nil {
		return "", fmt.Errorf("provider selection failed: %w", err)
	}

	if selected == "Add new provider" {
		return selectAndAuthEmbeddingProvider()
	}

	return strings.ToLower(selected), nil
}

func selectAndAuthEmbeddingProvider() (string, error) {
	embeddingOptions := []string{"OpenAI", "Google"}
	providerSelection := selection.New("Select embedding provider to configure", embeddingOptions)
	selected, err := providerSelection.RunPrompt()
	if err != nil {
		return "", fmt.Errorf("provider selection failed: %w", err)
	}

	switch selected {
	case "OpenAI":
		if err := handleOpenAIAuth(); err != nil {
			return "", err
		}
		return "openai", nil
	case "Google":
		if err := handleGoogleAuth(); err != nil {
			return "", err
		}
		return "google", nil
	}

	return "", fmt.Errorf("unknown provider selected: %s", selected)
}

// checkServerStatus checks if the Sidekick server is responsive by making an HTTP GET
// request to its root path and checking for a "sidekick" keyword in the response.
func checkServerStatus() bool {
	client := &http.Client{
		Timeout: 1 * time.Second,
	}

	resp, err := client.Get(fmt.Sprintf("http://localhost:%d", common.GetServerPort()))
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}

	return strings.Contains(strings.ToLower(string(body)), "sidekick")
}

func checkLanguageSpecificTools(baseDirectory string) error {
	extensionCounts := make(map[string]int)

	err := common.WalkCodeDirectory(baseDirectory, func(path string, entry fs.DirEntry) error {
		if !entry.IsDir() {
			ext := strings.ToLower(filepath.Ext(path))
			extensionCounts[ext]++
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("error walking directory: %w", err)
	}

	if extensionCounts[".go"] > 0 {
		if err = checkGoInstallation(); err != nil {
			return err
		}
		if _, err = common.FindOrInstallGopls(); err != nil {
			return fmt.Errorf("failed to find or install gopls during initialization: %w", err)
		}
	}

	return nil
}

func checkGoInstallation() error {
	cmd := exec.Command("go", "version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Detected Go files, but Go is not installed. Please install Go from https://golang.org/dl/")
	}
	return nil
}
