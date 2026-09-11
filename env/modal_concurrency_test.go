package env

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModalCreateSandboxConcurrentRecovery(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		input := ModalCreateSandboxInput{Name: t.Name()}
		entered := make(chan struct{}, 10)
		release := make(chan struct{})
		results := make(chan error, 10)
		create := func(ctx context.Context, input ModalCreateSandboxInput) (ModalCreateSandboxOutput, error) {
			entered <- struct{}{}
			<-release
			return ModalCreateSandboxOutput{SandboxName: input.Name}, nil
		}
		unexpected := func(context.Context, string) error {
			t.Error("unexpected recovery operation")
			return nil
		}
		run := func(ctx context.Context, input ModalCreateSandboxInput) {
			_, err := modalCreateSandboxWithRecovery(ctx, input, create, unexpected, unexpected)
			results <- err
		}

		go run(ctx, input)
		synctest.Wait()
		require.Len(t, entered, 1)

		go run(ctx, input)
		synctest.Wait()
		assert.Len(t, entered, 1, "same-name creation must wait until readiness and recovery finish")

		other := ModalCreateSandboxInput{Name: input.Name + "-other"}
		go run(ctx, other)
		synctest.Wait()
		assert.Len(t, entered, 2, "unrelated sandboxes must not block each other")

		waitCtx, cancel := context.WithCancel(ctx)
		go run(waitCtx, input)
		synctest.Wait()
		cancel()
		synctest.Wait()
		assert.Len(t, entered, 2, "a canceled waiter must not enter creation")
		select {
		case err := <-results:
			assert.ErrorIs(t, err, context.Canceled)
		default:
			t.Error("canceled waiter did not return while the creator was blocked")
		}

		close(release)
		synctest.Wait()
		for len(results) > 0 {
			require.NoError(t, <-results)
		}

		_, err := modalCreateSandboxWithRecovery(ctx, input, create, unexpected, unexpected)
		require.NoError(t, err, "subsequent calls must be able to acquire the same sandbox")
	})
}

func TestModalConcurrentRecoveryReusesReplacement(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		input := ModalCreateSandboxInput{Name: t.Name()}
		replacement := modalReplacementSandboxName(input.Name)
		recycleStarted := make(chan struct{})
		allowRecycle := make(chan struct{})
		type result struct {
			output ModalCreateSandboxOutput
			err    error
		}
		results := make(chan result, 2)
		ready := false
		creates, recycles := 0, 0
		create := func(ctx context.Context, in ModalCreateSandboxInput) (ModalCreateSandboxOutput, error) {
			creates++
			if ready {
				return ModalCreateSandboxOutput{SandboxName: replacement, Reused: true}, nil
			}
			if in.Name == replacement {
				ready = true
				return ModalCreateSandboxOutput{SandboxName: replacement}, nil
			}
			return ModalCreateSandboxOutput{}, &modalSandboxUnhealthyError{
				sandboxName: input.Name,
				cause:       context.DeadlineExceeded,
			}
		}
		recycle := func(context.Context, string) error {
			recycles++
			if recycles == 1 {
				close(recycleStarted)
			}
			<-allowRecycle
			return nil
		}
		await := func(context.Context, string) error {
			t.Error("unexpected shutdown wait")
			return nil
		}
		run := func() {
			out, err := modalCreateSandboxWithRecovery(ctx, input, create, recycle, await)
			results <- result{out, err}
		}

		go run()
		<-recycleStarted
		go run()
		synctest.Wait()
		assert.Equal(t, 1, creates)
		assert.Equal(t, 1, recycles)

		close(allowRecycle)
		synctest.Wait()
		require.Len(t, results, 2)
		reused := 0
		for range 2 {
			got := <-results
			require.NoError(t, got.err)
			assert.Equal(t, replacement, got.output.SandboxName)
			if got.output.Reused {
				reused++
			}
		}
		assert.Equal(t, 1, reused)
		assert.Equal(t, 1, recycles)
		assert.Equal(t, 3, creates, "initial attempt, replacement, then reuse")
	})
}
