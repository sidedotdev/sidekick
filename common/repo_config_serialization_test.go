package common

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/converter"
)

func TestRepoConfigLegacyTemporalPayload(t *testing.T) {
	t.Parallel()
	legacy := json.RawMessage(`{
		"CheckCommands":[{"WorkingDir":"src","Command":"check"}],
		"AutofixCommands":[{"Command":"fix"}],
		"TestCommands":[{"Command":"test"}],
		"IntegrationTestCommands":[{"Command":"integration"}],
		"Mission":"mission",
		"DisableHumanInTheLoop":true,
		"MaxIterations":19,
		"MaxPlanningIterations":7,
		"EditCode":{"Hints":"hints","HintsPath":"AGENTS.md"},
		"WorktreeSetup":"setup",
		"AgentConfig":{"coding":{"AutoIterations":3,"RemindAfter":4,"EscalateReminderEvery":2,"MaxIterations":8}},
		"CommandPermissions":{"autoApprove":[{"pattern":"go test"}]},
		"DevRun":{"server":{"command":"serve","workingDir":"web"}},
		"EnvType":"modal",
		"RepoMode":"worktree",
		"PortForwards":[{"hostPort":22855,"containerPort":32855}],
		"DevPodConfig":{"WorkspaceId":"workspace","credentialProxies":["test"]},
		"OpenShellConfig":{"PrebuildCommand":"build","From":"image","credentialProxies":["test"]},
		"ModalConfig":{"image":"image","credentialProxies":["test"]}
	}`)
	expected := RepoConfig{
		CheckCommands:           []CommandConfig{{WorkingDir: "src", Command: "check"}},
		AutofixCommands:         []CommandConfig{{Command: "fix"}},
		TestCommands:            []CommandConfig{{Command: "test"}},
		IntegrationTestCommands: []CommandConfig{{Command: "integration"}},
		Mission:                 "mission",
		DisableHumanInTheLoop:   true,
		MaxIterations:           19,
		MaxPlanningIterations:   7,
		EditCode:                EditCodeConfig{Hints: "hints", HintsPath: "AGENTS.md"},
		WorktreeSetup:           "setup",
		AgentConfig: map[string]AgentUseCaseConfig{
			"coding": {AutoIterations: 3, RemindAfter: 4, EscalateReminderEvery: 2, MaxIterations: 8},
		},
		CommandPermissions: CommandPermissionConfig{
			AutoApprove: []CommandPattern{{Pattern: "go test"}},
		},
		DevRun:       DevRunConfig{"server": {Command: "serve", WorkingDir: "web"}},
		EnvType:      "modal",
		RepoMode:     "worktree",
		PortForwards: []PortForwardConfig{{HostPort: 22855, ContainerPort: 32855}},
		DevPodConfig: DevPodEnvConfig{WorkspaceId: "workspace", CredentialProxies: []string{"test"}},
		OpenShellConfig: OpenShellEnvConfig{
			PrebuildCommand: "build", From: "image", CredentialProxies: []string{"test"},
		},
		ModalConfig: ModalEnvConfig{Image: "image", CredentialProxies: []string{"test"}},
	}
	dc := converter.GetDefaultDataConverter()
	payload, err := dc.ToPayload(legacy)
	require.NoError(t, err)
	var decoded RepoConfig
	require.NoError(t, dc.FromPayload(payload, &decoded))
	require.Equal(t, expected, decoded)

	payload, err = dc.ToPayload(decoded)
	require.NoError(t, err)
	var roundTrip RepoConfig
	require.NoError(t, dc.FromPayload(payload, &roundTrip))
	require.Equal(t, expected, roundTrip)

	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload.Data, &fields))
	for _, name := range []string{
		"checkCommands", "autofixCommands", "testCommands", "integrationTestCommands",
		"mission", "disableHumanInTheLoop", "maxIterations", "maxPlanningIterations",
		"editCode", "worktreeSetup", "agentConfig", "commandPermissions", "devRun",
		"envType", "repoMode", "portForwards", "devPodConfig", "openShellConfig", "modalConfig",
	} {
		require.Contains(t, fields, name)
	}
}
