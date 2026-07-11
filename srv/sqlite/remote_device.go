package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"time"
)

func (s *Storage) CreateRemoteDevice(ctx context.Context, device domain.RemoteDevice) error {
	if device.Created.IsZero() {
		device.Created = time.Now().UTC()
	} else {
		device.Created = device.Created.UTC()
	}
	if device.LastUsed.IsZero() {
		device.LastUsed = device.Created
	} else {
		device.LastUsed = device.LastUsed.UTC()
	}

	query := `
		INSERT INTO remote_devices (id, name, token_hash, created, last_used)
		VALUES (?, ?, ?, ?, ?)
	`

	_, err := s.db.ExecContext(ctx, query,
		device.Id,
		device.Name,
		device.TokenHash,
		device.Created.Format(time.RFC3339Nano),
		device.LastUsed.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("failed to create remote device: %w", err)
	}

	return nil
}

func (s *Storage) GetRemoteDeviceByTokenHash(ctx context.Context, tokenHash string) (domain.RemoteDevice, error) {
	query := `
		SELECT id, name, token_hash, created, last_used
		FROM remote_devices
		WHERE token_hash = ?
	`

	return s.scanRemoteDevice(s.db.QueryRowContext(ctx, query, tokenHash))
}

func (s *Storage) GetAllRemoteDevices(ctx context.Context) ([]domain.RemoteDevice, error) {
	query := `
		SELECT id, name, token_hash, created, last_used
		FROM remote_devices
	`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query remote devices: %w", err)
	}
	defer rows.Close()

	devices := make([]domain.RemoteDevice, 0)
	for rows.Next() {
		device, err := s.scanRemoteDevice(rows)
		if err != nil {
			return nil, err
		}
		devices = append(devices, device)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate remote devices: %w", err)
	}

	return devices, nil
}

func (s *Storage) UpdateRemoteDeviceLastUsed(ctx context.Context, deviceId string, lastUsed time.Time) error {
	query := `
		UPDATE remote_devices
		SET last_used = ?
		WHERE id = ?
	`

	result, err := s.db.ExecContext(ctx, query, lastUsed.UTC().Format(time.RFC3339Nano), deviceId)
	if err != nil {
		return fmt.Errorf("failed to update remote device last used: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return common.ErrNotFound
	}

	return nil
}

func (s *Storage) DeleteRemoteDevice(ctx context.Context, deviceId string) error {
	query := `
		DELETE FROM remote_devices
		WHERE id = ?
	`

	result, err := s.db.ExecContext(ctx, query, deviceId)
	if err != nil {
		return fmt.Errorf("failed to delete remote device: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return common.ErrNotFound
	}

	return nil
}

// rowScanner abstracts *sql.Row and *sql.Rows for scanning a single device.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

func (s *Storage) scanRemoteDevice(scanner rowScanner) (domain.RemoteDevice, error) {
	var device domain.RemoteDevice
	var createdStr, lastUsedStr string
	err := scanner.Scan(
		&device.Id,
		&device.Name,
		&device.TokenHash,
		&createdStr,
		&lastUsedStr,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return domain.RemoteDevice{}, common.ErrNotFound
		}
		return domain.RemoteDevice{}, fmt.Errorf("failed to scan remote device: %w", err)
	}

	device.Created, err = time.Parse(time.RFC3339Nano, createdStr)
	if err != nil {
		return domain.RemoteDevice{}, fmt.Errorf("failed to parse created timestamp: %w", err)
	}
	device.LastUsed, err = time.Parse(time.RFC3339Nano, lastUsedStr)
	if err != nil {
		return domain.RemoteDevice{}, fmt.Errorf("failed to parse last used timestamp: %w", err)
	}

	return device, nil
}
