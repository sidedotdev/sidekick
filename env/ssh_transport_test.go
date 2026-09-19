package env

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
func (s *stubSSHTransport) ReplaceReverseForwards(context.Context, []common.PortForwardConfig, []common.PortForwardConfig) error {
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
// file and then blocks, standing in for a long-lived holder connection. It
// returns both the args file and the fake's absolute path for injection into
// holders, which sidesteps PATH resolution entirely.
func startedForwardHolderArgs(t *testing.T) (argsFile, sshPath string) {
	t.Helper()
	argsFile = filepath.Join(t.TempDir(), "args")
	sshPath = installFakeSSH(t, "printf '%s\\n' \"$*\" >> "+argsFile+"\nexec sleep 30\n")
	return argsFile, sshPath
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

// waitForHolderInvocations waits until the fake ssh has recorded at least n
// invocations, tolerating scheduling delays in the fake's own startup under
// load, then returns them all.
func waitForHolderInvocations(t *testing.T, argsFile string, n int) []string {
	t.Helper()
	var invocations []string
	require.Eventually(t, func() bool {
		contents, err := os.ReadFile(argsFile)
		if err != nil {
			return false
		}
		invocations = strings.Split(strings.TrimSpace(string(contents)), "\n")
		return len(invocations) >= n
	}, 15*time.Second, 10*time.Millisecond, "expected %d fake ssh invocation(s)", n)
	return invocations
}

func TestReverseForwardHolderRunsDedicatedConnection(t *testing.T) {
	// Deliberately not parallel: overrides PATH.
	argsFile, sshPath := startedForwardHolderArgs(t)
	holder := &reverseForwardHolder{key: "test", executable: sshPath}
	t.Cleanup(holder.close)

	forwards := []common.PortForwardConfig{{HostPort: 8080, ContainerPort: 3000}}
	require.NoError(t, holder.ensure(context.Background(), &recordingSSHEnv{&LocalEnv{}}, forwards))

	invocations := waitForHolderInvocations(t, argsFile, 1)
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
	_, sshPath := startedForwardHolderArgs(t)
	holder := &reverseForwardHolder{key: "test", executable: sshPath}
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
	argsFile, sshPath := startedForwardHolderArgs(t)
	holder := &reverseForwardHolder{key: "test", executable: sshPath}
	t.Cleanup(holder.close)

	sshEnv := &recordingSSHEnv{&LocalEnv{}}
	forwards := []common.PortForwardConfig{{HostPort: 8080}}
	require.NoError(t, holder.ensure(context.Background(), sshEnv, forwards))
	require.NoError(t, holder.ensure(context.Background(), sshEnv, forwards))
	assert.Len(t, waitForHolderInvocations(t, argsFile, 1), 1, "a live holder must be reused")

	holder.mu.Lock()
	cmd, exited := holder.cmd, holder.exited
	holder.mu.Unlock()
	require.NotNil(t, cmd)
	require.NoError(t, cmd.Process.Kill())
	<-exited

	require.NoError(t, holder.ensure(context.Background(), sshEnv, forwards))
	assert.Len(t, waitForHolderInvocations(t, argsFile, 2), 2, "a dead holder must be replaced")
}

// TestReverseForwardHolderReportsImmediateExit covers a failed binding: ssh
// exits at once under ExitOnForwardFailure, and that must be an error rather
// than a silently routeless connection.
func TestReverseForwardHolderReportsImmediateExit(t *testing.T) {
	// Deliberately not parallel: overrides PATH and a package timing var.
	sshPath := installFakeSSH(t, "echo 'bind: Address already in use' >&2\nexit 255\n")
	originalGrace := reverseForwardHolderStartGrace
	// Generous grace: ensure returns as soon as the fake exits, so this only
	// bounds how long a heavily loaded machine may take to run it.
	reverseForwardHolderStartGrace = 30 * time.Second
	t.Cleanup(func() { reverseForwardHolderStartGrace = originalGrace })

	holder := &reverseForwardHolder{key: "test", executable: sshPath}
	err := holder.ensure(context.Background(), &recordingSSHEnv{&LocalEnv{}}, []common.PortForwardConfig{{HostPort: 8080}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Address already in use")
}

// TestReverseForwardHolderPlacesOptionsBeforeDestination guards the arg shape
// used by envs that terminate their args with a "--" separator: options placed
// after the destination are parsed by ssh as the remote command instead.
func TestReverseForwardHolderPlacesOptionsBeforeDestination(t *testing.T) {
	// Deliberately not parallel: overrides PATH.
	argsFile, sshPath := startedForwardHolderArgs(t)
	holder := &reverseForwardHolder{key: "test", executable: sshPath}
	t.Cleanup(holder.close)

	forwards := []common.PortForwardConfig{{HostPort: 8080, ContainerPort: 3000}}
	require.NoError(t, holder.ensure(context.Background(), &devpodShapedSSHEnv{&LocalEnv{}}, forwards))

	invocations := waitForHolderInvocations(t, argsFile, 1)
	require.Len(t, invocations, 1)
	args := invocations[0]
	destination := strings.Index(args, "workspace.devpod")
	require.GreaterOrEqual(t, destination, 0)
	assert.Less(t, strings.Index(args, "-N"), destination, "-N must precede the destination")
	assert.Less(t, strings.Index(args, "-R 127.0.0.1:3000:127.0.0.1:8080"), destination, "-R must precede the destination")
}

// holderAlive reports whether the holder registered under key is running.
func holderAlive(key string) bool {
	holder := getReverseForwardHolder(key)
	holder.mu.Lock()
	defer holder.mu.Unlock()
	return holder.aliveLocked()
}

// TestReverseForwardStateRetirementWaitsForInFlightOperation pins that
// shutdown retiring a remote's shared state does not let a newcomer run under
// a second lock while an operation still holds the first. The newcomer queues
// behind it and then runs under whichever state is published when its turn
// comes: the old one if it wins the lock before retirement, a fresh one after.
func TestReverseForwardStateRetirementWaitsForInFlightOperation(t *testing.T) {
	// Deliberately not parallel: retirement touches every remote's state.
	const key = "test:retire-in-flight"
	inFlight := lockReverseForwardState(key)
	inFlight.replaceLocked([]common.PortForwardConfig{{HostPort: 1}})
	unlockInFlight := sync.OnceFunc(inFlight.mu.Unlock)
	newcomer := make(chan *reverseForwardState, 1)
	queuedReceived := false
	// Cleanups run last-registered first: every lock this test may still hold
	// on a failure path is released before the state is retired.
	t.Cleanup(func() {
		state := lockReverseForwardState(key)
		state.retireLocked()
		state.mu.Unlock()
	})
	t.Cleanup(func() {
		unlockInFlight()
		if queuedReceived {
			return
		}
		select {
		case state := <-newcomer:
			state.mu.Unlock()
		case <-time.After(time.Second):
		}
	})

	retired := make(chan struct{})
	go func() {
		retireUnservedReverseForwardStates(func(string) bool { return false })
		close(retired)
	}()
	go func() { newcomer <- lockReverseForwardState(key) }()

	select {
	case <-retired:
		t.Fatal("retirement completed while an operation still held the state")
	case state := <-newcomer:
		state.mu.Unlock()
		queuedReceived = true
		t.Fatal("a second lock was handed out for one remote while an operation still held the first")
	case <-time.After(200 * time.Millisecond):
	}

	unlockInFlight()
	var queued *reverseForwardState
	select {
	case queued = <-newcomer:
		queuedReceived = true
	case <-time.After(10 * time.Second):
		t.Fatal("queued operation never acquired the remote's state")
	}
	assert.False(t, queued.retired, "a queued operation must never run under a retired state")
	reverseForwardStates.mu.Lock()
	published := reverseForwardStates.byRemote[key]
	reverseForwardStates.mu.Unlock()
	assert.True(t, queued == published, "the queued operation must run under the state later callers find")
	if queued != inFlight {
		assert.False(t, queued.replaced, "a retired replacement must not carry over")
	}
	queued.mu.Unlock()

	select {
	case <-retired:
	case <-time.After(10 * time.Second):
		t.Fatal("retirement never completed")
	}
	assert.True(t, inFlight.retired)
}

// TestLegacyTransportReplaceReverseForwardsRestartsHolder pins that a mapping
// change stops the holder bound under the old mappings before starting one for
// the new, across the transports production builds per operation. Holders are
// keyed by their forwards, so the transport built under the old mappings would
// otherwise keep a holder competing for the same remote ports, and re-running
// setup through it must not start that holder again. An empty desired list
// must leave no holder running, even against stale setup.
func TestLegacyTransportReplaceReverseForwardsRestartsHolder(t *testing.T) {
	// Deliberately not parallel: overrides PATH.
	argsFile, _ := startedForwardHolderArgs(t)
	const key = "test:replace-forwards"
	previous := []common.PortForwardConfig{{HostPort: 8080, ContainerPort: 3000}}
	desired := []common.PortForwardConfig{{HostPort: 9090, ContainerPort: 3000}}
	previousKey := reverseForwardHolderKey(key, previous)
	desiredKey := reverseForwardHolderKey(key, desired)
	sshEnv := &recordingSSHEnv{&LocalEnv{}}
	ctx := context.Background()

	before := &legacySSHTransport{key: key, forwards: previous, sshEnv: sshEnv}
	t.Cleanup(before.Close)
	require.NoError(t, before.EnsureReverseForwards(ctx, previous))
	require.Len(t, waitForHolderInvocations(t, argsFile, 1), 1)
	previousHolder := getReverseForwardHolder(previousKey)
	previousHolder.mu.Lock()
	previousExited := previousHolder.exited
	previousHolder.mu.Unlock()
	require.NotNil(t, previousExited)

	require.NoError(t, before.ReplaceReverseForwards(ctx, previous, desired))
	select {
	case <-previousExited:
	default:
		t.Fatal("the holder for the old mappings must be stopped before the new one binds")
	}
	invocations := waitForHolderInvocations(t, argsFile, 2)
	require.Len(t, invocations, 2)
	assert.Contains(t, invocations[1], "-R 127.0.0.1:3000:127.0.0.1:9090")

	after := &legacySSHTransport{key: key, forwards: desired, sshEnv: sshEnv}
	require.NoError(t, after.EnsureReverseForwards(ctx, desired))
	assert.Len(t, readHolderInvocations(t, argsFile), 2, "a fresh transport must reuse the replacement holder")

	require.NoError(t, before.EnsureReverseForwards(ctx, previous))
	assert.False(t, holderAlive(previousKey), "stale setup must not start a holder for replaced mappings")
	assert.True(t, holderAlive(desiredKey))
	assert.Len(t, readHolderInvocations(t, argsFile), 2)

	cleared := &legacySSHTransport{key: key, sshEnv: sshEnv}
	require.NoError(t, cleared.ReplaceReverseForwards(ctx, desired, nil))
	assert.False(t, holderAlive(desiredKey), "clearing the mappings must stop the holder")

	require.NoError(t, after.EnsureReverseForwards(ctx, desired))
	assert.False(t, holderAlive(desiredKey), "stale setup must not start a holder for cleared mappings")
	assert.Len(t, readHolderInvocations(t, argsFile), 2, "an empty mapping list must not start a holder")
}

// TestLegacyTransportReplaceReverseForwardsKeepsRetainedAndOtherRemotes
// covers a replacement that edits a multi-mapping list: the holder restarts
// with the mapping kept plus the one added and without the one dropped, while
// another remote's holder and the remote's pooled exec and SFTP channels keep
// serving through both the replacement and a later clear.
func TestLegacyTransportReplaceReverseForwardsKeepsRetainedAndOtherRemotes(t *testing.T) {
	// Deliberately not parallel: overrides PATH.
	argsFile, _ := startedForwardHolderArgs(t)
	const key = "test:replace-multi"
	const otherKey = "test:replace-multi-other"
	retained := common.PortForwardConfig{HostPort: 8080, ContainerPort: 3000}
	removed := common.PortForwardConfig{HostPort: 8081, ContainerPort: 3001}
	added := common.PortForwardConfig{HostPort: 8082, ContainerPort: 3002}
	initial := []common.PortForwardConfig{retained, removed}
	desired := []common.PortForwardConfig{retained, added}
	other := []common.PortForwardConfig{{HostPort: 9000, ContainerPort: 4000}}
	sshEnv := &recordingSSHEnv{&LocalEnv{}}
	ctx := context.Background()

	edited := &legacySSHTransport{key: key, forwards: initial, sshEnv: sshEnv}
	t.Cleanup(edited.Close)
	otherRemote := &legacySSHTransport{key: otherKey, forwards: other, sshEnv: sshEnv}
	t.Cleanup(otherRemote.Close)

	execConn := getPooledAgentExecConn(key)
	execClient := startTestAgentClient(t)
	execConn.mu.Lock()
	execConn.client = execClient
	execConn.mu.Unlock()
	sftpConn := sharedSFTPConnFor(key)
	sftpClient := newInMemorySFTPClient(t)
	sftpConn.mu.Lock()
	sftpConn.client = sftpClient
	sftpConn.mu.Unlock()
	dir := t.TempDir()
	assertChannelsServe := func(name string) {
		t.Helper()
		resp, err := edited.Exec(ctx, sideagent.ExecRequest{Argv: []string{"echo", name}})
		require.NoError(t, err)
		assert.Contains(t, string(resp.Stdout), name)
		execConn.mu.Lock()
		sameExec := execConn.client == execClient
		execConn.mu.Unlock()
		assert.True(t, sameExec, "the pooled exec channel must be the one established before the replacement")

		path := filepath.Join(dir, name)
		require.NoError(t, sftpWriteFile(ctx, edited, path, []byte(name), 0o644))
		content, err := sftpReadFile(ctx, edited, path)
		require.NoError(t, err)
		assert.Equal(t, name, string(content))
		sftpConn.mu.Lock()
		sameSFTP := sftpConn.client == sftpClient
		sftpConn.mu.Unlock()
		assert.True(t, sameSFTP, "the pooled SFTP channel must be the one established before the replacement")
	}
	exitedOf := func(holderKey string) chan struct{} {
		t.Helper()
		holder := getReverseForwardHolder(holderKey)
		holder.mu.Lock()
		defer holder.mu.Unlock()
		require.NotNil(t, holder.exited)
		return holder.exited
	}
	assertStillRunning := func(exited chan struct{}, msg string) {
		t.Helper()
		select {
		case <-exited:
			t.Fatal(msg)
		default:
		}
	}

	require.NoError(t, edited.EnsureReverseForwards(ctx, initial))
	require.NoError(t, otherRemote.EnsureReverseForwards(ctx, other))
	invocations := waitForHolderInvocations(t, argsFile, 2)
	require.Len(t, invocations, 2)
	assert.Contains(t, invocations[0], "-R 127.0.0.1:3000:127.0.0.1:8080")
	assert.Contains(t, invocations[0], "-R 127.0.0.1:3001:127.0.0.1:8081")
	initialExited := exitedOf(reverseForwardHolderKey(key, initial))
	otherExited := exitedOf(reverseForwardHolderKey(otherKey, other))
	assertChannelsServe("before-replace")

	require.NoError(t, edited.ReplaceReverseForwards(ctx, initial, desired))
	select {
	case <-initialExited:
	default:
		t.Fatal("the holder bound under the old mappings must be stopped")
	}
	invocations = waitForHolderInvocations(t, argsFile, 3)
	require.Len(t, invocations, 3)
	assert.Contains(t, invocations[2], "-R 127.0.0.1:3000:127.0.0.1:8080", "the retained mapping must stay bound")
	assert.Contains(t, invocations[2], "-R 127.0.0.1:3002:127.0.0.1:8082", "the added mapping must be bound")
	assert.NotContains(t, invocations[2], "127.0.0.1:8081", "the dropped mapping must not be bound again")
	assertStillRunning(otherExited, "another remote's holder must survive a replacement")
	assertChannelsServe("after-replace")

	require.NoError(t, edited.ReplaceReverseForwards(ctx, desired, nil))
	assert.False(t, holderAlive(reverseForwardHolderKey(key, desired)), "clearing must stop the remote's holder")
	assertStillRunning(otherExited, "another remote's holder must survive a clear")
	assert.Len(t, readHolderInvocations(t, argsFile), 3, "clearing must not start a holder")
	assertChannelsServe("after-clear")
}

// TestLegacyTransportCloseAfterReplacementStopsEffectiveHolder pins that
// closing a transport built under the old mappings stops the holder a
// replacement made effective, and forgets the replacement so the remote key
// starts afresh: setup afterwards binds the transport's own mappings again.
func TestLegacyTransportCloseAfterReplacementStopsEffectiveHolder(t *testing.T) {
	// Deliberately not parallel: overrides PATH.
	argsFile, _ := startedForwardHolderArgs(t)
	const key = "test:close-after-replace"
	previous := []common.PortForwardConfig{{HostPort: 8080, ContainerPort: 3000}}
	desired := []common.PortForwardConfig{{HostPort: 9090, ContainerPort: 3000}}
	ctx := context.Background()

	before := &legacySSHTransport{key: key, forwards: previous, sshEnv: &recordingSSHEnv{&LocalEnv{}}}
	t.Cleanup(before.Close)
	require.NoError(t, before.EnsureReverseForwards(ctx, previous))
	require.NoError(t, before.ReplaceReverseForwards(ctx, previous, desired))
	require.Len(t, waitForHolderInvocations(t, argsFile, 2), 2)
	require.True(t, holderAlive(reverseForwardHolderKey(key, desired)))

	before.Close()
	assert.False(t, holderAlive(reverseForwardHolderKey(key, desired)), "Close must stop the holder in effect, not the one the transport was built with")

	require.NoError(t, before.EnsureReverseForwards(ctx, previous))
	invocations := waitForHolderInvocations(t, argsFile, 3)
	require.Len(t, invocations, 3)
	assert.Contains(t, invocations[2], "-R 127.0.0.1:3000:127.0.0.1:8080")
}

// TestLegacyTransportReplaceReverseForwardsRetriesFailedBind covers a holder
// that exits at once because the remote refused a binding: the failure must be
// reported, and a retry must start a holder afresh.
func TestLegacyTransportReplaceReverseForwardsRetriesFailedBind(t *testing.T) {
	// Deliberately not parallel: overrides PATH.
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	failedOnce := filepath.Join(dir, "failed-once")
	installFakeSSH(t, strings.Join([]string{
		"if [ ! -f " + failedOnce + " ]; then",
		"  touch " + failedOnce,
		"  echo 'bind: Address already in use' >&2",
		"  exit 255",
		"fi",
		"printf '%s\\n' \"$*\" >> " + argsFile,
		"exec sleep 30",
		"",
	}, "\n"))
	const key = "test:replace-forwards-retry"
	desired := []common.PortForwardConfig{{HostPort: 8080, ContainerPort: 3000}}
	t.Cleanup(func() { closeReverseForwardHolder(reverseForwardHolderKey(key, desired)) })
	transport := &legacySSHTransport{key: key, forwards: desired, sshEnv: &recordingSSHEnv{&LocalEnv{}}}
	ctx := context.Background()

	err := transport.ReplaceReverseForwards(ctx, nil, desired)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Address already in use")

	require.NoError(t, transport.ReplaceReverseForwards(ctx, nil, desired))
	invocations := waitForHolderInvocations(t, argsFile, 1)
	require.Len(t, invocations, 1)
	assert.Contains(t, invocations[0], "-R 127.0.0.1:3000:127.0.0.1:8080")
}

// TestRunRemoteCommandGivesForwardsToTheHolderOnly pins the ownership rule:
// the dedicated holder binds the reverse forwards, and the command channel
// (whose lifetime is a command's) must not compete for the same bindings.
func TestRunRemoteCommandGivesForwardsToTheHolderOnly(t *testing.T) {
	// Deliberately not parallel: overrides PATH and package timing vars.
	argsFile, _ := startedForwardHolderArgs(t)
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
	argsFile, _ := startedForwardHolderArgs(t)
	holder := &reverseForwardHolder{key: "test"}

	require.NoError(t, holder.ensure(context.Background(), &recordingSSHEnv{&LocalEnv{}}, nil))
	assert.Empty(t, readHolderInvocations(t, argsFile))
}
