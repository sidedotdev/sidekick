package env

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/sideagent"
	"sidekick/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveSSHTransportKind(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		override string
		envType  EnvType
		want     SSHTransportKind
	}{
		{name: "no override defaults to native for a graduated provider", envType: EnvTypeDevPod, want: SSHTransportNative},
		{name: "no override leaves an ungraduated env type on legacy", envType: EnvTypeLocal, want: SSHTransportLegacy},
		{name: "override selects native", override: "native", envType: EnvTypeLocal, want: SSHTransportNative},
		{name: "override selects legacy", override: "legacy", envType: EnvTypeModal, want: SSHTransportLegacy},
		{name: "override is case and space insensitive", override: "  NATIVE ", envType: EnvTypeOpenShell, want: SSHTransportNative},
		{name: "unrecognized override falls back to the default", override: "quantum", envType: EnvTypeDevPod, want: SSHTransportNative},
		{name: "unrecognized override falls back to legacy for an ungraduated env type", override: "quantum", envType: EnvTypeLocal, want: SSHTransportLegacy},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, resolveSSHTransportKind(tc.override, tc.envType))
		})
	}
}

// TestSSHTransportProviderDefaults pins the graduation state itself: which
// providers run native with nothing set, and that an explicit override still
// decides for every provider either way.
func TestSSHTransportProviderDefaults(t *testing.T) {
	t.Parallel()

	cases := []struct {
		envType EnvType
		want    SSHTransportKind
	}{
		{EnvTypeModal, SSHTransportNative},
		{EnvTypeOpenShell, SSHTransportNative},
		{EnvTypeDevPod, SSHTransportNative},
	}

	for _, tc := range cases {
		t.Run(string(tc.envType), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, resolveSSHTransportKind("", tc.envType))
			assert.Equal(t, SSHTransportLegacy, resolveSSHTransportKind("legacy", tc.envType),
				"an explicit legacy override is the mitigation for a native regression")
			assert.Equal(t, SSHTransportNative, resolveSSHTransportKind("native", tc.envType))
		})
	}
}

// TestLegacyExecChannelIdentityIgnoresForwards pins that reverse forwards no
// longer split the exec channel pool: the holder binds them now, so two envs
// differing only in forwards must share one channel instead of paying for a
// second ssh connection each.
func TestLegacyExecChannelIdentityIgnoresForwards(t *testing.T) {
	t.Parallel()

	sshEnv := &ModalEnv{SandboxName: "exec-identity"}
	withForwards := &legacySSHTransport{
		key:      "modal:exec-identity",
		forwards: []common.PortForwardConfig{{HostPort: 8080}},
		sshEnv:   sshEnv,
	}
	withoutForwards := &legacySSHTransport{key: "modal:exec-identity", sshEnv: sshEnv}

	assert.Equal(t, withoutForwards.execKey(), withForwards.execKey())
}

// TestReverseForwardHolderKeyNormalizesForwards pins holder identity: a holder
// binding one set of listeners cannot serve another, but equivalent
// configurations must not spawn a second holder that fights for the same ports.
func TestReverseForwardHolderKeyNormalizesForwards(t *testing.T) {
	t.Parallel()

	base := "modal:holder-key"
	withoutForwards := reverseForwardHolderKey(base, nil)
	withForwards := reverseForwardHolderKey(base, []common.PortForwardConfig{
		{HostPort: 8080},
		{HostPort: 18855, ContainerPort: 28855},
	})
	reordered := reverseForwardHolderKey(base, []common.PortForwardConfig{
		{HostPort: 18855, ContainerPort: 28855},
		{HostPort: 8080, ContainerPort: 8080},
	})

	assert.NotEqual(t, withoutForwards, withForwards)
	assert.Equal(t, withForwards, reordered)
}

// stubSSHTransport stands in for an implementation during selection tests.
type stubSSHTransport struct {
	key      string
	forwards []common.PortForwardConfig
	sftpOps  []SFTPOp
}

func (s *stubSSHTransport) Exec(context.Context, sideagent.ExecRequest) (sideagent.ExecResponse, error) {
	return sideagent.ExecResponse{}, nil
}
func (s *stubSSHTransport) WithSFTP(_ context.Context, op SFTPOp) (any, error) {
	s.sftpOps = append(s.sftpOps, op)
	return []byte(nil), nil
}
func (s *stubSSHTransport) EnsureReverseForwards(context.Context, []common.PortForwardConfig) error {
	return nil
}
func (s *stubSSHTransport) Close() {}

func TestSSHTransportForSelectsImplementation(t *testing.T) {
	// Deliberately not parallel: sets an env var and a package-level hook.
	original := newNativeSSHTransport
	newNativeSSHTransport = func(key string, forwards []common.PortForwardConfig, sshEnv SSHCapableEnv) SSHTransport {
		return &stubSSHTransport{key: key, forwards: forwards}
	}
	t.Cleanup(func() { newNativeSSHTransport = original })

	sshEnv := &ModalEnv{SandboxName: "sandbox"}
	forwards := []common.PortForwardConfig{{HostPort: 8080}}

	t.Setenv(SSHTransportEnvVar, "native")
	native := sshTransportFor("modal:sandbox", forwards, sshEnv)
	stub, ok := native.(*stubSSHTransport)
	require.True(t, ok, "expected the native factory to be used")
	assert.Equal(t, "modal:sandbox", stub.key)
	assert.Equal(t, forwards, stub.forwards)

	t.Setenv(SSHTransportEnvVar, "legacy")
	legacy, ok := sshTransportFor("modal:sandbox", forwards, sshEnv).(*legacySSHTransport)
	require.True(t, ok, "expected the legacy transport")
	assert.Equal(t, "modal:sandbox", legacy.key)
}

// TestRemoteEnvFilesystemUsesSelectedTransport pins that filesystem ops honour
// the transport selection instead of reaching into the legacy pool directly.
func TestRemoteEnvFilesystemUsesSelectedTransport(t *testing.T) {
	// Deliberately not parallel: sets an env var and a package-level hook.
	original := newNativeSSHTransport
	var selected *stubSSHTransport
	newNativeSSHTransport = func(key string, forwards []common.PortForwardConfig, sshEnv SSHCapableEnv) SSHTransport {
		selected = &stubSSHTransport{key: key, forwards: forwards}
		return selected
	}
	t.Cleanup(func() { newNativeSSHTransport = original })
	t.Setenv(SSHTransportEnvVar, "native")

	remoteEnv := &ModalEnv{SandboxName: "fs-transport", WorkingDirectory: "/work"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := remoteEnv.ReadFile(ctx, "notes.txt")
	require.NoError(t, err, "filesystem ops must run over the selected transport")

	require.NotNil(t, selected, "the selected transport must be the one that serves the operation")
	assert.Equal(t, "modal:fs-transport", selected.key)
	require.Len(t, selected.sftpOps, 1)
	assert.Equal(t, "read", selected.sftpOps[0].Name)
	assert.Equal(t, "/work/notes.txt", selected.sftpOps[0].Path,
		"the env must resolve relative paths before handing the op to the transport")
}

// TestSSHTransportForFallsBackWhenNativeUnavailable pins the staging seam: a
// native selection with no native implementation installed must still yield a
// working transport rather than a nil interface.
func TestSSHTransportForFallsBackWhenNativeUnavailable(t *testing.T) {
	// Deliberately not parallel: sets an env var and a package-level hook.
	original := newNativeSSHTransport
	newNativeSSHTransport = nil
	t.Cleanup(func() { newNativeSSHTransport = original })

	t.Setenv(SSHTransportEnvVar, "native")
	transport := sshTransportFor("modal:sandbox", nil, &ModalEnv{SandboxName: "sandbox"})
	assert.IsType(t, &legacySSHTransport{}, transport)
}

// recordingSSHEnv reports fixed ssh args, including multiplexing options that
// helper connections must strip.
type recordingSSHEnv struct {
	*LocalEnv
}

func (r *recordingSSHEnv) SSHArgs(context.Context) ([]string, error) {
	return []string{
		"-o", "ControlMaster=auto",
		"-S", "/tmp/control-socket",
		"-o", "BatchMode=yes",
		"remote-host",
	}, nil
}

func (r *recordingSSHEnv) SSHConnConfig(context.Context) (SSHConnConfig, error) {
	return SSHConnConfig{
		Host:        "remote-host",
		BatchMode:   utils.Ptr(true),
		ControlPath: "/tmp/control-socket",
	}, nil
}

// devpodShapedSSHEnv mirrors DevPodEnv.SSHArgs, whose args end with a "--"
// separator following the destination so callers can append a remote command
// directly.
type devpodShapedSSHEnv struct {
	*LocalEnv
}

func (d *devpodShapedSSHEnv) SSHArgs(context.Context) ([]string, error) {
	return []string{"-o", "BatchMode=yes", "workspace.devpod", "--"}, nil
}

func (d *devpodShapedSSHEnv) SSHConnConfig(context.Context) (SSHConnConfig, error) {
	return SSHConnConfig{Host: "workspace.devpod", BatchMode: utils.Ptr(true), LegacyCommandSeparator: true}, nil
}

func TestInsertBeforeSSHDestinationTrailingSeparator(t *testing.T) {
	t.Parallel()

	got := insertBeforeSSHDestination([]string{"-o", "BatchMode=yes", "workspace.devpod", "--"}, []string{"-N"})
	assert.Equal(t, []string{"-o", "BatchMode=yes", "-N", "workspace.devpod", "--"}, got)
}

// forwardingSSHEnv stands in for an env whose args still carry reverse forwards
// inline, which is what the command channel has to strip: the holder owns those
// bindings, and a channel that competes for them breaks the holder's.
type forwardingSSHEnv struct {
	*LocalEnv
}

func (f *forwardingSSHEnv) SSHConnConfig(context.Context) (SSHConnConfig, error) {
	return SSHConnConfig{
		Host:        "remote-host",
		BatchMode:   utils.Ptr(true),
		ControlPath: "/tmp/control-socket",
	}, nil
}

func (f *forwardingSSHEnv) SSHArgs(context.Context) ([]string, error) {
	return []string{
		"-o", "ControlMaster=auto",
		"-S", "/tmp/control-socket",
		"-o", "BatchMode=yes",
		"-R", "127.0.0.1:3000:127.0.0.1:8080",
		"remote-host",
	}, nil
}

// startedForwardHolderArgs installs a fake ssh that appends its arguments to a
// file and then blocks, standing in for a long-lived holder connection.
func startedForwardHolderArgs(t *testing.T) (argsFile string) {
	t.Helper()
	argsFile = filepath.Join(t.TempDir(), "args")
	installFakeSSH(t, "printf '%s\\n' \"$*\" >> "+argsFile+"\nexec sleep 30\n")
	return argsFile
}

func readHolderInvocations(t *testing.T, argsFile string) []string {
	t.Helper()
	contents, err := os.ReadFile(argsFile)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return strings.Split(strings.TrimSpace(string(contents)), "\n")
}

func TestReverseForwardHolderRunsDedicatedConnection(t *testing.T) {
	// Deliberately not parallel: overrides PATH.
	argsFile := startedForwardHolderArgs(t)
	holder := &reverseForwardHolder{key: "test"}
	t.Cleanup(holder.close)

	forwards := []common.PortForwardConfig{{HostPort: 8080, ContainerPort: 3000}}
	require.NoError(t, holder.ensure(context.Background(), &recordingSSHEnv{&LocalEnv{}}, forwards))

	invocations := readHolderInvocations(t, argsFile)
	require.Len(t, invocations, 1)
	args := invocations[0]
	assert.Contains(t, args, "-N", "the holder must not run a remote command")
	assert.Contains(t, args, "-R 127.0.0.1:3000:127.0.0.1:8080")
	assert.Contains(t, args, "ExitOnForwardFailure=yes")
	assert.NotContains(t, args, "ControlMaster", "the holder must not attach to the multiplexing master")
}

// TestReverseForwardHolderOutlivesCommandContext is the point of the holder:
// forwards must survive the context of the command that first needed them.
func TestReverseForwardHolderOutlivesCommandContext(t *testing.T) {
	// Deliberately not parallel: overrides PATH.
	startedForwardHolderArgs(t)
	holder := &reverseForwardHolder{key: "test"}
	t.Cleanup(holder.close)

	ctx, cancel := context.WithCancel(context.Background())
	forwards := []common.PortForwardConfig{{HostPort: 8080}}
	require.NoError(t, holder.ensure(ctx, &recordingSSHEnv{&LocalEnv{}}, forwards))
	cancel()

	time.Sleep(50 * time.Millisecond)
	holder.mu.Lock()
	alive := holder.aliveLocked()
	holder.mu.Unlock()
	assert.True(t, alive, "holder must survive cancellation of the requesting command")
}

func TestReverseForwardHolderReusesAndRestarts(t *testing.T) {
	// Deliberately not parallel: overrides PATH.
	argsFile := startedForwardHolderArgs(t)
	holder := &reverseForwardHolder{key: "test"}
	t.Cleanup(holder.close)

	sshEnv := &recordingSSHEnv{&LocalEnv{}}
	forwards := []common.PortForwardConfig{{HostPort: 8080}}
	require.NoError(t, holder.ensure(context.Background(), sshEnv, forwards))
	require.NoError(t, holder.ensure(context.Background(), sshEnv, forwards))
	assert.Len(t, readHolderInvocations(t, argsFile), 1, "a live holder must be reused")

	holder.mu.Lock()
	cmd, exited := holder.cmd, holder.exited
	holder.mu.Unlock()
	require.NotNil(t, cmd)
	require.NoError(t, cmd.Process.Kill())
	<-exited

	require.NoError(t, holder.ensure(context.Background(), sshEnv, forwards))
	assert.Len(t, readHolderInvocations(t, argsFile), 2, "a dead holder must be replaced")
}

// TestReverseForwardHolderReportsImmediateExit covers a failed binding: ssh
// exits at once under ExitOnForwardFailure, and that must be an error rather
// than a silently routeless connection.
func TestReverseForwardHolderReportsImmediateExit(t *testing.T) {
	// Deliberately not parallel: overrides PATH and a package timing var.
	installFakeSSH(t, "echo 'bind: Address already in use' >&2\nexit 255\n")
	originalGrace := reverseForwardHolderStartGrace
	reverseForwardHolderStartGrace = 5 * time.Second
	t.Cleanup(func() { reverseForwardHolderStartGrace = originalGrace })

	holder := &reverseForwardHolder{key: "test"}
	err := holder.ensure(context.Background(), &recordingSSHEnv{&LocalEnv{}}, []common.PortForwardConfig{{HostPort: 8080}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Address already in use")
}

// TestReverseForwardHolderPlacesOptionsBeforeDestination guards the arg shape
// used by envs that terminate their args with a "--" separator: options placed
// after the destination are parsed by ssh as the remote command instead.
func TestReverseForwardHolderPlacesOptionsBeforeDestination(t *testing.T) {
	// Deliberately not parallel: overrides PATH.
	argsFile := startedForwardHolderArgs(t)
	holder := &reverseForwardHolder{key: "test"}
	t.Cleanup(holder.close)

	forwards := []common.PortForwardConfig{{HostPort: 8080, ContainerPort: 3000}}
	require.NoError(t, holder.ensure(context.Background(), &devpodShapedSSHEnv{&LocalEnv{}}, forwards))

	invocations := readHolderInvocations(t, argsFile)
	require.Len(t, invocations, 1)
	args := invocations[0]
	destination := strings.Index(args, "workspace.devpod")
	require.GreaterOrEqual(t, destination, 0)
	assert.Less(t, strings.Index(args, "-N"), destination, "-N must precede the destination")
	assert.Less(t, strings.Index(args, "-R 127.0.0.1:3000:127.0.0.1:8080"), destination, "-R must precede the destination")
}

// TestRunRemoteCommandGivesForwardsToTheHolderOnly pins the ownership rule:
// the dedicated holder binds the reverse forwards, and the command channel
// (whose lifetime is a command's) must not compete for the same bindings.
func TestRunRemoteCommandGivesForwardsToTheHolderOnly(t *testing.T) {
	// Deliberately not parallel: overrides PATH and package timing vars.
	argsFile := startedForwardHolderArgs(t)
	originalDialTimeout := agentExecDialTimeout
	agentExecDialTimeout = 200 * time.Millisecond
	t.Cleanup(func() { agentExecDialTimeout = originalDialTimeout })

	key := "test:forwards-owned-by-holder"
	forwards := []common.PortForwardConfig{{HostPort: 8080, ContainerPort: 3000}}
	t.Cleanup(func() {
		getPooledAgentExecConn(key).Close()
		closeReverseForwardHolder(reverseForwardHolderKey(key, forwards))
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := runRemoteCommand(ctx, key, forwards, &forwardingSSHEnv{&LocalEnv{}}, sideagent.ExecRequest{Argv: []string{"true"}})
	require.Error(t, err, "the fake ssh never speaks the agent protocol, so the command cannot succeed")

	var holderInvocations, channelInvocations []string
	for _, invocation := range readHolderInvocations(t, argsFile) {
		if strings.Contains(invocation, "-N") {
			holderInvocations = append(holderInvocations, invocation)
			continue
		}
		channelInvocations = append(channelInvocations, invocation)
	}

	require.Len(t, holderInvocations, 1, "exactly one dedicated forward holder must be started")
	assert.Contains(t, holderInvocations[0], "-R 127.0.0.1:3000:127.0.0.1:8080")
	require.NotEmpty(t, channelInvocations, "the command channel must still be dialed")
	for _, invocation := range channelInvocations {
		assert.NotContains(t, invocation, "-R ", "the command channel must not bind forwards the holder owns")
	}
}

// TestCloseAllReverseForwardHolders covers worker shutdown: holders are
// exempt from idle reaping, so shutdown is the only thing that reaps them.
func TestCloseAllReverseForwardHolders(t *testing.T) {
	// Deliberately not parallel: overrides PATH and closes every holder.
	startedForwardHolderArgs(t)
	key := "test:close-all-holders"
	holder := getReverseForwardHolder(key)
	t.Cleanup(func() { closeReverseForwardHolder(key) })

	require.NoError(t, holder.ensure(context.Background(), &recordingSSHEnv{&LocalEnv{}}, []common.PortForwardConfig{{HostPort: 8080}}))
	holder.mu.Lock()
	cmd := holder.cmd
	holder.mu.Unlock()
	require.NotNil(t, cmd, "expected a started holder process")

	CloseAllReverseForwardHolders()

	assert.NotNil(t, cmd.ProcessState, "shutdown must reap the holder process")
	assert.NotSame(t, holder, getReverseForwardHolder(key), "shutdown must evict every pool entry")
}

func TestReverseForwardHolderNoForwardsIsNoop(t *testing.T) {
	// Deliberately not parallel: overrides PATH.
	argsFile := startedForwardHolderArgs(t)
	holder := &reverseForwardHolder{key: "test"}

	require.NoError(t, holder.ensure(context.Background(), &recordingSSHEnv{&LocalEnv{}}, nil))
	assert.Empty(t, readHolderInvocations(t, argsFile))
}
