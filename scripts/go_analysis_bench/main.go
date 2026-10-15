package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"sidekick/coding/lsp"
	"sidekick/common"
)

type config struct {
	Label          string        `json:"label"`
	Output         string        `json:"output"`
	Seed           string        `json:"seed,omitempty"`
	Workspaces     int           `json:"workspaces"`
	Cycles         int           `json:"cycles"`
	Duration       time.Duration `json:"durationNanos"`
	IdleEvery      int           `json:"idleEvery"`
	Idle           time.Duration `json:"idleNanos"`
	FinalIdle      time.Duration `json:"finalIdleNanos"`
	SampleInterval time.Duration `json:"sampleIntervalNanos"`
	SteadyWindow   time.Duration `json:"steadyWindowNanos"`
	OperationLimit time.Duration `json:"operationLimitNanos"`
	DaemonNames    string        `json:"daemonNames"`
	AllowExisting  bool          `json:"allowExisting"`
}

func defaultConfig() config {
	return config{
		Label: "desktop", Output: ".side/tmp/go-analysis-bench-" + time.Now().Format("20060102-150405"),
		Workspaces: 3, Cycles: 100, IdleEvery: 10, Idle: 5 * time.Second,
		FinalIdle: 30 * time.Second, SampleInterval: 250 * time.Millisecond,
		SteadyWindow: 10 * time.Second, OperationLimit: time.Minute, DaemonNames: "gopls",
	}
}

func (c config) validate() error {
	switch {
	case c.Output == "":
		return fmt.Errorf("output directory is required")
	case c.Workspaces < 1:
		return fmt.Errorf("workspaces must be positive")
	case c.Cycles < 0 || c.Duration < 0 || (c.Cycles == 0 && c.Duration == 0):
		return fmt.Errorf("specify positive cycles or duration")
	case c.IdleEvery < 1 || c.Idle < 0:
		return fmt.Errorf("idle-every must be positive and idle must be nonnegative")
	case c.SampleInterval <= 0 || c.SteadyWindow < c.SampleInterval:
		return fmt.Errorf("sample interval must be positive and no larger than the steady window")
	case c.FinalIdle < c.SteadyWindow+c.SampleInterval:
		return fmt.Errorf("final-idle must exceed the steady window by at least one sample interval")
	case c.OperationLimit <= 0:
		return fmt.Errorf("operation timeout must be positive")
	}
	for _, name := range strings.Split(c.DaemonNames, ",") {
		if name == "" || filepath.Base(name) != name {
			return fmt.Errorf("daemon names must be comma-separated executable basenames")
		}
	}
	return nil
}

type benchmarkReport struct {
	SchemaVersion       int                       `json:"schemaVersion"`
	Config              config                    `json:"config"`
	StartedAt           time.Time                 `json:"startedAt"`
	EndedAt             time.Time                 `json:"endedAt"`
	GoVersion           string                    `json:"goVersion"`
	GoEnvironment       json.RawMessage           `json:"goEnvironment,omitempty"`
	GoplsVersion        string                    `json:"goplsVersion,omitempty"`
	SourceRevision      string                    `json:"sourceRevision,omitempty"`
	SourceDiffSHA256    string                    `json:"sourceDiffSha256,omitempty"`
	SeedRevision        string                    `json:"seedRevision,omitempty"`
	FixtureSHA256       string                    `json:"fixtureSha256"`
	WorkloadFingerprint string                    `json:"workloadFingerprint"`
	Hostname            string                    `json:"hostname"`
	Kernel              string                    `json:"kernel,omitempty"`
	Preexisting         []processMemory           `json:"preexistingAnalysisProcesses,omitempty"`
	CompletedCycles     int                       `json:"completedCycles"`
	CorrectnessFailures int                       `json:"correctnessFailures"`
	Memory              memorySummary             `json:"memory"`
	Latency             map[string]latencySummary `json:"latency"`
	Error               string                    `json:"error,omitempty"`
}

func fixtureChecksum() (string, error) {
	hash := sha256.New()
	err := fs.WalkDir(fixtureFiles, "testdata", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := fixtureFiles.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(hash, "%s\x00", path)
		hash.Write(data)
		return nil
	})
	return hex.EncodeToString(hash.Sum(nil)), err
}

func workloadFingerprint(report benchmarkReport) string {
	cfg := report.Config
	cfg.Label, cfg.Output, cfg.Seed, cfg.DaemonNames = "", "", "", ""
	cfg.AllowExisting = false
	data, _ := json.Marshal(struct {
		Config  config `json:"config"`
		Seed    string `json:"seed"`
		Fixture string `json:"fixture"`
		Go      string `json:"go"`
	}{cfg, report.SeedRevision, report.FixtureSHA256, report.GoVersion})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func setEnvironment(key, value string) (func(), error) {
	old, exists := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		return nil, err
	}
	return func() {
		if exists {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	}, nil
}

func runBenchmark(ctx context.Context, cfg config) (report benchmarkReport, resultErr error) {
	if err := cfg.validate(); err != nil {
		return report, err
	}
	if runtime.GOOS != "linux" {
		return report, fmt.Errorf("benchmark process accounting requires Linux")
	}
	output, err := filepath.Abs(cfg.Output)
	if err != nil {
		return report, err
	}
	cfg.Output = output
	if cfg.Seed != "" {
		cfg.Seed, err = filepath.Abs(cfg.Seed)
		if err != nil {
			return report, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return report, err
	}
	if err := os.Mkdir(output, 0755); err != nil {
		return report, fmt.Errorf("output must be a new directory: %w", err)
	}
	report = benchmarkReport{
		SchemaVersion: 1, Config: cfg, StartedAt: time.Now(), GoVersion: runtime.Version(),
	}
	report.Hostname, _ = os.Hostname()
	var monitor *memoryMonitor
	var files []*os.File
	var token string
	latency := newLatencyAccumulator()
	driver := &workload{latency: latency, timeout: cfg.OperationLimit}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer func() {
		if monitor != nil {
			var err error
			report.Memory, err = monitor.finish()
			resultErr = errors.Join(resultErr, err)
		}
		report.Latency = latency.summary()
		report.CorrectnessFailures = driver.failures
		if driver.activities != nil {
			resultErr = errors.Join(resultErr, driver.activities.Close())
		}
		for _, file := range files {
			resultErr = errors.Join(resultErr, file.Close())
		}
		if token != "" {
			resultErr = errors.Join(resultErr, cleanupProcesses(token))
		}
		report.EndedAt = time.Now()
		if resultErr != nil {
			report.Error = resultErr.Error()
		}
		resultErr = errors.Join(resultErr, writeJSON(filepath.Join(output, "report.json"), report))
	}()
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return report, err
	}
	token = hex.EncodeToString(tokenBytes)
	restoreToken, err := setEnvironment(runTokenEnv, token)
	if err != nil {
		return report, err
	}
	defer restoreToken()
	restoreWork, err := setEnvironment("GOWORK", "off")
	if err != nil {
		return report, err
	}
	defer restoreWork()

	all, err := readProcesses()
	if err != nil {
		return report, err
	}
	tracker := newProcessTracker(os.Getpid())
	for _, name := range strings.Split(cfg.DaemonNames, ",") {
		tracker.daemonNames[name] = true
	}
	report.Preexisting, _ = tracker.selectProcesses(all)
	var liveExisting []processMemory
	for _, process := range report.Preexisting {
		if process.RSSBytes != 0 {
			liveExisting = append(liveExisting, process)
		}
	}
	report.Preexisting = liveExisting
	if len(liveExisting) != 0 && !cfg.AllowExisting {
		return report, fmt.Errorf("preexisting analysis processes detected: %+v; use an isolated OS user, or --allow-existing for a contaminated diagnostic run", liveExisting)
	}
	goplsPath, err := common.FindOrInstallGopls()
	if err != nil {
		return report, err
	}
	goplsPath, err = commandOutput(ctx, "", "sh", "-c", `command -v "$1"`, "sh", goplsPath)
	if err != nil {
		return report, err
	}
	restorePath, err := setEnvironment("PATH", filepath.Dir(goplsPath)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err != nil {
		return report, err
	}
	defer restorePath()
	report.GoplsVersion, err = commandOutput(ctx, "", goplsPath, "version")
	if err != nil {
		return report, err
	}
	report.SourceRevision, err = commandOutput(ctx, "", "git", "rev-parse", "HEAD")
	if err != nil {
		return report, err
	}
	diff, err := commandOutput(ctx, "", "git", "diff", "--no-ext-diff", "HEAD", "--")
	if err != nil {
		return report, err
	}
	diffHash := sha256.Sum256([]byte(diff))
	report.SourceDiffSHA256 = hex.EncodeToString(diffHash[:])
	report.Kernel, err = commandOutput(ctx, "", "uname", "-sr")
	if err != nil {
		return report, err
	}
	report.FixtureSHA256, err = fixtureChecksum()
	if err != nil {
		return report, err
	}
	if cfg.Seed != "" {
		report.SeedRevision, err = commandOutput(ctx, cfg.Seed, "git", "rev-parse", "HEAD")
		if err != nil {
			return report, err
		}
	}
	report.WorkloadFingerprint = workloadFingerprint(report)
	workspaces := make([]workspace, cfg.Workspaces)
	for i := range workspaces {
		workspaces[i], err = prepareWorkspace(ctx, filepath.Join(output, fmt.Sprintf("workspace-%02d", i)), cfg.Seed)
		if err != nil {
			return report, err
		}
	}
	goEnv, err := commandOutput(ctx, workspaces[0].root, "go", "env", "-json",
		"GOOS", "GOARCH", "GOVERSION", "GOROOT", "GOMODCACHE", "GOPATH", "GOFLAGS", "GOWORK", "GOTOOLCHAIN")
	if err != nil {
		return report, err
	}
	report.GoEnvironment = json.RawMessage(goEnv)

	for _, name := range []string{"operations.jsonl", "actions.jsonl"} {
		file, err := os.Create(filepath.Join(output, name))
		if err != nil {
			return report, err
		}
		files = append(files, file)
	}
	driver.operations = json.NewEncoder(files[0])
	actions := json.NewEncoder(files[1])
	driver.activities = lsp.NewLSPActivities(func(language string) lsp.LSPClient {
		client := &lsp.Jsonrpc2LSPClient{LanguageName: language}
		return &actionRecordingClient{LSPClient: client, log: actions}
	})
	monitor, err = startMonitor(filepath.Join(output, "memory.jsonl"), cfg.SampleInterval, cfg.SteadyWindow, cfg.DaemonNames, cancel)
	if err != nil {
		return report, err
	}
	workloadStart := time.Now()
	for cycle := 0; cfg.Cycles == 0 || cycle < cfg.Cycles; cycle++ {
		if cfg.Duration > 0 && time.Since(workloadStart) >= cfg.Duration {
			break
		}
		monitor.setPhase("active")
		for i := range workspaces {
			if err := driver.cycle(ctx, &workspaces[i], i, cycle); err != nil {
				return report, err
			}
		}
		report.CompletedCycles++
		if report.CompletedCycles == 1 || report.CompletedCycles%10 == 0 {
			fmt.Fprintf(os.Stderr, "completed %d cycles across %d workspaces\n", report.CompletedCycles, cfg.Workspaces)
		}
		if report.CompletedCycles%cfg.IdleEvery == 0 {
			monitor.setPhase("idle")
			if err := wait(ctx, cfg.Idle); err != nil {
				return report, err
			}
		}
	}
	if report.CompletedCycles == 0 {
		return report, fmt.Errorf("duration expired before any complete workload cycle")
	}
	monitor.setPhase("finalIdle")
	if err := wait(ctx, cfg.FinalIdle); err != nil {
		return report, err
	}
	return report, nil
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func main() {
	cfg := defaultConfig()
	flag.StringVar(&cfg.Label, "label", cfg.Label, "run label")
	flag.StringVar(&cfg.Output, "output", cfg.Output, "new artifact directory")
	flag.StringVar(&cfg.Seed, "seed", "", "Git checkout to clone at HEAD; uncommitted files are not copied")
	flag.IntVar(&cfg.Workspaces, "workspaces", cfg.Workspaces, "independent working directories")
	flag.IntVar(&cfg.Cycles, "cycles", cfg.Cycles, "query/edit cycles; zero runs until duration expires")
	flag.DurationVar(&cfg.Duration, "duration", 0, "workload time limit, excluding setup and final idle")
	flag.IntVar(&cfg.IdleEvery, "idle-every", cfg.IdleEvery, "cycles between idle periods")
	flag.DurationVar(&cfg.Idle, "idle", cfg.Idle, "periodic idle duration")
	flag.DurationVar(&cfg.FinalIdle, "final-idle", cfg.FinalIdle, "final idle duration before cleanup")
	flag.DurationVar(&cfg.SampleInterval, "sample", cfg.SampleInterval, "process memory sampling interval")
	flag.DurationVar(&cfg.SteadyWindow, "steady-window", cfg.SteadyWindow, "final idle window for steady-state median")
	flag.DurationVar(&cfg.OperationLimit, "operation-timeout", cfg.OperationLimit, "timeout for each desktop operation")
	flag.StringVar(&cfg.DaemonNames, "daemon-names", cfg.DaemonNames, "shared-daemon executable basenames, comma-separated")
	flag.BoolVar(&cfg.AllowExisting, "allow-existing", false, "include preexisting analysis processes; invalidates clean comparisons")
	compare := flag.String("compare", "", "baseline report.json to compare after a successful run")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report, err := runBenchmark(ctx, cfg)
	if err == nil && *compare != "" {
		err = writeComparison(*compare, report)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("report: %s\npeak analysis RSS: %.1f MiB; steady analysis RSS: %.1f MiB\n",
		filepath.Join(report.Config.Output, "report.json"),
		float64(report.Memory.PeakRSSBytes)/(1024*1024), float64(report.Memory.SteadyRSSBytes)/(1024*1024))
}
