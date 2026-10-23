package common

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/segmentio/ksuid"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/activity"
)

func codecScanPrefix(id string) (string, error) {
	if id == "" || strings.ContainsAny(id, "/\\") {
		return "", fmt.Errorf("invalid codec scan ID")
	}
	return "scans/" + id + "/", nil
}

func codecScanQuery(at time.Time) string {
	return fmt.Sprintf("StartTime <= '%s'", at.UTC().Format(time.RFC3339Nano))
}

func (a *CodecCleanupActivities) readCodecScan(ctx context.Context, id string) (codecScanManifest, error) {
	prefix, err := codecScanPrefix(id)
	if err != nil {
		return codecScanManifest{}, err
	}
	values, err := a.Storage.MGet(ctx, codecScanWorkspace, []string{prefix + "manifest"})
	if err != nil {
		return codecScanManifest{}, err
	}
	if len(values) != 1 || len(values[0]) == 0 {
		return codecScanManifest{}, fmt.Errorf("codec scan %q does not exist", id)
	}
	var manifest codecScanManifest
	if err := json.Unmarshal(values[0], &manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func (a *CodecCleanupActivities) writeCodecScan(ctx context.Context, manifest codecScanManifest) error {
	prefix, err := codecScanPrefix(manifest.ScanID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	return a.Storage.MSetRaw(ctx, codecScanWorkspace, map[string][]byte{prefix + "manifest": data})
}

func (a *CodecCleanupActivities) BeginCodecScan(ctx context.Context, input CodecScanInput) (CodecScanPageResult, error) {
	prefix, err := codecScanPrefix(input.ScanID)
	if err != nil {
		return CodecScanPageResult{}, err
	}
	if input.StartedAt.IsZero() {
		return CodecScanPageResult{}, fmt.Errorf("codec scan start time is required")
	}
	if input.Retention <= 0 {
		input.Retention = DefaultCodecCleanupRetention
	}
	if _, ok := a.Storage.(SizedKeyScanner); !ok {
		return CodecScanPageResult{}, fmt.Errorf("storage does not support sized key scans")
	}
	existing, err := a.Storage.MGet(ctx, codecScanWorkspace, []string{prefix + "manifest"})
	if err != nil {
		return CodecScanPageResult{}, err
	}
	if len(existing) == 1 && len(existing[0]) > 0 {
		var manifest codecScanManifest
		if err := json.Unmarshal(existing[0], &manifest); err != nil {
			return CodecScanPageResult{}, fmt.Errorf("read existing codec scan: %w", err)
		}
		if manifest.ScanID != input.ScanID || manifest.DryRun != input.DryRun ||
			!manifest.StartedAt.Equal(input.StartedAt) || manifest.Retention != input.Retention {
			return CodecScanPageResult{}, fmt.Errorf("conflicting parameters for codec scan %q", input.ScanID)
		}
		return CodecScanPageResult{}, nil
	}
	count, err := a.TemporalClient.CountWorkflow(ctx, &workflowservice.CountWorkflowExecutionsRequest{
		Query: codecScanQuery(input.StartedAt),
	})
	if err != nil {
		return CodecScanPageResult{}, err
	}
	// The cleanup execution itself must be visible before any deletion is safe.
	if count == nil || count.Count <= 0 {
		return CodecScanPageResult{}, fmt.Errorf("empty visibility inventory; refusing codec cleanup")
	}
	err = a.writeCodecScan(ctx, codecScanManifest{CodecScanInput: input, ExpectedExecutions: count.Count})
	return CodecScanPageResult{}, err
}

func (a *CodecCleanupActivities) MarkCodecScanPage(ctx context.Context, input CodecScanPageInput) (CodecScanPageResult, error) {
	manifest, err := a.readCodecScan(ctx, input.ScanID)
	if err != nil {
		return CodecScanPageResult{}, err
	}
	if manifest.Sealed {
		return CodecScanPageResult{}, fmt.Errorf("codec scan is already sealed")
	}
	prefix, _ := codecScanPrefix(input.ScanID)
	resp, err := a.TemporalClient.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
		Query: codecScanQuery(manifest.StartedAt), PageSize: 50, NextPageToken: input.Token,
	})
	if err != nil {
		return CodecScanPageResult{}, err
	}
	if resp == nil || (len(resp.NextPageToken) > 0 && bytes.Equal(resp.NextPageToken, input.Token)) {
		return CodecScanPageResult{}, fmt.Errorf("invalid workflow inventory page")
	}
	marks := make(map[string][]byte)
	flush := func() error {
		if len(marks) == 0 {
			return nil
		}
		if err := a.Storage.MSetRaw(ctx, codecScanWorkspace, marks); err != nil {
			return err
		}
		clear(marks)
		activity.RecordHeartbeat(ctx)
		return nil
	}
	for _, execution := range resp.Executions {
		run := execution.GetExecution()
		if run.GetWorkflowId() == "" || run.GetRunId() == "" {
			return CodecScanPageResult{}, fmt.Errorf("workflow inventory contains an incomplete execution")
		}
		activity.RecordHeartbeat(ctx)
		iter := a.TemporalClient.GetWorkflowHistory(ctx, run.WorkflowId, run.RunId, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
		for n := 0; iter.HasNext(); n++ {
			event, err := iter.Next()
			if err != nil {
				return CodecScanPageResult{}, fmt.Errorf("read history %s/%s: %w", run.WorkflowId, run.RunId, err)
			}
			if event == nil {
				return CodecScanPageResult{}, fmt.Errorf("nil history event for %s/%s", run.WorkflowId, run.RunId)
			}
			for _, payload := range extractPayloadsFromEvent(event) {
				if key := string(payload.GetMetadata()[CodecMetadataKey]); key != "" {
					marks[prefix+"ref/"+key] = []byte{1}
					if len(marks) >= codecScanPageSize {
						if err := flush(); err != nil {
							return CodecScanPageResult{}, err
						}
					}
				}
			}
			if n%100 == 0 {
				activity.RecordHeartbeat(ctx)
			}
		}
	}
	if err := flush(); err != nil {
		return CodecScanPageResult{}, err
	}
	return CodecScanPageResult{
		NextToken: resp.NextPageToken, Done: len(resp.NextPageToken) == 0,
		Executions: int64(len(resp.Executions)),
	}, nil
}

func (a *CodecCleanupActivities) SealCodecScan(ctx context.Context, input CodecScanPageInput) (CodecScanPageResult, error) {
	manifest, err := a.readCodecScan(ctx, input.ScanID)
	if err != nil {
		return CodecScanPageResult{}, err
	}
	count, err := a.TemporalClient.CountWorkflow(ctx, &workflowservice.CountWorkflowExecutionsRequest{
		Query: codecScanQuery(manifest.StartedAt),
	})
	if err != nil {
		return CodecScanPageResult{}, err
	}
	if count == nil {
		return CodecScanPageResult{}, fmt.Errorf("workflow inventory count is missing; refusing codec cleanup")
	}
	if count.Count != manifest.ExpectedExecutions || input.Executions != manifest.ExpectedExecutions {
		return CodecScanPageResult{}, fmt.Errorf(
			"workflow inventory changed or was incomplete; refusing codec cleanup (initial=%d enumerated=%d final=%d cutoff=%s)",
			manifest.ExpectedExecutions, input.Executions, count.Count, manifest.StartedAt.UTC().Format(time.RFC3339Nano))
	}
	manifest.Sealed = true
	return CodecScanPageResult{Done: true}, a.writeCodecScan(ctx, manifest)
}

func (a *CodecCleanupActivities) SweepCodecScanPage(ctx context.Context, input CodecScanPageInput) (CodecScanPageResult, error) {
	manifest, err := a.readCodecScan(ctx, input.ScanID)
	if err != nil {
		return CodecScanPageResult{}, err
	}
	if !manifest.Sealed {
		return CodecScanPageResult{}, fmt.Errorf("codec scan must be sealed before sweeping")
	}
	scanner, ok := a.Storage.(SizedKeyScanner)
	if !ok {
		return CodecScanPageResult{}, fmt.Errorf("storage does not support sized key scans")
	}
	page, err := scanner.ScanKeys(ctx, CodecWorkspaceID, CodecKeyPrefix, input.Cursor, codecScanPageSize)
	if err != nil {
		return CodecScanPageResult{}, err
	}
	prefix, _ := codecScanPrefix(input.ScanID)
	cutoff := manifest.StartedAt.Add(-manifest.Retention)
	candidates := make([]KeySize, 0, len(page.Keys))
	markKeys := make([]string, 0, len(page.Keys))
	result := CodecScanPageResult{NextCursor: page.NextCursor, Done: page.Done}
	seen := make(map[string]bool)
	for _, key := range page.Keys {
		if seen[key.Key] {
			continue
		}
		seen[key.Key] = true
		id, err := ksuid.Parse(strings.TrimPrefix(key.Key, CodecKeyPrefix))
		if err != nil || !id.Time().Before(cutoff) {
			continue
		}
		if key.Bytes < 0 {
			return CodecScanPageResult{}, fmt.Errorf("invalid size for codec key %q", key.Key)
		}
		candidates = append(candidates, key)
		markKeys = append(markKeys, prefix+"ref/"+key.Key)
	}
	if len(candidates) == 0 {
		return result, nil
	}
	marked, err := a.Storage.MGet(ctx, codecScanWorkspace, markKeys)
	if err != nil {
		return CodecScanPageResult{}, err
	}
	if len(marked) != len(candidates) {
		return CodecScanPageResult{}, fmt.Errorf("incomplete reference lookup")
	}
	var batch []string
	var batchSizes []KeySize
	var batchBytes int64
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		activity.RecordHeartbeat(ctx)
		if a.RecordCandidates != nil {
			if err := a.RecordCandidates(ctx, input.ScanID, batchSizes); err != nil {
				return err
			}
		}
		if !manifest.DryRun {
			started := time.Now()
			if err := a.Storage.MDelete(ctx, CodecWorkspaceID, batch); err != nil {
				return err
			}
			if err := codecScanPause(ctx, max(10*time.Millisecond, 9*time.Since(started))); err != nil {
				return err
			}
		}
		result.Candidates += int64(len(batch))
		result.Bytes += batchBytes
		batch = nil
		batchSizes = nil
		batchBytes = 0
		return nil
	}
	for i, key := range candidates {
		if len(marked[i]) > 0 {
			continue
		}
		if key.Bytes > codecScanMaxBytes {
			result.SkippedOversize++
			continue
		}
		if len(batch) == codecScanPageSize || batchBytes+key.Bytes > codecScanMaxBytes {
			if err := flush(); err != nil {
				return CodecScanPageResult{}, err
			}
		}
		batch = append(batch, key.Key)
		batchSizes = append(batchSizes, key)
		batchBytes += key.Bytes
	}
	if err := flush(); err != nil {
		return CodecScanPageResult{}, err
	}
	return result, nil
}

func codecScanPause(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case <-ticker.C:
			activity.RecordHeartbeat(ctx)
		}
	}
}

func (a *CodecCleanupActivities) DropCodecScanPage(ctx context.Context, input CodecScanPageInput) (CodecScanPageResult, error) {
	prefix, err := codecScanPrefix(input.ScanID)
	if err != nil {
		return CodecScanPageResult{}, err
	}
	scanner, ok := a.Storage.(SizedKeyScanner)
	if !ok {
		return CodecScanPageResult{}, fmt.Errorf("storage does not support sized key scans")
	}
	page, err := scanner.ScanKeys(ctx, codecScanWorkspace, prefix, input.Cursor, codecScanPageSize)
	if err != nil {
		return CodecScanPageResult{}, err
	}
	for i := 0; i < len(page.Keys); i += codecScanPageSize {
		end := min(i+codecScanPageSize, len(page.Keys))
		keys := make([]string, 0, end-i)
		for _, key := range page.Keys[i:end] {
			keys = append(keys, key.Key)
		}
		if err := a.Storage.MDelete(ctx, codecScanWorkspace, keys); err != nil {
			return CodecScanPageResult{}, err
		}
	}
	return CodecScanPageResult{NextCursor: page.NextCursor, Done: page.Done}, nil
}
