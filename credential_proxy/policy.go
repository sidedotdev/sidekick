package credential_proxy

import "os"

// SideSourcesAllowed reports whether proxies backed by the host's own
// provider credentials may be started, activated or renewed by this process.
// They are a development debugging aid: on by default in development only,
// and switchable off there. Generic env/command sources are not affected.
func SideSourcesAllowed() bool {
	return os.Getenv("SIDE_APP_ENV") == "development" &&
		os.Getenv("SIDE_CREDENTIAL_PROXY_SIDE_AUTH_ENABLED") != "false"
}
