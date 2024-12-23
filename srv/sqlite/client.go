package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	zlog "github.com/rs/zerolog/log"
	_ "modernc.org/sqlite"
)

type Client struct {
	db *sql.DB
}

func NewClient(dbPath string) (*Client, error) {
	zlog.Info().Msg("Initializing SQLite client")

	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping SQLite database: %w", err)
	}

	zlog.Info().Msg("SQLite client initialized successfully")
	return &Client{db: db}, nil
}

func (c *Client) Close() error {
	zlog.Info().Msg("Closing SQLite connection")
	return c.db.Close()
}

func (c *Client) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	zlog.Debug().Str("query", query).Msg("Executing SQLite query")
	return c.db.ExecContext(ctx, query, args...)
}

func (c *Client) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	zlog.Debug().Str("query", query).Msg("Executing SQLite query")
	return c.db.QueryContext(ctx, query, args...)
}

func (c *Client) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	zlog.Debug().Str("query", query).Msg("Executing SQLite query")
	return c.db.QueryRowContext(ctx, query, args...)
}

func (c *Client) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	zlog.Debug().Msg("Beginning SQLite transaction")
	return c.db.BeginTx(ctx, opts)
}

func (c *Client) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	zlog.Debug().Str("query", query).Msg("Preparing SQLite statement")
	return c.db.PrepareContext(ctx, query)
}
