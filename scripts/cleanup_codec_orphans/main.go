package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sidekick"
	"sidekick/common"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/segmentio/ksuid"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
)

const (
	defaultBatchSize  = 5000
	defaultOrphanFile = "codec_orphans.bin"
	codecKeyPrefix    = "codec/"
	ksuidByteLength   = 20
)

type orphanProgress struct {
	CompletedBatches int `json:"completedBatches"`
	BatchSize        int `json:"batchSize"`
	TotalKeys        int `json:"totalKeys"`
}

func progressFilePath(orphanFile string) string {
	return orphanFile + ".progress.json"
}

func readProgress(path string) (*orphanProgress, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p orphanProgress
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func writeProgress(path string, p *orphanProgress) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	var dryRun bool
	var batchSize int
	var retention time.Duration
	var orphanFile string
	flag.BoolVar(&dryRun, "dry-run", true, "If true, report orphans without deleting")
	flag.IntVar(&batchSize, "batch-size", defaultBatchSize, "Number of keys to delete per batch")
	flag.DurationVar(&retention, "retention", common.DefaultCodecCleanupRetention, "Minimum age before a key is considered orphaned")
	flag.StringVar(&orphanFile, "orphan-file", defaultOrphanFile, "File to store/resume orphan keys")
	flag.Parse()

	if batchSize <= 0 {
		log.Fatal().Int("batchSize", batchSize).Msg("batch-size must be > 0")
	}

	service, err := sidekick.GetService()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize service")
	}

	hostPort := common.GetTemporalServerHostPort()
	tracingInterceptor, err := opentelemetry.NewTracingInterceptor(opentelemetry.TracerOptions{})
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create tracing interceptor")
	}

	temporalClient, err := client.Dial(client.Options{
		HostPort:     hostPort,
		Interceptors: []interceptor.ClientInterceptor{tracingInterceptor},
	})
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to Temporal")
	}
	defer temporalClient.Close()

	activities := &common.CodecCleanupActivities{
		TemporalClient: temporalClient,
		Storage:        service,
	}

	ctx := context.Background()

	orphans, err := resolveOrphans(ctx, activities, orphanFile, retention)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to resolve orphans")
	}
	if len(orphans) == 0 {
		log.Info().Msg("No orphans to delete")
		return
	}

	log.Info().Int("orphans", len(orphans)).Bool("dryRun", dryRun).Msg("Ready to delete orphans")

	if dryRun {
		fmt.Fprintf(os.Stderr, "\nDry run complete: %d orphans found (saved to %s)\n", len(orphans), orphanFile)
		return
	}

	progFile := progressFilePath(orphanFile)
	totalBatches := (len(orphans) + batchSize - 1) / batchSize

	startBatch := 0
	if prog, err := readProgress(progFile); err != nil {
		log.Fatal().Err(err).Msg("Failed to read progress file")
	} else if prog != nil && prog.BatchSize == batchSize && prog.TotalKeys == len(orphans) {
		if prog.CompletedBatches < 0 || prog.CompletedBatches > totalBatches {
			log.Fatal().
				Int("completedBatches", prog.CompletedBatches).Int("totalBatches", totalBatches).
				Msg("Progress file has invalid completedBatches; delete the progress file to restart")
		}
		startBatch = prog.CompletedBatches
		log.Info().Int("completedBatches", startBatch).Int("totalBatches", totalBatches).Msg("Resuming from saved progress")
	} else if prog != nil {
		log.Fatal().
			Int("fileBatchSize", prog.BatchSize).Int("currentBatchSize", batchSize).
			Int("fileTotalKeys", prog.TotalKeys).Int("currentTotalKeys", len(orphans)).
			Msg("Progress file parameters don't match current run; delete the progress file to restart")
	}

	if startBatch >= totalBatches {
		log.Info().Msg("All batches already completed")
		if err := os.Remove(orphanFile); err != nil && !os.IsNotExist(err) {
			log.Fatal().Err(err).Msg("Failed to remove orphan file")
		}
		if err := os.Remove(progFile); err != nil && !os.IsNotExist(err) {
			log.Fatal().Err(err).Msg("Failed to remove progress file")
		}
		return
	}

	for batchNum := startBatch; batchNum < totalBatches; batchNum++ {
		start := batchNum * batchSize
		end := start + batchSize
		if end > len(orphans) {
			end = len(orphans)
		}
		batch := orphans[start:end]

		if err := activities.DeleteCodecKeys(ctx, batch); err != nil {
			log.Error().Err(err).
				Int("batch", batchNum+1).Int("totalBatches", totalBatches).
				Int("deletedSoFar", start).
				Msg("Failed to delete batch, stopping")
			os.Exit(1)
		}

		if err := writeProgress(progFile, &orphanProgress{
			CompletedBatches: batchNum + 1,
			BatchSize:        batchSize,
			TotalKeys:        len(orphans),
		}); err != nil {
			log.Fatal().Err(err).Msg("Failed to write progress file")
		}

		remaining := len(orphans) - end
		log.Info().
			Int("batch", batchNum+1).Int("totalBatches", totalBatches).
			Int("batchSize", len(batch)).Int("remaining", remaining).
			Msg("Batch deleted")
	}

	if err := os.Remove(orphanFile); err != nil && !os.IsNotExist(err) {
		log.Fatal().Err(err).Msg("Failed to remove orphan file")
	}
	if err := os.Remove(progFile); err != nil && !os.IsNotExist(err) {
		log.Fatal().Err(err).Msg("Failed to remove progress file")
	}

	deletedThisRun := len(orphans) - startBatch*batchSize
	fmt.Fprintf(os.Stderr, "\nOrphan cleanup complete: %d keys deleted (%d total orphans)\n", deletedThisRun, len(orphans))
}

// resolveOrphans returns the list of orphan keys to delete, either from a
// previously saved file or by computing them fresh.
func resolveOrphans(ctx context.Context, activities *common.CodecCleanupActivities, orphanFile string, retention time.Duration) ([]string, error) {
	existing, err := readOrphanFile(orphanFile)
	if err != nil {
		return nil, fmt.Errorf("reading orphan file: %w", err)
	}

	if len(existing) > 0 {
		log.Info().Int("count", len(existing)).Str("file", orphanFile).Msg("Found previously saved orphan keys")
		usePrevious := true
		if err := huh.NewConfirm().
			Title(fmt.Sprintf("Found %d previously identified orphans in %s", len(existing), orphanFile)).
			Description("Use these, or recalculate fresh? (Recalculate if workflows were reset since last run.)").
			Value(&usePrevious).
			Affirmative("Use previous").
			Negative("Recalculate fresh").
			Run(); err != nil {
			return nil, fmt.Errorf("prompt failed: %w", err)
		}
		if usePrevious {
			return existing, nil
		}
		log.Info().Msg("Recalculating orphans fresh...")
	}

	if err := os.Remove(progressFilePath(orphanFile)); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("removing stale progress file: %w", err)
	}

	orphans, err := discoverOrphans(ctx, activities, retention)
	if err != nil {
		return nil, err
	}

	if len(orphans) > 0 {
		if err := writeOrphanFile(orphanFile, orphans); err != nil {
			return nil, fmt.Errorf("writing orphan file: %w", err)
		}
		log.Info().Int("count", len(orphans)).Str("file", orphanFile).Msg("Saved orphan keys to file")
	}

	return orphans, nil
}

func discoverOrphans(ctx context.Context, activities *common.CodecCleanupActivities, retention time.Duration) ([]string, error) {
	log.Info().Msg("Listing all codec keys...")
	allKeys, err := activities.ListAllCodecKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing codec keys: %w", err)
	}
	log.Info().Int("count", len(allKeys)).Msg("Found codec keys")
	if len(allKeys) == 0 {
		return nil, nil
	}

	now := time.Now()
	candidates := common.GetCandidateOrphanCodecKeys(allKeys, nil, now, retention)
	log.Info().Int("candidates", len(candidates)).Int("total", len(allKeys)).Msg("Pre-filtered to old-enough candidate keys")
	if len(candidates) == 0 {
		return nil, nil
	}

	candidateSet := make(map[string]struct{}, len(candidates))
	for _, k := range candidates {
		candidateSet[k] = struct{}{}
	}

	referenced := make(map[string]struct{})

	log.Info().Msg("Collecting keys from running workflows...")
	runningKeys, err := activities.ListRunningWorkflowCodecKeys(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to collect running workflow keys")
	}
	for _, k := range runningKeys {
		if _, ok := candidateSet[k]; ok {
			referenced[k] = struct{}{}
		}
	}
	log.Info().Int("runningKeys", len(runningKeys)).Int("referencedSoFar", len(referenced)).Msg("Collected running workflow keys")

	log.Info().Msg("Listing closed workflows...")
	closedWorkflows, err := activities.ListClosedWorkflowIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing closed workflows: %w", err)
	}
	log.Info().Int("count", len(closedWorkflows)).Msg("Found closed workflows, scanning histories...")

	for i, wf := range closedWorkflows {
		if len(referenced) == len(candidateSet) {
			log.Info().Msg("All candidates are referenced, no orphans")
			break
		}
		keys, err := activities.CollectCodecKeysFromHistory(ctx, wf.WorkflowID, wf.RunID)
		if err != nil {
			log.Warn().Err(err).Str("workflowId", wf.WorkflowID).Msg("Failed to collect keys from workflow")
			continue
		}
		for _, k := range keys {
			if _, ok := candidateSet[k]; ok {
				referenced[k] = struct{}{}
			}
		}
		if (i+1)%500 == 0 {
			log.Info().
				Int("workflowsScanned", i+1).Int("totalWorkflows", len(closedWorkflows)).
				Int("referenced", len(referenced)).Int("candidates", len(candidates)).
				Msg("Scanning progress")
		}
	}

	var orphans []string
	for _, key := range candidates {
		if _, ok := referenced[key]; !ok {
			orphans = append(orphans, key)
		}
	}

	log.Info().Int("orphans", len(orphans)).Int("candidates", len(candidates)).Msg("Orphan discovery complete")
	return orphans, nil
}

// Binary format: [uint32 count][20-byte KSUID][20-byte KSUID]...
// Keys are stored without the "codec/" prefix since it is constant.

func readOrphanFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var count uint32
	if err := binary.Read(f, binary.BigEndian, &count); err != nil {
		if err == io.EOF {
			return nil, nil
		}
		return nil, fmt.Errorf("reading count: %w", err)
	}

	keys := make([]string, 0, count)
	buf := make([]byte, ksuidByteLength)
	for i := uint32(0); i < count; i++ {
		if _, err := io.ReadFull(f, buf); err != nil {
			return nil, fmt.Errorf("reading KSUID %d: %w", i, err)
		}
		id, err := ksuid.FromBytes(buf)
		if err != nil {
			return nil, fmt.Errorf("parsing KSUID %d: %w", i, err)
		}
		keys = append(keys, codecKeyPrefix+id.String())
	}
	return keys, nil
}

func writeOrphanFile(path string, keys []string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := binary.Write(f, binary.BigEndian, uint32(len(keys))); err != nil {
		return fmt.Errorf("writing count: %w", err)
	}
	for i, key := range keys {
		id, err := ksuid.Parse(key[len(codecKeyPrefix):])
		if err != nil {
			return fmt.Errorf("parsing key %d: %w", i, err)
		}
		if _, err := f.Write(id.Bytes()); err != nil {
			return fmt.Errorf("writing KSUID %d: %w", i, err)
		}
	}
	return nil
}
