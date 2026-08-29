package fake

import (
	"context"
	"sync"
	"testing"

	"github.com/smidley/gantry/internal/collect/docker"
	"github.com/stretchr/testify/require"
)

// TestFakeContainerMaintenanceSeedIncludesRequiredNames pins the exact
// fixture the spec calls for: duplicati/watchtower/prowlarr/vaultwarden
// as the created/exited fake containers a UI dev can exercise removal
// against without a real daemon.
func TestFakeContainerMaintenanceSeedIncludesRequiredNames(t *testing.T) {
	names := map[string]bool{}
	for _, ct := range fakeContainerMaintenanceSeed {
		names[ct.Name] = true
	}
	for _, want := range []string{"duplicati", "watchtower", "prowlarr", "vaultwarden"} {
		require.True(t, names[want], "seed must include %q", want)
	}
}

// TestFakeContainerMaintenanceSeedIncludesAtLeastTwoCreatedRunners pins
// the "add a couple more created ones with plausible ages" instruction --
// modeling Scott's own real box's ephemeral GitHub-runner spawns.
func TestFakeContainerMaintenanceSeedIncludesAtLeastTwoCreatedRunners(t *testing.T) {
	created := 0
	for _, ct := range fakeContainerMaintenanceSeed {
		if ct.State == "created" {
			created++
		}
	}
	require.GreaterOrEqual(t, created, 2)
}

// TestFakeContainerMaintenanceSeedShortIDsAreUnique mirrors
// TestFakeImageSeedShortIDsAreUnique's exact concern: GET
// /api/containers/maintenance only ever shows the 12-char short id (see
// server.shortContainerID), so two seed entries sharing that prefix
// would be indistinguishable to anything a UI does with what it was
// shown.
func TestFakeContainerMaintenanceSeedShortIDsAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, ct := range fakeContainerMaintenanceSeed {
		short := ct.ID
		if len(short) > 12 {
			short = short[:12]
		}
		require.NotContains(t, seen, short, "seed ids %s and %s share GET's 12-char short id", seen[short], ct.ID)
		seen[short] = ct.ID
	}
}

// TestFakeContainerMaintenanceSeedHasManagedHintVariety pins the UI-dev
// value of the fixture: at least one dockerman-managed entry (the
// spec's "surface it so the UI can warn" case), at least one
// compose-project entry, and at least one with neither -- so every
// branch of the managed-hint UI has something to render against in fake
// mode.
func TestFakeContainerMaintenanceSeedHasManagedHintVariety(t *testing.T) {
	var sawDockerman, sawCompose, sawEmpty bool
	for _, ct := range fakeContainerMaintenanceSeed {
		switch ct.Managed {
		case "dockerman":
			sawDockerman = true
		case "":
			sawEmpty = true
		default:
			sawCompose = true
		}
	}
	require.True(t, sawDockerman, "seed must include a dockerman-managed container")
	require.True(t, sawCompose, "seed must include a compose-project-managed container")
	require.True(t, sawEmpty, "seed must include a plain, unmanaged container")
}

// TestFakeContainerMaintenanceSeedNeverIncludesDead mirrors the real
// box's own reported shape (7 exited + 17 created, zero dead) -- see
// fakeContainerMaintenanceSeed's own doc for why this isn't an
// oversight.
func TestFakeContainerMaintenanceSeedNeverIncludesDead(t *testing.T) {
	for _, ct := range fakeContainerMaintenanceSeed {
		require.NotEqual(t, "dead", ct.State)
	}
}

func TestSummarizeContainersMaintenanceCountsStates(t *testing.T) {
	cts := []docker.ContainerMaintenanceInfo{
		{ID: "a", State: "exited"},
		{ID: "b", State: "exited"},
		{ID: "c", State: "created"},
	}

	report := summarizeContainersMaintenance(cts)

	require.Equal(t, cts, report.Containers)
	require.Equal(t, docker.ContainerMaintenanceSummary{Exited: 2, Created: 1}, report.Summary)
}

func TestGeneratorContainersMaintenanceReturnsSeedCounts(t *testing.T) {
	g := newTestGenerator()

	report, err := g.ContainersMaintenance(context.Background())

	require.NoError(t, err)
	require.Equal(t, len(fakeContainerMaintenanceSeed), len(report.Containers))
	require.Equal(t, len(report.Containers), report.Summary.Exited+report.Summary.Created+report.Summary.Dead)
}

func TestGeneratorRemoveContainersDeletesFromInventory(t *testing.T) {
	g := newTestGenerator()
	before, err := g.ContainersMaintenance(context.Background())
	require.NoError(t, err)
	target := before.Containers[0]

	results, err := g.RemoveContainers(context.Background(), []string{target.ID})

	require.NoError(t, err)
	require.Equal(t, []docker.ContainerRemoveResult{
		{ID: target.ID, OK: true, Name: target.Name, Image: target.Image},
	}, results)

	after, err := g.ContainersMaintenance(context.Background())
	require.NoError(t, err)
	require.Equal(t, len(before.Containers)-1, len(after.Containers))
	for _, ct := range after.Containers {
		require.NotEqual(t, target.ID, ct.ID)
	}
}

func TestGeneratorRemoveContainersUnknownIDReturnsError(t *testing.T) {
	g := newTestGenerator()

	results, err := g.RemoveContainers(context.Background(), []string{"0000000000000000000000000000000000000000000000000000000000000000"})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.False(t, results[0].OK)
	require.NotEmpty(t, results[0].Error)
}

func TestGeneratorPruneContainersExitedRemovesOnlyExited(t *testing.T) {
	g := newTestGenerator()
	before, err := g.ContainersMaintenance(context.Background())
	require.NoError(t, err)
	wantDeleted := before.Summary.Exited

	result, err := g.PruneContainers(context.Background(), "exited", 0)

	require.NoError(t, err)
	require.Len(t, result.Deleted, wantDeleted)

	after, err := g.ContainersMaintenance(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, after.Summary.Exited)
	require.Equal(t, before.Summary.Created, after.Summary.Created)
}

func TestGeneratorPruneContainersCreatedRemovesOnlyCreated(t *testing.T) {
	g := newTestGenerator()
	before, err := g.ContainersMaintenance(context.Background())
	require.NoError(t, err)

	result, err := g.PruneContainers(context.Background(), "created", 0)

	require.NoError(t, err)
	require.Len(t, result.Deleted, before.Summary.Created)

	after, err := g.ContainersMaintenance(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, after.Summary.Created)
	require.Equal(t, before.Summary.Exited, after.Summary.Exited)
}

func TestGeneratorPruneContainersAllStoppedRemovesEverySeedEntry(t *testing.T) {
	g := newTestGenerator()
	before, err := g.ContainersMaintenance(context.Background())
	require.NoError(t, err)

	result, err := g.PruneContainers(context.Background(), "all-stopped", 0)

	require.NoError(t, err)
	require.Len(t, result.Deleted, len(before.Containers), "the seed has no dead entries, so all-stopped clears everything")

	after, err := g.ContainersMaintenance(context.Background())
	require.NoError(t, err)
	require.Empty(t, after.Containers)
}

// TestGeneratorPruneContainersOlderThanHoursFiltersRelativeToFixedEpoch
// pins a fake-mode-specific design decision: the seed's timestamps are
// authored as fixed offsets before fakeContainersBaseCreated, not
// relative to wall-clock time (see the seed's own doc), so the age
// filter must measure against that same fixed reference point -- against
// a real time.Now(), every seed entry would already read as years old
// and older_than_hours could never distinguish them.
func TestGeneratorPruneContainersOlderThanHoursFiltersRelativeToFixedEpoch(t *testing.T) {
	g := newTestGenerator()

	// A cutoff between the two seeded runners' ages (one ~2h before the
	// fixed epoch, one ~20m before it -- see fakeContainerMaintenanceSeed)
	// must catch only the older one.
	result, err := g.PruneContainers(context.Background(), "created", 1)

	require.NoError(t, err)
	require.NotEmpty(t, result.Deleted, "at least one created runner must be older than 1 hour before the fixed epoch")
	require.Less(t, len(result.Deleted), 2, "the freshly-created runner (~20 minutes old) must not be swept by a 1-hour cutoff")
}

func TestGeneratorPruneContainersUnknownModeIsError(t *testing.T) {
	g := newTestGenerator()

	_, err := g.PruneContainers(context.Background(), "bogus", 0)

	require.Error(t, err)
}

func TestGeneratorContainersMaintenanceStateIsIndependentPerInstance(t *testing.T) {
	g1 := newTestGenerator()
	g2 := newTestGenerator()

	before, err := g1.ContainersMaintenance(context.Background())
	require.NoError(t, err)
	_, err = g1.PruneContainers(context.Background(), "exited", 0)
	require.NoError(t, err)

	after2, err := g2.ContainersMaintenance(context.Background())
	require.NoError(t, err)
	require.Equal(t, len(before.Containers), len(after2.Containers), "one generator's mutation must not leak into another's")
}

// TestGeneratorContainersMaintenanceReturnsIndependentCopySafeUnderConcurrentRemove
// mirrors TestGeneratorImagesReturnsIndependentCopySafeUnderConcurrentRemove's
// exact concern for the container inventory: ContainersMaintenance must
// hand back a copy, not the same backing array RemoveContainers shifts
// in place -- run with -race, this must be clean.
func TestGeneratorContainersMaintenanceReturnsIndependentCopySafeUnderConcurrentRemove(t *testing.T) {
	g := newTestGenerator()
	stop := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			report, err := g.ContainersMaintenance(context.Background())
			require.NoError(t, err)
			for _, ct := range report.Containers {
				_ = ct.ID
				_ = ct.State
			}
		}
	}()

	seedLen := len(fakeContainerMaintenanceSeed)
	for i := 0; i < seedLen; i++ {
		report, err := g.ContainersMaintenance(context.Background())
		require.NoError(t, err)
		require.NotEmpty(t, report.Containers, "seed must still have an entry left to remove")
		_, err = g.RemoveContainers(context.Background(), []string{report.Containers[0].ID})
		require.NoError(t, err)
	}
	close(stop)
	wg.Wait()
}
