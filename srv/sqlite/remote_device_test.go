package sqlite

import (
	"context"
	"sidekick/common"
	"sidekick/domain"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoteDeviceStorage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	storage := NewTestSqliteStorage(t, "remote_device_test")

	t.Run("CreateRemoteDevice and GetRemoteDeviceByTokenHash", func(t *testing.T) {
		device := domain.RemoteDevice{
			Id:        "rd_test1",
			Name:      "Test Phone",
			TokenHash: "hash1",
			Created:   time.Now().UTC().Round(time.Microsecond),
			LastUsed:  time.Now().UTC().Round(time.Microsecond),
		}

		err := storage.CreateRemoteDevice(ctx, device)
		require.NoError(t, err)

		retrieved, err := storage.GetRemoteDeviceByTokenHash(ctx, device.TokenHash)
		require.NoError(t, err)
		assert.Equal(t, device, retrieved)

		_, err = storage.GetRemoteDeviceByTokenHash(ctx, "non-existent")
		assert.ErrorIs(t, err, common.ErrNotFound)
	})

	t.Run("CreateRemoteDevice defaults timestamps", func(t *testing.T) {
		before := time.Now().UTC()
		device := domain.RemoteDevice{
			Id:        "rd_test_defaults",
			Name:      "No Timestamps",
			TokenHash: "hash_defaults",
		}

		err := storage.CreateRemoteDevice(ctx, device)
		require.NoError(t, err)

		retrieved, err := storage.GetRemoteDeviceByTokenHash(ctx, device.TokenHash)
		require.NoError(t, err)
		assert.False(t, retrieved.Created.IsZero())
		assert.False(t, retrieved.LastUsed.IsZero())
		assert.True(t, !retrieved.Created.Before(before.Add(-time.Second)))
		assert.Equal(t, retrieved.Created, retrieved.LastUsed)
	})

	t.Run("GetAllRemoteDevices", func(t *testing.T) {
		allStorage := NewTestSqliteStorage(t, "remote_device_test_all")
		devices := []domain.RemoteDevice{
			{Id: "rd_a", Name: "A", TokenHash: "hash_a", Created: time.Now().UTC().Round(time.Microsecond), LastUsed: time.Now().UTC().Round(time.Microsecond)},
			{Id: "rd_b", Name: "B", TokenHash: "hash_b", Created: time.Now().UTC().Round(time.Microsecond), LastUsed: time.Now().UTC().Round(time.Microsecond)},
		}
		for _, d := range devices {
			require.NoError(t, allStorage.CreateRemoteDevice(ctx, d))
		}

		retrieved, err := allStorage.GetAllRemoteDevices(ctx)
		require.NoError(t, err)
		assert.Len(t, retrieved, len(devices))
		for _, d := range devices {
			assert.Contains(t, retrieved, d)
		}
	})

	t.Run("UpdateRemoteDeviceLastUsed", func(t *testing.T) {
		device := domain.RemoteDevice{
			Id:        "rd_update",
			Name:      "Update Me",
			TokenHash: "hash_update",
			Created:   time.Now().UTC().Round(time.Microsecond),
			LastUsed:  time.Now().UTC().Round(time.Microsecond),
		}
		require.NoError(t, storage.CreateRemoteDevice(ctx, device))

		newLastUsed := time.Now().UTC().Add(time.Hour).Round(time.Microsecond)
		err := storage.UpdateRemoteDeviceLastUsed(ctx, device.Id, newLastUsed)
		require.NoError(t, err)

		retrieved, err := storage.GetRemoteDeviceByTokenHash(ctx, device.TokenHash)
		require.NoError(t, err)
		assert.Equal(t, newLastUsed, retrieved.LastUsed)

		err = storage.UpdateRemoteDeviceLastUsed(ctx, "non-existent", newLastUsed)
		assert.ErrorIs(t, err, common.ErrNotFound)
	})

	t.Run("DeleteRemoteDevice", func(t *testing.T) {
		device := domain.RemoteDevice{
			Id:        "rd_delete",
			Name:      "Delete Me",
			TokenHash: "hash_delete",
			Created:   time.Now().UTC().Round(time.Microsecond),
			LastUsed:  time.Now().UTC().Round(time.Microsecond),
		}
		require.NoError(t, storage.CreateRemoteDevice(ctx, device))

		err := storage.DeleteRemoteDevice(ctx, device.Id)
		require.NoError(t, err)

		_, err = storage.GetRemoteDeviceByTokenHash(ctx, device.TokenHash)
		assert.ErrorIs(t, err, common.ErrNotFound)

		err = storage.DeleteRemoteDevice(ctx, "non-existent")
		assert.ErrorIs(t, err, common.ErrNotFound)
	})
}
