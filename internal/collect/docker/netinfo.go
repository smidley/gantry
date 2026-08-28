package docker

import (
	"sort"
	"strconv"

	"github.com/docker/docker/api/types/container"
)

// NetworkInfo is one docker network a container is attached to: its
// name and (when the network assigns one) this container's own IP on
// it. A macvlan (br0-style) network's own LAN IP comes through exactly
// like any other network's -- inspect reports it the same way, so there
// is nothing else to special-case. IP prefers the endpoint's IPv4
// address, falling back to its IPv6 one (GlobalIPv6Address) only when
// no IPv4 address is assigned at all -- a v6-only network still gets a
// usable address here instead of reading as unassigned.
type NetworkInfo struct {
	Name string
	IP   string
}

// PortInfo is one container-port binding: the container side (port +
// protocol) and, when docker published it, the host side it's bound to.
// An exposed-but-unpublished port (EXPOSE with no -p) still gets one
// entry, HostIP/HostPort both left at their zero value -- see
// extractPorts' own doc.
type PortInfo struct {
	ContainerPort int
	Proto         string
	HostIP        string
	HostPort      int
}

// extractNetworks reads one container's NetworkSettings.Networks into a
// name-sorted (map iteration order isn't stable) slice of NetworkInfo.
// Host-network mode (hostNet, Meta.HostNet) reports a single
// {Name: "host"} entry with no IP unconditionally -- there's no
// per-container address to report on host networking, the container
// shares the host's own -- regardless of whatever ns itself contains.
func extractNetworks(hostNet bool, ns *container.NetworkSettings) []NetworkInfo {
	if hostNet {
		return []NetworkInfo{{Name: "host"}}
	}
	if ns == nil || len(ns.Networks) == 0 {
		return nil
	}
	out := make([]NetworkInfo, 0, len(ns.Networks))
	for name, ep := range ns.Networks {
		n := NetworkInfo{Name: name}
		if ep != nil {
			n.IP = ep.IPAddress
			if n.IP == "" {
				n.IP = ep.GlobalIPv6Address
			}
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// extractPorts reads one container's NetworkSettings.Ports into a
// slice of PortInfo, sorted by container port/proto/host port/host IP
// for a fully deterministic result. A nil binding slice -- docker's own
// shape for a port that's EXPOSEd but never published with -p -- still
// produces one PortInfo (HostIP/HostPort left at their zero value):
// "unpublished" is itself useful information for the UI to show, not an
// absence to filter out. A published port typically carries two
// bindings (an IPv4 and an IPv6 wildcard) that share ContainerPort,
// Proto, AND HostPort -- only HostIP tells them apart, so it has to be
// the sort's final tiebreaker too, or those two entries compare equal
// and their relative order is left to sort.Slice's own unstable pivot
// choices (only reliably stable, by accident, on the small slices that
// land in Go's insertion-sort fallback).
func extractPorts(ns *container.NetworkSettings) []PortInfo {
	if ns == nil || len(ns.Ports) == 0 {
		return nil
	}
	out := make([]PortInfo, 0, len(ns.Ports))
	for port, bindings := range ns.Ports {
		if len(bindings) == 0 {
			out = append(out, PortInfo{ContainerPort: port.Int(), Proto: port.Proto()})
			continue
		}
		for _, b := range bindings {
			hostPort, _ := strconv.Atoi(b.HostPort) // docker always writes digits; a parse failure just leaves this 0
			out = append(out, PortInfo{ContainerPort: port.Int(), Proto: port.Proto(), HostIP: b.HostIP, HostPort: hostPort})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ContainerPort != out[j].ContainerPort {
			return out[i].ContainerPort < out[j].ContainerPort
		}
		if out[i].Proto != out[j].Proto {
			return out[i].Proto < out[j].Proto
		}
		if out[i].HostPort != out[j].HostPort {
			return out[i].HostPort < out[j].HostPort
		}
		return out[i].HostIP < out[j].HostIP
	})
	return out
}
