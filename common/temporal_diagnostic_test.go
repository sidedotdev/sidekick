package common

import "testing"

func TestGetTemporalDiagnosticHostPort(t *testing.T) {
	tests := []struct {
		name         string
		host         string
		port         string
		serverPort   string
		readOnlyPort string
		forwards     string
		want         string
	}{
		{name: "local default", want: "127.0.0.1:18855"},
		{name: "forwarded read-only proxy", forwards: "21855:31855", want: "127.0.0.1:31855"},
		{name: "forwarded full endpoint", forwards: "18855:28855", want: "127.0.0.1:28855"},
		{name: "prefer read-only proxy", forwards: "18855:28855,21855:31855", want: "127.0.0.1:31855"},
		{name: "unrelated forward", forwards: "8080:18080", want: "127.0.0.1:18855"},
		{name: "explicit remote host", host: "temporal.example", forwards: "21855:31855", want: "temporal.example:18855"},
		{name: "explicit local host", host: "127.0.0.1", forwards: "21855:31855", want: "127.0.0.1:18855"},
		{name: "explicit container-local port", port: "31855", forwards: "21855:31855", want: "127.0.0.1:31855"},
		{name: "explicit host and port", host: "temporal.example", port: "7233", forwards: "21855:31855", want: "temporal.example:7233"},
		{name: "custom server base port", serverPort: "9000", forwards: "22000:32000", want: "127.0.0.1:32000"},
		{name: "custom read-only port", readOnlyPort: "23000", forwards: "23000:33000", want: "127.0.0.1:33000"},
		{name: "malformed mapping", forwards: "garbage,21855:x,18855:28855", want: "127.0.0.1:28855"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SIDE_SERVER_PORT", tt.serverPort)
			t.Setenv("SIDE_TEMPORAL_SERVER_HOST", tt.host)
			t.Setenv("SIDE_TEMPORAL_SERVER_PORT", tt.port)
			t.Setenv("SIDE_TEMPORAL_READONLY_SERVER_PORT", tt.readOnlyPort)
			t.Setenv(PortForwardsEnvVar, "")
			serverAddress := GetTemporalServerHostPort()
			readOnlyAddress := GetTemporalReadOnlyServerHostPort()
			t.Setenv(PortForwardsEnvVar, tt.forwards)

			if got := GetTemporalDiagnosticHostPort(); got != tt.want {
				t.Fatalf("GetTemporalDiagnosticHostPort() = %q, want %q", got, tt.want)
			}
			if got := GetTemporalServerHostPort(); got != serverAddress {
				t.Fatalf("forwarding changed server address from %q to %q", serverAddress, got)
			}
			if got := GetTemporalReadOnlyServerHostPort(); got != readOnlyAddress {
				t.Fatalf("forwarding changed read-only bind address from %q to %q", readOnlyAddress, got)
			}
		})
	}
}

func TestGetTemporalClientHostPort(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		port     string
		forwards string
		want     string
	}{
		{name: "local default", want: "127.0.0.1:18855"},
		{name: "forwarded full endpoint", forwards: "18855:28855", want: "127.0.0.1:28855"},
		{name: "read-only endpoint is not sufficient", forwards: "21855:31855", want: "127.0.0.1:18855"},
		{name: "both endpoints", forwards: "18855:28855,21855:31855", want: "127.0.0.1:28855"},
		{name: "explicit host", host: "temporal.example", forwards: "18855:28855", want: "temporal.example:18855"},
		{name: "explicit port", port: "7233", forwards: "18855:28855", want: "127.0.0.1:7233"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SIDE_SERVER_PORT", "")
			t.Setenv("SIDE_TEMPORAL_SERVER_HOST", tt.host)
			t.Setenv("SIDE_TEMPORAL_SERVER_PORT", tt.port)
			t.Setenv(PortForwardsEnvVar, tt.forwards)
			if got := GetTemporalClientHostPort(); got != tt.want {
				t.Fatalf("GetTemporalClientHostPort() = %q, want %q", got, tt.want)
			}
		})
	}
}
