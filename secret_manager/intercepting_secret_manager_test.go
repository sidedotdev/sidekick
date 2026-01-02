package secret_manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

type stubSecretManager struct {
	fn func(secretName string) (string, error)
}

func (s stubSecretManager) GetSecret(secretName string) (string, error) {
	return s.fn(secretName)
}

func (s stubSecretManager) GetType() SecretManagerType {
	return MockSecretManagerType
}

func TestInterceptingSecretManager_OverridePassThroughAndMarshalUnmarshal(t *testing.T) {
	t.Parallel()

	const interceptorName = "test_interceptor_override_passthrough"

	RegisterSecretInterceptor(interceptorName, func(secretName string) (string, error, bool) {
		if secretName == "override_me" {
			return "overridden", nil, true
		}
		return "", nil, false
	})

	ism := InterceptingSecretManager{
		Underlying: SecretManagerContainer{
			SecretManager: &MockSecretManager{},
		},
		InterceptorName: interceptorName,
	}

	t.Run("Override", func(t *testing.T) {
		v, err := ism.GetSecret("override_me")
		require.NoError(t, err)
		require.Equal(t, "overridden", v)
	})

	t.Run("PassThrough", func(t *testing.T) {
		v, err := ism.GetSecret("SOME_PROVIDER_API_KEY")
		require.NoError(t, err)
		require.Equal(t, "fake secret", v)
	})

	t.Run("MarshalUnmarshalViaContainer", func(t *testing.T) {
		original := SecretManagerContainer{SecretManager: &ism}
		b, err := json.Marshal(original)
		require.NoError(t, err)

		var roundTripped SecretManagerContainer
		err = json.Unmarshal(b, &roundTripped)
		require.NoError(t, err)

		v, err := roundTripped.GetSecret("override_me")
		require.NoError(t, err)
		require.Equal(t, "overridden", v)

		v, err = roundTripped.GetSecret("SOME_PROVIDER_API_KEY")
		require.NoError(t, err)
		require.Equal(t, "fake secret", v)
	})
}

func TestInterceptingSecretManager_MissingUnderlyingFailsFast(t *testing.T) {
	t.Parallel()

	const interceptorName = "test_interceptor_missing_underlying"

	RegisterSecretInterceptor(interceptorName, func(secretName string) (string, error, bool) {
		return "", nil, false
	})

	ism := InterceptingSecretManager{
		InterceptorName: interceptorName,
	}

	_, err := ism.GetSecret("anything")
	require.Error(t, err)
}

func TestInterceptingSecretManager_MissingInterceptorRegistrationFailsFast(t *testing.T) {
	t.Parallel()

	ism := InterceptingSecretManager{
		Underlying: SecretManagerContainer{
			SecretManager: stubSecretManager{
				fn: func(secretName string) (string, error) {
					return "", fmt.Errorf("should not be called")
				},
			},
		},
		InterceptorName: "does_not_exist",
	}

	_, err := ism.GetSecret("anything")
	require.Error(t, err)
}

func TestInterceptingSecretManager_OverrideCanReturnErrSecretNotFound(t *testing.T) {
	t.Parallel()

	const interceptorName = "test_interceptor_err_not_found"

	RegisterSecretInterceptor(interceptorName, func(secretName string) (string, error, bool) {
		if secretName == "missing" {
			return "", fmt.Errorf("%w: missing", ErrSecretNotFound), true
		}
		return "", nil, false
	})

	ism := InterceptingSecretManager{
		Underlying: SecretManagerContainer{
			SecretManager: stubSecretManager{
				fn: func(secretName string) (string, error) {
					return "ok", nil
				},
			},
		},
		InterceptorName: interceptorName,
	}

	_, err := ism.GetSecret("missing")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrSecretNotFound))
}
