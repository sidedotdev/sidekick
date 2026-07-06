package common

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// PortForwardsEnvVar is injected into commands run in remote container
// environments that have port forwards configured. Its value lists the active
// reverse forwards as comma-separated "hostPort:containerPort" pairs, letting
// processes inside the container (e.g. tests) discover the container-local
// port at which a forwarded host service is reachable.
const PortForwardsEnvVar = "SIDE_PORT_FORWARDS"

// FormatPortForwards renders forwards as the PortForwardsEnvVar value, e.g.
// "18855:28855,8080:8080". Returns "" when there are no forwards.
func FormatPortForwards(forwards []PortForwardConfig) string {
	pairs := make([]string, 0, len(forwards))
	for _, forward := range forwards {
		pairs = append(pairs, fmt.Sprintf("%d:%d", forward.HostPort, forward.ContainerPortOrDefault()))
	}
	return strings.Join(pairs, ",")
}

// LookupForwardedPort returns the container-local port at which the given
// host port is reachable, according to PortForwardsEnvVar. It returns false
// when the variable is unset or does not list the host port.
func LookupForwardedPort(hostPort int) (int, bool) {
	for _, pair := range strings.Split(os.Getenv(PortForwardsEnvVar), ",") {
		hostPart, containerPart, found := strings.Cut(pair, ":")
		if !found {
			continue
		}
		parsedHostPort, err := strconv.Atoi(hostPart)
		if err != nil || parsedHostPort != hostPort {
			continue
		}
		containerPort, err := strconv.Atoi(containerPart)
		if err != nil {
			continue
		}
		return containerPort, true
	}
	return 0, false
}
