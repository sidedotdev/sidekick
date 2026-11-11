package llm2

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"sidekick/llm"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func runKeyringTestInSubprocess(t *testing.T, testName string) bool {
	t.Helper()
	const childEnv = "SIDE_KEYRING_TEST_SUBPROCESS"
	if os.Getenv(childEnv) == testName {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+regexp.QuoteMeta(testName)+"$")
	cmd.Env = append(os.Environ(), childEnv+"="+testName)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "isolated test %s:\n%s", testName, output)
	return true
}

func TestAnthropicOAuthRefreshDoesNotReplaceParentKeyring(t *testing.T) {
	if runKeyringTestInSubprocess(t, "TestAnthropicOAuthRefreshDoesNotReplaceParentKeyring") {
		return
	}

	sentinel := errors.New("parent keyring provider")
	keyring.MockInitWithError(sentinel)

	TestGetAnthropicOAuthCredentials_RefreshPreservesProfile(t)

	_, err := keyring.Get("sidekick", llm.AnthropicOAuthSecretName)
	require.ErrorIs(t, err, sentinel, "refresh tests must not replace the parent keyring provider")
}
