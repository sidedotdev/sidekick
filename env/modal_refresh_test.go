package env

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	modal "github.com/modal-labs/modal-client/go"
	pb "github.com/modal-labs/modal-client/go/proto/modal_proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type refreshTestSandbox struct {
	reload func(context.Context, *modal.SandboxReloadVolumesParams) error
}

func (s refreshTestSandbox) ReloadVolumes(ctx context.Context, params *modal.SandboxReloadVolumesParams) error {
	return s.reload(ctx, params)
}

func installRefreshLookup(t *testing.T, lookup func(context.Context, string) (modalVolumeReloader, error)) {
	t.Helper()
	original := modalRefreshLookup
	modalRefreshLookup = lookup
	t.Cleanup(func() { modalRefreshLookup = original })
}

func TestModalRefreshVolumesValidation(t *testing.T) {
	installRefreshLookup(t, func(context.Context, string) (modalVolumeReloader, error) {
		t.Fatal("invalid input reached the SDK")
		return nil, nil
	})
	tests := []struct {
		name    string
		env     Env
		errType string
	}{
		{"missing", nil, "UnsupportedModalRefresh"},
		{"local", &LocalEnv{}, "UnsupportedModalRefresh"},
		{"worktree", &LocalGitWorktreeEnv{}, "UnsupportedModalRefresh"},
		{"devpod", &DevPodEnv{}, "UnsupportedModalRefresh"},
		{"openshell", &OpenShellEnv{}, "UnsupportedModalRefresh"},
		{"nil modal", (*ModalEnv)(nil), "UnsupportedModalRefresh"},
		{"empty sandbox", &ModalEnv{}, "InvalidModalRefresh"},
		{"blank sandbox", &ModalEnv{SandboxName: " \t"}, "InvalidModalRefresh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ModalRefreshVolumesActivity(context.Background(), ModalRefreshVolumesInput{
				EnvContainer: EnvContainer{Env: tt.env},
			})
			assertNonRetryable(t, err, tt.errType)
			assert.ErrorContains(t, err, "modal.refresh_volumes")
		})
	}
}

func TestModalRefreshVolumesHostReload(t *testing.T) {
	environment := &ModalEnv{
		SandboxName: "invocation-sandbox", WorkingDirectory: "/repo",
		SSHHost: "unchanged.host", SSHPort: 2222, LocalRepoDir: "/local",
		runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
			t.Fatal("refresh must not execute a sandbox command")
			return EnvRunCommandOutput{}, "", nil
		},
		runModalAPICommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
			t.Fatal("refresh must not execute an API command")
			return EnvRunCommandOutput{}, nil
		},
	}
	before, err := json.Marshal(environment)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var calls []string
	installRefreshLookup(t, func(lookupCtx context.Context, name string) (modalVolumeReloader, error) {
		assert.Equal(t, environment.SandboxName, name)
		assert.Equal(t, ctx, lookupCtx)
		calls = append(calls, "lookup")
		return refreshTestSandbox{reload: func(reloadCtx context.Context, params *modal.SandboxReloadVolumesParams) error {
			assert.Equal(t, ctx, reloadCtx)
			require.NotNil(t, params)
			assert.Positive(t, params.Timeout)
			assert.LessOrEqual(t, params.Timeout, time.Minute)
			calls = append(calls, "reload")
			return nil
		}}, nil
	})
	output, err := ModalRefreshVolumesActivity(ctx, ModalRefreshVolumesInput{
		EnvContainer: EnvContainer{Env: environment},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"lookup", "reload"}, calls)
	after, err := json.Marshal(environment)
	require.NoError(t, err)
	assert.JSONEq(t, string(before), string(after))
	encodedOutput, err := json.Marshal(output)
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(encodedOutput))
}

func TestModalRefreshVolumesFailures(t *testing.T) {
	failure := errors.New("control plane unavailable")
	for _, stage := range []string{"lookup", "missing", "reload"} {
		t.Run(stage, func(t *testing.T) {
			installRefreshLookup(t, func(context.Context, string) (modalVolumeReloader, error) {
				if stage == "lookup" {
					return nil, failure
				}
				if stage == "missing" {
					return nil, nil
				}
				return refreshTestSandbox{reload: func(context.Context, *modal.SandboxReloadVolumesParams) error {
					return failure
				}}, nil
			})
			_, err := ModalRefreshVolumesActivity(context.Background(), ModalRefreshVolumesInput{
				EnvContainer: EnvContainer{Env: &ModalEnv{SandboxName: "current-sandbox"}},
			})
			require.Error(t, err)
			assert.ErrorContains(t, err, "modal.refresh_volumes")
			assert.ErrorContains(t, err, "current-sandbox")
			if stage != "missing" {
				assert.ErrorIs(t, err, failure)
			}
		})
	}
}

func TestModalRefreshVolumesContext(t *testing.T) {
	for _, stage := range []string{"before lookup", "lookup", "reload"} {
		for _, timeout := range []bool{false, true} {
			name := stage + "/cancel"
			if timeout {
				name = stage + "/deadline"
			}
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				expected := context.Canceled
				if timeout {
					cancel()
					duration := 20 * time.Millisecond
					if stage == "before lookup" {
						duration = -time.Second
					}
					ctx, cancel = context.WithTimeout(context.Background(), duration)
					expected = context.DeadlineExceeded
				}
				defer cancel()
				if stage == "before lookup" && !timeout {
					cancel()
				}
				block := func(callCtx context.Context) error {
					if !timeout {
						cancel()
					}
					select {
					case <-callCtx.Done():
						return callCtx.Err()
					case <-time.After(3 * time.Second):
						return errors.New("SDK context was not canceled")
					}
				}
				installRefreshLookup(t, func(callCtx context.Context, _ string) (modalVolumeReloader, error) {
					if stage == "before lookup" {
						t.Fatal("canceled action reached lookup")
					}
					if stage == "lookup" {
						return nil, block(callCtx)
					}
					return refreshTestSandbox{reload: func(callCtx context.Context, _ *modal.SandboxReloadVolumesParams) error {
						return block(callCtx)
					}}, nil
				})
				_, err := ModalRefreshVolumesActivity(ctx, ModalRefreshVolumesInput{
					EnvContainer: EnvContainer{Env: &ModalEnv{SandboxName: "current-sandbox"}},
				})
				assert.ErrorIs(t, err, expected)
			})
		}
	}
}

func TestModalRefreshVolumesTemporalCancellation(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	testEnv := suite.NewTestWorkflowEnvironment()
	testEnv.SetTestTimeout(10 * time.Second)
	testEnv.SetWorkerOptions(worker.Options{
		DefaultHeartbeatThrottleInterval: time.Millisecond,
		MaxHeartbeatThrottleInterval:     time.Millisecond,
	})
	var entered atomic.Bool
	observed := make(chan error, 1)
	installRefreshLookup(t, func(context.Context, string) (modalVolumeReloader, error) {
		return refreshTestSandbox{reload: func(ctx context.Context, _ *modal.SandboxReloadVolumesParams) error {
			entered.Store(true)
			select {
			case <-ctx.Done():
				observed <- ctx.Err()
				return ctx.Err()
			case <-time.After(5 * time.Second):
				err := errors.New("Temporal cancellation did not reach the SDK")
				observed <- err
				return err
			}
		}}, nil
	})
	testEnv.RegisterActivity(ModalRefreshVolumesActivity)
	testEnv.SetOnActivityHeartbeatListener(func(_ *activity.Info, _ converter.EncodedValues) {
		if entered.Load() {
			testEnv.CancelWorkflow()
		}
	})
	testEnv.ExecuteWorkflow(func(ctx workflow.Context) error {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: 8 * time.Second,
			HeartbeatTimeout:    3 * time.Second,
			WaitForCancellation: true,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
		})
		return workflow.ExecuteActivity(ctx, ModalRefreshVolumesActivity, ModalRefreshVolumesInput{
			EnvContainer: EnvContainer{Env: &ModalEnv{SandboxName: "current-sandbox"}},
		}).Get(ctx, nil)
	})
	require.Error(t, testEnv.GetWorkflowError())
	select {
	case err := <-observed:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(6 * time.Second):
		t.Fatal("reload did not finish after workflow cancellation")
	}
}

type refreshControlPlane struct {
	grpc.ClientConnInterface
	t        *testing.T
	state    string
	calls    []string
	rejected atomic.Int32
}

func (c *refreshControlPlane) Invoke(_ context.Context, method string, args, reply any, _ ...grpc.CallOption) error {
	c.calls = append(c.calls, method)
	switch method {
	case "/modal.client.ModalClient/SandboxGetFromName":
		request := args.(*pb.SandboxGetFromNameRequest)
		assert.Equal(c.t, modalAppName, request.GetAppName())
		assert.Equal(c.t, "invocation-sandbox", request.GetSandboxName())
		if c.state == "missing" {
			return status.Error(codes.NotFound, "missing sandbox")
		}
		proto.Merge(reply.(proto.Message), pb.SandboxGetFromNameResponse_builder{
			SandboxId: "sb-current",
		}.Build())
	case "/modal.client.ModalClient/SandboxWait":
		assert.Equal(c.t, "sb-current", args.(*pb.SandboxWaitRequest).GetSandboxId())
		response := pb.SandboxWaitResponse_builder{}
		if c.state == "finished" {
			response.Result = pb.GenericResult_builder{
				Status: pb.GenericResult_GENERIC_STATUS_SUCCESS,
			}.Build()
		}
		proto.Merge(reply.(proto.Message), response.Build())
	default:
		c.rejected.Add(1)
		c.t.Errorf("refresh attempted a forbidden RPC (including provisioning/recreation): %s", method)
		return status.Error(codes.Unimplemented, "forbidden refresh RPC")
	}
	return nil
}

func TestModalRefreshVolumesProductionLookupAndCredentialIsolation(t *testing.T) {
	const tokenID = "refresh-host-token-id-sentinel"
	const tokenSecret = "refresh-host-token-secret-sentinel"
	t.Setenv("MODAL_TOKEN_ID", tokenID)
	t.Setenv("MODAL_TOKEN_SECRET", tokenSecret)
	t.Setenv("MODAL_CONFIG_PATH", t.TempDir()+"/modal.toml")

	for _, state := range []string{"missing", "finished", "running"} {
		t.Run(state, func(t *testing.T) {
			transport := &refreshControlPlane{t: t, state: state}
			client, err := modal.NewClientWithOptions(&modal.ClientParams{
				ControlPlaneClient: pb.NewModalClientClient(transport),
			})
			require.NoError(t, err)
			modalClientMu.Lock()
			originalClient := modalClient
			modalClient = client
			modalClientMu.Unlock()
			t.Cleanup(func() {
				modalClientMu.Lock()
				modalClient = originalClient
				modalClientMu.Unlock()
				client.Close()
			})
			installRefreshLookup(t, lookupModalRefreshSandbox)
			originalReload := modalRefreshReload
			t.Cleanup(func() { modalRefreshReload = originalReload })
			var reloads atomic.Int32
			modalRefreshReload = func(ctx context.Context, sandbox modalVolumeReloader, params *modal.SandboxReloadVolumesParams) error {
				reloads.Add(1)
				assert.Equal(t, "running", state)
				actual, ok := sandbox.(*modal.Sandbox)
				if !assert.True(t, ok) {
					return errors.New("lookup did not return an SDK sandbox")
				}
				assert.Equal(t, "sb-current", actual.SandboxID)
				assert.NoError(t, ctx.Err())
				assert.NotNil(t, params)
				return nil
			}

			assertNoCredentials := func(value any) {
				t.Helper()
				payloads, err := converter.GetDefaultDataConverter().ToPayloads(value)
				require.NoError(t, err)
				encoded, err := proto.Marshal(payloads)
				require.NoError(t, err)
				assert.NotContains(t, string(encoded), tokenID)
				assert.NotContains(t, string(encoded), tokenSecret)
			}
			input := ModalRefreshVolumesInput{
				EnvContainer: EnvContainer{Env: &ModalEnv{
					SandboxName: "invocation-sandbox", WorkingDirectory: "/repo",
				}},
			}
			assertNoCredentials(input)
			var suite testsuite.WorkflowTestSuite
			testEnv := suite.NewTestActivityEnvironment()
			testEnv.RegisterActivity(ModalRefreshVolumesActivity)
			var heartbeats atomic.Int32
			testEnv.SetOnActivityHeartbeatListener(func(_ *activity.Info, details converter.EncodedValues) {
				heartbeats.Add(1)
				assert.False(t, details.HasValues(), "refresh heartbeats must contain no payload")
				assert.NotContains(t, fmt.Sprint(details), tokenID)
				assert.NotContains(t, fmt.Sprint(details), tokenSecret)
			})
			result, err := testEnv.ExecuteActivity(ModalRefreshVolumesActivity, input)
			if state == "running" {
				require.NoError(t, err)
				var output ModalRefreshVolumesOutput
				require.NoError(t, result.Get(&output))
				assertNoCredentials(output)
				assert.Equal(t, int32(1), reloads.Load())
			} else {
				require.ErrorContains(t, err, "is not running")
				assert.Zero(t, reloads.Load())
				assert.NotContains(t, err.Error(), tokenID)
				assert.NotContains(t, err.Error(), tokenSecret)
			}
			assert.Positive(t, heartbeats.Load())
			assert.Zero(t, transport.rejected.Load(), "refresh must not recreate sandboxes or provision volumes")
			expected := []string{"/modal.client.ModalClient/SandboxGetFromName"}
			if state != "missing" {
				expected = append(expected, "/modal.client.ModalClient/SandboxWait")
			}
			assert.Equal(t, expected, transport.calls)
		})
	}
}
