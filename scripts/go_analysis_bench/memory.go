package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type processMemory struct {
	PID                 int      `json:"pid"`
	PPID                int      `json:"ppid"`
	StartTicks          uint64   `json:"startTicks"`
	Executable          string   `json:"executable"`
	Command             string   `json:"command"`
	RSSBytes            uint64   `json:"rssBytes"`
	MetadataUnavailable []string `json:"metadataUnavailable,omitempty"`
}

type processID struct {
	pid   int
	start uint64
}

func (p processMemory) identity() processID {
	return processID{p.PID, p.StartTicks}
}

func parseProcStat(data []byte, pageSize uint64) (processMemory, error) {
	text := string(data)
	open, close := strings.IndexByte(text, '('), strings.LastIndexByte(text, ')')
	if open < 1 || close <= open {
		return processMemory{}, fmt.Errorf("invalid proc stat: %q", text)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(text[:open]))
	if err != nil {
		return processMemory{}, err
	}
	fields := strings.Fields(text[close+1:])
	if len(fields) < 22 {
		return processMemory{}, fmt.Errorf("truncated proc stat for PID %d", pid)
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil {
		return processMemory{}, err
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return processMemory{}, err
	}
	pages, err := strconv.ParseUint(fields[21], 10, 64)
	if err != nil {
		return processMemory{}, err
	}
	if pageSize == 0 || pages > ^uint64(0)/pageSize {
		return processMemory{}, fmt.Errorf("invalid RSS for PID %d", pid)
	}
	return processMemory{
		PID: pid, PPID: parent, StartTicks: start,
		Executable: text[open+1 : close], RSSBytes: pages * pageSize,
	}, nil
}

func readProcesses() ([]processMemory, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("process accounting requires Linux /proc")
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var processes []processMemory
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		dir := filepath.Join("/proc", entry.Name())
		status, err := os.ReadFile(filepath.Join(dir, "status"))
		if processGone(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		uid := ""
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "Uid:") {
				fields := strings.Fields(line)
				if len(fields) > 1 {
					uid = fields[1]
				}
			}
		}
		if uid == "" {
			return nil, fmt.Errorf("missing UID in %s/status", dir)
		}
		if uid != strconv.Itoa(os.Getuid()) {
			continue
		}
		process, err := readProcessStat(filepath.Join(dir, "stat"), pid, os.ReadFile)
		if processGone(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		process = enrichProcessMetadata(process, os.Readlink, os.ReadFile)
		processes = append(processes, process)
	}
	return processes, nil
}

type processTracker struct {
	root        int
	known       map[processID]bool
	daemonNames map[string]bool
}

func newProcessTracker(root int) *processTracker {
	return &processTracker{
		root: root, known: make(map[processID]bool),
		daemonNames: map[string]bool{"gopls": true},
	}
}

func (t *processTracker) selectProcesses(all []processMemory) ([]processMemory, uint64) {
	selected := map[int]bool{t.root: true}
	live := make(map[processID]bool, len(all))
	for _, p := range all {
		live[p.identity()] = true
		if t.known[p.identity()] || t.daemonNames[filepath.Base(p.Executable)] {
			selected[p.PID] = true
		}
	}
	for id := range t.known {
		if !live[id] {
			delete(t.known, id)
		}
	}
	// A daemon can be reparented to init; remembered identities retain its helpers.
	for changed := true; changed; {
		changed = false
		for _, p := range all {
			if selected[p.PPID] && !selected[p.PID] {
				selected[p.PID] = true
				changed = true
			}
		}
	}
	var result []processMemory
	var total uint64
	for _, p := range all {
		if selected[p.PID] && p.PID != t.root {
			t.known[p.identity()] = true
			result = append(result, p)
			total += p.RSSBytes
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].PID < result[j].PID })
	return result, total
}

type memorySample struct {
	At               time.Time       `json:"at"`
	Phase            string          `json:"phase"`
	AnalysisRSSBytes uint64          `json:"analysisRssBytes"`
	HarnessRSSBytes  uint64          `json:"harnessRssBytes"`
	Processes        []processMemory `json:"processes"`
}

type memoryMonitor struct {
	mu      sync.Mutex
	phase   string
	stats   memoryAccumulator
	err     error
	tracker *processTracker
	file    *os.File
	stop    chan struct{}
	done    chan struct{}
}

func startMonitor(path string, interval, steadyWindow time.Duration, daemonNames string, cancel context.CancelFunc) (*memoryMonitor, error) {
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	m := &memoryMonitor{
		phase: "startup", tracker: newProcessTracker(os.Getpid()), file: file,
		stats: memoryAccumulator{window: steadyWindow},
		stop:  make(chan struct{}), done: make(chan struct{}),
	}
	for _, name := range strings.Split(daemonNames, ",") {
		m.tracker.daemonNames[name] = true
	}
	if err := m.sample(); err != nil {
		file.Close()
		return nil, err
	}
	go func() {
		defer close(m.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-m.stop:
				return
			case <-ticker.C:
				if err := m.sample(); err != nil {
					m.mu.Lock()
					m.err = err
					m.mu.Unlock()
					cancel()
					return
				}
			}
		}
	}()
	return m, nil
}

func (m *memoryMonitor) sample() error {
	all, err := readProcesses()
	if err != nil {
		return err
	}
	processes, total := m.tracker.selectProcesses(all)
	sample := memorySample{At: time.Now(), AnalysisRSSBytes: total, Processes: processes}
	for _, p := range all {
		if p.PID == os.Getpid() {
			sample.HarnessRSSBytes = p.RSSBytes
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sample.Phase = m.phase
	if err := json.NewEncoder(m.file).Encode(sample); err != nil {
		return err
	}
	m.stats.observe(sample)
	return nil
}

func (m *memoryMonitor) setPhase(phase string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.phase = phase
}

func (m *memoryMonitor) finish() (memorySummary, error) {
	close(m.stop)
	<-m.done
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.file.Close(); err != nil && m.err == nil {
		m.err = err
	}
	result, err := m.stats.summary()
	if m.err != nil {
		return result, m.err
	}
	return result, err
}

// Cleanup ownership is independent of accounting, which includes unrelated
// same-user daemons so their memory cannot silently disappear from the report.
func cleanupProcesses(token string) error {
	if token == "" {
		return fmt.Errorf("refusing cleanup without a run token")
	}
	deadline := time.Now().Add(3 * time.Second)
	quiet := 0
	for {
		all, err := readProcesses()
		if err != nil {
			return err
		}
		owned, err := ownedProcesses(all, os.Getpid(), token, readProcessEnvironment)
		if err != nil {
			return err
		}
		if len(owned) == 0 {
			quiet++
			if quiet == 3 {
				return nil
			}
		} else {
			quiet = 0
		}
		for _, p := range owned {
			handle, err := os.FindProcess(p.PID)
			if err != nil {
				if processGone(err) {
					continue
				}
				return err
			}
			err = killOwnedProcess(handle, p, token)
			handle.Release()
			if err != nil && !processGone(err) && err != os.ErrProcessDone {
				return fmt.Errorf("cleanup PID %d: %w", p.PID, err)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("owned analysis processes did not exit within cleanup deadline")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

const runTokenEnv = "SIDEKICK_GO_ANALYSIS_BENCH_RUN"

func readProcessEnvironment(pid int) ([]byte, error) {
	return os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
}

func ownedProcesses(all []processMemory, root int, token string, readEnv func(int) ([]byte, error)) ([]processMemory, error) {
	if token == "" {
		return nil, fmt.Errorf("refusing cleanup without a run token")
	}
	var owned []processMemory
	for _, p := range all {
		if p.PID == root {
			continue
		}
		data, err := readEnv(p.PID)
		if processGone(err) || os.IsPermission(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range strings.Split(string(data), "\x00") {
			if entry == runTokenEnv+"="+token {
				owned = append(owned, p)
				break
			}
		}
	}
	return owned, nil
}

func killOwnedProcess(handle *os.Process, expected processMemory, token string) error {
	current, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", expected.PID))
	if err != nil {
		return err
	}
	now, err := parseProcStat(current, uint64(os.Getpagesize()))
	if err != nil || now.identity() != expected.identity() {
		return err
	}
	owned, err := ownedProcesses([]processMemory{now}, os.Getpid(), token, readProcessEnvironment)
	if err != nil || len(owned) == 0 {
		return err
	}
	return handle.Kill()
}

func enrichProcessMetadata(process processMemory, readLink func(string) (string, error), readFile func(string) ([]byte, error)) processMemory {
	dir := fmt.Sprintf("/proc/%d", process.PID)
	executable, err := readLink(filepath.Join(dir, "exe"))
	if err == nil {
		process.Executable = strings.TrimSuffix(executable, " (deleted)")
	} else {
		process.MetadataUnavailable = append(process.MetadataUnavailable, "exe: "+err.Error())
	}
	command, err := readFile(filepath.Join(dir, "cmdline"))
	if err == nil {
		process.Command = strings.TrimSpace(strings.ReplaceAll(string(command), "\x00", " "))
	} else {
		process.MetadataUnavailable = append(process.MetadataUnavailable, "cmdline: "+err.Error())
	}
	return process
}

func processGone(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH)
}

func readProcessStat(path string, expectedPID int, readFile func(string) ([]byte, error)) (processMemory, error) {
	for attempt := 0; attempt < 2; attempt++ {
		data, err := readFile(path)
		if err != nil {
			return processMemory{}, err
		}
		process, err := parseProcStat(data, uint64(os.Getpagesize()))
		if err != nil {
			return processMemory{}, err
		}
		if process.PID == expectedPID {
			return process, nil
		}
	}
	return processMemory{}, fmt.Errorf("stat PID does not match directory identity: %s", path)
}
