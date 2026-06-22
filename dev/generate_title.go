package dev

import (
	"context"
	"fmt"
	"strings"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/workflow"
)

type GenerateTitleInput struct {
	WorkspaceId string `json:"workspaceId"`
	TaskId      string `json:"taskId"`
	Description string `json:"description"`
}

type GenerateTitleOutput struct {
	Title string `json:"title"`
}

type GetTaskInput struct {
	WorkspaceId string `json:"workspaceId"`
	TaskId      string `json:"taskId"`
}

type UpdateTaskTitleInput struct {
	WorkspaceId string `json:"workspaceId"`
	TaskId      string `json:"taskId"`
	Title       string `json:"title"`
}

func (ima *DevAgentManagerActivities) GetTask(ctx context.Context, input GetTaskInput) (domain.Task, error) {
	return ima.Storage.GetTask(ctx, input.WorkspaceId, input.TaskId)
}

func (ima *DevAgentManagerActivities) UpdateTaskTitle(ctx context.Context, input UpdateTaskTitleInput) error {
	return ima.Storage.UpdateTaskTitle(ctx, input.WorkspaceId, input.TaskId, input.Title)
}

// GenerateTaskTitle is deprecated: new workflows should call generateTaskTitle,
// which performs the LLM call via the standard llm2 Stream activity path.
// This activity is retained for deterministic replay of workflows that recorded
// version 1 of the "generate-task-title" gate.
//
// Deprecated: use generateTaskTitle from within a workflow instead.
func (ima *DevAgentManagerActivities) GenerateTaskTitle(ctx context.Context, input GenerateTitleInput) (GenerateTitleOutput, error) {
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
				Content: llm2.TextContentBlocks(titleSystemPrompt),
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

const titleSystemPrompt = "Generate a short, descriptive title (max 10 words) for a task. Return ONLY the title text, nothing else."

// generateTaskTitle is the workflow-level replacement for the GenerateTaskTitle
// activity. It wires up a temporary ExecContext mirroring the one constructed
// by setupDevContextAction (including any config overrides supplied by the
// originating flow options) and issues an LLM call via the standard llm2
// Stream activity path, persisting the result via the UpdateTaskTitle
// activity.
func generateTaskTitle(ctx workflow.Context, input GenerateTitleInput, repoDir string, configOverrides common.ConfigOverrides) error {
	var ima *DevAgentManagerActivities

	var task domain.Task
	err := workflow.ExecuteActivity(ctx, ima.GetTask, GetTaskInput{
		WorkspaceId: input.WorkspaceId,
		TaskId:      input.TaskId,
	}).Get(ctx, &task)
	if err != nil {
		return fmt.Errorf("failed to get task: %w", err)
	}
	if task.Title != "" && task.Title != task.Description {
		return nil
	}

	eCtx, _, _, err := NewTempLocalExecContext(ctx, input.WorkspaceId, repoDir, configOverrides)
	if err != nil {
		return fmt.Errorf("failed to setup exec context: %w", err)
	}
	eCtx.FlowScope = &flow_action.FlowScope{SubflowName: "Generate Task Title"}

	modelConfig := eCtx.GetModelConfig(common.SummarizationKey, 0, "small")

	chatHistory := NewVersionedChatHistory(eCtx, eCtx.WorkspaceId)
	if err := AppendChatHistory(eCtx, chatHistory, llm.ChatMessage{
		Role:    llm.ChatMessageRoleSystem,
		Content: titleSystemPrompt,
	}); err != nil {
		return err
	}
	if err := AppendChatHistory(eCtx, chatHistory, llm.ChatMessage{
		Role:    llm.ChatMessageRoleUser,
		Content: "Task description:\n\n" + input.Description,
	}); err != nil {
		return err
	}

	actionCtx := eCtx.NewActionContext("generate.task_title")
	streamInput := persisted_ai.StreamInput{
		Options:     llm2.Options{ModelConfig: modelConfig},
		Secrets:     *eCtx.Secrets,
		ChatHistory: chatHistory,
		WorkspaceId: eCtx.WorkspaceId,
		FlowId:      workflow.GetInfo(ctx).WorkflowExecution.ID,
		Providers:   eCtx.Providers,
	}
	actionCtx.ActionParams = streamInput.ActionParams()

	response, err := flow_action.TrackWithOptions(actionCtx, flow_action.TrackOptions{FailuresOnly: true}, func(trackedCtx flow_action.ActionContext, flowAction *domain.FlowAction) (common.MessageResponse, error) {
		streamInput.FlowActionId = flowAction.Id
		return persisted_ai.ExecuteChatStream(trackedCtx, streamInput, nil)
	})
	if err != nil {
		return fmt.Errorf("LLM call failed: %w", err)
	}

	title := strings.TrimSpace(response.GetMessage().GetContentString())
	title = strings.Trim(title, "\"'`")
	if title == "" {
		return fmt.Errorf("empty title generated")
	}

	return workflow.ExecuteActivity(ctx, ima.UpdateTaskTitle, UpdateTaskTitleInput{
		WorkspaceId: input.WorkspaceId,
		TaskId:      input.TaskId,
		Title:       title,
	}).Get(ctx, nil)
}
