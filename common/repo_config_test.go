package common

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestModalVolumeMount_Validate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		mount ModalVolumeMount
		error string
	}{
		{
			name:  "valid mount",
			mount: ModalVolumeMount{Name: "cache", MountPath: "/root/.cache/example"},
		},
		{
			name:  "valid read-only mount",
			mount: ModalVolumeMount{Name: "cache", MountPath: "/opt/data", ReadOnly: true},
		},
		{
			name:  "blank name",
			mount: ModalVolumeMount{Name: "  ", MountPath: "/root/.cache/example"},
			error: "requires a name",
		},
		{
			name:  "relative mount path",
			mount: ModalVolumeMount{Name: "cache", MountPath: "root/.cache/example"},
			error: "requires an absolute mount_path",
		},
		{
			name:  "empty mount path",
			mount: ModalVolumeMount{Name: "cache"},
			error: "requires an absolute mount_path",
		},
		{
			name:  "filesystem root",
			mount: ModalVolumeMount{Name: "cache", MountPath: "/"},
			error: "filesystem root",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.mount.Validate()
			if test.error == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", test.error)
			}
			if !strings.Contains(err.Error(), test.error) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), test.error)
			}
		})
	}
}

func TestDevRunCommandConfig_UnmarshalJSON_CamelCase(t *testing.T) {
	t.Parallel()
	input := `{"workingDir":"frontend","command":"npm run dev","stopTimeoutSeconds":15}`
	var cfg DevRunCommandConfig
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.WorkingDir != "frontend" {
		t.Errorf("WorkingDir = %q, want %q", cfg.WorkingDir, "frontend")
	}
	if cfg.Command != "npm run dev" {
		t.Errorf("Command = %q, want %q", cfg.Command, "npm run dev")
	}
	if cfg.StopTimeoutSeconds != 15 {
		t.Errorf("StopTimeoutSeconds = %d, want %d", cfg.StopTimeoutSeconds, 15)
	}
}

func TestDevRunCommandConfig_UnmarshalJSON_LegacyPascalCase(t *testing.T) {
	t.Parallel()
	input := `{"WorkingDir":"backend","Command":"go run .","StopTimeoutSeconds":30}`
	var cfg DevRunCommandConfig
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.WorkingDir != "backend" {
		t.Errorf("WorkingDir = %q, want %q", cfg.WorkingDir, "backend")
	}
	if cfg.Command != "go run ." {
		t.Errorf("Command = %q, want %q", cfg.Command, "go run .")
	}
	if cfg.StopTimeoutSeconds != 30 {
		t.Errorf("StopTimeoutSeconds = %d, want %d", cfg.StopTimeoutSeconds, 30)
	}
}

func TestDevRunCommandConfig_UnmarshalJSON_CamelCaseTakesPrecedence(t *testing.T) {
	t.Parallel()
	input := `{"workingDir":"new","WorkingDir":"old","command":"new-cmd","Command":"old-cmd"}`
	var cfg DevRunCommandConfig
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.WorkingDir != "new" {
		t.Errorf("WorkingDir = %q, want %q (camelCase should take precedence)", cfg.WorkingDir, "new")
	}
	if cfg.Command != "new-cmd" {
		t.Errorf("Command = %q, want %q (camelCase should take precedence)", cfg.Command, "new-cmd")
	}
}

func TestDevRunCommandConfig_MarshalJSON_ProducesCamelCase(t *testing.T) {
	t.Parallel()
	cfg := DevRunCommandConfig{
		WorkingDir:         "frontend",
		Command:            "npm start",
		StopTimeoutSeconds: 20,
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unexpected error unmarshaling raw: %v", err)
	}
	if _, ok := raw["workingDir"]; !ok {
		t.Error("expected camelCase key 'workingDir' in marshaled output")
	}
	if _, ok := raw["command"]; !ok {
		t.Error("expected camelCase key 'command' in marshaled output")
	}
	if _, ok := raw["stopTimeoutSeconds"]; !ok {
		t.Error("expected camelCase key 'stopTimeoutSeconds' in marshaled output")
	}
	if _, ok := raw["WorkingDir"]; ok {
		t.Error("unexpected PascalCase key 'WorkingDir' in marshaled output")
	}
}

func TestDevRunCommandConfig_UnmarshalJSON_EmptyObject(t *testing.T) {
	t.Parallel()
	var cfg DevRunCommandConfig
	if err := json.Unmarshal([]byte(`{}`), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.WorkingDir != "" || cfg.Command != "" || cfg.StopTimeoutSeconds != 0 {
		t.Errorf("expected zero-value config, got %+v", cfg)
	}
}

func TestDevRunConfig_UnmarshalJSON_LegacyFormat(t *testing.T) {
	t.Parallel()
	input := `{"server":{"WorkingDir":".","Command":"make serve","StopTimeoutSeconds":5}}`
	var cfg DevRunConfig
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cmd, ok := cfg["server"]
	if !ok {
		t.Fatal("expected 'server' key in config")
	}
	if cmd.WorkingDir != "." {
		t.Errorf("WorkingDir = %q, want %q", cmd.WorkingDir, ".")
	}
	if cmd.Command != "make serve" {
		t.Errorf("Command = %q, want %q", cmd.Command, "make serve")
	}
	if cmd.StopTimeoutSeconds != 5 {
		t.Errorf("StopTimeoutSeconds = %d, want %d", cmd.StopTimeoutSeconds, 5)
	}
}

func TestModalEnvConfig_Validate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		config ModalEnvConfig
		error  string
	}{
		{
			name: "empty config",
		},
		{
			name: "valid full config",
			config: ModalEnvConfig{
				VM:          true,
				Image:       "ubuntu:24.04",
				CPU:         0.5,
				CPULimit:    4,
				Memory:      2048,
				MemoryLimit: 8192,
				IdleSeconds: 60,
				Volumes: []ModalVolumeMount{
					{Name: "a", MountPath: "/a"},
					{Name: "b", MountPath: "/b", ReadOnly: true},
				},
			},
		},
		{
			name:   "image and dockerfile conflict",
			config: ModalEnvConfig{Image: "ubuntu:24.04", DockerfilePath: "Dockerfile.modal"},
			error:  "cannot both be set",
		},
		{
			name:   "negative cpu",
			config: ModalEnvConfig{CPU: -1},
			error:  "cpu must not be negative",
		},
		{
			name:   "negative cpu limit",
			config: ModalEnvConfig{CPULimit: -1},
			error:  "cpu_limit must not be negative",
		},
		{
			name:   "cpu limit below default request",
			config: ModalEnvConfig{CPULimit: 0.1},
			error:  "cpu_limit",
		},
		{
			name:   "cpu limit below request",
			config: ModalEnvConfig{CPU: 2, CPULimit: 1},
			error:  "cpu_limit",
		},
		{
			name:   "negative memory",
			config: ModalEnvConfig{Memory: -1},
			error:  "memory must not be negative",
		},
		{
			name:   "negative memory limit",
			config: ModalEnvConfig{MemoryLimit: -1},
			error:  "memory_limit must not be negative",
		},
		{
			name:   "memory limit below default request",
			config: ModalEnvConfig{MemoryLimit: 512},
			error:  "memory_limit",
		},
		{
			name:   "memory limit below request",
			config: ModalEnvConfig{Memory: 4096, MemoryLimit: 2048},
			error:  "memory_limit",
		},
		{
			name:   "negative idle seconds",
			config: ModalEnvConfig{IdleSeconds: -1},
			error:  "idle_seconds",
		},
		{
			name:   "invalid volume mount",
			config: ModalEnvConfig{Volumes: []ModalVolumeMount{{MountPath: "/cache"}}},
			error:  "requires a name",
		},
		{
			name: "duplicate normalized mount paths",
			config: ModalEnvConfig{Volumes: []ModalVolumeMount{
				{Name: "a", MountPath: "/cache"},
				{Name: "b", MountPath: "/cache/"},
			}},
			error: "configured more than once",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.config.Validate()
			if test.error == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", test.error)
			}
			if !strings.Contains(err.Error(), test.error) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), test.error)
			}
		})
	}
}
