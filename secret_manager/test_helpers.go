package secret_manager

import (
	"fmt"
	"sync"
)

// SecretInterceptFunc selectively intercepts secret lookups in tests.
// When handled is true, the wrapper returns exactly (value, err).
type SecretInterceptFunc func(secretName string) (value string, err error, handled bool)

var (
	secretInterceptorMu       sync.RWMutex
	secretInterceptorRegistry = map[string]SecretInterceptFunc{}
)

// RegisterSecretInterceptor registers an interceptor by name for use with
// InterceptingSecretManager in tests.
//
// This is intentionally global so behavior survives JSON marshal/unmarshal of the
// manager, which only persists the interceptor name.
func RegisterSecretInterceptor(name string, fn SecretInterceptFunc) {
	secretInterceptorMu.Lock()
	defer secretInterceptorMu.Unlock()
	secretInterceptorRegistry[name] = fn
}

func getSecretInterceptor(name string) (SecretInterceptFunc, bool) {
	secretInterceptorMu.RLock()
	defer secretInterceptorMu.RUnlock()
	fn, ok := secretInterceptorRegistry[name]
	return fn, ok
}

// InterceptingSecretManager is a test-focused SecretManager wrapper that can
// override GetSecret results while delegating all other lookups to an underlying
// manager.
//
// It is JSON-serializable via SecretManagerContainer; the interceptor behavior
// is restored by looking up InterceptorName in a registry.
type InterceptingSecretManager struct {
	Underlying      SecretManagerContainer `json:"underlying"`
	InterceptorName string                 `json:"interceptorName"`
}

func (i InterceptingSecretManager) GetType() SecretManagerType {
	return InterceptingSecretManagerType
}

func (i InterceptingSecretManager) GetSecret(secretName string) (string, error) {
	if i.InterceptorName == "" {
		return "", fmt.Errorf("interceptor name is empty")
	}
	fn, ok := getSecretInterceptor(i.InterceptorName)
	if !ok {
		return "", fmt.Errorf("no secret interceptor registered for %q", i.InterceptorName)
	}

	if value, err, handled := fn(secretName); handled {
		return value, err
	}

	if i.Underlying.SecretManager == nil {
		return "", fmt.Errorf("underlying secret manager is nil")
	}
	return i.Underlying.GetSecret(secretName)
}
