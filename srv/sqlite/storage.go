package sqlite

import (
	"database/sql"
)

type Storage struct {
	db   *sql.DB
	kvDb *sql.DB
}

func NewStorage(db, kvDb *sql.DB) *Storage {
	return &Storage{db: db, kvDb: kvDb}
}

/* TODO
// Ensure Storage implements SubflowStorage interface
var _ srv.Storage = (*Storage)(nil)
*/
