// Command modal_mem_measure measures peak memory usage inside a Modal sandbox
// while running this repo's side.yml test command sets, to size the modal
// memory_request_mib / memory_limit_mib configuration with real data instead
// of guesswork.
//
// It creates (or reuses) a sandbox built from Dockerfile.modal with no memory
// limit (so usage can burst past the request and the true peak is observable),
// syncs the repo, runs one phase's commands concurrently (the way sidekick
// runs test_commands), and samples sandbox-wide memory usage throughout,
// reporting the observed peak.
//
// Usage:
//
//	go run ./scripts/modal_mem_measure -phase unit
//	go run ./scripts/modal_mem_measure -phase integration -skip-setup
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"sidekick/common"
	"sidekick/env"
)

type phaseCommand struct {
	workingDir string // relative to the repo root; empty means the repo root
	command    string
}

// phases mirrors side.yml's test_commands and integration_test_commands.
var phases = map[string][]phaseCommand{
	"unit": {
		{"", "go run ./scripts/affected_tests/ -test.timeout 120s ./..."},
		{"frontend", "bun run test:unit -- --no-color"},
		{"frontend", "bun run type-check"},
		{"frontend", "bun run lint:check"},
	},
	"integration": {
		{"", "SIDE_INTEGRATION_TEST=true go run ./scripts/affected_tests/ -test.timeout 240s ./..."},
		{"", "go run ./scripts/lint_chat_history_append/"},
		{"", "go run ./scripts/lint_track_callback/ ./..."},
		{"", "go run ./scripts/lint_workflow_go_context/ ./..."},
		{"", "SIDE_E2E_TEST=true go run ./scripts/affected_tests/ -test.timeout 295s ./..."},
	},
}

func must(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
		os.Exit(1)
	}
}

// phaseScript builds a shell script that runs the phase's commands
// concurrently while a background loop samples sandbox-wide memory usage
// every 500ms, then reports the sampled peak (plus the cgroup lifetime peak
// when the kernel exposes it) and per-command exit statuses.
func phaseScript(cmds []phaseCommand) string {
	script := `samples=/tmp/side_mem_samples
rm -f "$samples"
( while :; do
    if [ -r /sys/fs/cgroup/memory.current ]; then
      cat /sys/fs/cgroup/memory.current
    else
      awk '/MemTotal:/{t=$2} /MemAvailable:/{a=$2} END{print (t-a)*1024}' /proc/meminfo
    fi
    sleep 0.5
  done >> "$samples" 2>/dev/null ) &
sampler=$!
`
	for i, c := range cmds {
		dir := "."
		if c.workingDir != "" {
			dir = c.workingDir
		}
		script += fmt.Sprintf("( start=$(date +%%s); cd %q && %s; status=$?; echo $(($(date +%%s)-start)) > /tmp/side_mem_cmd_%d.elapsed; exit $status ) > /tmp/side_mem_cmd_%d.log 2>&1 &\npid_%d=$!\n", dir, c.command, i, i, i)
	}
	script += "fail=0\n"
	for i := range cmds {
		script += fmt.Sprintf("wait $pid_%d; st_%d=$?; elapsed_%d=$(cat /tmp/side_mem_cmd_%d.elapsed); [ $st_%d -eq 0 ] || fail=1\n", i, i, i, i, i)
	}
	script += `kill $sampler 2>/dev/null || true
echo "SAMPLED_PEAK_BYTES=$(sort -n "$samples" | tail -1)"
if [ -r /sys/fs/cgroup/memory.peak ]; then echo "CGROUP_PEAK_BYTES=$(cat /sys/fs/cgroup/memory.peak)"; fi
`
	for i, c := range cmds {
		script += fmt.Sprintf("echo \"CMD_%d elapsed=${elapsed_%d}s exit=$st_%d: %s\"\n", i, i, i, c.command)
		script += fmt.Sprintf("if [ $st_%d -ne 0 ]; then echo '--- log tail ---'; tail -40 /tmp/side_mem_cmd_%d.log; fi\n", i, i)
	}
	script += "exit $fail\n"
	return script
}

func parseVolumeMounts(spec string) ([]common.ModalVolumeMount, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	var mounts []common.ModalVolumeMount
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, mountPath, found := strings.Cut(entry, ":")
		if !found {
			return nil, fmt.Errorf("volume %q must be given as name:/absolute/mount/path", entry)
		}
		mount := common.ModalVolumeMount{
			Name:      strings.TrimSpace(name),
			MountPath: strings.TrimSpace(mountPath),
		}
		if err := mount.Validate(); err != nil {
			return nil, err
		}
		mounts = append(mounts, mount)
	}
	return mounts, nil
}

func main() {
	sandbox := flag.String("sandbox", "side-mem-measure", "Modal sandbox name (reused across runs when still alive)")
	phase := flag.String("phase", "unit", "which side.yml command set to measure: unit or integration")
	adhoc := flag.String("run", "", "instead of a phase, run this shell command in the sandbox repo dir (for debugging)")
	skipSetup := flag.Bool("skip-setup", false, "skip repo sync and frontend/module setup (for reruns against a warm sandbox)")
	keep := flag.Bool("keep", false, "keep the sandbox alive after the run")
	vm := flag.Bool("vm", false, "use Modal's VM runtime")
	cpu := flag.Float64("cpu", 0, "CPU request; zero uses the Modal environment default")
	cpuLimit := flag.Float64("cpu-limit", 0, "CPU limit; zero leaves burst CPU unlimited")
	memory := flag.Int("memory", 0, "memory request in MiB; zero uses the Modal environment default")
	memoryLimit := flag.Int("memory-limit", 0, "memory limit in MiB; zero leaves burst memory unlimited")
	volumes := flag.String("volumes", "", "comma-separated persistent volume mounts, each as name:/absolute/mount/path")
	flag.Parse()

	volumeMounts, err := parseVolumeMounts(*volumes)
	must(err, "parse volumes")

	cmds, ok := phases[*phase]
	if !ok && *adhoc == "" {
		fmt.Fprintf(os.Stderr, "unknown phase %q\n", *phase)
		os.Exit(1)
	}

	repoDir, err := os.Getwd()
	must(err, "getwd")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()

	// No memory/CPU limits so the workload bursts freely and we observe its
	// true peak; a long idle timeout and disabled active snapshots keep the
	// watchdog from interfering with (or adding overhead to) the run.
	cfgJSON, err := json.Marshal(common.ModalEnvConfig{
		VM:                    *vm,
		DockerfilePath:        "Dockerfile.modal",
		CPU:                   *cpu,
		CPULimit:              *cpuLimit,
		Memory:                *memory,
		MemoryLimit:           *memoryLimit,
		IdleSeconds:           7200,
		ActiveSnapshotSeconds: -1,
		Volumes:               volumeMounts,
	})
	must(err, "marshal config")

	fmt.Printf("creating (or reusing) modal sandbox %q...\n", *sandbox)
	start := time.Now()
	created, err := env.CreateSandboxActivity(ctx, env.CreateSandboxInput{
		EnvType: env.EnvTypeModal,
		Name:    *sandbox,
		RepoDir: repoDir,
		Config:  cfgJSON,
	})
	must(err, "create sandbox")
	fmt.Printf("sandbox ready in %s (reused=%v, ssh=%s:%d)\n",
		time.Since(start).Round(time.Second), created.Reused, created.SSHHost, created.SSHPort)
	if !*keep {
		defer func() {
			_, _ = env.DeleteSandboxActivity(context.Background(), env.DeleteSandboxInput{
				EnvType: env.EnvTypeModal, SandboxName: created.SandboxName,
			})
		}()
	}

	e := &env.ModalEnv{
		SandboxName:  created.SandboxName,
		SSHHost:      created.SSHHost,
		SSHPort:      created.SSHPort,
		LocalRepoDir: repoDir,
	}

	runScript := func(what, script string) string {
		phaseStart := time.Now()
		out, err := e.RunCommand(ctx, env.EnvRunCommandInput{Command: "sh", Args: []string{"-c", script}})
		must(err, what)
		fmt.Printf("%s finished in %s (exit=%d)\n%s%s\n", what, time.Since(phaseStart).Round(time.Second), out.ExitStatus, out.Stdout, out.Stderr)
		if out.ExitStatus != 0 {
			os.Exit(1)
		}
		return out.Stdout
	}

	if !*skipSetup {
		syncStart := time.Now()
		syncOutput, err := env.SyncRepoToRemoteActivity(ctx, env.SyncRepoToRemoteInput{
			EnvContainer: env.EnvContainer{Env: e},
			LocalRepoDir: repoDir,
		})
		must(err, "sync repo")
		e.WorkingDirectory = syncOutput.RemoteRepoDir
		fmt.Printf("repo synced to %s in %s\n", syncOutput.RemoteRepoDir, time.Since(syncStart).Round(time.Second))

		// Mirror side.yml's worktree_setup plus module prefetch so the phase
		// measures test execution rather than network downloads.
		runScript("setup", "set -e\ncd frontend && bun ci && mkdir -p dist && touch dist/empty.txt\ncd ..\ngo mod download")
	} else {
		out, err := e.RunCommand(ctx, env.EnvRunCommandInput{Command: "sh", Args: []string{"-c", "ls -d \"$HOME\"/*/.git | head -1 | xargs dirname"}})
		must(err, "find synced repo")
		e.WorkingDirectory = out.Stdout[:len(out.Stdout)-1]
		fmt.Printf("reusing synced repo at %s\n", e.WorkingDirectory)
	}

	if *adhoc != "" {
		runScript("ad-hoc command", *adhoc)
		return
	}
	fmt.Printf("running %q phase (%d concurrent commands)...\n", *phase, len(cmds))
	runScript(*phase+" phase", phaseScript(cmds))
}
