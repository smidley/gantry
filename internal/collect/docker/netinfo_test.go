package docker

import (
	"strconv"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"github.com/stretchr/testify/require"
)

// TestExtractNetworksHostMode pins host-network containers: a single
// synthetic {Name: "host"} entry with no IP, regardless of whatever
// NetworkSettings itself says -- there's no per-container address on
// host networking, the container shares the host's own.
func TestExtractNetworksHostMode(t *testing.T) {
	got := extractNetworks(true, &container.NetworkSettings{
		Networks: map[string]*network.EndpointSettings{"bridge": {IPAddress: "172.17.0.2"}},
	})
	require.Equal(t, []NetworkInfo{{Name: "host"}}, got)
}

// TestExtractNetworksNilSettings pins the defensive nil-NetworkSettings
// case (a container inspect response with no networking info at all)
// alongside the "no networks attached" case -- neither may panic, both
// report no networks.
func TestExtractNetworksNilSettings(t *testing.T) {
	require.Nil(t, extractNetworks(false, nil))
	require.Nil(t, extractNetworks(false, &container.NetworkSettings{}))
}

// TestExtractNetworksBridgeAndMacvlan pins the ordinary case -- a
// container's own IP on each attached network, sorted by network name
// for a deterministic result (map iteration order isn't) -- and that a
// macvlan (br0-style) network's own LAN IP comes through exactly like
// any other network's, with no special-casing.
func TestExtractNetworksBridgeAndMacvlan(t *testing.T) {
	got := extractNetworks(false, &container.NetworkSettings{
		Networks: map[string]*network.EndpointSettings{
			"br0":    {IPAddress: "192.168.1.50"},
			"bridge": {IPAddress: "172.17.0.2"},
		},
	})
	require.Equal(t, []NetworkInfo{
		{Name: "br0", IP: "192.168.1.50"},
		{Name: "bridge", IP: "172.17.0.2"},
	}, got)
}

// TestExtractNetworksNilEndpoint pins a defensive case: a network entry
// present in the map but with a nil *EndpointSettings value must still
// surface the network's name, just with no IP, rather than panicking.
func TestExtractNetworksNilEndpoint(t *testing.T) {
	got := extractNetworks(false, &container.NetworkSettings{
		Networks: map[string]*network.EndpointSettings{"bridge": nil},
	})
	require.Equal(t, []NetworkInfo{{Name: "bridge"}}, got)
}

// TestExtractPortsNilSettings pins the defensive nil-NetworkSettings
// case alongside "no ports at all".
func TestExtractPortsNilSettings(t *testing.T) {
	require.Nil(t, extractPorts(nil))
	require.Nil(t, extractPorts(&container.NetworkSettings{}))
}

// networkSettingsWithPorts builds a *container.NetworkSettings carrying
// only Ports, via plain field assignment rather than a struct literal
// naming the embedded NetworkSettingsBase directly -- Ports currently
// lives there (a docker SDK v28 shape the field will move off of in
// v29, per its own doc comment), and staticcheck's deprecation check
// flags any literal that names the embedded type, even though Ports
// itself carries no per-field deprecation notice.
func networkSettingsWithPorts(ports nat.PortMap) *container.NetworkSettings {
	ns := &container.NetworkSettings{}
	ns.Ports = ports
	return ns
}

// TestExtractPortsUnpublishedGetsEmptyHostFields pins the nil-binding
// case (EXPOSE with no -p): still one entry, with HostIP/HostPort left
// at their zero values -- "exposed but unpublished" is itself useful
// information, not something to filter out.
func TestExtractPortsUnpublishedGetsEmptyHostFields(t *testing.T) {
	got := extractPorts(networkSettingsWithPorts(nat.PortMap{"8096/tcp": nil}))
	require.Equal(t, []PortInfo{{ContainerPort: 8096, Proto: "tcp"}}, got)
}

// TestExtractPortsPublishedIPv4AndIPv6 pins the ordinary published case:
// one PortInfo per binding (docker typically emits both an IPv4 and an
// IPv6 wildcard binding for a single -p), sorted deterministically.
func TestExtractPortsPublishedIPv4AndIPv6(t *testing.T) {
	got := extractPorts(networkSettingsWithPorts(nat.PortMap{
		"8096/tcp": []nat.PortBinding{
			{HostIP: "0.0.0.0", HostPort: "8096"},
			{HostIP: "::", HostPort: "8096"},
		},
	}))
	require.Equal(t, []PortInfo{
		{ContainerPort: 8096, Proto: "tcp", HostIP: "0.0.0.0", HostPort: 8096},
		{ContainerPort: 8096, Proto: "tcp", HostIP: "::", HostPort: 8096},
	}, got)
}

// TestExtractPortsMultiplePortsSortedByContainerPort pins ordering
// across distinct container ports, including a udp entry, so the DTO's
// own slice order is stable across ticks rather than following map
// iteration's random order.
func TestExtractPortsMultiplePortsSortedByContainerPort(t *testing.T) {
	got := extractPorts(networkSettingsWithPorts(nat.PortMap{
		"53/udp":   []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "53"}},
		"443/tcp":  []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "443"}},
		"8096/tcp": nil,
	}))
	require.Equal(t, []PortInfo{
		{ContainerPort: 53, Proto: "udp", HostIP: "0.0.0.0", HostPort: 53},
		{ContainerPort: 443, Proto: "tcp", HostIP: "0.0.0.0", HostPort: 443},
		{ContainerPort: 8096, Proto: "tcp"},
	}, got)
}

// TestExtractPortsDualStackOrderIsFullyDeterministic pins the sort's
// final tiebreaker: a published port's two bindings (the IPv4 and IPv6
// wildcard docker typically emits for one -p) share ContainerPort,
// Proto, AND HostPort -- only HostIP tells them apart, so a less func
// that stops at HostPort compares them equal and leaves their relative
// order to sort.Slice's own (unstable) pivot choices. At a small size
// that accidentally lands on Go's insertion-sort fallback this can look
// stable by luck; this fixture uses 16 container ports (32 bindings,
// past pdqsort's insertion-sort-only threshold) and re-extracts from the
// same, map-iteration-order-randomized input 100 times, requiring the
// exact same byte-identical slice every time.
func TestExtractPortsDualStackOrderIsFullyDeterministic(t *testing.T) {
	ports := nat.PortMap{}
	var want []PortInfo
	for p := 8000; p < 8016; p++ {
		key := nat.Port(strconv.Itoa(p) + "/tcp")
		ports[key] = []nat.PortBinding{
			{HostIP: "::", HostPort: strconv.Itoa(p)},
			{HostIP: "0.0.0.0", HostPort: strconv.Itoa(p)},
		}
		want = append(want,
			PortInfo{ContainerPort: p, Proto: "tcp", HostIP: "0.0.0.0", HostPort: p},
			PortInfo{ContainerPort: p, Proto: "tcp", HostIP: "::", HostPort: p},
		)
	}
	ns := networkSettingsWithPorts(ports)

	for i := 0; i < 100; i++ {
		got := extractPorts(ns)
		require.Equal(t, want, got, "extraction %d: HostIP must break the ContainerPort/Proto/HostPort tie deterministically", i)
	}
}
