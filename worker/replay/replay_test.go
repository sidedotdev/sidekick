package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"go.temporal.io/sdk/worker"
	sidekick_worker "sidekick/worker"
)

// replayTestData represents the mapping of versions to workflow IDs for replay testing
type replayTestData map[string][]string

// TestReplayFromS3Integration performs an integration test for replaying workflow histories from S3.
// It lists specified versions of workflow histories from the S3 bucket "genflow.dev",
// fetches them using the (unexported) fetchAndCacheHistory function (which utilizes local caching),
// and then attempts to replay them using the current worker's registered workflows.
func TestReplayFromS3Integration(t *testing.T) {
	t.Parallel()
	// Define the Sidekick versions for which to test replay from S3.
	// As per user guidance, "0.5.0" should have at least one history file available.
	sidekickVersionsToTest := []string{"0.5.0"}

	ctx := context.Background()

	// Read test data file
	testDataBytes, err := os.ReadFile("replay_test_data.json")
	if err != nil {
		t.Fatalf("Failed to read replay test data file: %v", err)
	}

	var testData replayTestData
	if err := json.Unmarshal(testDataBytes, &testData); err != nil {
		t.Fatalf("Failed to parse replay test data: %v", err)
	}

	for _, version := range sidekickVersionsToTest {
		// Create a subtest for each Sidekick version to isolate test runs.
		// Replace dots in version string for valid test name.
		versionTestName := fmt.Sprintf("Version_%s", strings.ReplaceAll(version, ".", "_"))
		t.Run(versionTestName, func(t *testing.T) {
			t.Parallel()
			workflowIds, exists := testData[version]
			if !exists {
				t.Logf("No workflow IDs found in test data for version %s", version)
				if version == "0.5.0" {
					t.Errorf("Expected workflow IDs for version 0.5.0 in test data file")
				}
				return
			}

			if len(workflowIds) == 0 {
				t.Logf("Empty workflow ID list in test data for version %s", version)
				if version == "0.5.0" {
					t.Errorf("Expected non-empty workflow ID list for version 0.5.0 in test data file")
				}
				return
			}

			for _, workflowId := range workflowIds {

				workflowTestName := fmt.Sprintf("WorkflowID_%s", workflowId)
				// Create a subtest for each workflow ID to isolate replay attempts.
				t.Run(workflowTestName, func(t *testing.T) {
					t.Logf("Attempting to fetch and replay history for workflowID: %s, version: %s", workflowId, version)

					// cachedHistoryFile is an unexported function in replay.go (package main).
					// This test file (package main) can call it directly.
					historyFile, errFetch := cachedHistoryFile(ctx, s3Region, workflowId, version)
					if errFetch != nil {
						t.Fatalf("fetchAndCacheHistory failed for workflowID %s, version %s: %v", workflowId, version, errFetch)
					}

					replayer := worker.NewWorkflowReplayer()
					sidekick_worker.RegisterWorkflows(replayer) // Register current workflows.

					errReplay := replayer.ReplayWorkflowHistoryFromJSONFile(nil, historyFile) // Logger can be nil for replayer.
					if errReplay != nil {
						t.Errorf("ReplayWorkflowHistory failed for workflowID %s, version %s: %v", workflowId, version, errReplay)
					} else {
						t.Logf("Successfully replayed workflow history for workflowID: %s, version: %s", workflowId, version)
					}
				})
			}
		})
	}
}
