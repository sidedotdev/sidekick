package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"

	"sidekick/evaldata"
	"sidekick/srv/sqlite"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	workspaceId := flag.String("workspace-id", "", "Workspace ID to extract data from (required)")
	outDir := flag.String("out-dir", ".", "Output directory for dataset files")
	repoDir := flag.String("repo-dir", "", "Repository directory for commit derivation (optional, uses workspace's LocalRepoDir if not set)")
	fullExtract := flag.Bool("full", false, "Force full extraction, ignoring existing data")
	flag.Parse()

	if *workspaceId == "" {
		log.Fatal().Msg("--workspace-id is required")
	}

	log.Info().
		Str("workspaceId", *workspaceId).
		Str("outDir", *outDir).
		Str("repoDir", *repoDir).
		Bool("fullExtract", *fullExtract).
		Msg("Starting evaluation data extraction")

	// Ensure output directory exists
	if err := os.MkdirAll(*outDir, 0755); err != nil {
		log.Fatal().Err(err).Msg("Failed to create output directory")
	}

	datasetAPath := filepath.Join(*outDir, "dataset_a_file_paths.unvalidated.jsonl")
	datasetBPath := filepath.Join(*outDir, "dataset_b_line_ranges.unvalidated.jsonl")

	// Load existing case IDs for incremental extraction
	var existingCaseIds map[string]bool
	var existingRowCount int
	if !*fullExtract {
		if existingRows, err := evaldata.ReadDatasetAJSONL(datasetAPath); err == nil {
			existingCaseIds = evaldata.ExtractCaseIds(existingRows)
			existingRowCount = len(existingRows)
			log.Info().
				Int("existingCases", len(existingCaseIds)).
				Msg("Found existing dataset, will extract incrementally")
		}
	}

	// Open storage
	storage, err := sqlite.NewStorage()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to open storage")
	}

	// Create extractor and run extraction
	opts := evaldata.ExtractOptions{
		RepoDir:         *repoDir,
		ExistingCaseIds: existingCaseIds,
	}
	extractor := evaldata.NewExtractorWithOptions(storage, opts)
	ctx := context.Background()

	result, err := extractor.Extract(ctx, *workspaceId)
	if err != nil {
		log.Fatal().Err(err).Msg("Extraction failed")
	}

	log.Info().
		Int("newDatasetARows", len(result.DatasetA)).
		Int("newDatasetBRows", len(result.DatasetB)).
		Int("existingRows", existingRowCount).
		Msg("Extraction complete")

	if len(result.DatasetA) == 0 {
		log.Info().Msg("No new cases to extract")
		return
	}

	// Write or append datasets
	if existingCaseIds == nil || *fullExtract {
		// Full write
		if err := evaldata.WriteDatasetAJSONL(datasetAPath, result.DatasetA); err != nil {
			log.Fatal().Err(err).Msg("Failed to write Dataset A")
		}
		if err := evaldata.WriteDatasetBJSONL(datasetBPath, result.DatasetB); err != nil {
			log.Fatal().Err(err).Msg("Failed to write Dataset B")
		}
		log.Info().
			Str("datasetA", datasetAPath).
			Str("datasetB", datasetBPath).
			Msg("Wrote datasets")
	} else {
		// Incremental append
		if err := evaldata.AppendDatasetAJSONL(datasetAPath, result.DatasetA); err != nil {
			log.Fatal().Err(err).Msg("Failed to append to Dataset A")
		}
		if err := evaldata.AppendDatasetBJSONL(datasetBPath, result.DatasetB); err != nil {
			log.Fatal().Err(err).Msg("Failed to append to Dataset B")
		}
		log.Info().
			Str("datasetA", datasetAPath).
			Str("datasetB", datasetBPath).
			Int("appendedRows", len(result.DatasetA)).
			Msg("Appended to existing datasets")
	}
}
