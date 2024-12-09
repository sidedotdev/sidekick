package unix

import (
	"context"
	"strings"
	"testing"
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
