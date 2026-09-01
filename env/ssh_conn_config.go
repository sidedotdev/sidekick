package env

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// SSHHostKeyPolicy selects how a connection verifies the remote host key.
type SSHHostKeyPolicy string

const (
	// SSHHostKeyVerify checks the host key against the user's known_hosts.
	SSHHostKeyVerify SSHHostKeyPolicy = "verify"
	// SSHHostKeyAcceptAny skips verification and records nothing. It is only
	// appropriate where the endpoint is authenticated by other means — an
	// ephemeral address plus a key we injected ourselves — and the host key is
	// regenerated on every boot, so pinning it would be meaningless.
	SSHHostKeyAcceptAny SSHHostKeyPolicy = "accept-any"
	// SSHHostKeyAcceptNew records the key of a host seen for the first time
	// and verifies every later connection against it, refusing a key that
	// changed. It exists because users configure it, and collapsing it into
	// either neighbour would misrepresent their config: verify would refuse
	// hosts OpenSSH would have accepted, and accept-any would stop noticing a
	// changed key.
	SSHHostKeyAcceptNew SSHHostKeyPolicy = "accept-new"
)

// SSHOption is a provider ssh_config directive that SSHConnConfig does not
// model, carried verbatim so the legacy renderer can reproduce it exactly.
type SSHOption struct {
	Key   string
	Value string
}

// nativeIgnorableOptions are directives that only steer the ssh binary itself,
// so a native client disregarding them changes nothing observable.
var nativeIgnorableOptions = map[string]bool{
	"batchmode": true,
	"loglevel":  true,
}

// knownHostsDisabled reports whether a known-hosts path is OpenSSH's way of
// saying there are no host keys to consult, rather than a file to read.
func knownHostsDisabled(path string) bool {
	return path == "" || path == os.DevNull || strings.EqualFold(path, "none")
}

// SSHConnConfig describes how to reach a remote environment independently of
// which transport does the reaching: the legacy transport renders it back to
// OpenSSH CLI args, while a native client dials it directly. Providers produce
// it, so structure never has to be inferred from argv.
type SSHConnConfig struct {
	Host string
	Port int
	User string

	IdentityFiles []string
	HostKeyPolicy SSHHostKeyPolicy

	// KnownHostsFiles overrides where verified host keys are read and written.
	// OpenSSH takes a list, and every entry is consulted, so all of them are
	// kept: dropping one would trust fewer keys than the user configured.
	KnownHostsFiles []string

	// GlobalKnownHostsFiles overrides the system-wide host key files. A native
	// transport consults only KnownHostsFiles, so ValidateNative accepts these
	// only when they name OpenSSH's "no keys here" path.
	GlobalKnownHostsFiles []string

	// BatchMode and LogLevel steer the ssh binary only: never prompt, and keep
	// chatter out of the stderr that callers parse. A nil BatchMode leaves the
	// directive out, while false renders the explicit "no" a provider asked
	// for — which is also OpenSSH's default, so it states no intent an absent
	// directive would not. Neither form reaches a native client, which is
	// non-interactive whatever this says.
	BatchMode *bool
	LogLevel  string

	// LegacyOptions carries provider directives with no typed field. The
	// legacy renderer emits them verbatim; a native transport must refuse to
	// dial rather than ignore one, since a dropped directive can weaken host
	// key verification or sever reachability. See ValidateNative.
	LegacyOptions []SSHOption

	// ConnectTimeout bounds a dial. Nil leaves the transport's own default in
	// place; an explicit zero is OpenSSH's "no client-side timeout", leaving
	// only the caller's context to bound the dial.
	ConnectTimeout *time.Duration
	// DialAttempts bounds how many times one dial is retried before it is
	// reported as failed. Zero leaves the transport's own default in place.
	DialAttempts int

	// KeepaliveInterval and KeepaliveMaxFailures detect a peer that vanished
	// without closing the connection, which matters most for the long-lived
	// connections holding reverse forwards. An explicit zero interval disables
	// keepalives, as it does in OpenSSH.
	KeepaliveInterval    *time.Duration
	KeepaliveMaxFailures *int

	// HTTPConnectProxy is the "host:port" of an HTTP proxy to tunnel through
	// with CONNECT. On proxy-only networks it is the only route to ephemeral
	// endpoints, and it delegates name resolution to the proxy.
	HTTPConnectProxy string

	// ProxyCommand is an arbitrary command whose stdin/stdout carry the SSH
	// connection, as resolved from a user's ssh_config. A native transport
	// runs it and speaks SSH over its stdio; HTTPConnectProxy takes
	// precedence, since it expresses the same tunnel without a subprocess.
	ProxyCommand string

	// ControlPath, ControlPersist and ControlPersistForever configure OpenSSH
	// connection multiplexing, and are legacy-only: a native client
	// multiplexes channels over a single connection by design.
	// ControlPersistForever keeps the master up indefinitely and takes
	// precedence over ControlPersist.
	ControlPath           string
	ControlPersist        time.Duration
	ControlPersistForever bool

	// LegacyCommandSeparator appends "--" after the destination, so a caller
	// appending a remote command cannot have it parsed as an ssh option.
	// Legacy-only: a native transport passes the command out of band.
	LegacyCommandSeparator bool
}

// ValidateNative reports whether a native transport can honour this config in
// full. It fails closed: every carried directive that is not a pure ssh-binary
// concern is named in the error instead of being silently disregarded.
func (c SSHConnConfig) ValidateNative() error {
	var unsupported []string
	for _, option := range c.LegacyOptions {
		if nativeIgnorableOptions[strings.ToLower(option.Key)] {
			continue
		}
		unsupported = append(unsupported, option.Key)
	}
	if len(unsupported) > 0 {
		return fmt.Errorf("native ssh transport cannot honour ssh_config directives: %s",
			strings.Join(unsupported, ", "))
	}
	for _, path := range c.GlobalKnownHostsFiles {
		if !knownHostsDisabled(path) {
			return fmt.Errorf("native ssh transport cannot honour GlobalKnownHostsFile=%s", path)
		}
	}
	// Native keepalives tolerate at least one unanswered probe before closing
	// the connection, so a threshold of zero would be honoured as something
	// else. Refusing keeps the directive with ssh itself, which does implement
	// it. A zero interval makes the threshold moot: nothing probes at all.
	if c.KeepaliveMaxFailures != nil && *c.KeepaliveMaxFailures == 0 && c.keepaliveInterval() > 0 {
		return fmt.Errorf("native ssh transport cannot honour ServerAliveCountMax=0")
	}
	// Recording a first-seen key means writing the user's known_hosts, which
	// concurrent dials make unsafe without atomic append. Until that exists,
	// refusing is the only honest answer: verifying instead would reject hosts
	// OpenSSH accepts, and skipping verification would silently weaken it.
	if c.HostKeyPolicy == SSHHostKeyAcceptNew {
		return fmt.Errorf("native ssh transport cannot honour StrictHostKeyChecking=accept-new")
	}
	return nil
}

// withResolvedReachability returns c with the fields that describe how to
// reach the host taken from resolved, keeping c's own operational settings
// (multiplexing, keepalives, logging). Providers whose host is an ssh_config
// alias use this to combine what OpenSSH resolved with what they configured.
func (c SSHConnConfig) withResolvedReachability(resolved SSHConnConfig) SSHConnConfig {
	c.Host = resolved.Host
	c.User = resolved.User
	c.Port = resolved.Port
	c.IdentityFiles = resolved.IdentityFiles
	c.HostKeyPolicy = resolved.HostKeyPolicy
	c.KnownHostsFiles = resolved.KnownHostsFiles
	c.ProxyCommand = resolved.ProxyCommand
	return c
}

// connectTimeout reports how long a dial may take and whether any client-side
// bound applies at all: an explicitly configured ConnectTimeout of zero means
// OpenSSH imposes none, leaving only the caller's context to bound the dial.
func (c SSHConnConfig) connectTimeout(fallback time.Duration) (time.Duration, bool) {
	switch {
	case c.ConnectTimeout == nil:
		return fallback, true
	case *c.ConnectTimeout <= 0:
		return 0, false
	default:
		return *c.ConnectTimeout, true
	}
}

// keepaliveInterval reports how often to probe the peer, with an absent
// directive meaning no probing, exactly as an explicit zero does.
func (c SSHConnConfig) keepaliveInterval() time.Duration {
	if c.KeepaliveInterval == nil {
		return 0
	}
	return *c.KeepaliveInterval
}

// keepaliveMaxFailures reports how many unanswered probes to tolerate before
// closing a connection. ValidateNative refuses an explicit zero, so the floor
// of one only ever stands in for an absent directive.
func (c SSHConnConfig) keepaliveMaxFailures() int {
	if c.KeepaliveMaxFailures == nil {
		return 1
	}
	return max(*c.KeepaliveMaxFailures, 1)
}

// optionalDirective renders a pointer field for a connection fingerprint,
// keeping an absent directive distinct from every value it could hold.
func optionalDirective[T any](value *T) string {
	if value == nil {
		return "unset"
	}
	return fmt.Sprint(*value)
}

// Destination is the positional target OpenSSH expects.
func (c SSHConnConfig) Destination() string {
	if c.User == "" {
		return c.Host
	}
	return c.User + "@" + c.Host
}

// Addr is the dial target for transports that connect without the ssh binary.
func (c SSHConnConfig) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// LegacyArgs renders the config as OpenSSH CLI args ending in the destination,
// so a remote command can be appended directly.
func (c SSHConnConfig) LegacyArgs() []string {
	args := make([]string, 0, 32)
	if c.ControlPath != "" {
		args = append(args, "-o", "ControlMaster=auto", "-S", c.ControlPath)
		switch {
		case c.ControlPersistForever:
			args = append(args, "-o", "ControlPersist=yes")
		case c.ControlPersist > 0:
			args = append(args, "-o", "ControlPersist="+wholeSeconds(c.ControlPersist))
		}
	}
	// Provider directives keep the position and order they had in the config
	// the provider emitted, so the rendered argv stays byte-for-byte what the
	// legacy path sent before this config existed.
	for _, option := range c.LegacyOptions {
		args = append(args, "-o", option.Key+"="+option.Value)
	}
	if c.BatchMode != nil {
		args = append(args, "-o", "BatchMode="+openSSHBool(*c.BatchMode))
	}
	knownHostsFiles := strings.Join(c.KnownHostsFiles, " ")
	switch {
	case c.HostKeyPolicy == SSHHostKeyAcceptAny:
		if knownHostsFiles == "" {
			knownHostsFiles = os.DevNull
		}
		args = append(args, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile="+knownHostsFiles)
	case c.HostKeyPolicy == SSHHostKeyAcceptNew:
		args = append(args, "-o", "StrictHostKeyChecking=accept-new")
		if knownHostsFiles != "" {
			args = append(args, "-o", "UserKnownHostsFile="+knownHostsFiles)
		}
	case knownHostsFiles != "":
		args = append(args, "-o", "UserKnownHostsFile="+knownHostsFiles)
	}
	if globalKnownHostsFiles := strings.Join(c.GlobalKnownHostsFiles, " "); globalKnownHostsFiles != "" {
		args = append(args, "-o", "GlobalKnownHostsFile="+globalKnownHostsFiles)
	}
	if c.ConnectTimeout != nil {
		args = append(args, "-o", "ConnectTimeout="+wholeSeconds(*c.ConnectTimeout))
	}
	if c.DialAttempts > 0 {
		args = append(args, "-o", "ConnectionAttempts="+strconv.Itoa(c.DialAttempts))
	}
	if c.KeepaliveInterval != nil {
		args = append(args, "-o", "ServerAliveInterval="+wholeSeconds(*c.KeepaliveInterval))
	}
	if c.KeepaliveMaxFailures != nil {
		args = append(args, "-o", "ServerAliveCountMax="+strconv.Itoa(*c.KeepaliveMaxFailures))
	}
	if c.LogLevel != "" {
		args = append(args, "-o", "LogLevel="+c.LogLevel)
	}
	if c.HTTPConnectProxy != "" {
		args = append(args, "-o", "ProxyCommand=nc -X connect -x "+c.HTTPConnectProxy+" %h %p")
	} else if c.ProxyCommand != "" {
		args = append(args, "-o", "ProxyCommand="+c.ProxyCommand)
	}
	for _, identityFile := range c.IdentityFiles {
		args = append(args, "-i", identityFile)
	}
	if c.Port > 0 {
		args = append(args, "-p", strconv.Itoa(c.Port))
	}
	args = append(args, c.Destination())
	if c.LegacyCommandSeparator {
		args = append(args, "--")
	}
	return args
}

// wholeSeconds formats a duration the way OpenSSH options express time.
func wholeSeconds(d time.Duration) string {
	return strconv.Itoa(int(d.Round(time.Second).Seconds()))
}

// openSSHBool renders a boolean directive the way ssh_config spells it.
func openSSHBool(enabled bool) string {
	if enabled {
		return "yes"
	}
	return "no"
}
