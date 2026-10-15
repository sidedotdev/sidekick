package env

import (
	"sort"

	"sidekick/sideagent"
)

func (e *DevPodEnv) commandRequest(input EnvRunCommandInput) sideagent.ExecRequest {
	keys := make([]string, 0, len(e.EnvVars))
	for key := range e.EnvVars {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	envVars := make([]string, 0, len(keys)+len(input.EnvVars))
	for _, key := range keys {
		envVars = append(envVars, key+"="+e.EnvVars[key])
	}
	input.EnvVars = append(envVars, input.EnvVars...)
	return agentExecRequest(e.WorkingDirectory, e.GetType(), e.PortForwards, input)
}
