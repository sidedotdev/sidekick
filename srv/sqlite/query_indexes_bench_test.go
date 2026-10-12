package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sidekick/domain"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type indexCandidate struct {
	name       string
	table      string
	definition string
}

var indexCandidates = []indexCandidate{
	{"idx_flow_actions_workspace_flow", "flow_actions", "(workspace_id, flow_id)"},
	{"idx_worktrees_workspace_flow", "worktrees", "(workspace_id, flow_id)"},
	{"idx_flows_workspace_parent_created", "flows", "(workspace_id, parent_id, created, id)"},
	{"idx_tasks_active_workspace_status", "tasks", "(workspace_id, status) WHERE archived IS NULL"},
	{"idx_tasks_archived_workspace_order", "tasks", "(workspace_id, archived DESC, updated DESC) WHERE archived IS NOT NULL"},
}

type indexReadResult struct {
	rows  any
	total int64
}

type indexReadProbe struct {
	name     string
	expected int
	total    int64
	run      func(*Storage) (indexReadResult, error)
}

// BenchmarkQueryIndexes is opt-in via -bench. It uses fresh file-backed databases
// without background maintenance so each candidate has an isolated control.
func BenchmarkQueryIndexes(b *testing.B) {
	for _, size := range []string{"small", "large"} {
		b.Run(size, func(b *testing.B) {
			for _, candidate := range indexCandidates {
				b.Run(candidate.name, func(b *testing.B) {
					benchmarkIndexCandidate(b, candidate, size)
				})
			}
		})
	}
}

func benchmarkIndexCandidate(b *testing.B, candidate indexCandidate, size string) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", sqliteUri(filepath.Join(b.TempDir(), "indexes.db")))
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, db.Close()) })
	db.SetMaxOpenConns(1)
	require.NoError(b, migrateUp(db, "index-benchmark"))
	s := &Storage{db: &trackedDB{DB: db, name: "main", tracker: newBusyTracker()}}

	for _, index := range indexCandidates {
		_, err := db.Exec("DROP INDEX IF EXISTS " + index.name)
		require.NoError(b, err)
	}
	rows, fanout := seedIndexBenchmark(b, db, candidate.table, size)
	analysisLimit := 400
	if os.Getenv("SIDE_INDEX_BENCH_FULL_ANALYZE") == "1" {
		analysisLimit = 0
	}
	_, err = db.Exec(fmt.Sprintf("PRAGMA analysis_limit=%d; ANALYZE; VACUUM", analysisLimit))
	require.NoError(b, err)

	var version string
	require.NoError(b, db.QueryRow("SELECT sqlite_version()").Scan(&version))
	b.Logf("SQLite %s; table=%s rows=%d fanout=%d; analysis_limit=%d; 10 workspaces",
		version, candidate.table, rows, fanout, analysisLimit)

	var baselineBytes int64
	usedBytes := func() int64 {
		var pages, free, pageSize int64
		require.NoError(b, db.QueryRow("PRAGMA page_count").Scan(&pages))
		require.NoError(b, db.QueryRow("PRAGMA freelist_count").Scan(&free))
		require.NoError(b, db.QueryRow("PRAGMA page_size").Scan(&pageSize))
		return (pages - free) * pageSize
	}
	baselineBytes = usedBytes()
	variants := []string{"baseline", "indexed"}
	if os.Getenv("SIDE_INDEX_BENCH_REVERSE") == "1" {
		variants = []string{"indexed", "baseline"}
	}
	fingerprints := make(map[string][32]byte)
	probes := indexReadProbes(candidate.table, rows, fanout)

	// Complete both read controls before writes can change the fixture.
	for _, phase := range []string{"read", "upsert"} {
		for _, variant := range variants {
			_, err := db.Exec("DROP INDEX IF EXISTS " + candidate.name)
			require.NoError(b, err)
			before := usedBytes()
			var build time.Duration
			var indexBytes int64
			if variant == "indexed" {
				start := time.Now()
				_, err = db.Exec("CREATE INDEX " + candidate.name + " ON " + candidate.table + candidate.definition)
				build = time.Since(start)
				require.NoError(b, err)
				indexBytes = usedBytes() - before
			}
			_, err = db.Exec("ANALYZE")
			require.NoError(b, err)
			_, err = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
			require.NoError(b, err)

			report := func(b *testing.B) {
				b.ReportMetric(float64(indexBytes), "index-B")
				b.ReportMetric(float64(baselineBytes), "baseline-B")
				b.ReportMetric(float64(build.Microseconds())/1000, "build-ms")
			}
			if phase == "read" {
				for _, probe := range probes {
					result, err := probe.run(s)
					require.NoError(b, err)
					require.Equal(b, probe.expected, reflect.ValueOf(result.rows).Len())
					require.Equal(b, probe.total, result.total)
					hash := indexResultFingerprint(b, result)
					if previous, ok := fingerprints[probe.name]; ok {
						require.Equal(b, previous, hash, "index must not change results")
					} else {
						fingerprints[probe.name] = hash
					}
					b.Run(variant+"/"+probe.name, func(b *testing.B) {
						b.ReportAllocs()
						b.ResetTimer()
						for i := 0; i < b.N; i++ {
							if _, err := probe.run(s); err != nil {
								b.Fatal(err)
							}
						}
						b.StopTimer()
						report(b)
					})
				}
			} else {
				b.Run(variant+"/upsert", func(b *testing.B) {
					// Use identical serialized records and warmed pages in both variants.
					for i := 0; i < b.N; i++ {
						require.NoError(b, persistIndexBenchmarkRow(ctx, s, candidate.table, (i*7919)%rows, fanout))
					}
					_, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
					require.NoError(b, err)
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if err := persistIndexBenchmarkRow(ctx, s, candidate.table, (i*7919)%rows, fanout); err != nil {
							b.Fatal(err)
						}
					}
					b.StopTimer()
					report(b)
				})
			}
		}
	}
}

func seedIndexBenchmark(b *testing.B, db *sql.DB, table, size string) (int, int) {
	b.Helper()
	rows, fanout := 1000, 2
	switch table {
	case "flow_actions":
		rows, fanout = 10000, 100
		if size == "large" {
			rows, fanout = 375000, 750
		}
	case "worktrees":
		rows, fanout = 100, 1
		if size == "large" {
			rows = 10000
		}
	case "flows":
		if size == "large" {
			rows = 10000
		}
	case "tasks":
		fanout = 1
		if size == "large" {
			rows = 20000
		}
	default:
		b.Fatalf("unknown fixture table %q", table)
	}
	ws := "printf('ws_%027d', n%10)"
	id := "printf('id_%027d', n)"
	parent := fmt.Sprintf("printf('id_%%027d', (n/10)/%d*10+n%%10)", fanout)
	stamp := "strftime('%Y-%m-%dT%H:%M:%SZ', '2026-01-01', '+'||n||' seconds')"
	var insert string
	switch table {
	case "flow_actions":
		insert = `INSERT INTO flow_actions
			(id, workspace_id, flow_id, subflow_name, subflow_description, subflow_id,
			 created, updated, action_type, action_params, action_status, action_result,
			 is_human_action, is_callback_action)
			SELECT ` + id + `, ` + ws + `, ` + parent + `, '', '', '', ` + stamp + `, ` + stamp + `,
			 'test', '{}', 'complete', ?2, 0, 0 FROM fixture`
	case "worktrees":
		insert = `INSERT INTO worktrees (id, workspace_id, flow_id, name, created, working_directory)
			SELECT ` + id + `, ` + ws + `, ` + parent + `, 'test', ` + stamp + `, '' FROM fixture`
	case "flows":
		insert = `INSERT INTO flows (id, workspace_id, parent_id, type, status, created, updated)
			SELECT ` + id + `, ` + ws + `, ` + parent + `, 'test', 'complete', ` + stamp + `, ` + stamp + ` FROM fixture`
	case "tasks":
		insert = `INSERT INTO tasks
			(id, workspace_id, title, description, status, links, agent_type, flow_type,
			 archived, created, updated, flow_options)
			SELECT ` + id + `, ` + ws + `, 'test', ?2,
			 CASE WHEN (n/10)%40=0 THEN 'in_progress' ELSE 'complete' END,
			 '[]', 'llm', 'test', CASE WHEN (n/10)%20=0 THEN NULL ELSE ` + stamp + ` END,
			 ` + stamp + `, ` + stamp + `, '{}' FROM fixture`
	}
	query := `WITH RECURSIVE fixture(n) AS
		(VALUES(0) UNION ALL SELECT n+1 FROM fixture WHERE n+1 < ?1) ` + insert
	args := []any{rows}
	if table == "flow_actions" || table == "tasks" {
		args = append(args, strings.Repeat("x", 1024))
	}
	_, err := db.Exec(query, args...)
	require.NoError(b, err)
	var count int
	require.NoError(b, db.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&count))
	require.Equal(b, rows, count)
	return rows, fanout
}

func indexReadProbes(table string, rows, fanout int) []indexReadProbe {
	ctx := context.Background()
	ws, id := fmt.Sprintf("ws_%027d", 0), fmt.Sprintf("id_%027d", 0)
	switch table {
	case "flow_actions":
		return []indexReadProbe{{"list", fanout, 0, func(s *Storage) (indexReadResult, error) {
			rows, err := s.GetFlowActions(ctx, ws, id)
			return indexReadResult{rows: rows}, err
		}}}
	case "flows":
		return []indexReadProbe{{"list", fanout, 0, func(s *Storage) (indexReadResult, error) {
			rows, err := s.GetFlowsForTask(ctx, ws, id)
			return indexReadResult{rows: rows}, err
		}}}
	case "worktrees":
		return []indexReadProbe{
			{"flow", 1, 0, func(s *Storage) (indexReadResult, error) {
				rows, err := s.GetWorktreesForFlow(ctx, ws, id)
				return indexReadResult{rows: rows}, err
			}},
			{"workspace", rows / 10, 0, func(s *Storage) (indexReadResult, error) {
				rows, err := s.GetWorktrees(ctx, ws)
				return indexReadResult{rows: rows}, err
			}},
		}
	case "tasks":
		active := (rows/10 + 19) / 20
		archived := int64(rows/10 - active)
		return []indexReadProbe{
			{"active", active, 0, func(s *Storage) (indexReadResult, error) {
				rows, err := s.GetTasks(ctx, ws, nil)
				return indexReadResult{rows: rows}, err
			}},
			{"status", (rows/10 + 39) / 40, 0, func(s *Storage) (indexReadResult, error) {
				rows, err := s.GetTasks(ctx, ws, []domain.TaskStatus{"in_progress"})
				return indexReadResult{rows: rows}, err
			}},
			{"archive", 25, archived, func(s *Storage) (indexReadResult, error) {
				rows, total, err := s.GetArchivedTasks(ctx, ws, 1, 25)
				return indexReadResult{rows: rows, total: total}, err
			}},
		}
	default:
		panic("unknown fixture table: " + table)
	}
}

func indexResultFingerprint(b *testing.B, result indexReadResult) [32]byte {
	b.Helper()
	data, err := json.Marshal(result.rows)
	require.NoError(b, err)
	var rows []json.RawMessage
	require.NoError(b, json.Unmarshal(data, &rows))
	encoded := make([]string, len(rows))
	for i, row := range rows {
		encoded[i] = string(row)
	}
	sort.Strings(encoded)
	data, err = json.Marshal(encoded)
	require.NoError(b, err)
	return sha256.Sum256(data)
}

func persistIndexBenchmarkRow(ctx context.Context, s *Storage, table string, i, fanout int) error {
	id := fmt.Sprintf("id_%027d", i)
	ws := fmt.Sprintf("ws_%027d", i%10)
	parent := fmt.Sprintf("id_%027d", (i/10)/fanout*10+i%10)
	stamp := time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC)
	switch table {
	case "flow_actions":
		return s.PersistFlowAction(ctx, domain.FlowAction{
			Id: id, WorkspaceId: ws, FlowId: parent, Created: stamp, Updated: stamp,
			ActionType: "test", ActionStatus: "complete", ActionParams: map[string]interface{}{},
			ActionResult: strings.Repeat("x", 1024),
		})
	case "worktrees":
		return s.PersistWorktree(ctx, domain.Worktree{
			Id: id, WorkspaceId: ws, FlowId: parent, Name: "test", Created: stamp,
		})
	case "flows":
		return s.PersistFlow(ctx, domain.Flow{
			Id: id, WorkspaceId: ws, ParentId: parent, Type: "test", Status: "complete",
			Created: stamp, Updated: stamp,
		})
	case "tasks":
		task := domain.Task{
			Id: id, WorkspaceId: ws, Title: "test", Description: strings.Repeat("x", 1024),
			Status: "complete", AgentType: "llm", FlowType: "test", Created: stamp, Updated: stamp,
		}
		if (i/10)%40 == 0 {
			task.Status = "in_progress"
		}
		if (i/10)%20 != 0 {
			task.Archived = &stamp
		}
		return s.PersistTask(ctx, task)
	default:
		return fmt.Errorf("unknown fixture table %q", table)
	}
}
