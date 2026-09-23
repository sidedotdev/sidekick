package dev

import (
	"go.temporal.io/sdk/testsuite"

	"sidekick/coding/git"
	"sidekick/env"
)

// Nil receivers for referencing activity methods in OnActivity mocks, matching
// how workflows schedule them.
var (
	ea *env.Activities
	ga *git.Activities
)

func testGitActivities() *git.Activities {
	return &git.Activities{Env: &env.Activities{}}
}

// registerRunCommandActivity registers a real command runner under the wire
// name workflows schedule it by.
func registerRunCommandActivity(testEnv *testsuite.TestWorkflowEnvironment) {
	testEnv.RegisterActivity((&env.Activities{}).EnvRunCommandActivity)
}
