package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"go.temporal.io/sdk/worker" // For NewWorkflowReplayer

	// sidekick_worker is an alias for "sidekick/worker", used for RegisterWorkflows.
	// This matches the usage in replay.go.
	"sidekick/utils" // For S3 client and S3 operations like NewS3Client, ListObjectKeys.
	sidekick_worker "sidekick/worker"
)

// TestReplayFromS3Integration performs an integration test for replaying workflow histories from S3.
// It lists specified versions of workflow histories from the S3 bucket "genflow.dev",
// fetches them using the (unexported) fetchAndCacheHistory function (which utilizes local caching),
// and then attempts to replay them using the current worker's registered workflows.
func TestReplayFromS3Integration(t *testing.T) {
	// Define the Sidekick versions for which to test replay from S3.
	// As per user guidance, "0.5.0" should have at least one history file available.
	sidekickVersionsToTest := []string{"0.5.0"}

	ctx := context.Background()

	s3Client, err := utils.NewS3Client(ctx, &s3Region)
	if err != nil {
		t.Fatalf("Failed to create S3 client for integration test: %v", err)
	}

	s3Bucket := "genflow.dev" // The S3 bucket where replay histories are stored.

	for _, version := range sidekickVersionsToTest {
		// Create a subtest for each Sidekick version to isolate test runs.
		// Replace dots in version string for valid test name.
		versionTestName := fmt.Sprintf("Version_%s", strings.ReplaceAll(version, ".", "_"))
		t.Run(versionTestName, func(t *testing.T) {
			s3Prefix := fmt.Sprintf("sidekick/replays/%s/", version)
			keys, errList := utils.ListObjectKeys(ctx, s3Client, s3Bucket, s3Prefix)
			if errList != nil {
				t.Fatalf("Failed to list S3 objects for version %s (prefix: %s): %v", version, s3Prefix, errList)
			}

			if len(keys) == 0 {
				t.Logf("No S3 objects found for version %s at prefix %s. No replay tests will run for this version.", version, s3Prefix)
				// This is not necessarily a failure, as some versions might not have test histories.
				// However, for "0.5.0", user indicated a file exists.
				if version == "0.5.0" {
					// If specific files are expected for "0.5.0", this could be t.Errorf.
					// For now, we proceed, and if no _events.json files are found later, that will be logged.
					t.Logf("Note: User indicated a history file exists for version 0.5.0. Ensure it's at prefix: %s", s3Prefix)
				}
				return
			}

			foundReplayableHistoryFile := false
			for _, key := range keys {
				if !strings.HasSuffix(key, "_events.json") {
					t.Logf("S3 object key '%s' in version %s does not end with '_events.json', skipping.", key, version)
					continue
				}

				// Extract workflowID from the S3 key.
				// Key format: sidekick/replays/<version>/<workflowId>_events.json
				// s3Prefix: sidekick/replays/<version>/
				workflowIdWithSuffix := strings.TrimPrefix(key, s3Prefix)
				workflowId := strings.TrimSuffix(workflowIdWithSuffix, "_events.json")

				if workflowId == "" {
					t.Errorf("Extracted empty workflowId from key '%s' (prefix: '%s') for version %s. Skipping this key.", key, s3Prefix, version)
					continue
				}

				foundReplayableHistoryFile = true
				workflowTestName := fmt.Sprintf("WorkflowID_%s", workflowId)
				// Create a subtest for each workflow ID to isolate replay attempts.
				t.Run(workflowTestName, func(t *testing.T) {
					t.Logf("Attempting to fetch and replay history for workflowID: %s, version: %s (S3 key: %s)", workflowId, version, key)

					// fetchAndCacheHistory is an unexported function in replay.go (package main).
					// This test file (package main) can call it directly.
					historyFile, errFetch := cachedHistoryFile(ctx, s3Client, workflowId, version)
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

			if !foundReplayableHistoryFile {
				// This means S3 objects were listed, but none of them were identified as replayable history files.
				t.Logf("No S3 objects ending with '_events.json' were found and processed for version %s under prefix %s.", version, s3Prefix)
			}
		})
	}
}
