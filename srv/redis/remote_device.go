package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"time"

	"github.com/redis/go-redis/v9"
)

const remoteDevicesSetKey = "remote_devices"

func remoteDeviceKey(deviceId string) string {
	return fmt.Sprintf("remote_device:%s", deviceId)
}

func remoteDeviceTokenHashKey(tokenHash string) string {
	return fmt.Sprintf("remote_device_token_hash:%s", tokenHash)
}

func (s Storage) CreateRemoteDevice(ctx context.Context, device domain.RemoteDevice) error {
	if device.Created.IsZero() {
		device.Created = time.Now().UTC()
	}
	if device.LastUsed.IsZero() {
		device.LastUsed = device.Created
	}

	deviceJSON, err := json.Marshal(device)
	if err != nil {
		return fmt.Errorf("failed to marshal remote device: %w", err)
	}

	pipe := s.Client.Pipeline()
	pipe.Set(ctx, remoteDeviceKey(device.Id), deviceJSON, 0)
	pipe.Set(ctx, remoteDeviceTokenHashKey(device.TokenHash), device.Id, 0)
	pipe.SAdd(ctx, remoteDevicesSetKey, device.Id)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("failed to create remote device: %w", err)
	}

	return nil
}

func (s Storage) getRemoteDevice(ctx context.Context, deviceId string) (domain.RemoteDevice, error) {
	deviceJSON, err := s.Client.Get(ctx, remoteDeviceKey(deviceId)).Result()
	if err != nil {
		if err == redis.Nil {
			return domain.RemoteDevice{}, common.ErrNotFound
		}
		return domain.RemoteDevice{}, fmt.Errorf("failed to get remote device: %w", err)
	}

	var device domain.RemoteDevice
	if err := json.Unmarshal([]byte(deviceJSON), &device); err != nil {
		return domain.RemoteDevice{}, fmt.Errorf("failed to unmarshal remote device: %w", err)
	}

	return device, nil
}

func (s Storage) GetRemoteDeviceByTokenHash(ctx context.Context, tokenHash string) (domain.RemoteDevice, error) {
	deviceId, err := s.Client.Get(ctx, remoteDeviceTokenHashKey(tokenHash)).Result()
	if err != nil {
		if err == redis.Nil {
			return domain.RemoteDevice{}, common.ErrNotFound
		}
		return domain.RemoteDevice{}, fmt.Errorf("failed to get remote device by token hash: %w", err)
	}

	return s.getRemoteDevice(ctx, deviceId)
}

func (s Storage) GetAllRemoteDevices(ctx context.Context) ([]domain.RemoteDevice, error) {
	deviceIds, err := s.Client.SMembers(ctx, remoteDevicesSetKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get remote device IDs: %w", err)
	}

	devices := make([]domain.RemoteDevice, 0, len(deviceIds))
	for _, deviceId := range deviceIds {
		device, err := s.getRemoteDevice(ctx, deviceId)
		if err != nil {
			if err == common.ErrNotFound {
				continue
			}
			return nil, fmt.Errorf("failed to get remote device %s: %w", deviceId, err)
		}
		devices = append(devices, device)
	}

	return devices, nil
}

func (s Storage) UpdateRemoteDeviceLastUsed(ctx context.Context, deviceId string, lastUsed time.Time) error {
	device, err := s.getRemoteDevice(ctx, deviceId)
	if err != nil {
		return err
	}

	device.LastUsed = lastUsed.UTC()
	deviceJSON, err := json.Marshal(device)
	if err != nil {
		return fmt.Errorf("failed to marshal remote device: %w", err)
	}

	if err := s.Client.Set(ctx, remoteDeviceKey(deviceId), deviceJSON, 0).Err(); err != nil {
		return fmt.Errorf("failed to update remote device last used: %w", err)
	}

	return nil
}

func (s Storage) DeleteRemoteDevice(ctx context.Context, deviceId string) error {
	device, err := s.getRemoteDevice(ctx, deviceId)
	if err != nil {
		return err
	}

	pipe := s.Client.Pipeline()
	pipe.Del(ctx, remoteDeviceKey(deviceId))
	pipe.Del(ctx, remoteDeviceTokenHashKey(device.TokenHash))
	pipe.SRem(ctx, remoteDevicesSetKey, deviceId)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("failed to delete remote device: %w", err)
	}

	return nil
}
