package env

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type switchRestoreTestEnv struct {
	*LocalGitWorktreeEnv
	afterRestore func()
}

func (e *switchRestoreTestEnv) RestoreFromSwitch(ctx context.Context, bundle SwitchBundle) error {
	if err := e.LocalGitWorktreeEnv.RestoreFromSwitch(ctx, bundle); err != nil {
		return err
	}
	if e.afterRestore != nil {
		e.afterRestore()
	}
	return nil
}

func TestRestoreFromSwitchActivityVerifiesHeadAndCanRetry(t *testing.T) {
	t.Parallel()

	for _, withWIP := range []bool{false, true} {
		name := "clean"
		if withWIP {
			name = "with WIP"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			_, dir, branch := setupSwitchTestRepo(t)
			files := []string{"tracked.txt", "sub/untracked.txt"}
			if withWIP {
				writeTestFile(t, filepath.Join(dir, "tracked.txt"), "uncommitted edit")
				writeTestFile(t, filepath.Join(dir, "sub", "untracked.txt"), "untracked text")
			}
			before := captureWorktreeState(t, dir, files)
			local := &LocalGitWorktreeEnv{WorkingDirectory: dir}
			bundle, err := local.ExportForSwitch(ctx, branch)
			require.NoError(t, err)
			if bundle.WIPArchivePath != "" {
				t.Cleanup(func() { _ = os.Remove(bundle.WIPArchivePath) })
			}

			var unexpectedHead string
			target := &switchRestoreTestEnv{
				LocalGitWorktreeEnv: local,
				afterRestore: func() {
					runGitIn(t, dir, "reset", "--hard", "HEAD~1")
					unexpectedHead = captureWorktreeState(t, dir, files).Head
				},
			}
			input := RestoreFromSwitchInput{
				EnvContainer: EnvContainer{Env: target},
				Bundle:       bundle,
			}
			_, err = RestoreFromSwitchActivity(ctx, input)
			require.Error(t, err)
			require.Contains(t, err.Error(), unexpectedHead)
			require.Contains(t, err.Error(), bundle.HeadCommit)

			target.afterRestore = nil
			_, err = RestoreFromSwitchActivity(ctx, input)
			require.NoError(t, err)
			require.Equal(t, before, captureWorktreeState(t, dir, files))
			require.NoFileExists(t, filepath.Join(dir, HibernationPatchFile))
		})
	}
}
