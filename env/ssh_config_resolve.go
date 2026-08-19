package env

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sidekick/coding/unix"
)

// sshConfigResolveTimeout bounds `ssh -G`, which is a local config expansion
// and never dials, so it should return promptly or not at all.
const sshConfigResolveTimeout = 15 * time.Second

// resolveSSHConnConfig asks OpenSSH itself to resolve host against the user's
// ssh_config and maps the result into a typed config.
//
// Parsing ~/.ssh/config directly is not an option: correctness requires Host
// pattern matching, Include, Match and first-wins precedence, and a parser
// that silently resolved a wrong value would send connections to the wrong
// place. `ssh -G` performs exactly that resolution and prints the effective
// settings as flat "key value" lines.
//
// Only the directives that describe reachability are mapped. Every other line
// is an OpenSSH default rather than an expression of intent, so carrying them
// as unmodeled options would make ValidateNative reject every host.
func resolveSSHConnConfig(ctx context.Context, host string) (SSHConnConfig, error) {
	resolveCtx, cancel := context.WithTimeout(ctx, sshConfigResolveTimeout)
	defer cancel()

	output, err := unix.RunCommandActivity(resolveCtx, unix.RunCommandActivityInput{
		WorkingDir: ".",
		Command:    "ssh",
		Args:       []string{"-G", host},
	})
	if err != nil {
		return SSHConnConfig{}, fmt.Errorf("resolve ssh config for %s: %w", host, err)
	}
	if output.ExitStatus != 0 {
		return SSHConnConfig{}, fmt.Errorf("resolve ssh config for %s: ssh -G exited with status %d: %s",
			host, output.ExitStatus, strings.TrimSpace(output.Stderr))
	}
	return parseResolvedSSHConfig(output.Stdout, host)
}

// sshRiskyDirective is an ssh -G directive that SSHConnConfig does not model
// and whose non-default value would change where a connection goes or what
// the remote end runs.
type sshRiskyDirective struct {
	// Name is the canonical OpenSSH spelling, for error messages.
	Name string
	// Default is the value OpenSSH reports when the directive is unset.
	Default string
}

// sshConfigRiskyDirectives are the directives whose omission would fail
// silently: the connection would still succeed, just not the one the user
// configured. A non-default value is carried as an unmodeled option so
// ValidateNative refuses the dial by name.
//
// Directives outside this table are either pure ssh-binary concerns or fail
// loudly when disregarded — an auth method, cipher or kex the peer requires
// ends the handshake with an error rather than a wrong connection.
var sshConfigRiskyDirectives = map[string]sshRiskyDirective{
	"proxyjump":          {Name: "ProxyJump", Default: "none"},
	"remotecommand":      {Name: "RemoteCommand", Default: "none"},
	"localforward":       {Name: "LocalForward"},
	"remoteforward":      {Name: "RemoteForward"},
	"dynamicforward":     {Name: "DynamicForward"},
	"setenv":             {Name: "SetEnv"},
	"sendenv":            {Name: "SendEnv"},
	"bindaddress":        {Name: "BindAddress"},
	"bindinterface":      {Name: "BindInterface"},
	"addressfamily":      {Name: "AddressFamily", Default: "any"},
	"permitlocalcommand": {Name: "PermitLocalCommand", Default: "no"},
	"localcommand":       {Name: "LocalCommand", Default: "none"},
}

// hostKeyPolicyFromStrictSetting maps OpenSSH's StrictHostKeyChecking to a
// policy. "ask" — the OpenSSH default — becomes verify because no one is
// present to answer the prompt, so an unknown key is refused either way.
// Unrecognized values are an error rather than a guess, since guessing here
// either breaks connections or weakens verification.
func hostKeyPolicyFromStrictSetting(value string) (SSHHostKeyPolicy, error) {
	switch strings.ToLower(value) {
	case "yes", "ask":
		return SSHHostKeyVerify, nil
	case "accept-new":
		return SSHHostKeyAcceptNew, nil
	case "no", "off":
		return SSHHostKeyAcceptAny, nil
	}
	return "", fmt.Errorf("unrecognized StrictHostKeyChecking value %q", value)
}

// parseResolvedSSHConfig maps `ssh -G` output into a typed config.
func parseResolvedSSHConfig(resolvedOutput, host string) (SSHConnConfig, error) {
	config := SSHConnConfig{HostKeyPolicy: SSHHostKeyVerify}

	scanner := bufio.NewScanner(strings.NewReader(resolvedOutput))
	for scanner.Scan() {
		key, value, found := strings.Cut(strings.TrimSpace(scanner.Text()), " ")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" || strings.EqualFold(value, "none") {
			continue
		}

		switch strings.ToLower(key) {
		case "hostname":
			config.Host = value
		case "user":
			config.User = value
		case "port":
			port, err := strconv.Atoi(value)
			if err != nil {
				return SSHConnConfig{}, fmt.Errorf("resolve ssh config for %s: invalid port %q: %w", host, value, err)
			}
			config.Port = port
		case "identityfile":
			config.IdentityFiles = append(config.IdentityFiles, expandSSHConfigPath(value))
		case "proxycommand":
			config.ProxyCommand = value
		case "connecttimeout":
			if seconds, err := strconv.Atoi(value); err == nil {
				config.ConnectTimeout = time.Duration(seconds) * time.Second
			}
		case "stricthostkeychecking":
			policy, err := hostKeyPolicyFromStrictSetting(value)
			if err != nil {
				return SSHConnConfig{}, fmt.Errorf("resolve ssh config for %s: %w", host, err)
			}
			config.HostKeyPolicy = policy
		case "userknownhostsfile":
			// The directive takes a list; the first entry is where OpenSSH
			// records a newly accepted key, so it is the one that matters.
			if first := strings.Fields(value); len(first) > 0 {
				config.KnownHostsFile = expandSSHConfigPath(first[0])
			}
		default:
			risky, found := sshConfigRiskyDirectives[strings.ToLower(key)]
			if found && !strings.EqualFold(value, risky.Default) {
				config.LegacyOptions = append(config.LegacyOptions, SSHOption{Key: risky.Name, Value: value})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return SSHConnConfig{}, fmt.Errorf("resolve ssh config for %s: %w", host, err)
	}

	if config.Host == "" {
		return SSHConnConfig{}, fmt.Errorf("resolve ssh config for %s: no hostname in ssh -G output", host)
	}
	return config, nil
}

// expandSSHConfigPath expands the leading "~" OpenSSH accepts in path
// directives, which a native client opening the file directly cannot.
func expandSSHConfigPath(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~/"))
}
