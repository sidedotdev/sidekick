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
	repoDir := flag.String("repo-dir", "", "Repository directory for commit derivation (optional, uses worktree dir if not set)")
	flag.Parse()

	if *workspaceId == "" {
		log.Fatal().Msg("--workspace-id is required")
	}

	log.Info().
		Str("workspaceId", *workspaceId).
		Str("outDir", *outDir).
		Str("repoDir", *repoDir).
		Msg("Starting evaluation data extraction")

	// Open storage
	storage, err := sqlite.NewStorage()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to open storage")
	}

	// Create extractor and run extraction
	opts := evaldata.ExtractOptions{RepoDir: *repoDir}
	extractor := evaldata.NewExtractorWithOptions(storage, opts)
	ctx := context.Background()

	result, err := extractor.Extract(ctx, *workspaceId)
	if err != nil {
		log.Fatal().Err(err).Msg("Extraction failed")
	}

	log.Info().
		Int("datasetARows", len(result.DatasetA)).
		Int("datasetBRows", len(result.DatasetB)).
		Msg("Extraction complete")

	// Ensure output directory exists
	if err := os.MkdirAll(*outDir, 0755); err != nil {
		log.Fatal().Err(err).Msg("Failed to create output directory")
	}

	// Write Dataset A
	datasetAPath := filepath.Join(*outDir, "dataset_a_file_paths.unvalidated.jsonl")
	if err := evaldata.WriteDatasetAJSONL(datasetAPath, result.DatasetA); err != nil {
		log.Fatal().Err(err).Msg("Failed to write Dataset A")
	}
	log.Info().Str("path", datasetAPath).Msg("Wrote Dataset A")

	// Write Dataset B
	datasetBPath := filepath.Join(*outDir, "dataset_b_context_calls.unvalidated.jsonl")
	if err := evaldata.WriteDatasetBJSONL(datasetBPath, result.DatasetB); err != nil {
		log.Fatal().Err(err).Msg("Failed to write Dataset B")
	}
	log.Info().Str("path", datasetBPath).Msg("Wrote Dataset B")
}
