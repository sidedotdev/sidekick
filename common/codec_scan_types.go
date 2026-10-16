package common

import "time"

const (
	codecScanWorkspace = "__temporal_codec_cleanup"
	codecScanPageSize  = 200
	codecScanMaxBytes  = 32 * 1024 * 1024
)

type CodecScanInput struct {
	ScanID    string        `json:"scanId"`
	StartedAt time.Time     `json:"startedAt"`
	Retention time.Duration `json:"retention"`
	DryRun    bool          `json:"dryRun"`
}

type CodecScanPageInput struct {
	ScanID     string `json:"scanId"`
	Token      []byte `json:"token,omitempty"`
	Cursor     string `json:"cursor,omitempty"`
	Executions int64  `json:"executions,omitempty"`
}

type CodecScanPageResult struct {
	NextToken       []byte `json:"nextToken,omitempty"`
	NextCursor      string `json:"nextCursor,omitempty"`
	Done            bool   `json:"done"`
	Executions      int64  `json:"executions,omitempty"`
	Candidates      int64  `json:"candidates,omitempty"`
	Bytes           int64  `json:"bytes,omitempty"`
	SkippedOversize int64  `json:"skippedOversize,omitempty"`
}

type codecScanManifest struct {
	CodecScanInput
	ExpectedExecutions int64 `json:"expectedExecutions"`
	Sealed             bool  `json:"sealed"`
}

type CodecScanWorkflowState struct {
	ScanID          string    `json:"scanId"`
	StartedAt       time.Time `json:"startedAt"`
	Phase           string    `json:"phase"`
	Token           []byte    `json:"token,omitempty"`
	Cursor          string    `json:"cursor,omitempty"`
	Executions      int64     `json:"executions"`
	Candidates      int64     `json:"candidates"`
	Bytes           int64     `json:"bytes"`
	SkippedOversize int64     `json:"skippedOversize"`
	Error           string    `json:"error,omitempty"`
}
