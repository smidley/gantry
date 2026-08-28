package fake

import (
	"context"
	"testing"

	"github.com/smidley/gantry/internal/collect/docker"
	"github.com/stretchr/testify/require"
)

func newTestGenerator() *Generator {
	return New(&capture{}, &eventCapture{}, 1)
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
	require.Equal(t, 12, len(report.Images))
	require.Equal(t, 5, report.Summary.InUse)
	require.Equal(t, 3, report.Summary.Unused)
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
	require.Equal(t, 3, after.Summary.Unused)
}

func TestGeneratorPruneImagesUnusedRemovesOnlyUnused(t *testing.T) {
	g := newTestGenerator()

	result, err := g.PruneImages(context.Background(), "unused")
	require.NoError(t, err)
	require.Len(t, result.Deleted, 3)

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
