package sqlite

import (
	"context"
	"database/sql"

	"github.com/sidekick-agent/sidekick/domain"
)

type Storage struct {
	db *sql.DB
}

func NewStorage(db *sql.DB) *Storage {
	return &Storage{db: db}
}

// Ensure Storage implements FlowStorage interface
var _ domain.FlowStorage = (*Storage)(nil)

func (s *Storage) PersistFlow(ctx context.Context, flow domain.Flow) error {
	// TODO: Implement PersistFlow
	panic("not implemented")
}

func (s *Storage) GetFlow(ctx context.Context, workspaceId, flowId string) (domain.Flow, error) {
	// TODO: Implement GetFlow
	panic("not implemented")
}

func (s *Storage) GetFlowsForTask(ctx context.Context, workspaceId, taskId string) ([]domain.Flow, error) {
	// TODO: Implement GetFlowsForTask
	panic("not implemented")
}
