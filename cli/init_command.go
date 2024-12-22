package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sidekick/coding/tree_sitter"
	"sidekick/common"
	"sidekick/db"
	"sidekick/llm"
	"sidekick/models"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/erikgeiser/promptkit/selection"
	"github.com/erikgeiser/promptkit/textinput"
	"github.com/segmentio/ksuid"
	"github.com/zalando/go-keyring"
)

type InitCommandHandler struct {
	dbAccessor db.DatabaseAccessor
}

func NewInitCommandHandler(dbAccessor db.DatabaseAccessor) *InitCommandHandler {
	return &InitCommandHandler{
		dbAccessor: dbAccessor,
	}
}

func (h *InitCommandHandler) handleInitCommand() error {
	fmt.Println("Starting initialization...")

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

	config, configCheck, err := checkConfig(baseDir)
	if err != nil {
		return fmt.Errorf("error during config check: %w", err)
	}

	if !configCheck.hasTestCommands {
		err = ensureTestCommands(&config, configCheck.filePath)
		if err != nil {
			return fmt.Errorf("error prompting for test command: %w", err)
		}
		fmt.Println("✔ Your test command has been saved in side.toml.")
	} else {
		fmt.Println("✔ Found valid test commands in side.toml.")
	}

	workspaceName, err := getRepoName(baseDir)
	if err != nil {
		workspaceName = filepath.Base(baseDir)
	}

	ctx := context.Background()

	// check if redis is running
	err = h.dbAccessor.CheckConnection(ctx)
	if err != nil {
		return fmt.Errorf("Redis isn't running, please install and run it: https://redis.io/docs/install/install-redis/")
	}

	workspace, err := h.findOrCreateWorkspace(ctx, workspaceName, baseDir)
	if err != nil {
		return fmt.Errorf("error finding or creating workspace: %w", err)
	}
	fmt.Printf("✔ Workspace found or created successfully: %v\n", workspace.Id)

	embeddingProviders, err := ensureEmbeddingSecrets()
	if err != nil {
		return fmt.Errorf("error checking or prompting for embedding secrets: %w", err)
	}

	llmProviders, err := ensureAISecrets()
	if err != nil {
		return fmt.Errorf("error checking or prompting for AI secrets: %w", err)
	}

	existingConfig, err := h.dbAccessor.GetWorkspaceConfig(ctx, workspace.Id)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return fmt.Errorf("error retrieving workspace configuration: %w", err)
	}

	err = h.ensureWorkspaceConfig(ctx, workspace.Id, &existingConfig, llmProviders, embeddingProviders)
	if err != nil {
		return fmt.Errorf("error ensuring workspace configuration: %w", err)
	}
	fmt.Println("✔ Workspace configuration has been set up.")

	if checkServerStatus() {
		fmt.Printf("✔ Sidekick server is running. Go to http://localhost:%d\n", common.GetServerPort())
	} else {
		fmt.Println("ℹ Sidekick server is not running. Please run 'side start' to start the server.")
	}

	return nil
}

func (h *InitCommandHandler) ensureWorkspaceConfig(ctx context.Context, workspaceID string, currentConfig *models.WorkspaceConfig, llmProviders, embeddingProviders []string) error {
	if currentConfig == nil {
		currentConfig = &models.WorkspaceConfig{}
	}

	// Set up LLM configuration
	currentConfig.LLM.Defaults = []common.ModelConfig{}
	for _, provider := range llmProviders {
		modelConfig := common.ModelConfig{
			Provider: provider,
		}
		currentConfig.LLM.Defaults = append(currentConfig.LLM.Defaults, modelConfig)
	}

	// Set up Embedding configuration
	currentConfig.Embedding.Defaults = []common.ModelConfig{}
	for _, provider := range embeddingProviders {
		modelConfig := common.ModelConfig{
			Provider: provider,
		}
		currentConfig.Embedding.Defaults = append(currentConfig.Embedding.Defaults, modelConfig)
	}

	// Persist the updated configuration
	err := h.dbAccessor.PersistWorkspaceConfig(ctx, workspaceID, *currentConfig)
	if err != nil {
		return fmt.Errorf("failed to persist workspace config: %w", err)
	}

	return nil
}

// FIXME make a call to an API instead of directly using a database. this may
// require running the server locally as a daemon if not already running or
// configured to be remote
func (h *InitCommandHandler) findOrCreateWorkspace(ctx context.Context, workspaceName, repoDir string) (*models.Workspace, error) {
	workspaces, err := h.dbAccessor.GetAllWorkspaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("error retrieving workspaces: %w", err)
	}

	for _, workspace := range workspaces {
		if workspace.LocalRepoDir == repoDir {
			return &workspace, nil
		}
	}

	workspace := models.Workspace{
		Name:         workspaceName,
		Id:           "ws_" + ksuid.New().String(),
		LocalRepoDir: repoDir,
		Created:      time.Now(),
		Updated:      time.Now(),
	}

	if err := h.dbAccessor.PersistWorkspace(ctx, workspace); err != nil {
		return nil, fmt.Errorf("error persisting workspace: %w", err)
	}

	return &workspace, nil
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

func checkConfig(baseDir string) (common.RepoConfig, configCheckResult, error) {
	var config common.RepoConfig
	var result configCheckResult
	result.filePath = filepath.Join(baseDir, "side.toml")

	_, err := os.Stat(result.filePath)
	fileExists := !os.IsNotExist(err)
	if !fileExists {
		return config, result, nil
	}

	if fileExists {
		_, err := toml.DecodeFile(result.filePath, &config)
		if err != nil {
			return config, result, fmt.Errorf("error decoding config file: %w", err)
		}
		if len(config.TestCommands) > 0 {
			result.hasTestCommands = true
		}
	}

	return config, result, nil
}

func saveConfig(filePath string, config common.RepoConfig) error {
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("error creating config file: %w", err)
	}
	defer file.Close()

	if err := toml.NewEncoder(file).Encode(config); err != nil {
		return fmt.Errorf("error writing to config file: %w", err)
	}

	return nil
}

func ensureTestCommands(config *common.RepoConfig, filePath string) error {
	fmt.Println("\nPlease enter the command you use to run your tests.")
	fmt.Println("Examples:")
	fmt.Println("- If you are using JavaScript, you might use: jest")
	fmt.Println("- If you are using Python, you might use: pytest")
	fmt.Println("- If you are using another tool, please specify its command.")
	fmt.Print("Enter your test command: ")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	testCommand := scanner.Text()

	if testCommand == "" {
		return fmt.Errorf("No command entered. Exiting.")
	}

	config.TestCommands = []common.CommandConfig{
		{Command: testCommand},
	}

	if err := saveConfig(filePath, *config); err != nil {
		return err
	}

	return nil
}

func ensureAISecrets() ([]string, error) {
	service := "sidekick"

	openaiKey, openaiErr := keyring.Get(service, llm.OpenaiApiKeySecretName)
	anthropicKey, anthropicErr := keyring.Get(service, llm.AnthropicApiKeySecretName)
	var providers []string

	if anthropicErr == nil && anthropicKey != "" {
		// NOTE: anthropic is preferred when available as the primary default
		// provider, so is appended first
		providers = append(providers, "Anthropic")
		fmt.Println("✔ Found existing ANTHROPIC_API_KEY in keyring.")
	}

	if openaiErr == nil && openaiKey != "" {
		providers = append(providers, "OpenAI")
		fmt.Println("✔ Found existing OPENAI_API_KEY in keyring.")
	}

	if openaiErr == keyring.ErrNotFound && anthropicErr == keyring.ErrNotFound {
		providerSelection := selection.New("Select your LLM API provider", []string{"OpenAI", "Anthropic"})
		provider, err := providerSelection.RunPrompt()
		if err != nil {
			return nil, fmt.Errorf("provider selection failed: %w", err)
		}

		secretName := ""
		switch provider {
		case "OpenAI":
			secretName = llm.OpenaiApiKeySecretName
		case "Anthropic":
			secretName = llm.AnthropicApiKeySecretName
		default:
			return nil, fmt.Errorf("invalid selection: %s", provider)
		}

		apiKeyInput := textinput.New(fmt.Sprintf("Enter your %s API Key: ", provider))
		apiKeyInput.Hidden = true

		apiKey, err := apiKeyInput.RunPrompt()
		if err != nil {
			return nil, fmt.Errorf("failed to get %s API Key: %w", provider, err)
		}

		if apiKey != "" {
			err := keyring.Set(service, secretName, apiKey)
			if err != nil {
				return nil, fmt.Errorf("error storing API key in keyring: %w", err)
			}
			fmt.Printf("%s saved to keyring.\n", secretName)
		} else {
			return nil, fmt.Errorf("%s API Key not provided. Exiting.", provider)
		}
	}

	return providers, nil
}

func ensureEmbeddingSecrets() ([]string, error) {
	service := "sidekick"
	var providers []string

	openaiKey, err := keyring.Get(service, llm.OpenaiApiKeySecretName)
	if err == nil && openaiKey != "" {
		providers = append(providers, "OpenAI")
		fmt.Println("✔ Found existing OPENAI_API_KEY in keyring for embeddings.")
		return providers, nil
	}

	if err != keyring.ErrNotFound {
		return nil, fmt.Errorf("error retrieving OpenAI API key from keyring: %w", err)
	}

	apiKeyInput := textinput.New("Enter your OpenAI API Key (required for embeddings): ")
	apiKeyInput.Hidden = true

	apiKey, err := apiKeyInput.RunPrompt()
	if err != nil {
		return nil, fmt.Errorf("failed to get OpenAI API Key: %w", err)
	}

	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI API Key not provided. Exiting.")
	}

	err = keyring.Set(service, llm.OpenaiApiKeySecretName, apiKey)
	if err != nil {
		return nil, fmt.Errorf("error storing OpenAI API key in keyring: %w", err)
	}
	fmt.Println("OpenAI API Key saved to keyring.")

	providers = append(providers, "OpenAI")
	return providers, nil
}

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

	err := tree_sitter.WalkCodeDirectory(baseDirectory, func(path string, entry fs.DirEntry) error {
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
		} else if err = checkGoplsInstallation(); err != nil {
			return err
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

func checkGoplsInstallation() error {
	cmd := exec.Command("gopls", "version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Detected Go files, but gopls is not installed. To install, run: go install golang.org/x/tools/gopls@latest")
	}
	return nil
}
