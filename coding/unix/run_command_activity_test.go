package unix

import (
	"context"
	"strings"
	"testing"
	"time"
)

func Test_RunCommandActivity(t *testing.T) {
	ctx := context.Background()

	t.Run("returns stdout and no error when command is successful", func(t *testing.T) {
		// Arrange
		input := RunCommandActivityInput{
			WorkingDir: ".",
			Command:    "echo",
			Args:       []string{"Hello, World!"},
		}

		// Act
		output, err := RunCommandActivity(ctx, input)

		// Assert
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		expectedOutput := "Hello, World!\n"
		if output.Stdout != expectedOutput {
			t.Errorf("expected %v, got %v", expectedOutput, output.Stdout)
		}
		if output.Stderr != "" {
			t.Errorf("expected empty stderr, got %v", output.Stderr)
		}
	})

	t.Run("returns stderr and exit status when command fails", func(t *testing.T) {
		// Arrange
		input := RunCommandActivityInput{
			WorkingDir: ".",
			Command:    "ls",
			Args:       []string{"/non/existent/directory"},
		}

		// Act
		output, err := RunCommandActivity(ctx, input)

		// Assert
		if err != nil {
			t.Errorf("expected error to be nil, got %v", err)
		}
		if output.Stdout != "" {
			t.Errorf("expected empty stdout, got %v", output.Stdout)
		}
		expectedError := "No such file or directory"
		if !strings.Contains(output.Stderr, expectedError) {
			t.Errorf("expected stderr to contain %v, got %v", expectedError, output.Stderr)
		}
		if output.ExitStatus == 0 {
			t.Errorf("expected non-zero exit status, got %v", output.ExitStatus)
		}
	})
}

func Test_RunCommandActivity_ContextCancellation(t *testing.T) {
	t.Parallel()

	t.Run("returns context error when context is cancelled during execution", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		input := RunCommandActivityInput{
			WorkingDir: ".",
			Command:    "sleep",
			Args:       []string{"10"},
		}

		_, err := RunCommandActivity(ctx, input)

		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		if ctx.Err() == nil {
			t.Fatal("expected context to be done")
		}
		if err != context.DeadlineExceeded {
			t.Errorf("expected context.DeadlineExceeded, got %v", err)
		}
	})

	t.Run("returns normal exit status when command fails without context cancellation", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()

		input := RunCommandActivityInput{
			WorkingDir: ".",
			Command:    "bash",
			Args:       []string{"-c", "exit 42"},
		}

		output, err := RunCommandActivity(ctx, input)

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if output.ExitStatus != 42 {
			t.Errorf("expected exit status 42, got %d", output.ExitStatus)
		}
	})
}