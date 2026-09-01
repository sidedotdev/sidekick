// Command modal_perf_repro measures the steady-state per-operation cost of the
// Modal environment, simulating what a basic_dev workflow does against a warm
// sandbox: shell commands (git status, ripgrep searches like
// bulk_search_repository), symbol retrieval (tree-sitter GetSymbolsActivity and
// gopls-backed definition lookups, like get_symbol_definitions), and SFTP
// filesystem operations (reads, stats, dir listings, edit cycles).
//
// Run it before and after a change and tee the output to compare:
//
//	go run ./scripts/modal_perf_repro | tee scripts/modal_perf_repro/BEFORE.txt
//
// The sandbox is reused across runs (same -sandbox name) so both runs measure
// a warm sandbox rather than sandbox creation.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sidekick/coding/lsp"
	"sidekick/common"
	"sidekick/dev"
	"sidekick/env"
	"sidekick/sideagent"
)

func must(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
		os.Exit(1)
	}
}

type phaseResult struct {
	name  string
	ops   int
	total time.Duration
}

func main() {
	sandbox := flag.String("sandbox", "side-perf-repro", "Modal sandbox name (reused across runs when still alive)")
	ops := flag.Int("ops", 5, "operations per phase")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	fmt.Printf("creating (or reusing) modal sandbox %q...\n", *sandbox)
	start := time.Now()
	created, err := env.CreateSandboxActivity(ctx, env.CreateSandboxInput{
		EnvType: env.EnvTypeModal,
		Name:    *sandbox,
	})
	must(err, "create sandbox")
	fmt.Printf("sandbox ready in %s (reused=%v)\n", time.Since(start).Round(time.Millisecond), created.Reused)

	const repoDir = "/root/side-perf-repro"
	rootEnv := &env.ModalEnv{
		WorkingDirectory: "/root",
		SandboxName:      created.SandboxName,
		SSHHost:          created.SSHHost,
		SSHPort:          created.SSHPort,
	}

	// One-time repo setup, mimicking the git worktree with a go module that a
	// basic_dev flow works in.
	setupScript := fmt.Sprintf(`
set -e
mkdir -p %[1]s
cd %[1]s
if [ ! -d .git ]; then
  git init -q -b main
  git config user.email perf@example.com
  git config user.name perf
  printf 'module example.com/perf\n\ngo 1.21\n' > go.mod
  mkdir -p pkg/alpha pkg/beta
  for i in $(seq 1 20); do
    printf 'package alpha\n\nfunc AlphaFunc%%d() int { return %%d }\n' "$i" "$i" > pkg/alpha/file$i.go
    printf 'package beta\n\nfunc BetaFunc%%d() int { return %%d }\n' "$i" "$i" > pkg/beta/file$i.go
  done
  git add -A
  git commit -qm initial
fi
printf 'package main\n\nimport "fmt"\n\nfunc main() {\n\tfmt.Println("hi")\n}\n' > main.go
git add -A
git diff --cached --quiet || git commit -qm main
`, repoDir)
	setupStart := time.Now()
	setupOut, err := rootEnv.RunCommand(ctx, env.EnvRunCommandInput{Command: "sh", Args: []string{"-c", setupScript}})
	must(err, "repo setup")
	if setupOut.ExitStatus != 0 {
		must(fmt.Errorf("exit %d: %s%s", setupOut.ExitStatus, setupOut.Stdout, setupOut.Stderr), "repo setup")
	}
	fmt.Printf("repo setup done in %s\n", time.Since(setupStart).Round(time.Millisecond))

	e := &env.ModalEnv{
		WorkingDirectory: repoDir,
		SandboxName:      created.SandboxName,
		SSHHost:          created.SSHHost,
		SSHPort:          created.SSHPort,
	}
	envContainer := env.EnvContainer{Env: e}

	// Warm up the SSH control master and the pooled SFTP connection so phases
	// measure steady-state per-operation cost, like a long-lived worker
	// mid-flow.
	warmupStart := time.Now()
	_, err = e.RunCommand(ctx, env.EnvRunCommandInput{Command: "true"})
	must(err, "warmup command")
	_, err = e.ReadFile(ctx, "main.go")
	must(err, "warmup sftp")
	fmt.Printf("ssh/sftp warmup done in %s\n", time.Since(warmupStart).Round(time.Millisecond))

	goAvailable := false
	if out, err := e.RunCommand(ctx, env.EnvRunCommandInput{Command: "sh", Args: []string{"-c", "command -v go"}}); err == nil && out.ExitStatus == 0 {
		goAvailable = true
	}

	lspActivities := lsp.NewLSPActivities(func(lang string) lsp.LSPClient {
		return &lsp.Jsonrpc2LSPClient{LanguageName: lang}
	})
	if goAvailable {
		// First lookup may install gopls and warm its daemon; time it
		// separately so phases measure steady-state cost.
		goplsWarmupStart := time.Now()
		// A freshly started daemon may transiently answer "no views" while it
		// builds the workspace view; retry until lookups reach steady state.
		var warmupErr error
		for deadline := time.Now().Add(90 * time.Second); ; {
			defs, err := lspActivities.GetSingleFileDefinitions(ctx, lsp.LSPDefinitionLocationsRequest{
				FilePath:     "main.go",
				EnvContainer: &envContainer,
				Symbols:      []string{"Println"},
			})
			if err == nil && len(defs) == 1 && defs[0].Error == "" {
				warmupErr = nil
				break
			}
			if err != nil {
				warmupErr = err
			} else {
				warmupErr = fmt.Errorf("lookup did not fully resolve: %+v", defs)
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(2 * time.Second)
		}
		must(warmupErr, "gopls warmup")
		fmt.Printf("gopls warmup done in %s\n", time.Since(goplsWarmupStart).Round(time.Millisecond))
	} else {
		fmt.Println("go toolchain not available in sandbox; skipping gopls phase")
	}

	runPhase := func(name string, fn func(i int) error) phaseResult {
		phaseStart := time.Now()
		for i := 0; i < *ops; i++ {
			if err := fn(i); err != nil {
				fmt.Printf("%-55s FAILED on op %d after %s: %v\n",
					name, i+1, time.Since(phaseStart).Round(time.Millisecond), err)
				os.Exit(1)
			}
		}
		total := time.Since(phaseStart)
		fmt.Printf("%-55s %3d ops  total %9s  avg %9s\n",
			name, *ops, total.Round(time.Millisecond), (total / time.Duration(*ops)).Round(time.Millisecond))
		return phaseResult{name: name, ops: *ops, total: total}
	}

	checkRun := func(command string, args ...string) error {
		out, err := e.RunCommand(ctx, env.EnvRunCommandInput{Command: command, Args: args})
		if err != nil {
			return err
		}
		if out.ExitStatus != 0 {
			return fmt.Errorf("%s exited %d: %s%s", command, out.ExitStatus, out.Stdout, out.Stderr)
		}
		return nil
	}

	// Diagnostics (excluded from TOTAL so it stays comparable across runs).
	// ControlMaster already keeps the authenticated SSH transport open, so a
	// raw ssh exec measures per-command *session* cost: channel open, exec
	// request, and data/exit/close each take a network round trip even on the
	// reused transport, which is why pooling more SSH connections would not
	// help. RunCommand rides the pooled side-agent exec channel, so the gap
	// between it and raw ssh shows the savings of avoiding per-command
	// session setup. The persistent shell channel is the original prototype
	// of that idea (~1 round trip per command) but inherits every shell
	// quoting hazard; the direct side-agent exec channel keeps the 1-RTT
	// shape while sending argv verbatim over a framed protocol to our own
	// remote binary — no shell, no quoting (see https://ruuda.nl/2026/deptool)
	// — and isolates the channel from RunCommand's env-layer overhead. The
	// Stat phase below measures the floor of a single SFTP round trip.
	sshArgs, err := e.SSHArgs(ctx)
	must(err, "ssh args")
	fmt.Println("\ndiagnostic phases (excluded from TOTAL):")
	runPhase("raw ssh exec: true (per-exec session setup)", func(int) error {
		if out, err := exec.CommandContext(ctx, "ssh", append(append([]string{}, sshArgs...), "true")...).CombinedOutput(); err != nil {
			return fmt.Errorf("raw ssh: %w: %s", err, out)
		}
		return nil
	})
	runPhase("RunCommand: true (pooled side-agent channel)", func(int) error {
		return checkRun("true")
	})
	shell, err := startPersistentShell(ctx, sshArgs)
	must(err, "start persistent shell channel")
	_, err = shell.run("true")
	must(err, "warm persistent shell channel")
	runPhase("persistent shell channel: true (1-RTT prototype)", func(int) error {
		code, err := shell.run("true")
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("exit %d", code)
		}
		return nil
	})
	shell.close()

	agentPath, err := ensureRemoteAgent(ctx, e, sshArgs)
	must(err, "ensure remote side-agent")
	agent, err := startAgentChannel(ctx, sshArgs, agentPath)
	must(err, "start agent exec channel")
	agentRun := func(dir string, argv ...string) error {
		resp, err := agent.client.Exec(ctx, sideagent.ExecRequest{Dir: dir, Argv: argv})
		if err != nil {
			return err
		}
		if resp.Error != "" {
			return fmt.Errorf("agent exec: %s", resp.Error)
		}
		if resp.ExitStatus != 0 {
			return fmt.Errorf("exit %d: %s%s", resp.ExitStatus, resp.Stdout, resp.Stderr)
		}
		return nil
	}
	must(agentRun("", "true"), "warm agent exec channel")
	runPhase("agent exec channel: true (no-shell 1-RTT prototype)", func(int) error {
		return agentRun("", "true")
	})
	runPhase("agent exec channel: git status --porcelain", func(int) error {
		return agentRun(repoDir, "git", "status", "--porcelain")
	})

	// Argv travels verbatim over the framed protocol, so arguments that are
	// impossible to quote portably across shells must arrive intact. printf
	// repeats its format per argument; \x01 is an unambiguous separator since
	// argv strings cannot contain NUL.
	trickyArgs := []string{"a b", "it's", `she said "hi"`, "line1\nline2", "$HOME", "`id`", "\\", "*", "; rm -rf /tmp/nope"}
	trickyResp, err := agent.client.Exec(ctx, sideagent.ExecRequest{
		Argv: append([]string{"printf", "\x01%s"}, trickyArgs...),
	})
	must(err, "agent quoting check")
	if want := "\x01" + strings.Join(trickyArgs, "\x01"); trickyResp.ExitStatus != 0 || string(trickyResp.Stdout) != want {
		must(fmt.Errorf("exit %d, stdout %q, want %q", trickyResp.ExitStatus, trickyResp.Stdout, want), "agent quoting check")
	}
	fmt.Println("agent exec channel: shell-hostile argv round-tripped verbatim")
	agent.close()

	fmt.Printf("\nbenchmark phases (%d ops each):\n", *ops)
	var results []phaseResult
	results = append(results, runPhase("RunCommand: git status --porcelain", func(int) error {
		return checkRun("git", "status", "--porcelain")
	}))
	results = append(results, runPhase("RunCommand: rg search (bulk_search_repository)", func(i int) error {
		return checkRun("rg", "-n", fmt.Sprintf("AlphaFunc%d", i+1), ".")
	}))
	results = append(results, runPhase("GetSymbolsActivity (tree-sitter)", func(i int) error {
		_, err := dev.GetSymbolsActivity(envContainer, fmt.Sprintf("pkg/alpha/file%d.go", i+1))
		return err
	}))
	if goAvailable {
		results = append(results, runPhase("gopls GetSingleFileDefinitions", func(i int) error {
			defs, err := lspActivities.GetSingleFileDefinitions(ctx, lsp.LSPDefinitionLocationsRequest{
				FilePath:     "main.go",
				EnvContainer: &envContainer,
				Symbols:      []string{"Println"},
			})
			if err != nil {
				return err
			}
			if len(defs) != 1 || defs[0].Error != "" {
				return fmt.Errorf("unexpected definitions result: %+v", defs)
			}
			return nil
		}))
	}
	results = append(results, runPhase("ReadFile", func(i int) error {
		_, err := e.ReadFile(ctx, fmt.Sprintf("pkg/alpha/file%d.go", i+1))
		return err
	}))
	results = append(results, runPhase("Stat", func(i int) error {
		_, err := e.Stat(ctx, fmt.Sprintf("pkg/beta/file%d.go", i+1))
		return err
	}))
	results = append(results, runPhase("ReadDir", func(int) error {
		_, err := e.ReadDir(ctx, "pkg/alpha")
		return err
	}))
	results = append(results, runPhase("Edit cycle (ReadFile+WriteFile+git diff)", func(i int) error {
		p := fmt.Sprintf("pkg/beta/file%d.go", i+1)
		data, err := e.ReadFile(ctx, p)
		if err != nil {
			return err
		}
		if err := e.WriteFile(ctx, p, append(data, []byte("// edited\n")...), 0644); err != nil {
			return err
		}
		return checkRun("git", "diff", "--stat")
	}))

	var grand time.Duration
	var totalOps int
	for _, r := range results {
		grand += r.total
		totalOps += r.ops
	}
	fmt.Printf("\nTOTAL: %d ops in %s (avg %s/op)\n",
		totalOps, grand.Round(time.Millisecond), (grand / time.Duration(totalOps)).Round(time.Millisecond))

	// Attribute file-op cost to SFTP protocol round trips, using Stat (a
	// single request/response) as the measured round-trip floor. Reads pay
	// separate open/data/EOF/close round trips, dir listings similarly.
	var statAvg time.Duration
	for _, r := range results {
		if r.name == "Stat" {
			statAvg = r.total / time.Duration(r.ops)
		}
	}
	if statAvg > 0 {
		fmt.Println("\nSFTP round-trip attribution (Stat = 1 round trip):")
		for _, r := range results {
			switch r.name {
			case "ReadFile", "Stat", "ReadDir":
				avg := r.total / time.Duration(r.ops)
				fmt.Printf("%-55s ~%.1f round trips\n", r.name, float64(avg)/float64(statAvg))
			}
		}
	}

	// Reset the worktree so repeated runs start from the same state.
	must(checkRun("git", "checkout", "--", "."), "reset worktree")
}

// persistentShell prototypes a long-lived remote command runner: one SSH
// session hosting `sh -s`, with commands streamed over stdin and completion
// detected via a sentinel line carrying the exit status. Unlike per-command
// ssh execs — which pay ~3 protocol round trips (channel open, exec request,
// data/exit/close) even when the transport is reused via ControlMaster — each
// command here costs a single network round trip.
type persistentShell struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	seq    int
}

func startPersistentShell(ctx context.Context, sshArgs []string) (*persistentShell, error) {
	cmd := exec.CommandContext(ctx, "ssh", append(append([]string{}, sshArgs...), "sh -s")...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &persistentShell{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout)}, nil
}

// run executes command in the remote shell and returns its exit status. The
// command's own stdout is not separated from the sentinel stream, which is
// sufficient for benchmarking quiet commands.
func (p *persistentShell) run(command string) (int, error) {
	p.seq++
	sentinel := fmt.Sprintf("__side_done_%d__", p.seq)
	if _, err := fmt.Fprintf(p.stdin, "%s\nprintf '%s %%s\\n' \"$?\"\n", command, sentinel); err != nil {
		return 0, err
	}
	for {
		line, err := p.stdout.ReadString('\n')
		if err != nil {
			return 0, err
		}
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), sentinel+" "); ok {
			return strconv.Atoi(rest)
		}
	}
}

func (p *persistentShell) close() {
	p.stdin.Close()
	p.cmd.Wait()
}

// ensureRemoteAgent resolves the cached side-agent binary for the sandbox's
// OS/arch (building from source when needed) and uploads it to its
// content-addressed remote path, skipping the upload when already present.
// This bootstrap is the only step that passes a command line through a shell;
// afterwards every command travels as verbatim argv over the framed protocol.
func ensureRemoteAgent(ctx context.Context, e *env.ModalEnv, sshArgs []string) (string, error) {
	out, err := e.RunCommand(ctx, env.EnvRunCommandInput{Command: "uname", Args: []string{"-sm"}})
	if err != nil {
		return "", fmt.Errorf("uname: %w", err)
	}
	parts := strings.Fields(strings.TrimSpace(out.Stdout))
	if len(parts) < 2 {
		return "", fmt.Errorf("unexpected uname output: %q", out.Stdout)
	}

	localPath, err := common.GetAgentBinaryPath(parts[0], parts[1])
	if err != nil {
		return "", fmt.Errorf("get agent binary: %w", err)
	}
	remotePath := "/tmp/side-agent-" + filepath.Base(localPath)

	check := exec.CommandContext(ctx, "ssh", append(append([]string{}, sshArgs...), "test -x "+remotePath)...)
	if check.Run() == nil {
		return remotePath, nil
	}
	localFile, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer localFile.Close()
	upload := exec.CommandContext(ctx, "ssh", append(append([]string{}, sshArgs...), "cat > "+remotePath+" && chmod +x "+remotePath)...)
	upload.Stdin = localFile
	if uploadOut, err := upload.CombinedOutput(); err != nil {
		return "", fmt.Errorf("upload side-agent: %w: %s", err, uploadOut)
	}
	return remotePath, nil
}

// agentChannel is one SSH session hosting the side-agent exec server. Like
// the persistent shell it costs a single network round trip per command, but
// argv is framed rather than spliced into a shell line, so it has none of the
// shell's quoting or output-separation problems.
type agentChannel struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	client *sideagent.Client
}

func startAgentChannel(ctx context.Context, sshArgs []string, remotePath string) (*agentChannel, error) {
	cmd := exec.CommandContext(ctx, "ssh", append(append([]string{}, sshArgs...), remotePath+" exec")...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &agentChannel{cmd: cmd, stdin: stdin, client: sideagent.NewClient(stdout, stdin)}, nil
}

func (a *agentChannel) close() {
	a.stdin.Close()
	a.cmd.Wait()
}
