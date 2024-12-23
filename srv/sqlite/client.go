package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	zlog "github.com/rs/zerolog/log"
)

type Client struct {
	db *sql.DB
}

func NewClient(dbPath, dbName string) (*Client, error) {
	zlog.Debug().Msg("Initializing SQLite client")

	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	db, err := sql.Open(dbName, dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping SQLite database: %w", err)
	}

	client := &Client{db: db}

	//if err := client.Migrate(); err != nil {
	//	return nil, fmt.Errorf("failed to run migrations: %w", err)
	//}

	zlog.Debug().Msg("SQLite client initialized successfully")
	return client, nil
}

func (c *Client) Close() error {
	zlog.Debug().Msg("Closing SQLite connection")
	return c.db.Close()
}

func (c *Client) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	zlog.Trace().Str("query", query).Msg("Executing SQLite query")
	return c.db.ExecContext(ctx, query, args...)
}

func (c *Client) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	zlog.Trace().Str("query", query).Msg("Executing SQLite query")
	return c.db.QueryContext(ctx, query, args...)
}

func (c *Client) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	zlog.Trace().Str("query", query).Msg("Executing SQLite query")
	return c.db.QueryRowContext(ctx, query, args...)
}

func (c *Client) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	zlog.Trace().Msg("Beginning SQLite transaction")
	return c.db.BeginTx(ctx, opts)
}

func (c *Client) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	zlog.Trace().Str("query", query).Msg("Preparing SQLite statement")
	return c.db.PrepareContext(ctx, query)
}
