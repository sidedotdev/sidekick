package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	_ "github.com/golang-migrate/migrate/v4/source/file"
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

	client := &Client{db: db}

	if err := client.Migrate(); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	zlog.Info().Msg("SQLite client initialized successfully")
	return client, nil
}

func (c *Client) Migrate() error {
	driver, err := sqlite.WithInstance(c.db, &sqlite.Config{})
	if err != nil {
		return fmt.Errorf("failed to create migration driver: %w", err)
	}

	migrationsDir := filepath.Join("srv", "sqlite", "migrations")
	m, err := migrate.NewWithDatabaseInstance(
		fmt.Sprintf("file://%s", migrationsDir),
		"sqlite",
		driver,
	)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	return nil
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
