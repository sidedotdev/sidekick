// Command test_timing measures side.yml's integration_test_commands running
// all concurrently, with the affected-tests wrapper replaced by unfiltered
// `go test -json -count=1` so nothing is skipped, and reports per-command
// wall clocks plus go-test package/test/subtest timing breakdowns (see
// TEST_TIMING_LOCAL.md and TEST_TIMING_MODAL.md for example output).
//
// Usage:
//
//	go run ./scripts/test_timing                        # run locally
//	go run ./scripts/test_timing -where modal           # run in a Modal sandbox
//	go run ./scripts/test_timing -breakdown it=out.json # parse go test -json streams
//
// Modal mode creates (or reuses) a sandbox with side.yml's modal config
// verbatim, syncs the repo, runs the configured worktree setup and executes
// run.sh in the remote repo directory. For batches longer than a few minutes
// there, pass -env SIDE_TIMING_MODE=startpoll and re-invoke with
// SIDE_TIMING_MODE=poll until it prints state=completed, then
// SIDE_TIMING_MODE=report: Modal reaps an ephemeral sandbox that has no
// active foreground command, regardless of background CPU work, so the
// detached batch must be accompanied by a foreground poll session.
//
// TODO: drive the modal startpoll/poll/report sequence automatically instead
// of requiring repeated invocations.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"sidekick/dev"
	"sidekick/env"
)

func must(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
		os.Exit(1)
	}
}

func main() {
	breakdown := flag.Bool("breakdown", false, "parse `go test -json` streams given as label=path args and print the markdown breakdown")
	where := flag.String("where", "local", "where to run the batch: local or modal")
	sandboxName := flag.String("sandbox", "side-timing-default", "Modal sandbox name (reused when still alive)")
	scriptPath := flag.String("script", "scripts/test_timing/run.sh", "repo-relative shell script executed in the (remote) repo dir")
	uploads := flag.String("upload", "scripts/test_timing/run.sh,scripts/test_timing/main.go,scripts/test_timing/breakdown.go",
		"comma-separated repo-relative files copied into the sandbox so local edits apply without a re-sync")
	envVars := flag.String("env", "", "comma-separated KEY=VALUE pairs exported before the script runs (e.g. SIDE_TIMING_MODE=report)")
	skipSetup := flag.Bool("skip-setup", false, "skip repo sync and worktree setup (for reruns against a warm sandbox)")
	deleteAfter := flag.Bool("delete", false, "delete the sandbox after the run")
	timeout := flag.Duration("timeout", 90*time.Minute, "overall deadline")
	flag.Parse()

	if *breakdown {
		must(runBreakdown(flag.Args(), os.Stdout), "breakdown")
		return
	}

	if *where == "local" {
		cmd := exec.Command("sh", *scriptPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = append(os.Environ(), splitEnvPairs(*envVars)...)
		must(cmd.Run(), "run "+*scriptPath)
		return
	}
	if *where != "modal" {
		fmt.Fprintf(os.Stderr, "unknown -where %q (want local or modal)\n", *where)
		os.Exit(2)
	}

	repoDir, err := os.Getwd()
	must(err, "getwd")

	repoConfig, err := dev.GetRepoConfigActivity(env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: repoDir}})
	must(err, "read side.yml")
	configJSON, err := json.Marshal(repoConfig.ModalConfig)
	must(err, "marshal modal config")
	fmt.Printf("side.yml modal config: %s\n", configJSON)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	start := time.Now()
	created, err := env.CreateSandboxActivity(ctx, env.CreateSandboxInput{
		EnvType: env.EnvTypeModal,
		Name:    *sandboxName,
		RepoDir: repoDir,
		Config:  configJSON,
	})
	must(err, "create sandbox")
	fmt.Printf("sandbox ready in %s (reused=%v, ssh=%s:%d)\n",
		time.Since(start).Round(time.Second), created.Reused, created.SSHHost, created.SSHPort)

	remoteEnv := &env.ModalEnv{
		SandboxName:  created.SandboxName,
		SSHHost:      created.SSHHost,
		SSHPort:      created.SSHPort,
		LocalRepoDir: repoDir,
	}
	if *deleteAfter {
		defer func() {
			_, _ = env.DeleteSandboxActivity(context.Background(), env.DeleteSandboxInput{
				EnvType: env.EnvTypeModal, SandboxName: created.SandboxName,
			})
		}()
	}

	runScript := func(what, script string) {
		phaseStart := time.Now()
		out, err := remoteEnv.RunCommand(ctx, env.EnvRunCommandInput{Command: "sh", Args: []string{"-c", script}})
		must(err, what)
		fmt.Printf("%s finished in %s (exit=%d)\n%s%s\n",
			what, time.Since(phaseStart).Round(time.Second), out.ExitStatus, out.Stdout, out.Stderr)
		if out.ExitStatus != 0 {
			os.Exit(1)
		}
	}

	if *skipSetup {
		out, err := remoteEnv.RunCommand(ctx, env.EnvRunCommandInput{
			Command: "sh", Args: []string{"-c", `ls -d "$HOME"/*/.git | head -1 | xargs dirname`},
		})
		must(err, "find synced repo")
		remoteEnv.WorkingDirectory = strings.TrimSpace(out.Stdout)
		fmt.Printf("reusing synced repo at %s\n", remoteEnv.WorkingDirectory)
	} else {
		syncStart := time.Now()
		syncOutput, err := env.SyncRepoToRemoteActivity(ctx, env.SyncRepoToRemoteInput{
			EnvContainer: env.EnvContainer{Env: remoteEnv},
			LocalRepoDir: repoDir,
		})
		must(err, "sync repo")
		remoteEnv.WorkingDirectory = syncOutput.RemoteRepoDir
		fmt.Printf("repo synced to %s in %s\n", syncOutput.RemoteRepoDir, time.Since(syncStart).Round(time.Second))
		// Module prefetch mirrors the warm local module cache, so the measured
		// run reflects test execution rather than downloads.
		runScript("worktree setup", repoConfig.WorktreeSetup+"\ncd \""+remoteEnv.WorkingDirectory+"\" && go mod download")
	}

	for _, rel := range strings.Split(*uploads, ",") {
		rel = strings.TrimSpace(rel)
		if rel == "" {
			continue
		}
		data, err := os.ReadFile(rel)
		must(err, "read "+rel)
		remotePath := path.Join(remoteEnv.WorkingDirectory, rel)
		must(remoteEnv.MkdirAll(ctx, path.Dir(remotePath), 0o755), "mkdir "+path.Dir(remotePath))
		must(remoteEnv.WriteFile(ctx, remotePath, data, 0o644), "upload "+rel)
		fmt.Printf("uploaded %s\n", remotePath)
	}

	prefix := ""
	for _, kv := range splitEnvPairs(*envVars) {
		prefix += "export " + kv + "\n"
	}
	runScript("timing batch", prefix+"sh "+*scriptPath)
}

func splitEnvPairs(commaSeparated string) []string {
	var pairs []string
	for _, kv := range strings.Split(commaSeparated, ",") {
		if kv = strings.TrimSpace(kv); kv != "" {
			pairs = append(pairs, kv)
		}
	}
	return pairs
}