package dev

import (
	"context"
	"fmt"
	"strings"

	"sidekick/common"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"

	"github.com/rs/zerolog/log"
)

type GenerateTitleInput struct {
	WorkspaceId string `json:"workspaceId"`
	TaskId      string `json:"taskId"`
	Description string `json:"description"`
}

type GenerateTitleOutput struct {
	Title string `json:"title"`
}

func (ima *DevAgentManagerActivities) GenerateTaskTitle(ctx context.Context, input GenerateTitleInput) (GenerateTitleOutput, error) {
	// Skip LLM call if the task already has a manually-provided title
	task, err := ima.Storage.GetTask(ctx, input.WorkspaceId, input.TaskId)
	if err != nil {
		return GenerateTitleOutput{}, fmt.Errorf("failed to get task: %w", err)
	}
	if task.Title != "" && task.Title != task.Description {
		return GenerateTitleOutput{Title: task.Title}, nil
	}

	workspaceConfig, err := ima.Storage.GetWorkspaceConfig(ctx, input.WorkspaceId)
	if err != nil {
		return GenerateTitleOutput{}, fmt.Errorf("failed to get workspace config: %w", err)
	}

	if len(workspaceConfig.LLM.Defaults) == 0 {
		return GenerateTitleOutput{}, fmt.Errorf("no default LLM configured")
	}

	modelConfig := workspaceConfig.LLM.Defaults[0]

	localConfig, _ := common.LoadSidekickConfig(common.GetSidekickConfigPath())
	providers := make([]common.ModelProviderPublicConfig, len(localConfig.Providers))
	for i, p := range localConfig.Providers {
		providers[i] = common.ModelProviderPublicConfig{
			Name:          p.Name,
			Type:          p.Type,
			BaseURL:       p.BaseURL,
			DefaultLLM:    p.DefaultLLM,
			SmallLLM:      p.SmallLLM,
			AuthType:      p.AuthType,
			CustomHeaders: p.CustomHeaders,
		}
	}

	// Use the configured small model for the workspace default provider
	for _, p := range providers {
		if p.Name == modelConfig.Provider && p.SmallLLM != "" {
			modelConfig.Model = p.SmallLLM
			break
		}
	}

	provider, err := persisted_ai.GetLlm2Provider(modelConfig, providers)
	if err != nil {
		return GenerateTitleOutput{}, fmt.Errorf("failed to create LLM provider: %w", err)
	}

	secrets := secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
		&secret_manager.LocalConfigSecretManager{},
		&secret_manager.EnvSecretManager{},
	})

	eventChan := make(chan llm2.Event, 10)
	go func() {
		for range eventChan {
		}
	}()

	response, err := provider.Stream(ctx, llm2.StreamRequest{
		Messages: []llm2.Message{
			{
				Role:    llm2.RoleSystem,
				Content: llm2.TextContentBlocks("Generate a short, descriptive title (max 10 words) for a task. Return ONLY the title text, nothing else."),
			},
			{
				Role:    llm2.RoleUser,
				Content: llm2.TextContentBlocks("Task description:\n\n" + input.Description),
			},
		},
		Options:       llm2.Options{ModelConfig: modelConfig},
		SecretManager: secrets,
	}, eventChan)
	if err != nil {
		return GenerateTitleOutput{}, fmt.Errorf("LLM call failed: %w", err)
	}

	title := strings.TrimSpace(response.Output.GetContentString())
	title = strings.Trim(title, "\"'`")
	if title == "" {
		return GenerateTitleOutput{}, fmt.Errorf("empty title generated")
	}

	if err := ima.Storage.UpdateTaskTitle(ctx, input.WorkspaceId, input.TaskId, title); err != nil {
		log.Error().Err(err).Str("taskId", input.TaskId).Msg("failed to persist generated title")
		return GenerateTitleOutput{Title: title}, fmt.Errorf("failed to update task title: %w", err)
	}

	return GenerateTitleOutput{Title: title}, nil
}
