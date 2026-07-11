package domain

import (
	"context"
	"time"
)

// RemoteDevice represents a device that has been paired to remote-control the
// Sidekick server. Authentication is performed by matching the SHA-256 hash of
// a high-entropy token presented by the device; the plaintext token is only
// ever shown once during pairing.
type RemoteDevice struct {
	Id        string    `json:"id"`
	Name      string    `json:"name"`      // human-readable label for the device
	TokenHash string    `json:"tokenHash"` // SHA-256 hash of the device's auth token
	Created   time.Time `json:"created"`
	LastUsed  time.Time `json:"lastUsed"`
}

// RemoteDeviceStorage defines the interface for paired-device database operations
type RemoteDeviceStorage interface {
	CreateRemoteDevice(ctx context.Context, device RemoteDevice) error
	GetRemoteDeviceByTokenHash(ctx context.Context, tokenHash string) (RemoteDevice, error)
	GetAllRemoteDevices(ctx context.Context) ([]RemoteDevice, error)
	UpdateRemoteDeviceLastUsed(ctx context.Context, deviceId string, lastUsed time.Time) error
	DeleteRemoteDevice(ctx context.Context, deviceId string) error
}
