package docker

import (
	"context"
	"fmt"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/stretchr/testify/require"
)

// summaryOf builds a minimal image.Summary -- id, tags, size are all
// classifyImages actually reads.
func summaryOf(id string, tags []string, size int64) image.Summary {
	return image.Summary{ID: id, RepoTags: tags, Size: size}
}

// ctrOf builds a minimal container.Summary -- name (docker's own leading
// "/" included, as ContainerList really returns it), the image ref
// string recorded at create time, and the id it's actually pinned to.
func ctrOf(name, imageRef, imageID string) container.Summary {
	return container.Summary{Names: []string{"/" + name}, Image: imageRef, ImageID: imageID}
}

func TestClassifyImagesTagResolvedContainerIsInUse(t *testing.T) {
	// The container's ImageID is deliberately a DIFFERENT id than the one
	// currently holding the "app:latest" tag -- only a real tag-lookup
	// (not an accidental ID-fallback match) can make this pass.
	imgs := []image.Summary{summaryOf("sha256:new", []string{"app:latest"}, 100)}
	containers := []container.Summary{ctrOf("web", "app:latest", "sha256:stale")}

	report := classifyImages(imgs, containers)

	require.Len(t, report.Images, 1)
	require.Equal(t, "in-use", report.Images[0].State)
	require.Equal(t, []string{"web"}, report.Images[0].Containers)
}

func TestClassifyImagesIDPinnedContainerIsInUseWhenTagDoesNotResolve(t *testing.T) {
	// "app:old" no longer names any current image (the tag moved on),
	// so this container can only be attributed via its ImageID.
	imgs := []image.Summary{summaryOf("sha256:abc", nil, 100)}
	containers := []container.Summary{ctrOf("web", "app:old", "sha256:abc")}

	report := classifyImages(imgs, containers)

	require.Len(t, report.Images, 1)
	require.Equal(t, "in-use", report.Images[0].State)
	require.Equal(t, []string{"web"}, report.Images[0].Containers)
}

func TestClassifyImagesUntaggedImageIsDangling(t *testing.T) {
	imgs := []image.Summary{summaryOf("sha256:abc", nil, 500)}

	report := classifyImages(imgs, nil)

	require.Len(t, report.Images, 1)
	require.Equal(t, "dangling", report.Images[0].State)
	require.Empty(t, report.Images[0].Containers)
	require.Equal(t, 1, report.Summary.Dangling)
	require.Equal(t, int64(500), report.Summary.ReclaimableBytes)
}

// TestClassifyImagesDigestOnlyUnreferencedImageIsUnusedNotDangling pins
// F2: a digest-pinned image (pulled by "repo@sha256:...", so RepoTags is
// empty but RepoDigests isn't) is not garbage just because it has no
// tags -- it must classify the same way a tagged-but-unreferenced image
// does, not as dangling.
func TestClassifyImagesDigestOnlyUnreferencedImageIsUnusedNotDangling(t *testing.T) {
	imgs := []image.Summary{{ID: "sha256:abc", RepoDigests: []string{"redis@sha256:deadbeef"}, Size: 300}}

	report := classifyImages(imgs, nil)

	require.Len(t, report.Images, 1)
	require.Equal(t, "unused", report.Images[0].State)
	require.Equal(t, []string{"redis@sha256:deadbeef"}, report.Images[0].RepoDigests)
	require.Equal(t, 1, report.Summary.Unused)
	require.Zero(t, report.Summary.Dangling)
	require.Equal(t, int64(300), report.Summary.ReclaimableBytes)
}

// TestClassifyImagesDigestOnlyReferencedImageIsInUse pins the other half
// of F2: a digest-pinned image a container actually references is
// in-use, the same as any tagged image -- RepoDigests doesn't change
// the container-join rule at all, only the dangling fallback.
func TestClassifyImagesDigestOnlyReferencedImageIsInUse(t *testing.T) {
	imgs := []image.Summary{{ID: "sha256:abc", RepoDigests: []string{"redis@sha256:deadbeef"}, Size: 300}}
	containers := []container.Summary{ctrOf("cache", "redis@sha256:deadbeef", "sha256:abc")}

	report := classifyImages(imgs, containers)

	require.Equal(t, "in-use", report.Images[0].State)
	require.Equal(t, 1, report.Summary.InUse)
	require.Zero(t, report.Summary.ReclaimableBytes)
}

// TestClassifyImagesTrueDanglingRequiresBothTagsAndDigestsEmpty is the
// converse of the two tests above: an image with NEITHER tags nor
// digests is the only case that's actually dangling.
func TestClassifyImagesTrueDanglingRequiresBothTagsAndDigestsEmpty(t *testing.T) {
	imgs := []image.Summary{{ID: "sha256:abc", Size: 500}}

	report := classifyImages(imgs, nil)

	require.Equal(t, "dangling", report.Images[0].State)
	require.Equal(t, 1, report.Summary.Dangling)
}

func TestClassifyImagesTaggedUnreferencedImageIsUnused(t *testing.T) {
	imgs := []image.Summary{summaryOf("sha256:abc", []string{"orphan:old"}, 300)}

	report := classifyImages(imgs, nil)

	require.Len(t, report.Images, 1)
	require.Equal(t, "unused", report.Images[0].State)
	require.Equal(t, 1, report.Summary.Unused)
	require.Equal(t, int64(300), report.Summary.ReclaimableBytes)
}

func TestClassifyImagesInUseImageExcludedFromReclaimable(t *testing.T) {
	imgs := []image.Summary{summaryOf("sha256:abc", []string{"app:latest"}, 9000)}
	containers := []container.Summary{ctrOf("web", "app:latest", "sha256:abc")}

	report := classifyImages(imgs, containers)

	require.Equal(t, 1, report.Summary.InUse)
	require.Equal(t, int64(0), report.Summary.ReclaimableBytes)
}

func TestClassifyImagesMultipleContainersReferencingSameImageAreSortedTogether(t *testing.T) {
	imgs := []image.Summary{summaryOf("sha256:abc", []string{"nginx:latest"}, 100)}
	containers := []container.Summary{
		ctrOf("web-front", "nginx:latest", "sha256:abc"),
		// A stopped container's ImageID still pins the image just like a
		// running one's -- ContainerList(All:true) reports both the same
		// way, no Status/State field involved in this join at all.
		ctrOf("old-proxy", "nginx:latest", "sha256:abc"),
	}

	report := classifyImages(imgs, containers)

	require.Equal(t, []string{"old-proxy", "web-front"}, report.Images[0].Containers)
}

func TestClassifyImagesSummaryCountsMatchPerImageStates(t *testing.T) {
	imgs := []image.Summary{
		summaryOf("sha256:inuse", []string{"app:latest"}, 100),
		summaryOf("sha256:unused1", []string{"old:v1"}, 200),
		summaryOf("sha256:unused2", []string{"old:v2"}, 300),
		summaryOf("sha256:dangling1", nil, 400),
	}
	containers := []container.Summary{ctrOf("web", "app:latest", "sha256:inuse")}

	report := classifyImages(imgs, containers)

	require.Equal(t, 1, report.Summary.InUse)
	require.Equal(t, 2, report.Summary.Unused)
	require.Equal(t, 1, report.Summary.Dangling)
	require.Equal(t, int64(900), report.Summary.ReclaimableBytes)
}

func TestClassifyImagesContainerWithUnresolvableRefIsIgnored(t *testing.T) {
	// Neither the tag nor the id resolves to any current image (e.g. both
	// were removed between ContainerList and ImageList) -- must not panic
	// and must not mark any image in-use.
	imgs := []image.Summary{summaryOf("sha256:abc", []string{"app:latest"}, 100)}
	containers := []container.Summary{ctrOf("ghost", "gone:vanished", "sha256:alsogone")}

	report := classifyImages(imgs, containers)

	require.Equal(t, "unused", report.Images[0].State)
	require.Empty(t, report.Images[0].Containers)
}

func TestClassifyImagesEmptyInputsReturnEmptyReport(t *testing.T) {
	report := classifyImages(nil, nil)

	require.NotNil(t, report.Images)
	require.Empty(t, report.Images)
	require.Zero(t, report.Summary)
}

func TestRemoveImagesWithOneFailureDoesNotAbortTheRest(t *testing.T) {
	removeOne := func(id string) error {
		if id == "sha256:bad" {
			return fmt.Errorf("conflict: image is being used by a container")
		}
		return nil
	}

	results := removeImagesWith([]string{"sha256:good", "sha256:bad"}, nil, removeOne)

	require.Equal(t, []ImageRemoveResult{
		{ID: "sha256:good", OK: true},
		{ID: "sha256:bad", OK: false, Error: "conflict: image is being used by a container"},
	}, results)
}

func TestRemoveImagesWithEnrichesSuccessFromPreFetchedMap(t *testing.T) {
	pre := map[string]image.Summary{
		"sha256:good": {ID: "sha256:good", RepoTags: []string{"app:old"}, Size: 42},
	}
	removeOne := func(string) error { return nil }

	results := removeImagesWith([]string{"sha256:good"}, pre, removeOne)

	require.Equal(t, []ImageRemoveResult{
		{ID: "sha256:good", OK: true, RepoTags: []string{"app:old"}, SizeBytes: 42},
	}, results)
}

func TestRemoveImagesWithSucceedsWithoutEnrichmentWhenIDMissingFromPre(t *testing.T) {
	removeOne := func(string) error { return nil }

	results := removeImagesWith([]string{"sha256:unknown"}, nil, removeOne)

	require.Equal(t, []ImageRemoveResult{{ID: "sha256:unknown", OK: true}}, results)
}

func TestMergeDanglingPruneUsesDeletedIDAndPreFetchedSize(t *testing.T) {
	report := image.PruneReport{
		ImagesDeleted:  []image.DeleteResponse{{Deleted: "sha256:abc"}},
		SpaceReclaimed: 1234,
	}
	sizeByID := map[string]int64{"sha256:abc": 999}

	result := mergeDanglingPrune(report, sizeByID)

	require.Equal(t, ImagePruneResult{
		Deleted:        []DeletedImage{{ID: "sha256:abc", SizeBytes: 999}},
		ReclaimedBytes: 1234,
	}, result)
}

func TestMergeDanglingPruneFallsBackToUntaggedIDWhenDeletedIsEmpty(t *testing.T) {
	// A moby prune response can report an image solely as "Untagged"
	// (its last tag removed) rather than "Deleted" (the content itself
	// removed) -- either way it's gone from `docker images`, so either
	// field names the id this result is about.
	report := image.PruneReport{ImagesDeleted: []image.DeleteResponse{{Untagged: "sha256:abc"}}}

	result := mergeDanglingPrune(report, nil)

	require.Equal(t, []DeletedImage{{ID: "sha256:abc"}}, result.Deleted)
}

func TestPruneUnusedWithSumsReclaimedBytesAndCollectsPerIDErrors(t *testing.T) {
	unused := []ImageInfo{
		{ID: "sha256:a", RepoTags: []string{"old:1"}, SizeBytes: 100},
		{ID: "sha256:b", RepoTags: []string{"old:2"}, SizeBytes: 200},
	}
	removeOne := func(id string) error {
		if id == "sha256:b" {
			return fmt.Errorf("in use")
		}
		return nil
	}

	result := pruneUnusedWith(unused, removeOne)

	require.Equal(t, []DeletedImage{{ID: "sha256:a", RepoTags: []string{"old:1"}, SizeBytes: 100}}, result.Deleted)
	require.Equal(t, int64(100), result.ReclaimedBytes)
	require.Equal(t, []string{"sha256:b: in use"}, result.Errors)
}

// fakeImagesClient is a hand-rolled imagesClient double -- injected via
// Collector's own imgCli field (see imagesClient's own doc) so
// pruneDangling/RemoveImages' real wrapper calls (not just the pure
// orchestration functions above, already covered without any client at
// all) can be pinned without a daemon.
type fakeImagesClient struct {
	imageListReturn     []image.Summary
	containerListReturn []container.Summary

	imageListFilters   []filters.Args
	imagesPruneFilters []filters.Args
	imageRemoveOptions []image.RemoveOptions

	imagesPruneReturn image.PruneReport
}

func (f *fakeImagesClient) ImageList(_ context.Context, options image.ListOptions) ([]image.Summary, error) {
	f.imageListFilters = append(f.imageListFilters, options.Filters)
	return f.imageListReturn, nil
}

func (f *fakeImagesClient) ContainerList(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
	return f.containerListReturn, nil
}

func (f *fakeImagesClient) ImageRemove(_ context.Context, _ string, options image.RemoveOptions) ([]image.DeleteResponse, error) {
	f.imageRemoveOptions = append(f.imageRemoveOptions, options)
	return nil, nil
}

func (f *fakeImagesClient) ImagesPrune(_ context.Context, pruneFilters filters.Args) (image.PruneReport, error) {
	f.imagesPruneFilters = append(f.imagesPruneFilters, pruneFilters)
	return f.imagesPruneReturn, nil
}

// TestPruneDanglingFiltersBothImageListAndImagesPruneByDanglingTrue pins
// F5: pruneDangling's own doc says it hands the whole operation to the
// daemon's dangling=true filter -- assert that's actually the filter
// going out on BOTH calls it makes (the pre-fetch ImageList, and the
// real ImagesPrune), not just trust the literal never drifts.
func TestPruneDanglingFiltersBothImageListAndImagesPruneByDanglingTrue(t *testing.T) {
	fc := &fakeImagesClient{}
	c := &Collector{imgCli: fc}

	_, err := c.pruneDangling(context.Background())

	require.NoError(t, err)
	require.Len(t, fc.imageListFilters, 1)
	require.Equal(t, []string{"true"}, fc.imageListFilters[0].Get("dangling"))
	require.Len(t, fc.imagesPruneFilters, 1)
	require.Equal(t, []string{"true"}, fc.imagesPruneFilters[0].Get("dangling"))
}

// TestRemoveImagesCallsImageRemoveWithForceFalseAndPruneChildrenTrue pins
// F5's other half: RemoveImages' own doc promises Force:false (never
// forced past an in-use conflict) and PruneChildren:true (matching
// `docker rmi`'s own default) -- assert those are the actual options
// reaching the real wrapper, not just the doc's word for it.
func TestRemoveImagesCallsImageRemoveWithForceFalseAndPruneChildrenTrue(t *testing.T) {
	fc := &fakeImagesClient{}
	c := &Collector{imgCli: fc}

	_, err := c.RemoveImages(context.Background(), []string{"sha256:" + fmt.Sprintf("%064x", 1)})

	require.NoError(t, err)
	require.Equal(t, []image.RemoveOptions{{Force: false, PruneChildren: true}}, fc.imageRemoveOptions)
}
