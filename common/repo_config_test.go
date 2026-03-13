package common

import (
	"encoding/json"
	"testing"
)

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
