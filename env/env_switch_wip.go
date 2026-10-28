package env

import (
	"context"
	"fmt"
)

type SaveEnvSwitchWIPInput struct {
	EnvContainer EnvContainer `json:"envContainer"`
	Branch       string       `json:"branch"`
	HeadCommit   string       `json:"headCommit"`
}

type SaveEnvSwitchWIPOutput struct{}

func SaveEnvSwitchWIPActivity(ctx context.Context, input SaveEnvSwitchWIPInput) (SaveEnvSwitchWIPOutput, error) {
	if input.EnvContainer.Env == nil {
		return SaveEnvSwitchWIPOutput{}, fmt.Errorf("cannot save environment switch WIP without an environment")
	}
	switch input.EnvContainer.Env.GetType() {
	case EnvTypeLocal, EnvTypeLocalGitWorktree, EnvTypeDevPod:
		err := backupLocalSwitchWIP(ctx, input.EnvContainer.Env, SwitchBundle{
			Branch:     input.Branch,
			HeadCommit: input.HeadCommit,
		})
		return SaveEnvSwitchWIPOutput{}, err
	default:
		return SaveEnvSwitchWIPOutput{}, fmt.Errorf("cannot save host checkout WIP from %s env", input.EnvContainer.Env.GetType())
	}
}
