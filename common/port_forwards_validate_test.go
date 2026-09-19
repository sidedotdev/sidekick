package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPortForwardConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		forward PortForwardConfig
		wantErr string
	}{
		{name: "host port only", forward: PortForwardConfig{HostPort: 8080}},
		{name: "explicit container port", forward: PortForwardConfig{HostPort: 8080, ContainerPort: 9090}},
		{name: "max ports", forward: PortForwardConfig{HostPort: 65535, ContainerPort: 65535}},
		{name: "zero host port", forward: PortForwardConfig{HostPort: 0}, wantErr: "host_port must be between 1 and 65535, got 0"},
		{name: "negative host port", forward: PortForwardConfig{HostPort: -1}, wantErr: "host_port must be between 1 and 65535, got -1"},
		{name: "host port too large", forward: PortForwardConfig{HostPort: 65536}, wantErr: "host_port must be between 1 and 65535, got 65536"},
		{name: "negative container port", forward: PortForwardConfig{HostPort: 8080, ContainerPort: -5}, wantErr: "container_port must be between 1 and 65535, got -5"},
		{name: "container port too large", forward: PortForwardConfig{HostPort: 8080, ContainerPort: 70000}, wantErr: "container_port must be between 1 and 65535, got 70000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.forward.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestValidatePortForwards(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		forwards []PortForwardConfig
		wantErrs []string
	}{
		{name: "nil list"},
		{name: "empty list", forwards: []PortForwardConfig{}},
		{
			name:     "distinct forwards",
			forwards: []PortForwardConfig{{HostPort: 8080}, {HostPort: 5432, ContainerPort: 15432}},
		},
		{
			name:     "same host port to distinct container ports",
			forwards: []PortForwardConfig{{HostPort: 8080}, {HostPort: 8080, ContainerPort: 8081}},
		},
		{
			name:     "duplicate entries",
			forwards: []PortForwardConfig{{HostPort: 8080}, {HostPort: 8080}},
			wantErrs: []string{"container_port 8080 is used by both host_port 8080 and host_port 8080"},
		},
		{
			name:     "explicit container port collides with defaulted one",
			forwards: []PortForwardConfig{{HostPort: 9090}, {HostPort: 8080, ContainerPort: 9090}},
			wantErrs: []string{"container_port 9090 is used by both host_port 9090 and host_port 8080"},
		},
		{
			name:     "reports every problem",
			forwards: []PortForwardConfig{{HostPort: 0}, {HostPort: 8080}, {HostPort: 8080}, {HostPort: 1, ContainerPort: 99999}},
			wantErrs: []string{
				"host_port must be between 1 and 65535, got 0",
				"container_port 8080 is used by both host_port 8080 and host_port 8080",
				"container_port must be between 1 and 65535, got 99999",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidatePortForwards(tt.forwards)
			if len(tt.wantErrs) == 0 {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			for _, wantErr := range tt.wantErrs {
				assert.Contains(t, err.Error(), wantErr)
			}
		})
	}
}
