package domain

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

const MaxHistoryPageSize = 500

var ErrInvalidHistoryPage = errors.New("invalid history page")

type HistoryPageRequest struct {
	Limit  int
	Before string
}

type HistoryCursor struct {
	WorkspaceID string    `json:"workspaceId"`
	FlowID      string    `json:"flowId"`
	Kind        string    `json:"kind"`
	ID          string    `json:"id"`
	Created     time.Time `json:"created"`
}

func (p HistoryPageRequest) Cursor(workspaceID, flowID, kind string) (HistoryCursor, error) {
	cursor := HistoryCursor{WorkspaceID: workspaceID, FlowID: flowID, Kind: kind}
	if p.Limit < 1 || p.Limit > MaxHistoryPageSize || workspaceID == "" || flowID == "" {
		return cursor, ErrInvalidHistoryPage
	}
	if p.Before == "" {
		return cursor, nil
	}
	if len(p.Before) > 2048 {
		return cursor, ErrInvalidHistoryPage
	}
	data, err := base64.RawURLEncoding.DecodeString(p.Before)
	if err != nil {
		return cursor, ErrInvalidHistoryPage
	}
	var decoded HistoryCursor
	if json.Unmarshal(data, &decoded) != nil || decoded.ID == "" ||
		decoded.WorkspaceID != workspaceID || decoded.FlowID != flowID || decoded.Kind != kind {
		return cursor, ErrInvalidHistoryPage
	}
	return decoded, nil
}

func (c HistoryCursor) Encode() string {
	data, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(data)
}

type FlowActionsPage struct {
	FlowActions []FlowAction `json:"flowActions"`
	NextCursor  string       `json:"nextCursor"`
}

type SubflowsPage struct {
	Subflows   []Subflow `json:"subflows"`
	NextCursor string    `json:"nextCursor"`
}

type FlowHistoryStorage interface {
	GetFlowActionsPage(ctx context.Context, workspaceID, flowID string, page HistoryPageRequest) (FlowActionsPage, error)
	GetSubflowsPage(ctx context.Context, workspaceID, flowID string, page HistoryPageRequest) (SubflowsPage, error)
}
