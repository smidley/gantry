package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"
)

// TestMetaFromInspectExtractsExitCode pins the passthrough Container
// Detail's anomaly banner needs: a running container's State.ExitCode is
// docker's own meaningless-while-running 0, and an exited container's is
// whatever it actually exited with -- both flow straight into Meta.
// ExitCode, unconditionally, the same "always present, contextually
// interpreted" convention Meta.State/Health already follow.
func TestMetaFromInspectExtractsExitCode(t *testing.T) {
	cases := []struct {
		name     string
		state    *container.State
		wantCode int
	}{
		{name: "running container reads 0", state: &container.State{Status: "running", ExitCode: 0}, wantCode: 0},
		{name: "exited container reads its real exit code", state: &container.State{Status: "exited", ExitCode: 137}, wantCode: 137},
		{name: "clean exit reads 0", state: &container.State{Status: "exited", ExitCode: 0}, wantCode: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{
					ID:    "abc123",
					Name:  "/demo",
					State: c.state,
				},
			}
			m := metaFromInspect(resp, nil)
			require.Equal(t, c.wantCode, m.ExitCode)
			require.Equal(t, c.state.Status, m.State)
		})
	}
}

// TestAllocFromHostConfigCarriesCPUSetRaw pins that CPUSetRaw rides
// alongside CPUSetCores/HasCPUSet -- CPUSetPin (cgroupv2.go) needs the
// raw string, not just the count, to render a display string, and this
// is the API-fallback/Meta-level path's only source of it (the cgroup v2
// fast path has its own, fresher one -- see readCgroupStats).
func TestAllocFromHostConfigCarriesCPUSetRaw(t *testing.T) {
	a := allocFromHostConfig(container.Resources{CpusetCpus: "0-1"})
	require.True(t, a.HasCPUSet)
	require.Equal(t, 2, a.CPUSetCores)
	require.Equal(t, "0-1", a.CPUSetRaw)
}

// TestAllocFromHostConfigNoCpusetLeavesRawEmpty pins the unset case: no
// --cpuset-cpus at all must leave HasCPUSet false and CPUSetRaw "", not
// a stray empty-but-"set" string CPUSetPin could misread.
func TestAllocFromHostConfigNoCpusetLeavesRawEmpty(t *testing.T) {
	a := allocFromHostConfig(container.Resources{})
	require.False(t, a.HasCPUSet)
	require.Equal(t, "", a.CPUSetRaw)
}
