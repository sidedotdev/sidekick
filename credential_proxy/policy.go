package credential_proxy

import "os"

// SideSourcesAllowed reports whether proxies backed by the host's own
// provider credentials may be started, activated or renewed by this process.
// The emergency host-side opt-out does not affect generic env/command sources.
func SideSourcesAllowed() bool {
	return os.Getenv("SIDE_CREDENTIAL_PROXY_SIDE_AUTH_ENABLED") != "false"
}
