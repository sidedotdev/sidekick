package lsp

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"

	"sidekick/env"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

type leaseSSHClient struct {
	MockLSPClient
	transport *ReadWriteCloser
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
	args    []string
	started chan *exec.Cmd
	exited  chan *exec.Cmd
}

// The semantic responses are mocked, but transport ownership is exercised
// through an SSH exec session whose child must exit on stdin EOF.
func startAnalysisSSHServer(t *testing.T) *analysisSSHServer {
	t.Helper()
	_, err := exec.LookPath("ssh")
	require.NoError(t, err)
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
		started: make(chan *exec.Cmd, 4),
		exited:  make(chan *exec.Cmd, 4),
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
							cmd := exec.CommandContext(ctx, "sh", "-c", payload.Command)
							cmd.Stdin = channel
							cmd.Stdout = channel
							cmd.Stderr = channel.Stderr()
							if err := cmd.Start(); err != nil {
								t.Errorf("start SSH child: %v", err)
								_ = request.Reply(false, nil)
								return
							}
							_ = request.Reply(true, nil)
							server.started <- cmd
							if err := cmd.Wait(); err != nil && ctx.Err() == nil {
								t.Errorf("SSH child failed: %v", err)
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

func TestModalClientReleaseClosesSSHSession(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"definition", "references", "failed references", "sync", "autofix", "canceled sync"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			server := startAnalysisSSHServer(t)
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
				client := &leaseSSHClient{}
				client.InitializeFunc = func(ctx context.Context, _ InitializeParams) (InitializeResponse, error) {
					transport, err := lspServerStdioReadWriteCloser(ctx, "golang", container)
					if err != nil {
						return InitializeResponse{}, err
					}
					client.transport = transport
					select {
					case <-server.started:
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
			select {
			case child := <-server.exited:
				require.True(t, child.ProcessState.Success(), "remote command must exit on EOF")
			case <-time.After(3 * time.Second):
				t.Fatal("completed Modal operation retained its SSH session")
			}
			require.NotNil(t, clients[0].transport.command.ProcessState, "local SSH process must be reaped")
			require.True(t, clients[0].transport.command.ProcessState.Success())
			require.Empty(t, activities.InitializedClients)
		})
	}
}

func TestClientCacheReusesNonModalClients(t *testing.T) {
	t.Parallel()
	for _, envType := range []env.EnvType{env.EnvTypeLocal, env.EnvTypeDevPod} {
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
				got, release, err := activities.findOrInitClient(context.Background(), "/workspace", "golang", container)
				require.NoError(t, err)
				require.Same(t, client, got)
				release()
				require.Zero(t, client.closed.Load())
			}
			require.Equal(t, 1, created)
		})
	}
}
