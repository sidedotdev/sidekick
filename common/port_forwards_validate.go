package common

import (
	"errors"
	"fmt"
)

const (
	minPort = 1
	maxPort = 65535
)

// Validate reports whether the forward describes a usable host/container port
// pair. Both sides must be valid TCP ports; a zero container port is allowed
// because it defaults to the host port.
func (c PortForwardConfig) Validate() error {
	if c.HostPort < minPort || c.HostPort > maxPort {
		return fmt.Errorf("port forward host_port must be between %d and %d, got %d", minPort, maxPort, c.HostPort)
	}
	if c.ContainerPort != 0 && (c.ContainerPort < minPort || c.ContainerPort > maxPort) {
		return fmt.Errorf("port forward container_port must be between %d and %d, got %d", minPort, maxPort, c.ContainerPort)
	}
	return nil
}

// ValidatePortForwards checks every forward and rejects lists that would
// fail to bind: reverse forwards listen on the container side, so two
// forwards sharing an effective container port cannot coexist.
func ValidatePortForwards(forwards []PortForwardConfig) error {
	var errs []error
	seenContainerPorts := make(map[int]int, len(forwards))
	for _, forward := range forwards {
		if err := forward.Validate(); err != nil {
			errs = append(errs, err)
			continue
		}
		containerPort := forward.ContainerPortOrDefault()
		if hostPort, exists := seenContainerPorts[containerPort]; exists {
			errs = append(errs, fmt.Errorf(
				"port forward container_port %d is used by both host_port %d and host_port %d",
				containerPort, hostPort, forward.HostPort,
			))
			continue
		}
		seenContainerPorts[containerPort] = forward.HostPort
	}
	return errors.Join(errs...)
}
