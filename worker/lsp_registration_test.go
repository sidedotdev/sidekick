package worker

import (
	"reflect"
	"testing"

	"sidekick/coding/lsp"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
)

type recordingActivityRegistry struct {
	worker.ActivityRegistry
	names               []string
	register            func(interface{})
	registerWithOptions func(interface{}, activity.RegisterOptions)
}

func (r *recordingActivityRegistry) RegisterActivity(value interface{}) {
	typ := reflect.TypeOf(value)
	for i := range typ.NumMethod() {
		r.names = append(r.names, typ.Method(i).Name)
	}
	r.register(value)
}

func (r *recordingActivityRegistry) RegisterActivityWithOptions(value interface{}, options activity.RegisterOptions) {
	r.names = append(r.names, options.Name)
	r.registerWithOptions(value, options)
}

func TestRegisterLSPActivitiesPreservesNamesAndExcludesLifecycle(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	environment := suite.NewTestActivityEnvironment()
	registry := &recordingActivityRegistry{
		register:            environment.RegisterActivity,
		registerWithOptions: environment.RegisterActivityWithOptions,
	}
	activities := lsp.NewLSPActivities(nil)
	t.Cleanup(func() { require.NoError(t, activities.Close()) })
	registerLSPActivities(registry, activities)

	require.ElementsMatch(t, []string{
		"AutofixActivity",
		"FindReferencesActivity",
		"GetSymbolDefinitionLocations",
		"GetSingleFileDefinitions",
		"TextDocumentDidOpenActivity",
		"TextDocumentDidChangeActivity",
		"TextDocumentDidSaveActivity",
		"TextDocumentDidCloseActivity",
		"SyncDocumentActivity",
	}, registry.names)
	_, err := environment.ExecuteActivity("SyncDocumentActivity", lsp.SyncDocumentActivityInput{})
	require.ErrorContains(t, err, "document sync requires an environment")
}
