package insight

import (
	"testing"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

func TestHostAttributionDoesNotRenormalizeSmallNeighbors(t *testing.T) {
	small := mkMatch(map[string][]store.Sample{
		"neighbor": seriesRange(testNow-100, testNow, 10, 2),
		"other":    seriesRange(testNow-100, testNow, 10, 1),
	})
	cpu := cpuStarvationIn(testNow, true, true, 2)
	cpu.ContainerCPUPct = small
	require.Empty(t, evalCPUStarvation(cpu, librarySpecs[2].defaults))
	mem := memorySqueezeOOMIn(testNow, true, true)
	mem.ContainerMemPct = small
	require.Empty(t, evalMemorySqueeze(mem, librarySpecs[6].defaults))
}

func TestCPUAttributionReportsHostCapacity(t *testing.T) {
	in := cpuStarvationIn(testNow, true, true, 2)
	f := evalCPUStarvation(in, librarySpecs[2].defaults)
	require.Len(t, f, 1)
	require.InDelta(t, 46, f[0].Evidence.CulpritSharePct, 0.001)
}

func TestOOMDoesNotBlameNeighborOnHealthyHostOrContainerLimit(t *testing.T) {
	for _, scenario := range []string{"healthy-host", "container-limit", "stale-pressure"} {
		t.Run(scenario, func(t *testing.T) {
			in := memorySqueezeOOMIn(testNow, true, true)
			switch scenario {
			case "healthy-host":
				in.HostMemUsedPct = mkMatch(map[string][]store.Sample{"": {{TS: testNow, Val: 30}}})
			case "container-limit":
				in.ContainerMemLimitBytes = mkMatch(map[string][]store.Sample{"minecraft": {{TS: testNow, Val: 1 << 30}}})
			case "stale-pressure":
				in.HostMemUsedPct = mkMatch(map[string][]store.Sample{"": {{TS: testNow - 60, Val: 99}}})
			}
			require.Empty(t, evalMemorySqueeze(in, librarySpecs[6].defaults))
		})
	}
}

func TestMemorySharedAttributionReportsCombinedHostCapacity(t *testing.T) {
	in := memorySqueezeHostIn(testNow, true, true)
	in.ContainerMemPct = mkMatch(map[string][]store.Sample{
		"one": {{TS: testNow, Val: 20}}, "two": {{TS: testNow, Val: 15}}, "three": {{TS: testNow, Val: 1}},
	})
	f := evalMemorySqueeze(in, librarySpecs[6].defaults)
	require.Len(t, f, 1)
	require.ElementsMatch(t, []string{"one", "two"}, f[0].Culprit.Names)
	require.InDelta(t, 35, f[0].Evidence.CulpritSharePct, 0.001)
}
