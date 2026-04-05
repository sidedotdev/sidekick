package env

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"sidekick/coding/unix"

	"go.temporal.io/sdk/temporal"
)

var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

type OpenShellCreateInput struct {
	// Source for --from: a Dockerfile path, community sandbox name, or image reference.
	// When empty, openshell uses its default base image.
	Source  string `json:"source,omitempty"`
	RepoDir string `json:"repoDir"`
	// Name assigns a specific sandbox name via --name.
	Name string `json:"name,omitempty"`
}

type OpenShellCreateOutput struct {
	SandboxName string `json:"sandboxName"`
}

// OpenShellSandboxName returns a sandbox name for a given repo
func OpenShellSandboxName(repoDir string) string {
	return "side/" + DevPodWorkspaceName(repoDir)
}

// OpenShellCreateActivity creates an OpenShell sandbox.
func OpenShellCreateActivity(ctx context.Context, input OpenShellCreateInput) (OpenShellCreateOutput, error) {
	args := []string{"sandbox", "create"}
	if input.Name != "" {
		args = append(args, "--name", input.Name)
	}
	if input.Source != "" {
		args = append(args, "--from", input.Source)
	}
	args = append(args, "--", "echo", "ready")

	workingDir := input.RepoDir
	if workingDir == "" {
		workingDir = "."
	}

	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: workingDir,
		Command:    "openshell",
		Args:       args,
	})
	if err != nil {
		return OpenShellCreateOutput{}, fmt.Errorf("openshell sandbox create failed: %w", err)
	}
	if output.ExitStatus != 0 {
		return OpenShellCreateOutput{}, fmt.Errorf("openshell sandbox create exited with status %d: %s", output.ExitStatus, output.Stderr)
	}

	name := parseCreatedSandboxName(output.Stderr + "\n" + output.Stdout)
	if name == "" {
		return OpenShellCreateOutput{}, fmt.Errorf("could not determine sandbox name from openshell output")
	}
	return OpenShellCreateOutput{SandboxName: name}, nil
}

func parseCreatedSandboxName(output string) string {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := ansiEscapeRe.ReplaceAllString(scanner.Text(), "")
		line = strings.TrimSpace(line)
		prefix := "Created sandbox:"
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

type OpenShellSyncRepoInput struct {
	SandboxName      string `json:"sandboxName"`
	LocalRepoDir     string `json:"localRepoDir"`
	ContainerRepoDir string `json:"containerRepoDir"`
}

type OpenShellSyncRepoOutput struct {
	ContainerRepoDir string `json:"containerRepoDir"`
}

// OpenShellSyncRepoActivity uploads a local git repository to an OpenShell
// sandbox using a git bundle transferred over ssh.
func OpenShellSyncRepoActivity(ctx context.Context, input OpenShellSyncRepoInput) (OpenShellSyncRepoOutput, error) {
	sshArgs, err := openShellSSHArgs(ctx, input.SandboxName)
	if err != nil {
		return OpenShellSyncRepoOutput{}, fmt.Errorf("failed to get SSH args: %w", err)
	}

	// Create a git bundle containing all refs
	bundlePath := filepath.Join(os.TempDir(), fmt.Sprintf("openshell-repo-%s.bundle", input.SandboxName))
	defer os.Remove(bundlePath)

	bundleOutput, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: input.LocalRepoDir,
		Command:    "git",
		Args:       []string{"bundle", "create", bundlePath, "--all"},
	})
	if err != nil {
		return OpenShellSyncRepoOutput{}, fmt.Errorf("failed to create git bundle: %w", err)
	}
	if bundleOutput.ExitStatus != 0 {
		return OpenShellSyncRepoOutput{}, fmt.Errorf("git bundle create failed (exit %d): %s", bundleOutput.ExitStatus, bundleOutput.Stderr)
	}

	// Upload the bundle using ssh with cat + stdin redirect
	// (avoids needing scp binary or separate SSH config for scp)
	remoteBundlePath := "/tmp/repo-" + input.SandboxName + ".bundle"
	uploadArgs := make([]string, len(sshArgs))
	copy(uploadArgs, sshArgs)
	uploadArgs = append(uploadArgs, fmt.Sprintf("cat > %s", shellQuote(remoteBundlePath)))

	uploadOutput, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: os.TempDir(),
		Command:    "sh",
		Args:       []string{"-c", fmt.Sprintf("cat %s | ssh %s", shellQuote(bundlePath), strings.Join(quoteArgs(uploadArgs), " "))},
	})
	if err != nil {
		return OpenShellSyncRepoOutput{}, fmt.Errorf("failed to upload bundle to sandbox: %w", err)
	}
	if uploadOutput.ExitStatus != 0 {
		return OpenShellSyncRepoOutput{}, fmt.Errorf("bundle upload failed (exit %d): %s", uploadOutput.ExitStatus, uploadOutput.Stderr)
	}

	// Clone or update the repo inside the sandbox.
	// If the repo already exists (sandbox reuse), fetch from the bundle and
	// reset to match; otherwise clone fresh.
	quotedRepo := shellQuote(input.ContainerRepoDir)
	quotedBundle := shellQuote(remoteBundlePath)
	cloneScript := fmt.Sprintf(
		"if [ -d %s/.git ]; then "+
			"cd %s && git fetch %s '+refs/*:refs/*' --prune && git reset --hard HEAD; "+
			"else "+
			"mkdir -p %s && git clone %s %s && cd %s && "+
			"git config user.name 'Sidekick' && git config user.email 'sidekick@localhost'; "+
			"fi && rm -f %s",
		quotedRepo,
		quotedRepo, quotedBundle,
		shellQuote(filepath.Dir(input.ContainerRepoDir)), quotedBundle, quotedRepo, quotedRepo,
		quotedBundle,
	)

	cloneArgs := make([]string, len(sshArgs))
	copy(cloneArgs, sshArgs)
	cloneArgs = append(cloneArgs, cloneScript)

	cloneOutput, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: os.TempDir(),
		Command:    "ssh",
		Args:       cloneArgs,
	})
	if err != nil {
		return OpenShellSyncRepoOutput{}, fmt.Errorf("failed to clone bundle in sandbox: %w", err)
	}
	if cloneOutput.ExitStatus != 0 {
		return OpenShellSyncRepoOutput{}, fmt.Errorf("git clone in sandbox failed (exit %d): %s", cloneOutput.ExitStatus, cloneOutput.Stderr)
	}

	return OpenShellSyncRepoOutput{ContainerRepoDir: input.ContainerRepoDir}, nil
}

// quoteArgs shell-quotes each argument for use in a sh -c command.
func quoteArgs(args []string) []string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return quoted
}

type CreateOpenShellWorktreeInput struct {
	EnvContainer EnvContainer `json:"envContainer"`
	RepoDir      string       `json:"repoDir"`
	BranchName   string       `json:"branchName"`
	StartBranch  string       `json:"startBranch,omitempty"`
	WorkspaceId  string       `json:"workspaceId"`
}

type CreateOpenShellWorktreeOutput struct {
	WorktreePath string `json:"worktreePath"`
}

// CreateOpenShellWorktreeActivity creates a git worktree inside a running
// OpenShell sandbox so that worktree .git references resolve within the
// sandbox filesystem.
func CreateOpenShellWorktreeActivity(ctx context.Context, input CreateOpenShellWorktreeInput) (CreateOpenShellWorktreeOutput, error) {
	repoName := filepath.Base(input.RepoDir)
	branchSuffix := strings.TrimPrefix(input.BranchName, "side/")
	dirName := repoName + "-" + branchSuffix
	worktreePath := filepath.Join("/tmp", "sidekick-worktrees", input.WorkspaceId, dirName)

	mkdirOutput, err := input.EnvContainer.Env.RunCommand(ctx, EnvRunCommandInput{
		Command: "mkdir",
		Args:    []string{"-p", worktreePath},
	})
	if err != nil {
		return CreateOpenShellWorktreeOutput{}, fmt.Errorf("failed to create worktree directory in sandbox: %w", err)
	}
	if mkdirOutput.ExitStatus != 0 {
		return CreateOpenShellWorktreeOutput{}, fmt.Errorf("mkdir failed in sandbox (exit %d): %s", mkdirOutput.ExitStatus, mkdirOutput.Stderr)
	}

	baseRef := "HEAD"
	if input.StartBranch != "" {
		baseRef = input.StartBranch
	}
	addOutput, err := input.EnvContainer.Env.RunCommand(ctx, EnvRunCommandInput{
		Command: "git",
		Args:    []string{"worktree", "add", "-b", input.BranchName, worktreePath, baseRef},
	})
	if err != nil {
		return CreateOpenShellWorktreeOutput{}, fmt.Errorf("failed to run git worktree add in sandbox: %w", err)
	}
	if addOutput.ExitStatus != 0 {
		err := fmt.Errorf("git worktree add failed in sandbox (exit %d): %s", addOutput.ExitStatus, addOutput.Stderr)
		if strings.Contains(addOutput.Stderr, "already exists") {
			return CreateOpenShellWorktreeOutput{}, temporal.NewNonRetryableApplicationError(
				err.Error(),
				ErrTypeBranchAlreadyExists,
				err,
			)
		}
		return CreateOpenShellWorktreeOutput{}, err
	}

	return CreateOpenShellWorktreeOutput{WorktreePath: worktreePath}, nil
}

type OpenShellCheckSandboxInput struct {
	SandboxName string `json:"sandboxName"`
}

type OpenShellCheckSandboxOutput struct {
	Alive bool `json:"alive"`
}

// OpenShellCheckSandboxActivity checks whether a named sandbox exists and is
// reachable by fetching its metadata.
func OpenShellCheckSandboxActivity(ctx context.Context, input OpenShellCheckSandboxInput) (OpenShellCheckSandboxOutput, error) {
	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: ".",
		Command:    "openshell",
		Args:       []string{"sandbox", "get", input.SandboxName},
	})
	if err != nil {
		return OpenShellCheckSandboxOutput{Alive: false}, nil
	}
	return OpenShellCheckSandboxOutput{Alive: output.ExitStatus == 0}, nil
}

// OpenShellDeleteActivity deletes an OpenShell sandbox.
func OpenShellDeleteActivity(ctx context.Context, sandboxName string) error {
	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: ".",
		Command:    "openshell",
		Args:       []string{"sandbox", "delete", sandboxName},
	})
	if err != nil {
		return fmt.Errorf("openshell sandbox delete failed: %w", err)
	}
	if output.ExitStatus != 0 {
		return fmt.Errorf("openshell sandbox delete exited with status %d: %s", output.ExitStatus, output.Stderr)
	}
	return nil
}

// OpenShellStopActivity deletes an OpenShell sandbox.
// OpenShell has no stop-without-delete; this is equivalent to delete.
func OpenShellStopActivity(ctx context.Context, sandboxName string) error {
	return OpenShellDeleteActivity(ctx, sandboxName)
}

// openShellSSHArgs runs `openshell sandbox ssh-config <name>`, parses the
// resulting SSH config block, and returns ssh CLI arguments with ControlMaster
// multiplexing enabled.
func openShellSSHArgs(ctx context.Context, sandboxName string) ([]string, error) {
	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: ".",
		Command:    "openshell",
		Args:       []string{"sandbox", "ssh-config", sandboxName},
	})
	if err != nil {
		return nil, fmt.Errorf("openshell sandbox ssh-config failed: %w", err)
	}
	if output.ExitStatus != 0 {
		return nil, fmt.Errorf("openshell sandbox ssh-config exited with status %d: %s", output.ExitStatus, output.Stderr)
	}

	return parseSSHConfigArgs(output.Stdout, sandboxName)
}

// parseSSHConfigArgs parses an OpenSSH config block and returns ssh CLI args
// that pass all options explicitly via -o flags, so no ~/.ssh/config entry is
// needed. The Host alias is used as the destination hostname.
func parseSSHConfigArgs(configOutput string, sandboxName string) ([]string, error) {
	var host string
	var user string
	var opts []string

	scanner := bufio.NewScanner(strings.NewReader(configOutput))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		value := strings.TrimSpace(parts[1])

		switch key {
		case "Host":
			host = value
		case "User":
			user = value
		default:
			opts = append(opts, "-o", key+"="+value)
		}
	}

	if host == "" {
		return nil, fmt.Errorf("no Host directive found in ssh-config output for sandbox %s", sandboxName)
	}

	args := []string{
		"-o", "ControlMaster=auto",
		"-S", "/tmp/ssh-%r@%h:%p",
		"-o", "ControlPersist=yes",
	}
	args = append(args, opts...)

	dest := host
	if user != "" {
		dest = user + "@" + host
	}
	args = append(args, dest)

	return args, nil
}
