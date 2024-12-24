package sqlite

import (
	"database/sql"
)

type Storage struct {
	db *sql.DB
}

func NewStorage(db *sql.DB) *Storage {
	return &Storage{db: db}
}

/* TODO
// Ensure Storage implements SubflowStorage interface
var _ srv.Storage = Storage{}
*/
