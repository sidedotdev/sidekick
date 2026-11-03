package env

import (
	"context"
	"errors"
	"testing"

	modal "github.com/modal-labs/libmodal/modal-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// refreshHarness fakes the control plane behind an endpoint refresh and
// records what the refresh did with it.
type refreshHarness struct {
	live        *modal.Sandbox
	registerErr error
	restore     modalRestoreContext
	created     ModalCreateSandboxOutput

	registered []string
	served     bool
	creates    []ModalCreateSandboxInput
	recorded   []modalEndpointRefresh
}

func (h *refreshHarness) deps() modalRefreshDeps {
	return modalRefreshDeps{
		find:      func(context.Context, string) (*modal.Sandbox, error) { return h.live, nil },
		awaitSSHD: func(context.Context, *modal.Sandbox) error { return nil },
		endpoint: func(context.Context, *modal.Sandbox) (string, int, error) {
			h.served = true
			return "live.host", 1111, nil
		},
		terminate: func(context.Context, *modal.Sandbox) error { return errors.New("unexpected terminate") },
		awaitGone: func(context.Context, string) error { return errors.New("unexpected wait") },
		register: func(_ context.Context, name, sandboxId string) error {
			h.registered = append(h.registered, name+"/"+sandboxId)
			return h.registerErr
		},
		restoreContext: func(context.Context, string) (modalRestoreContext, error) { return h.restore, nil },
		create: func(_ context.Context, input ModalCreateSandboxInput) (ModalCreateSandboxOutput, error) {
			h.creates = append(h.creates, input)
			return h.created, nil
		},
		recordRestore: func(_ context.Context, _ string, refresh modalEndpointRefresh) {
			h.recorded = append(h.recorded, refresh)
		},
	}
}

// TestModalRefreshEndpoint covers the two things a refresh must get right
// beyond re-resolving an endpoint: a running sandbox is registered with the
// guard before anything is told how to reach it, and a recreation is
// described by what was actually created rather than what was intended.
func TestModalRefreshEndpoint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	checkpoint := &modalSnapshotRecord{ImageId: "im-1", SandboxId: "sb-A", Sealed: true, Meta: []byte(`{"cpu":2}`)}
	clean := modalIncarnationLineage{LatestSandboxId: "sb-A", ShutdownImageId: "im-1"}
	recreated := func(imageId string) ModalCreateSandboxOutput {
		return ModalCreateSandboxOutput{SandboxName: "side--x", SSHHost: "new.host", SSHPort: 2222, SnapshotImageId: imageId, Resumed: true}
	}

	t.Run("a running sandbox is registered before its endpoint is served", func(t *testing.T) {
		t.Parallel()
		h := &refreshHarness{live: &modal.Sandbox{SandboxID: "sb-live"}}
		refresh, err := modalRefreshEndpoint(ctx, "side--x", h.deps())
		require.NoError(t, err)
		assert.Equal(t, modalEndpointRefresh{SSHHost: "live.host", SSHPort: 1111}, refresh)
		assert.Equal(t, []string{"side--x/sb-live"}, h.registered)
		assert.True(t, h.served)
		assert.Empty(t, h.creates)
	})

	t.Run("a running sandbox the guard cannot record is withheld", func(t *testing.T) {
		t.Parallel()
		h := &refreshHarness{live: &modal.Sandbox{SandboxID: "sb-live"}, registerErr: errors.New("guard unreachable")}
		_, err := modalRefreshEndpoint(ctx, "side--x", h.deps())
		require.ErrorContains(t, err, "guard unreachable")
		assert.False(t, h.served, "an unregistered sandbox must not be served")
	})

	t.Run("restoring the sealed shutdown checkpoint is a resume", func(t *testing.T) {
		t.Parallel()
		h := &refreshHarness{restore: modalRestoreContext{Record: checkpoint, Lineage: clean}, created: recreated("im-1")}
		refresh, err := modalRefreshEndpoint(ctx, "side--x", h.deps())
		require.NoError(t, err)
		assert.True(t, refresh.Restored)
		assert.True(t, refresh.Hibernated)
		assert.Empty(t, refresh.restoreNotice("side--x"))
		assert.Equal(t, "new.host", refresh.SSHHost)
		require.Len(t, h.creates, 1)
		assert.Equal(t, "side--x", h.creates[0].Name)
		assert.Equal(t, float64(2), h.creates[0].Config.CPU, "the restore must use the checkpoint's own configuration")
		require.Len(t, h.recorded, 1)
		assert.True(t, h.recorded[0].Hibernated)
	})

	t.Run("a recreation that fell back to a clean image warns of total loss", func(t *testing.T) {
		t.Parallel()
		h := &refreshHarness{restore: modalRestoreContext{Record: checkpoint, Lineage: clean}, created: recreated("")}
		refresh, err := modalRefreshEndpoint(ctx, "side--x", h.deps())
		require.NoError(t, err)
		assert.True(t, refresh.Restored)
		assert.False(t, refresh.Hibernated)
		assert.Contains(t, refresh.restoreNotice("side--x"), "from scratch")
		require.Len(t, h.recorded, 1)
	})

	t.Run("a restore after an unrecorded successor warns", func(t *testing.T) {
		t.Parallel()
		h := &refreshHarness{
			restore: modalRestoreContext{Record: checkpoint, Lineage: modalIncarnationLineage{LatestSandboxId: "sb-B"}},
			created: recreated("im-1"),
		}
		refresh, err := modalRefreshEndpoint(ctx, "side--x", h.deps())
		require.NoError(t, err)
		assert.False(t, refresh.Hibernated)
		assert.Contains(t, refresh.restoreNotice("side--x"), "sb-B")
	})

	t.Run("a live successor found by the create is not a restore", func(t *testing.T) {
		t.Parallel()
		h := &refreshHarness{
			restore: modalRestoreContext{Record: checkpoint, Lineage: clean},
			created: ModalCreateSandboxOutput{SandboxName: "side--x", SSHHost: "new.host", SSHPort: 2222, Reused: true},
		}
		refresh, err := modalRefreshEndpoint(ctx, "side--x", h.deps())
		require.NoError(t, err)
		assert.False(t, refresh.Restored)
		assert.Empty(t, refresh.restoreNotice("side--x"))
		assert.Empty(t, h.recorded)
	})

	t.Run("nothing to restore from", func(t *testing.T) {
		t.Parallel()
		h := &refreshHarness{restore: modalRestoreContext{Lineage: modalIncarnationLineage{LatestSandboxId: "sb-A"}}}
		_, err := modalRefreshEndpoint(ctx, "side--x", h.deps())
		require.ErrorContains(t, err, "no snapshot to restore from")
		assert.ErrorContains(t, err, "sb-A")
		assert.Empty(t, h.creates)
	})
}

func TestModalRestoreRequiresRegistrationProof(t *testing.T) {
	t.Parallel()
	restore := modalRestoreContext{
		Record:  &modalSnapshotRecord{ImageId: "im-1", SandboxId: "sb-A", Sealed: true},
		Lineage: modalIncarnationLineage{LatestSandboxId: "sb-A", ShutdownImageId: "im-1"},
	}
	refresh := restore.outcome(ModalCreateSandboxOutput{
		SandboxName: "side--x", SnapshotImageId: "im-1",
	})
	assert.False(t, refresh.Hibernated, "lineage read before creation cannot rule out an intervening incarnation")
	assert.NotEmpty(t, refresh.restoreNotice("side--x"))
}
