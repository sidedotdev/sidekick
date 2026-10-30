package temporal

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"go.temporal.io/api/history/v1"
	"google.golang.org/protobuf/proto"
)

// History snapshot files start with the workflow ID so a snapshot can never be
// replayed against, or attributed to, a different workflow by mistake.
const historySnapshotHeaderSep = '\n'

// EncodeHistorySnapshot serializes a workflow history together with the ID of
// the workflow it belongs to.
func EncodeHistorySnapshot(workflowId string, hist *history.History) ([]byte, error) {
	if workflowId == "" {
		return nil, errors.New("history snapshot requires a workflow id")
	}
	if strings.ContainsRune(workflowId, historySnapshotHeaderSep) {
		return nil, fmt.Errorf("workflow id %q contains a newline", workflowId)
	}
	data, err := proto.Marshal(hist)
	if err != nil {
		return nil, fmt.Errorf("failed to encode history snapshot: %w", err)
	}
	return append([]byte(workflowId+string(historySnapshotHeaderSep)), data...), nil
}

// DecodeHistorySnapshot parses a snapshot written by EncodeHistorySnapshot and
// returns the workflow ID it was captured from.
func DecodeHistorySnapshot(data []byte) (string, *history.History, error) {
	sep := bytes.IndexByte(data, byte(historySnapshotHeaderSep))
	if sep < 0 {
		return "", nil, errors.New("history snapshot is missing its workflow id header")
	}
	workflowId := string(data[:sep])
	var hist history.History
	if err := proto.Unmarshal(data[sep+1:], &hist); err != nil {
		return "", nil, fmt.Errorf("failed to decode history snapshot: %w", err)
	}
	return workflowId, &hist, nil
}

// DecodeHistorySnapshotFor is DecodeHistorySnapshot with a check that the
// snapshot was captured from the expected workflow.
func DecodeHistorySnapshotFor(workflowId string, data []byte) (*history.History, error) {
	cachedId, hist, err := DecodeHistorySnapshot(data)
	if err != nil {
		return nil, err
	}
	if cachedId != workflowId {
		return nil, fmt.Errorf("history snapshot belongs to workflow %q, not %q", cachedId, workflowId)
	}
	return hist, nil
}
