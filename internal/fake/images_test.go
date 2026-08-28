package fake

import (
	"context"
	"sync"
	"testing"

	"github.com/smidley/gantry/internal/collect/docker"
	"github.com/stretchr/testify/require"
)

func newTestGenerator() *Generator {
	return New(&capture{}, &eventCapture{}, 1)
}

// TestGeneratorImagesReturnsIndependentCopySafeUnderConcurrentRemove
// pins F3: Images used to return summarizeImages(g.images) -- the
// SAME backing array RemoveImages shifts in place via append(g.images
// [:idx], g.images[idx+1:]...). A caller iterating what Images handed
// back, after the call has already unlocked, could then be reading the
// exact memory a concurrent RemoveImages is mutating. Run with -race:
// this must be clean, not just "doesn't panic".
func TestGeneratorImagesReturnsIndependentCopySafeUnderConcurrentRemove(t *testing.T) {
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
			report, err := g.Images(context.Background())
			require.NoError(t, err)
			for _, im := range report.Images {
				_ = im.ID
				_ = im.State
			}
		}
	}()

	// fakeImageSeed has 4 dangling entries -- enough removals to give the
	// reader goroutine above plenty of overlap while this shrinks the
	// backing array.
	for i := 0; i < 4; i++ {
		report, err := g.Images(context.Background())
		require.NoError(t, err)
		var id string
		for _, im := range report.Images {
			if im.State == "dangling" {
				id = im.ID
				break
			}
		}
		require.NotEmpty(t, id, "seed must still have a dangling image left to remove")
		_, err = g.RemoveImages(context.Background(), []string{id})
		require.NoError(t, err)
	}
	close(stop)
	wg.Wait()
}

// TestFakeImageSeedShortIDsAreUnique pins the exact bug a real box's own
// GET /api/images -> POST /api/images/remove round trip would hit: the
// server layer only ever shows GET's own 12-character short id (see
// server.shortImageID), so if two seed entries shared that prefix,
// nothing a UI does with what it was shown could tell them apart.
func TestFakeImageSeedShortIDsAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, im := range fakeImageSeed {
		short := im.ID
		if len(short) > 19 { // len("sha256:") + 12
			short = short[:19]
		}
		require.NotContains(t, seen, short, "seed ids %s and %s share GET's 12-char short id", seen[short], im.ID)
		seen[short] = im.ID
	}
}

// TestFakeImageSeedIncludesADigestPinnedUnusedImage pins N3: the seed
// had no entry with RepoDigests set and no RepoTags, so GET
// /api/images' digest-ref display path (see server.digestRefsOrNone)
// was only ever exercised by a synthetic test fixture, never visible
// against GANTRY_FAKE_DATA=1 for whoever builds the UI against it.
func TestFakeImageSeedIncludesADigestPinnedUnusedImage(t *testing.T) {
	found := false
	for _, im := range fakeImageSeed {
		if len(im.RepoTags) == 0 && len(im.RepoDigests) > 0 {
			found = true
			require.Equal(t, "unused", im.State, "a digest-pinned image is not dangling -- see classifyImages' own doc")
		}
	}
	require.True(t, found, "fake mode's seed must include at least one digest-pinned (no RepoTags) image")
}

// TestGeneratorRemoveImagesResolvesUniqueShortID pins the same round
// trip from RemoveImages' side: a real docker daemon resolves an
// unambiguous short id prefix on its own (docker.Collector.RemoveImages
// passes it straight through to the SDK, which forwards to the daemon),
// so fake mode's own in-memory match must do the same, or a UI that
// naively echoes GET's own id field back into a remove call would work
// against a real box and silently fail in fake mode.
func TestGeneratorRemoveImagesResolvesUniqueShortID(t *testing.T) {
	g := newTestGenerator()
	before, err := g.Images(context.Background())
	require.NoError(t, err)
	var full string
	for _, im := range before.Images {
		if im.State == "dangling" {
			full = im.ID
			break
		}
	}
	require.NotEmpty(t, full, "seed must contain at least one dangling image")
	short := full[7:19] // strip "sha256:", keep docker's own 12-char short form

	results, err := g.RemoveImages(context.Background(), []string{short})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.True(t, results[0].OK, "error: %s", results[0].Error)
	require.Equal(t, short, results[0].ID, "the result must echo back exactly what the caller sent, not the resolved full id")

	after, err := g.Images(context.Background())
	require.NoError(t, err)
	require.Equal(t, len(before.Images)-1, len(after.Images))
}

func TestSummarizeImagesCountsStatesAndSumsReclaimableBytes(t *testing.T) {
	images := []docker.ImageInfo{
		{ID: "a", State: "in-use", SizeBytes: 900},
		{ID: "b", State: "unused", SizeBytes: 100},
		{ID: "c", State: "dangling", SizeBytes: 50},
	}

	report := summarizeImages(images)

	require.Equal(t, images, report.Images)
	require.Equal(t, docker.ImagesSummary{InUse: 1, Unused: 1, Dangling: 1, ReclaimableBytes: 150}, report.Summary)
}

func TestGeneratorImagesReturnsSeedOfMixedStates(t *testing.T) {
	g := newTestGenerator()

	report, err := g.Images(context.Background())

	require.NoError(t, err)
	require.Equal(t, 13, len(report.Images))
	require.Equal(t, 5, report.Summary.InUse)
	require.Equal(t, 4, report.Summary.Unused)
	require.Equal(t, 4, report.Summary.Dangling)
}

func TestGeneratorRemoveImagesDeletesFromInventory(t *testing.T) {
	g := newTestGenerator()
	before, err := g.Images(context.Background())
	require.NoError(t, err)
	var dangling docker.ImageInfo
	for _, im := range before.Images {
		if im.State == "dangling" {
			dangling = im
			break
		}
	}
	require.NotEmpty(t, dangling.ID, "seed must contain at least one dangling image")

	results, err := g.RemoveImages(context.Background(), []string{dangling.ID})
	require.NoError(t, err)
	require.Equal(t, []docker.ImageRemoveResult{
		{ID: dangling.ID, OK: true, RepoTags: dangling.RepoTags, SizeBytes: dangling.SizeBytes},
	}, results)
	danglingID := dangling.ID

	after, err := g.Images(context.Background())
	require.NoError(t, err)
	require.Equal(t, len(before.Images)-1, len(after.Images))
	for _, im := range after.Images {
		require.NotEqual(t, danglingID, im.ID)
	}
}

func TestGeneratorRemoveImagesRefusesInUseImage(t *testing.T) {
	g := newTestGenerator()
	report, err := g.Images(context.Background())
	require.NoError(t, err)
	var inUse docker.ImageInfo
	for _, im := range report.Images {
		if im.State == "in-use" {
			inUse = im
			break
		}
	}
	require.NotEmpty(t, inUse.ID, "seed must contain at least one in-use image")

	results, err := g.RemoveImages(context.Background(), []string{inUse.ID})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.False(t, results[0].OK)
	require.NotEmpty(t, results[0].Error)

	after, err := g.Images(context.Background())
	require.NoError(t, err)
	require.Equal(t, len(report.Images), len(after.Images), "refused removal must not mutate the inventory")
}

func TestGeneratorRemoveImagesUnknownIDReturnsError(t *testing.T) {
	g := newTestGenerator()

	results, err := g.RemoveImages(context.Background(), []string{"sha256:doesnotexist"})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.False(t, results[0].OK)
	require.NotEmpty(t, results[0].Error)
}

func TestGeneratorPruneImagesDanglingRemovesOnlyDangling(t *testing.T) {
	g := newTestGenerator()

	result, err := g.PruneImages(context.Background(), "dangling")
	require.NoError(t, err)
	require.Len(t, result.Deleted, 4)
	require.Positive(t, result.ReclaimedBytes)

	after, err := g.Images(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, after.Summary.Dangling)
	require.Equal(t, 5, after.Summary.InUse)
	require.Equal(t, 4, after.Summary.Unused)
}

func TestGeneratorPruneImagesUnusedRemovesOnlyUnused(t *testing.T) {
	g := newTestGenerator()

	result, err := g.PruneImages(context.Background(), "unused")
	require.NoError(t, err)
	require.Len(t, result.Deleted, 4)

	after, err := g.Images(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, after.Summary.Unused)
	require.Equal(t, 5, after.Summary.InUse)
	require.Equal(t, 4, after.Summary.Dangling)
}

func TestGeneratorPruneImagesUnknownModeIsError(t *testing.T) {
	g := newTestGenerator()

	_, err := g.PruneImages(context.Background(), "bogus")

	require.Error(t, err)
}

func TestGeneratorImagesStateIsIndependentPerInstance(t *testing.T) {
	g1 := newTestGenerator()
	g2 := newTestGenerator()

	before, err := g1.Images(context.Background())
	require.NoError(t, err)
	_, err = g1.PruneImages(context.Background(), "dangling")
	require.NoError(t, err)

	after2, err := g2.Images(context.Background())
	require.NoError(t, err)
	require.Equal(t, len(before.Images), len(after2.Images), "one generator's mutation must not leak into another's")
}
