package credential_proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"sidekick/common"
)

const maxCredentialBytes = 16 * 1024

// Resolve must only be called with a source authorized by trusted local
// configuration; repository declarations alone do not grant access to host
// credentials.
func Resolve(ctx context.Context, s common.CredentialSource) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if (s.Env == "") == (s.Command == "") {
		return "", errors.New("credential requires exactly one source")
	}

	var value string
	if s.Env != "" {
		value = os.Getenv(s.Env)
	} else {
		commandCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		cmd := exec.CommandContext(commandCtx, "sh", "-c", s.Command)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		cmd.WaitDelay = 100 * time.Millisecond
		output := &credentialOutput{}
		cmd.Stdout = output
		cmd.Stderr = io.Discard
		if err := cmd.Run(); err != nil {
			if commandCtx.Err() != nil {
				return "", commandCtx.Err()
			}
			return "", errors.New("credential command failed")
		}
		if output.exceeded {
			return "", errors.New("credential exceeds size limit")
		}
		value = strings.TrimSuffix(output.String(), "\n")
		value = strings.TrimSuffix(value, "\r")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if value == "" {
		return "", errors.New("credential source returned an empty value")
	}
	if len(value) > maxCredentialBytes {
		return "", errors.New("credential exceeds size limit")
	}
	for _, b := range []byte(value) {
		if b == 127 || b < 32 && b != '\t' {
			return "", errors.New("credential contains invalid header characters")
		}
	}
	return value, nil
}

type credentialOutput struct {
	buffer   bytes.Buffer
	exceeded bool
}

func (b *credentialOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := maxCredentialBytes - b.Len()
	if len(p) > remaining {
		b.exceeded = true
		p = p[:remaining]
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

func (b *credentialOutput) Len() int {
	return b.buffer.Len()
}

func (b *credentialOutput) String() string {
	return b.buffer.String()
}
