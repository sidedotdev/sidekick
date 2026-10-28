package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/rs/zerolog/log"
)

func main() {
	cfg := probeConfig{}
	flag.IntVar(&cfg.Messages, "messages", 12000, "Measured messages per case")
	flag.IntVar(&cfg.Warmup, "warmup", 256, "Unmeasured warmup messages")
	flag.IntVar(&cfg.BodyBytes, "body-bytes", 4096, "Synthetic JSON body size")
	flag.DurationVar(&cfg.Interval, "interval", time.Millisecond, "Steady traffic publish interval (closed loop)")
	flag.IntVar(&cfg.Workers, "workers", 32, "Concurrent publishers during bursts")
	flag.IntVar(&cfg.BurstSize, "burst-size", 256, "Messages per burst")
	flag.DurationVar(&cfg.BurstPause, "burst-pause", 20*time.Millisecond, "Pause between bursts")
	runs := flag.Int("runs", 3, "Independent repetitions, alternating compression order")
	timeout := flag.Duration("timeout", 3*time.Minute, "Timeout per case")
	root := flag.String("temp-root", ".side/tmp", "Parent directory for private storage directories")
	mode := flag.String("case", "", "Internal subprocess case: none or s2")
	traffic := flag.String("traffic", "", "Internal subprocess traffic: steady or burst")
	summaryPath := flag.String("summarize", "", "Summarize a JSONL report without starting any servers")
	flag.Parse()

	if *summaryPath != "" {
		file, err := os.Open(*summaryPath)
		if err != nil {
			log.Fatal().Err(err).Msg("Open report")
		}
		defer file.Close()
		summary, err := summarizeReports(file)
		if err != nil {
			log.Fatal().Err(err).Msg("Invalid comparison")
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(summary); err != nil {
			log.Fatal().Err(err).Msg("Write summary")
		}
		return
	}

	if err := cfg.validate(); err != nil {
		log.Fatal().Err(err).Msg("Invalid probe configuration")
	}
	if *runs < 1 || *timeout <= 0 {
		log.Fatal().Msg("runs and timeout must be positive")
	}
	if *mode != "" {
		dir, err := os.MkdirTemp(*root, "jetstream-latency-")
		if err != nil {
			log.Fatal().Err(err).Msg("Create isolated storage")
		}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		result, err := runTrial(ctx, cfg, *mode, *traffic, dir)
		cancel()
		removeErr := os.RemoveAll(dir)
		if err != nil {
			log.Fatal().Err(err).Msg("Probe failed")
		}
		if removeErr != nil {
			log.Fatal().Err(removeErr).Msg("Remove isolated storage")
		}
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			log.Fatal().Err(err).Msg("Write result")
		}
		return
	}

	executable, err := os.Executable()
	if err != nil {
		log.Fatal().Err(err).Msg("Resolve subprocess executable")
	}
	for run := 0; run < *runs; run++ {
		modes := []string{"none", "s2"}
		if run%2 == 1 {
			modes[0], modes[1] = modes[1], modes[0]
		}
		for _, pattern := range []string{"steady", "burst"} {
			for _, compression := range modes {
				args := append([]string{}, os.Args[1:]...)
				args = append(args, "-case="+compression, "-traffic="+pattern)
				ctx, cancel := context.WithTimeout(context.Background(), *timeout+15*time.Second)
				cmd := exec.CommandContext(ctx, executable, args...)
				cmd.Stderr = os.Stderr
				output, err := cmd.Output()
				cancel()
				if err != nil {
					log.Fatal().Err(err).Int("run", run+1).
						Str("compression", compression).Str("traffic", pattern).Msg("Subprocess failed")
				}
				var result trialReport
				if err := json.Unmarshal(output, &result); err != nil {
					log.Fatal().Err(err).Msg("Invalid subprocess report")
				}
				result.Run = run + 1
				if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
					log.Fatal().Err(err).Msg("Write result")
				}
			}
		}
	}
}

type probeConfig struct {
	Messages   int           `json:"messages"`
	Warmup     int           `json:"warmup"`
	BodyBytes  int           `json:"bodyBytes"`
	Interval   time.Duration `json:"interval"`
	Workers    int           `json:"workers"`
	BurstSize  int           `json:"burstSize"`
	BurstPause time.Duration `json:"burstPause"`
}

func (c probeConfig) validate() error {
	if c.Messages < 1 || c.Warmup < 0 || c.BodyBytes < 1 || c.BodyBytes > 512*1024 ||
		c.Workers < 1 || c.BurstSize < 1 || c.Interval < 0 || c.BurstPause < 0 {
		return fmt.Errorf("require positive messages/body-bytes/workers/burst-size, nonnegative warmup/interval/burst-pause, body-bytes <= 524288")
	}
	return nil
}
