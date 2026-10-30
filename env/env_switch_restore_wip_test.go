package env

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type switchWIPRestoreTestEnv struct {
	*switchWIPTestEnv
	afterRestore func() error
}

func (e *switchWIPRestoreTestEnv) RestoreFromSwitch(ctx context.Context, bundle SwitchBundle) error {
	if err := restoreFromSwitch(ctx, e, bundle); err != nil {
		return err
	}
	if e.afterRestore != nil {
		return e.afterRestore()
	}
	return nil
}

func TestRestoreFromSwitchSourceWIP(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		targetType   EnvType
		omitSource   bool
		restoreFails bool
		wrongHead    bool
		cleanupFails bool
	}{
		{name: "modal", targetType: EnvTypeModal},
		{name: "openshell", targetType: EnvTypeOpenShell},
		{name: "shared devpod checkout", targetType: EnvTypeDevPod},
		{name: "shared local checkout", targetType: EnvTypeLocal},
		{name: "legacy input", targetType: EnvTypeModal, omitSource: true},
		{name: "restore failure", targetType: EnvTypeModal, restoreFails: true},
		{name: "verification failure", targetType: EnvTypeModal, wrongHead: true},
		{name: "cleanup failure", targetType: EnvTypeModal, cleanupFails: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			repoDir, dir, branch := setupSwitchTestRepo(t)
			writeTestFile(t, filepath.Join(dir, "tracked.txt"), "source WIP\n")
			source := &LocalGitWorktreeEnv{WorkingDirectory: dir}
			before := captureWorktreeState(t, dir, []string{"tracked.txt"})
			bundle, err := source.ExportForSwitch(ctx, branch)
			require.NoError(t, err)
			t.Cleanup(func() { _ = os.Remove(bundle.WIPArchivePath) })

			targetDir := dir
			if tt.targetType == EnvTypeModal || tt.targetType == EnvTypeOpenShell {
				targetDir = filepath.Join(t.TempDir(), "remote")
				runGitIn(t, repoDir, "clone", "--branch", branch, repoDir, targetDir)
			}
			target := &switchWIPRestoreTestEnv{
				switchWIPTestEnv: &switchWIPTestEnv{
					LocalEnv: &LocalEnv{WorkingDirectory: targetDir},
					envType:  tt.targetType,
				},
				afterRestore: func() error {
					require.Equal(t, before, captureWorktreeState(t, dir, []string{"tracked.txt"}))
					if tt.restoreFails {
						return fmt.Errorf("restore failed")
					}
					if tt.wrongHead {
						runGitIn(t, targetDir, "reset", "--hard", "HEAD~1")
					}
					return nil
				},
			}
			if tt.cleanupFails {
				lock := strings.TrimSpace(runGitIn(t, repoDir, "rev-parse", "--path-format=absolute", "--git-path", "refs/stash.lock"))
				require.NoError(t, os.WriteFile(lock, nil, 0o600))
			}
			input := RestoreFromSwitchInput{
				EnvContainer:       EnvContainer{Env: target},
				Bundle:             bundle,
				SourceEnvContainer: &EnvContainer{Env: source},
			}
			if tt.omitSource {
				input.SourceEnvContainer = nil
			}
			_, err = RestoreFromSwitchActivity(ctx, input)
			if tt.restoreFails || tt.wrongHead {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				content, err := os.ReadFile(filepath.Join(targetDir, "tracked.txt"))
				require.NoError(t, err)
				require.Equal(t, "source WIP\n", string(content))
			}
			shouldClean := (tt.targetType == EnvTypeModal || tt.targetType == EnvTypeOpenShell) &&
				!tt.omitSource && !tt.restoreFails && !tt.wrongHead && !tt.cleanupFails
			if shouldClean {
				require.Empty(t, runGitIn(t, dir, "status", "--porcelain"))
				refs := strings.Fields(runGitIn(t, repoDir, "for-each-ref", "--format=%(refname)", "refs/side/env-switch-wip/"))
				require.Len(t, refs, 1)
				runGitIn(t, dir, "stash", "apply", "--index", refs[0])
			}
			require.Equal(t, before, captureWorktreeState(t, dir, []string{"tracked.txt"}))
		})
	}
}
