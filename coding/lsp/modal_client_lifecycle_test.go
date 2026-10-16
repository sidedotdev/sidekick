package lsp

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"sidekick/env"
	"sidekick/sideagent"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

type leaseSSHClient struct {
	MockLSPClient
	transport *ReadWriteCloser
	begin     func(context.Context, func(error)) (func(), error)
}

func (c *leaseSSHClient) Close() error {
	if c.transport == nil {
		return nil
	}
	return c.transport.Close()
}

type leaseSSHEnv struct {
	*fakeSSHEnv
	writeFile func(context.Context, string, []byte, fs.FileMode) error
}

func (e *leaseSSHEnv) WriteFile(ctx context.Context, path string, data []byte, perm fs.FileMode) error {
	return e.writeFile(ctx, path, data, perm)
}

type analysisSSHServer struct {
	args     []string
	started  chan *exec.Cmd
	exited   chan *exec.Cmd
	agent    string
	sessions string
	shell    string
	mu       sync.Mutex
	pids     []int
}

// The semantic responses are mocked, but transport ownership is exercised
// through an SSH exec session whose child must exit on stdin EOF.
func startAnalysisSSHServer(t *testing.T, agent string) *analysisSSHServer {
	t.Helper()
	_, err := exec.LookPath("ssh")
	require.NoError(t, err)
	dir := t.TempDir()
	shellPath, err := exec.LookPath("sh")
	require.NoError(t, err)
	shell, err := os.ReadFile(shellPath)
	require.NoError(t, err)
	shim := filepath.Join(dir, "sshd")
	require.NoError(t, os.WriteFile(shim, shell, 0o700))
	_, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(key)
	require.NoError(t, err)
	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	server := &analysisSSHServer{
		args: []string{
			"-F", "/dev/null",
			"-o", "BatchMode=yes",
			"-o", "PreferredAuthentications=none",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "ControlMaster=no",
			"-p", strconv.Itoa(port),
			"test@127.0.0.1",
		},
		started:  make(chan *exec.Cmd, 4),
		exited:   make(chan *exec.Cmd, 4),
		agent:    agent,
		sessions: filepath.Join(dir, "sessions"),
		shell:    shim,
	}
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		workers.Wait()
	})
	workers.Go(func() {
		for {
			raw, err := listener.Accept()
			if err != nil {
				if ctx.Err() == nil {
					t.Errorf("accept SSH connection: %v", err)
				}
				return
			}
			workers.Go(func() {
				defer raw.Close()
				stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
				defer stop()
				conn, channels, requests, err := ssh.NewServerConn(raw, config)
				if err != nil {
					if ctx.Err() == nil {
						t.Errorf("SSH handshake: %v", err)
					}
					return
				}
				defer conn.Close()
				go ssh.DiscardRequests(requests)
				for incoming := range channels {
					if incoming.ChannelType() != "session" {
						_ = incoming.Reject(ssh.UnknownChannelType, "session required")
						continue
					}
					channel, requests, err := incoming.Accept()
					if err != nil {
						t.Errorf("accept SSH session: %v", err)
						continue
					}
					workers.Go(func() {
						defer channel.Close()
						for request := range requests {
							if request.Type != "exec" {
								_ = request.Reply(false, nil)
								continue
							}
							var payload struct{ Command string }
							if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
								t.Errorf("decode SSH exec: %v", err)
								_ = request.Reply(false, nil)
								return
							}
							command := payload.Command
							if strings.HasPrefix(command, "/tmp/side-agent-") {
								_, mode, found := strings.Cut(command, " background ")
								if !found {
									t.Error("analysis command did not opt into managed background execution")
									if err := request.Reply(false, nil); err != nil {
										t.Errorf("reject SSH exec request: %v", err)
									}
									return
								}
								command = lspShellQuote(server.agent) + " background " + mode
							}
							// A retained shell named sshd supplies the same process
							// boundary that a real SSH server supplies on Linux.
							cmd := exec.CommandContext(ctx, server.shell, "-c", command+"; status=$?; exit $status")
							cmd.Env = append(os.Environ(), "SIDEKICK_SESSION_DIR="+server.sessions)
							cmd.Stdin = channel
							cmd.Stdout = channel
							var stderr bytes.Buffer
							cmd.Stderr = io.MultiWriter(channel.Stderr(), &stderr)
							if err := cmd.Start(); err != nil {
								t.Errorf("start SSH child: %v", err)
								_ = request.Reply(false, nil)
								return
							}
							server.mu.Lock()
							server.pids = append(server.pids, cmd.Process.Pid)
							server.mu.Unlock()
							_ = request.Reply(true, nil)
							server.started <- cmd
							if err := cmd.Wait(); err != nil && ctx.Err() == nil {
								t.Errorf("SSH child failed: %v; stderr=%s", err, stderr.String())
							}
							server.exited <- cmd
							_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{}))
							return
						}
					})
				}
			})
		}
	})
	return server
}

func TestManagedAnalysisSSHSessionActivity(t *testing.T) {
	t.Parallel()
	agent := filepath.Join(t.TempDir(), "side-agent")
	build := exec.Command("go", "build", "-o", agent, "../../cmd/side-agent")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "%s", output)
	for _, operation := range []string{"definition", "references", "failed references", "sync", "autofix", "canceled sync"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			server := startAnalysisSSHServer(t, agent)
			dir := t.TempDir()
			marker := filepath.Join(dir, "activity")
			lock := filepath.Join(dir, "worktree.lock")
			seal := sideagent.NewSeal(lock, filepath.Join(dir, "sealed"))
			begin := server.activityLeaser(t, marker, lock)
			catPath, err := exec.LookPath("cat")
			require.NoError(t, err)
			container := &env.EnvContainer{Env: &fakeSSHEnv{
				fakeEnv: &fakeEnv{
					envType: env.EnvTypeModal,
					workDir: "/workspace",
					runCommand: func(context.Context, env.EnvRunCommandInput) (env.EnvRunCommandOutput, error) {
						return env.EnvRunCommandOutput{Stdout: catPath, ExitStatus: 0}, nil
					},
					readFile: func(context.Context, string) ([]byte, error) {
						return []byte("package fixture\nfunc Target() {}\n"), nil
					},
				},
				sshArgs: server.args,
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var clients []*leaseSSHClient
			var events []string
			assertActive := func(event string) {
				t.Helper()
				select {
				case <-server.exited:
					t.Fatalf("SSH session ended during %s", event)
				default:
				}
				sealed, err := seal.TrySeal(0)
				require.NoError(t, err)
				require.False(t, sealed, "activity must protect %s, including cancellation cleanup", event)
				require.True(t, server.watchdogBusy(t, marker), "watchdog must see active %s", event)
				events = append(events, event)
			}
			content := []byte("package fixture\nfunc Target() {}\n")
			if operation == "autofix" {
				remote := container.Env.(*fakeSSHEnv)
				remote.readFile = func(context.Context, string) ([]byte, error) {
					assertActive("read")
					return content, nil
				}
				container.Env = &leaseSSHEnv{
					fakeSSHEnv: remote,
					writeFile: func(_ context.Context, path string, data []byte, _ fs.FileMode) error {
						assertActive("write")
						require.Equal(t, "/workspace/main.go", path)
						content = data
						return nil
					},
				}
			}
			activities := NewLSPActivities(func(string) LSPClient {
				client := &leaseSSHClient{begin: begin}
				client.InitializeFunc = func(ctx context.Context, _ InitializeParams) (InitializeResponse, error) {
					transport, err := lspServerStdioReadWriteCloser(ctx, "golang", container)
					if err != nil {
						return InitializeResponse{}, err
					}
					client.transport = transport
					select {
					case child := <-server.started:
						require.Eventually(t, func() bool {
							for _, pid := range sessionChildren(t, child.Process.Pid) {
								if _, err := os.Stat(filepath.Join(server.sessions, pid)); err == nil {
									return true
								}
							}
							return false
						}, time.Second, time.Millisecond, "SSH wrapper must register before serving")
						return InitializeResponse{}, nil
					case <-ctx.Done():
						return InitializeResponse{}, ctx.Err()
					}
				}
				client.TextDocumentDefinitionFunc = func(context.Context, string, int, int) ([]Location, error) {
					assertActive("definition")
					return nil, nil
				}
				client.TextDocumentReferencesFunc = func(context.Context, string, int, int) ([]Location, error) {
					assertActive("references")
					if operation == "failed references" {
						return nil, errors.New("references failed")
					}
					return []Location{{URI: "file:///workspace/main.go"}}, nil
				}
				client.TextDocumentDidOpenFunc = func(context.Context, DidOpenTextDocumentParams) error {
					assertActive("open")
					return nil
				}
				client.TextDocumentDidChangeFunc = func(context.Context, DidChangeTextDocumentParams) error {
					assertActive("change")
					if operation == "canceled sync" {
						cancel()
						return ctx.Err()
					}
					return nil
				}
				client.TextDocumentDidSaveFunc = func(context.Context, DidSaveTextDocumentParams) error {
					assertActive("save")
					return nil
				}
				client.TextDocumentDidCloseFunc = func(context.Context, DidCloseTextDocumentParams) error {
					assertActive("close")
					return nil
				}
				client.TextDocumentCodeActionFunc = func(_ context.Context, params CodeActionParams) ([]CodeAction, error) {
					assertActive("autofix")
					require.Equal(t, []CodeActionKind{
						CodeActionKindSourceFixAll, CodeActionKindSourceOrganizeImports,
					}, params.Context.Only)
					edit := TextDocumentEdit{}
					edit.TextDocument.URI = params.TextDocument.URI
					edit.Edits = []TextEdit{{
						Range: Range{
							Start: Position{Line: 1, Character: 5},
							End:   Position{Line: 1, Character: 11},
						},
						NewText: "Fixed",
					}}
					kind := CodeActionKindSourceFixAll
					return []CodeAction{{
						Kind: &kind,
						Edit: &WorkspaceEdit{DocumentChanges: []TextDocumentEdit{edit}},
					}}, nil
				}
				clients = append(clients, client)
				return client
			})
			activities.idleTimeout = time.Hour
			t.Cleanup(func() { require.NoError(t, activities.Close()) })

			switch operation {
			case "definition":
				request := definitionRequest("/workspace")
				request.EnvContainer = container
				_, err = activities.GetSingleFileDefinitions(ctx, request)
				require.NoError(t, err)
				require.NotEmpty(t, events)
			case "references", "failed references":
				locations, err := activities.FindReferencesActivity(ctx, FindReferencesActivityInput{
					EnvContainer: *container, RelativeFilePath: "main.go",
					SymbolText: "Target", PreloadedFileContent: content,
				})
				if operation == "failed references" {
					require.ErrorContains(t, err, "references failed")
				} else {
					require.NoError(t, err)
					require.Equal(t, []Location{{URI: "file:///workspace/main.go"}}, locations)
				}
				require.Equal(t, []string{"references"}, events)
			case "sync", "canceled sync":
				_, err = activities.SyncDocumentActivity(ctx, SyncDocumentActivityInput{
					EnvContainer: *container, FilePath: "main.go", Content: "package fixture\n",
				})
				if operation == "canceled sync" {
					require.ErrorIs(t, err, context.Canceled)
					require.Equal(t, []string{"open", "change", "close"}, events)
				} else {
					require.NoError(t, err)
					require.Equal(t, []string{"open", "change", "save", "close"}, events)
				}
			case "autofix":
				output, err := activities.AutofixActivity(ctx, AutofixActivityInput{
					EnvContainer: *container, DocumentURI: "file:///workspace/main.go",
				})
				require.NoError(t, err)
				require.Len(t, output.AppliedEdits, 1)
				require.Empty(t, output.FailedEdits)
				require.Equal(t, "package fixture\nfunc Fixed() {}\n", string(content))
				require.Equal(t, []string{"read", "autofix", "read", "write"}, events)
			}
			require.Len(t, clients, 1)
			sealed, err := seal.TrySeal(0)
			require.NoError(t, err)
			require.True(t, sealed, "release must drain activity admission")
			require.NoError(t, seal.Lift())
			old := time.Unix(1, 0)
			require.NoError(t, os.Chtimes(marker, old, old))
			require.False(t, server.watchdogBusy(t, marker), "cached managed sessions must not pin idling")

			if operation == "canceled sync" {
				require.Empty(t, activities.InitializedClients)
			} else {
				require.Len(t, activities.InitializedClients, 1)
				select {
				case <-server.exited:
					t.Fatal("released activity unnecessarily closed the cached SSH session")
				default:
				}
				request := definitionRequest("/workspace")
				request.EnvContainer = container
				_, err := activities.GetSingleFileDefinitions(ctx, request)
				require.NoError(t, err)
				require.Len(t, clients, 1, "later work must reuse the same connection")
				require.NoError(t, os.Chtimes(marker, old, old))
				require.False(t, server.watchdogBusy(t, marker))
			}

			require.NoError(t, activities.Close())
			select {
			case child := <-server.exited:
				require.True(t, child.ProcessState.Success(), "remote command must exit on EOF")
			case <-time.After(3 * time.Second):
				t.Fatal("closing the cache retained its SSH session")
			}
			require.NotNil(t, clients[0].transport.command.ProcessState, "local SSH process must be reaped")
			require.True(t, clients[0].transport.command.ProcessState.Success())
			require.Empty(t, activities.InitializedClients)
		})
	}
}

func TestClientCacheReusesClientsAcrossEnvironmentTypes(t *testing.T) {
	t.Parallel()
	for _, envType := range []env.EnvType{env.EnvTypeLocal, env.EnvTypeDevPod, env.EnvTypeOpenShell, env.EnvTypeModal} {
		t.Run(fmt.Sprint(envType), func(t *testing.T) {
			t.Parallel()
			client := &ownedTestClient{}
			created := 0
			activities := NewLSPActivities(func(string) LSPClient {
				created++
				return client
			})
			activities.idleTimeout = time.Hour
			t.Cleanup(func() { require.NoError(t, activities.Close()) })
			container := &env.EnvContainer{Env: &fakeEnv{envType: envType, workDir: "/workspace"}}
			for range 2 {
				_, got, release, err := activities.findOrInitClient(context.Background(), "/workspace", "golang", container)
				require.NoError(t, err)
				require.Same(t, client, got)
				release()
				require.Zero(t, client.closed.Load())
			}
			require.Equal(t, 1, created)
		})
	}
}

func (c *leaseSSHClient) BeginActivity(ctx context.Context, onLost func(error)) (func(), error) {
	return c.begin(ctx, onLost)
}

func (s *analysisSSHServer) activityLeaser(t *testing.T, marker, lock string) func(context.Context, func(error)) (func(), error) {
	t.Helper()
	cmd := exec.Command("ssh", append(append([]string{}, s.args...), lspShellQuote(s.agent)+" exec")...)
	stream, err := startServerTransport(cmd)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, stream.Close()) })
	select {
	case <-s.started:
	case <-time.After(3 * time.Second):
		t.Fatal("activity SSH channel did not start")
	}
	client := sideagent.NewClient(stream, stream)
	return func(ctx context.Context, onLost func(error)) (func(), error) {
		leaseCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		ready := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			resp, err := client.Exec(leaseCtx, sideagent.ExecRequest{
				ActivityLease: true,
				ReadLockFile:  lock,
				TouchPath:     marker,
				OnReady:       func() { close(ready) },
			})
			if err == nil {
				err = fmt.Errorf("activity ended: %+v", resp)
			}
			if leaseCtx.Err() == nil && onLost != nil {
				onLost(err)
			}
			done <- err
		}()
		select {
		case <-ready:
			return func() {
				cancel()
				<-done
			}, nil
		case err := <-done:
			cancel()
			return nil, err
		case <-ctx.Done():
			cancel()
			<-done
			return nil, ctx.Err()
		}
	}
}

func (s *analysisSSHServer) watchdogBusy(t *testing.T, marker string) bool {
	t.Helper()
	dir := t.TempDir()
	s.mu.Lock()
	var pids []string
	for _, pid := range s.pids {
		pids = append(pids, sessionChildren(t, pid)...)
	}
	s.mu.Unlock()
	pgrep := `#!/bin/sh
case "$*" in
    "-x sshd -d,"|"-x sshd|sshd-session -d,") echo 1 ;;
    "-P 1") printf '%s\n' "$FIXTURE_SESSION_PIDS" ;;
esac
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pgrep"), []byte(pgrep), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cut"), []byte("#!/bin/sh\necho 0\n"), 0o700))
	source, err := os.ReadFile("../../env/modal_watchdog.sh")
	require.NoError(t, err)
	script, _, found := strings.Cut(string(source), "# Admission fence.")
	require.True(t, found)
	script = strings.ReplaceAll(script, "/tmp/.sidekick-activity", marker)
	script = strings.ReplaceAll(script, "/dev/pts/", filepath.Join(dir, "pts")+"/")
	script += "\nif is_busy; then printf 'BUSY:%s\\n' \"$busy_reason\"; else echo IDLE; fi\n"
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(),
		"PATH="+dir+":"+os.Getenv("PATH"),
		"SIDEKICK_SESSION_DIR="+s.sessions,
		"FIXTURE_SESSION_PIDS="+strings.Join(pids, "\n"))
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
	if string(output) == "IDLE\n" {
		return false
	}
	reason, found := strings.CutPrefix(strings.TrimSpace(string(output)), "BUSY:")
	require.True(t, found, "invalid watchdog output: %q", output)
	t.Logf("watchdog busy_reason=%s", reason)
	for _, pid := range pids {
		stat, statErr := os.ReadFile(filepath.Join("/proc", pid, "stat"))
		status, statusErr := os.ReadFile(filepath.Join("/proc", pid, "status"))
		var tgid string
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "Tgid:") {
				tgid = strings.TrimSpace(strings.TrimPrefix(line, "Tgid:"))
			}
		}
		registration, regErr := os.ReadFile(filepath.Join(s.sessions, pid))
		t.Logf("inspected pid=%s tgid=%s statusError=%v stat=%q statError=%v registration=%q registrationError=%v",
			pid, tgid, statusErr, stat, statErr, registration, regErr)
	}
	return true
}

func sessionChildren(t *testing.T, pid int) []string {
	t.Helper()
	data, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil
		}
		require.NoError(t, err)
	}
	return strings.Fields(string(data))
}
