package env

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"sidekick/common"

	"github.com/rs/zerolog/log"
)

const remoteWalkerPrefix = "/tmp/side-walker-"

// WalkEntry represents a single entry discovered during a directory walk.
type WalkEntry struct {
	// Path is the full path on the target environment.
	Path string
	// IsDir indicates whether the entry is a directory.
	IsDir bool
}

// WalkCodeDirectoryViaEnv walks the environment's working directory using the
// sidekick ignore file set. It delegates to the Env.Walk method, which handles
// both local and remote environments transparently.
func WalkCodeDirectoryViaEnv(
	ctx context.Context,
	ec EnvContainer,
	handleEntry func(path string, isDir bool) error,
) error {
	return ec.Env.Walk(ctx, common.SidekickIgnoreFileNames, handleEntry)
}

func walkCodeDirectorySSH(
	ctx context.Context,
	sshEnv SSHCapableEnv,
	baseDirectory string,
	ignoreFileNames []string,
	handleEntry func(path string, isDir bool) error,
) error {
	// Detect remote OS/arch for cross-compilation
	envInfo, err := getRemoteEnvInfo(ctx, sshEnv)
	if err != nil {
		return fmt.Errorf("detect remote environment: %w", err)
	}

	targetOS := common.NormalizeOS(envInfo.OS)
	targetArch := common.NormalizeArch(envInfo.Arch)

	localBinaryPath, err := common.GetWalkerBinaryPath(targetOS, targetArch)
	if err != nil {
		return fmt.Errorf("get walker binary: %w", err)
	}

	sshArgs, err := sshEnv.SSHArgs(ctx)
	if err != nil {
		return fmt.Errorf("get SSH args: %w", err)
	}

	remotePath := remoteWalkerPrefix + filepath.Base(localBinaryPath)
	if err := ensureRemoteBinary(ctx, sshArgs, localBinaryPath, remotePath); err != nil {
		return fmt.Errorf("upload walker binary: %w", err)
	}

	return streamRemoteWalk(ctx, sshArgs, remotePath, baseDirectory, ignoreFileNames, handleEntry)
}

func getRemoteEnvInfo(ctx context.Context, sshEnv SSHCapableEnv) (GetEnvironmentInfoOutput, error) {
	out, err := sshEnv.RunCommand(ctx, EnvRunCommandInput{
		Command: "uname",
		Args:    []string{"-sm"},
	})
	if err != nil {
		return GetEnvironmentInfoOutput{}, fmt.Errorf("uname failed: %w", err)
	}
	info := strings.TrimSpace(out.Stdout)
	parts := strings.Fields(info)
	if len(parts) < 2 {
		return GetEnvironmentInfoOutput{}, fmt.Errorf("unexpected uname output: %s", info)
	}
	return GetEnvironmentInfoOutput{OS: parts[0], Arch: parts[1]}, nil
}

// ensureRemoteBinary checks whether the binary exists on the remote host and
// uploads it via stdin pipe if it does not.
func ensureRemoteBinary(ctx context.Context, sshArgs []string, localPath, remotePath string) error {
	// Check if the binary already exists and is executable
	checkCmd := exec.CommandContext(ctx, "ssh", append(cloneArgs(sshArgs), "test -x "+shellQuote(remotePath))...)
	if err := checkCmd.Run(); err == nil {
		log.Debug().Str("remotePath", remotePath).Msg("walker binary already present")
		return nil
	}

	log.Info().Str("remotePath", remotePath).Msg("uploading walker binary")

	localFile, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open local binary: %w", err)
	}
	defer localFile.Close()

	uploadScript := fmt.Sprintf("cat > %s && chmod +x %s",
		shellQuote(remotePath),
		shellQuote(remotePath),
	)
	cmd := exec.CommandContext(ctx, "ssh", append(cloneArgs(sshArgs), uploadScript)...)
	cmd.Stdin = localFile

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("upload failed: %w: %s", err, string(out))
	}
	return nil
}

// streamRemoteWalk executes the walker binary on the remote host and processes
// the output stream in realtime. The walker protocol emits one line per entry
// in the format "f:<relative-path>" or "d:<relative-path>".
func streamRemoteWalk(
	ctx context.Context,
	sshArgs []string,
	walkerPath string,
	baseDirectory string,
	ignoreFileNames []string,
	handleEntry func(path string, isDir bool) error,
) error {
	remoteCmd := shellQuote(walkerPath) + " " + shellQuote(baseDirectory)
	for _, name := range ignoreFileNames {
		remoteCmd += " " + shellQuote(name)
	}
	// -tt forces PTY allocation so output is not buffered
	runArgs := append([]string{"-tt"}, append(cloneArgs(sshArgs), remoteCmd)...)

	cmd := exec.CommandContext(ctx, "ssh", runArgs...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start remote walker: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		ok, isDir, relPath := parseWalkerLine(line)
		if !ok {
			continue
		}

		fullPath := filepath.Join(baseDirectory, relPath)
		if err := handleEntry(fullPath, isDir); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		_ = cmd.Wait()
		return fmt.Errorf("read remote walker output: %w", err)
	}

	return cmd.Wait()
}

// parseWalkerLine parses a single line of the walker binary output protocol.
// Returns (ok, isDir, relativePath).
func parseWalkerLine(line string) (bool, bool, string) {
	if len(line) < 2 || line[1] != ':' {
		return false, false, ""
	}
	prefix := line[0]
	if prefix != 'f' && prefix != 'd' {
		return false, false, ""
	}
	return true, prefix == 'd', line[2:]
}

func cloneArgs(args []string) []string {
	c := make([]string, len(args))
	copy(c, args)
	return c
}
