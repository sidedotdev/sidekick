package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"sidekick/common"
)

func TestServiceOwnsControlAndInferenceListeners(t *testing.T) {
	t.Parallel()

	socket := filepath.Join(socketTempDir(t), "private", "control.sock")
	load := func() (map[string]common.CredentialProxyConfig, error) {
		return map[string]common.CredentialProxyConfig{
			"internal": {
				Upstream: "https://example.invalid",
				Auth: common.CredentialProxyAuthConfig{
					Bearer: &common.CredentialSource{Env: "UNUSED_TEST_SECRET"},
				},
			},
		}, nil
	}
	service, err := StartService(0, socket, load, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()

	for _, path := range []string{filepath.Dir(socket), socket} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatal("control channel is accessible to other users")
		}
	}
	other, err := StartService(0, socket, load, func() bool { return false })
	if err == nil {
		other.Close()
		t.Fatal("second service took over the control socket")
	}
	definitions, err := load()
	if err != nil {
		t.Fatal(err)
	}
	activation, err := RequestCapability(context.Background(), socket, ActivationRequest{
		Definitions: definitions, Active: []string{"internal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, service.URL()+"/not-activated", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+activation.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("owning listener rejected provisioned token: %d", response.StatusCode)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(socket); !os.IsNotExist(err) {
		t.Fatal("control socket remains after shutdown")
	}
	if _, err := RequestCapability(context.Background(), socket, ActivationRequest{
		Definitions: definitions, Active: []string{"internal"},
	}); err == nil {
		t.Fatal("closed service still issues capabilities")
	}
}
