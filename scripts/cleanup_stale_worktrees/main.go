package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sidekick"
	"sidekick/dev"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	var workspaceId string
	var dryRun bool
	var outputPath string
	flag.StringVar(&workspaceId, "workspace", "", "Workspace ID to clean worktrees for")
	flag.BoolVar(&dryRun, "dry-run", true, "If true, do not delete anything; only report candidates")
	flag.StringVar(&outputPath, "output", "", "If set, write the JSON cleanup report to this file path")
	flag.Parse()

	if workspaceId == "" {
		log.Fatal().Msg("missing required flag: -workspace")
	}

	service, err := sidekick.GetService()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize service")
	}

	acts := &dev.DevAgentManagerActivities{Storage: service}
	report, err := acts.CleanupStaleWorktrees(context.Background(), dev.CleanupStaleWorktreesInput{
		WorkspaceId: workspaceId,
		DryRun:      dryRun,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("cleanup failed")
	}

	if outputPath != "" {
		if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
			log.Fatal().Err(err).Str("outputPath", outputPath).Msg("failed to create output directory")
		}
		b, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			log.Fatal().Err(err).Msg("failed to serialize report")
		}
		if err := os.WriteFile(outputPath, append(b, '\n'), 0644); err != nil {
			log.Fatal().Err(err).Str("outputPath", outputPath).Msg("failed to write report")
		}
		log.Info().Str("outputPath", outputPath).Msg("wrote cleanup report")
	}

	log.Info().
		Str("workspaceId", report.WorkspaceId).
		Str("baseDir", report.BaseDir).
		Bool("dryRun", report.DryRun).
		Int("candidates", len(report.Candidates)).
		Msg("cleanup complete")
}
