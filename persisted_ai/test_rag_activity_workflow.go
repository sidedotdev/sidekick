package persisted_ai

import (
	"context"
	"sidekick/env"
	"sidekick/secret_manager"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func TestOpenAiEmbedActivityWorkflow(ctx workflow.Context) (string, error) {
	retrypolicy := &temporal.RetryPolicy{
		InitialInterval:        time.Second,
		BackoffCoefficient:     2.0,
		MaximumInterval:        100 * time.Second,
		MaximumAttempts:        100,
		NonRetryableErrorTypes: []string{"SomeApplicationError", "AnotherApplicationError"},
	}

	activityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         retrypolicy,
	}

	ctx = workflow.WithActivityOptions(ctx, activityOptions)

	workspaceId := "TODO"

	devEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{
		RepoDir: "/Users/ehsanhoque/sidekick",
	})
	if err != nil {
		return "", err
	}

	//ra := RagActivities{DatabaseAccessor: newTestRedisDatabase(), Embedder: embedding.OpenAIEmbedder{}}
	var ra *RagActivities
	var rankedOutline string

	err = workflow.ExecuteActivity(ctx, ra.RankedDirSignatureOutline, RankedDirSignatureOutlineOptions{
		CharLimit: 15000,
		RankedViaEmbeddingOptions: RankedViaEmbeddingOptions{
			WorkspaceId:   workspaceId,
			EnvContainer:  env.EnvContainer{Env: devEnv},
			EmbeddingType: "ada2",
			RankQuery:     "Add a component for a kanban board that allows you to add a card to a column.",
			Secrets:       secret_manager.SecretManagerContainer{SecretManager: secret_manager.EnvSecretManager{}},
		},
	}).Get(ctx, &rankedOutline)

	if err != nil {
		return "", err
	}
	return rankedOutline, nil
}

/*
keys := []string{
    "test_workspace:test_type:1:ada_embedding",
    // Add more keys as needed
}

// Fetch multiple values using MGet
values, err := newTestRedisDatabase().Client.MGet(context.Background(), keys...).Result()
if err != nil {
    return "", err
}

for _, value := range values {
    // Check if the value is nil (key might not exist)
    if value == nil {
        continue
    }

    // Assert the value to string and convert it to []byte
    stringValue, ok := value.(string)
    if !ok {
        return "", fmt.Errorf("value is not a string type")
    }
    byteValue := []byte(stringValue)

    // Unmarshal the byteValue into EmbeddingVector
    var ev EmbeddingVector
    if err := ev.UnmarshalBinary(byteValue); err != nil {
        return "", err
    }

    // Process the EmbeddingVector as needed
    // For example, print it out
    fmt.Printf("OMG unmarshaled ev: %v\n", ev)
}
*/
