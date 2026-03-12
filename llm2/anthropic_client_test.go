package llm2

import (
	"reflect"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
)

func TestNewAnthropicClientWithoutEnvDefaults(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "env-api-key")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "env-auth-token")
	t.Setenv("ANTHROPIC_BASE_URL", "https://env.example.com")

	opts := []option.RequestOption{
		option.WithAPIKey("explicit-api-key"),
		option.WithBaseURL("https://explicit.example.com"),
	}

	client := newAnthropicClient(opts...)

	if got, want := len(client.Options), len(opts)+1; got != want {
		t.Fatalf("len(client.Options) = %d, want %d", got, want)
	}
	if reflect.ValueOf(client.Completions).IsZero() {
		t.Fatal("Completions service was not initialized")
	}
	if reflect.ValueOf(client.Messages).IsZero() {
		t.Fatal("Messages service was not initialized")
	}
	if reflect.ValueOf(client.Models).IsZero() {
		t.Fatal("Models service was not initialized")
	}
	if reflect.ValueOf(client.Beta).IsZero() {
		t.Fatal("Beta service was not initialized")
	}
}
