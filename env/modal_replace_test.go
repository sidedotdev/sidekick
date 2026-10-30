package env

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"

	"sidekick/common"
)

func TestModalReplaceSandboxActivity(t *testing.T) {
	// Not parallel: subtests swap the package-level recreation seams.
	origCheck := modalRecreateCheckSandbox
	origSnapshot := modalRecreateSnapshot
	origLatest := modalRecreateLatestSnapshot
	origTerminate := modalRecreateTerminateSandbox
	origCreate := modalRecreateCreateSandbox
	origDeleteSnapshots := modalRecreateDeleteSnapshots
	origReplaceForwards := modalReplaceReverseForwards
	t.Cleanup(func() {
		modalRecreateCheckSandbox = origCheck
		modalRecreateSnapshot = origSnapshot
		modalRecreateLatestSnapshot = origLatest
		modalRecreateTerminateSandbox = origTerminate
		modalRecreateCreateSandbox = origCreate
		modalRecreateDeleteSnapshots = origDeleteSnapshots
		modalReplaceReverseForwards = origReplaceForwards
	})
	successorName := ModalReplacementSandboxName("side--repo-abc")
	predecessorForwards := []common.PortForwardConfig{{HostPort: 18855}}
	newInput := func(config common.ModalEnvConfig, forwards []common.PortForwardConfig) ModalReplaceSandboxInput {
		return ModalReplaceSandboxInput{
			EnvContainer: EnvContainer{Env: &ModalEnv{
				WorkingDirectory: "/root/repo",
				SandboxName:      "side--repo-abc",
				SSHHost:          "old.modal.host",
				SSHPort:          1111,
				LocalRepoDir:     "/host/repo",
				PortForwards:     predecessorForwards,
			}},
			Config:       config,
			PortForwards: forwards,
		}
	}
	compatible := func(imageId string) *modalSnapshotRecord {
		return &modalSnapshotRecord{ImageId: imageId, ImageVersion: modalSnapshotImageVersion}
	}
	installForwardSeam := func(seams *recreateSeams) {
		modalReplaceReverseForwards = func(_ context.Context, modalEnv *ModalEnv, previous, forwards []common.PortForwardConfig) error {
			seams.sequence = append(seams.sequence, "forwards:"+modalEnv.SandboxName)
			return nil
		}
	}

	t.Run("live predecessor keeps running while its checkpointed successor is created and snapshotted", func(t *testing.T) {
		seams := installRecreateSeams(t, true, compatible("im-live"), compatible("im-stale"), nil, nil)
		installForwardSeam(seams)
		output, err := ModalReplaceSandboxActivity(context.Background(), newInput(common.ModalEnvConfig{Memory: 2048}, predecessorForwards))
		require.NoError(t, err)
		assert.Equal(t, []string{"check", "snapshot", "create", "snapshot-successor:" + successorName}, seams.sequence,
			"the predecessor must be neither terminated nor stripped of its snapshots")
		assert.Equal(t, "im-live", seams.createInput.SnapshotImageId)
		assert.True(t, seams.createInput.SkipSeedSnapshots)
		assert.Equal(t, successorName, seams.createInput.Name)
		assert.Equal(t, 2048, seams.createInput.Config.Memory)

		successor, ok := output.EnvContainer.Env.(*ModalEnv)
		require.True(t, ok)
		assert.Equal(t, successorName, successor.SandboxName)
		assert.Equal(t, "new.modal.host", successor.SSHHost)
		assert.Equal(t, 2222, successor.SSHPort)
		assert.Equal(t, "/root/repo", successor.WorkingDirectory, "a replacement keeps the flow's EnvIdentity")
		assert.Equal(t, "/host/repo", successor.LocalRepoDir)
		assert.Equal(t, predecessorForwards, successor.PortForwards)
		assert.False(t, CannotReplaceFromSnapshot(err))
	})

	t.Run("changed port forwards are swapped on the successor after its checkpoint", func(t *testing.T) {
		seams := installRecreateSeams(t, true, compatible("im-live"), nil, nil, nil)
		installForwardSeam(seams)
		desired := []common.PortForwardConfig{{HostPort: 3000, ContainerPort: 3001}}
		output, err := ModalReplaceSandboxActivity(context.Background(), newInput(common.ModalEnvConfig{Memory: 2048}, desired))
		require.NoError(t, err)
		assert.Equal(t, []string{"check", "snapshot", "create", "snapshot-successor:" + successorName, "forwards:" + successorName}, seams.sequence)
		assert.Equal(t, desired, output.EnvContainer.Env.(*ModalEnv).PortForwards)
	})

	t.Run("absent predecessor is replaced from its stored snapshot", func(t *testing.T) {
		seams := installRecreateSeams(t, false, nil, compatible("im-stored"), nil, nil)
		installForwardSeam(seams)
		_, err := ModalReplaceSandboxActivity(context.Background(), newInput(common.ModalEnvConfig{Memory: 2048}, predecessorForwards))
		require.NoError(t, err)
		assert.Equal(t, []string{"check", "latest", "create", "snapshot-successor:" + successorName}, seams.sequence)
		assert.Equal(t, "im-stored", seams.createInput.SnapshotImageId)
	})

	t.Run("without any snapshot the replacement is refused so the flow's work can be transferred instead", func(t *testing.T) {
		seams := installRecreateSeams(t, false, nil, nil, nil, nil)
		_, err := ModalReplaceSandboxActivity(context.Background(), newInput(common.ModalEnvConfig{Memory: 2048}, predecessorForwards))
		require.Error(t, err)
		assert.True(t, CannotReplaceFromSnapshot(err))
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr)
		assert.True(t, appErr.NonRetryable())
		assert.Equal(t, []string{"check", "latest"}, seams.sequence, "nothing may be created from repo seeds")
	})

	t.Run("snapshot the new config cannot boot is refused without touching the live sandbox", func(t *testing.T) {
		seams := installRecreateSeams(t, true, compatible("im-live"), nil, nil, nil)
		_, err := ModalReplaceSandboxActivity(context.Background(), newInput(common.ModalEnvConfig{
			Volumes: []common.ModalVolumeMount{{Name: "new", MountPath: "/data"}},
		}, predecessorForwards))
		require.Error(t, err)
		assert.True(t, CannotReplaceFromSnapshot(err))
		assert.Equal(t, []string{"check", "snapshot"}, seams.sequence)
	})

	t.Run("creation failure leaves the predecessor and its snapshots in place", func(t *testing.T) {
		seams := installRecreateSeams(t, true, compatible("im-live"), nil, nil, errors.New("quota exceeded"))
		_, err := ModalReplaceSandboxActivity(context.Background(), newInput(common.ModalEnvConfig{Memory: 2048}, predecessorForwards))
		require.Error(t, err)
		assert.False(t, CannotReplaceFromSnapshot(err), "a transient failure must be retried, not trigger a transfer")
		assert.Equal(t, []string{"check", "snapshot", "create"}, seams.sequence)
	})

	for _, stage := range []string{"snapshot", "forwards"} {
		t.Run("post-create "+stage+" failure preserves predecessor", func(t *testing.T) {
			seams := installRecreateSeams(t, true, compatible("im-live"), nil, nil, nil)
			installForwardSeam(seams)
			failure := errors.New("successor " + stage + " failed")
			if stage == "snapshot" {
				snapshot := modalRecreateSnapshot
				modalRecreateSnapshot = func(ctx context.Context, container *ModalEnv) (*modalSnapshotRecord, error) {
					record, err := snapshot(ctx, container)
					if container.SandboxName == successorName {
						require.Equal(t, "new.modal.host", container.SSHHost)
						return nil, failure
					}
					return record, err
				}
			} else {
				modalReplaceReverseForwards = func(_ context.Context, container *ModalEnv, previous, desired []common.PortForwardConfig) error {
					require.Equal(t, successorName, container.SandboxName)
					require.Equal(t, "new.modal.host", container.SSHHost)
					seams.sequence = append(seams.sequence, "forwards:"+container.SandboxName)
					return failure
				}
			}
			input := newInput(common.ModalEnvConfig{Memory: 2048}, []common.PortForwardConfig{{HostPort: 3000}})
			output, err := ModalReplaceSandboxActivity(context.Background(), input)
			require.ErrorIs(t, err, failure)
			assert.Nil(t, output.EnvContainer.Env)
			assert.False(t, CannotReplaceFromSnapshot(err))
			expected := []string{"check", "snapshot", "create", "snapshot-successor:" + successorName}
			if stage == "forwards" {
				expected = append(expected, "forwards:"+successorName)
			}
			assert.Equal(t, expected, seams.sequence, "failure must not terminate or delete predecessor snapshots")
			assert.Equal(t, "side--repo-abc", input.EnvContainer.Env.(*ModalEnv).SandboxName)
			assert.Equal(t, "old.modal.host", input.EnvContainer.Env.(*ModalEnv).SSHHost)
			assert.Equal(t, predecessorForwards, input.EnvContainer.Env.(*ModalEnv).PortForwards)
		})
	}

	t.Run("invalid input is rejected before anything happens", func(t *testing.T) {
		seams := installRecreateSeams(t, true, compatible("im-live"), nil, nil, nil)
		_, err := ModalReplaceSandboxActivity(context.Background(), newInput(common.ModalEnvConfig{Memory: 2048},
			[]common.PortForwardConfig{{HostPort: 3000, ContainerPort: 4000}, {HostPort: 4000}}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid Modal configuration")
		assert.Empty(t, seams.sequence)

		_, err = ModalReplaceSandboxActivity(context.Background(), ModalReplaceSandboxInput{EnvContainer: EnvContainer{Env: &LocalEnv{}}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not Modal")
		assert.Empty(t, seams.sequence)
	})
}
