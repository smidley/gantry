package docker

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/errdefs"
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

// TestRemoveImagesWithMapsMultiTagConflictToAClearerMessage pins N2:
// describeImageRemoveError's mapping used to be wired only into
// pruneImagesWith -- POST /api/images/remove hits the exact same
// permanent multi-tag conflict (removing a 2+-tag image by id) and used
// to get back the daemon's own raw, retry-shaped string instead of the
// same "untag manually" explanation pruneUnused already gave it.
func TestRemoveImagesWithMapsMultiTagConflictToAClearerMessage(t *testing.T) {
	raw := "conflict: unable to delete deadbeefcafe (must be forced) - image is referenced in multiple repositories"
	removeOne := func(string) error { return errdefs.Conflict(errors.New(raw)) }

	results := removeImagesWith([]string{"sha256:multi"}, nil, removeOne)

	require.Len(t, results, 1)
	require.False(t, results[0].OK)
	require.Contains(t, results[0].Error, "skipped: image has multiple tags (untag manually)")
	require.Contains(t, results[0].Error, raw, "the raw daemon string must still be present, not discarded")
}

func TestPruneImagesWithSumsReclaimedBytesAndCollectsPerIDErrors(t *testing.T) {
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

	result := pruneImagesWith(unused, removeOne)

	require.Equal(t, []DeletedImage{{ID: "sha256:a", RepoTags: []string{"old:1"}, SizeBytes: 100}}, result.Deleted)
	require.Equal(t, int64(100), result.ReclaimedBytes)
	require.Equal(t, []string{"sha256:b: in use"}, result.Errors)
}

// TestPruneImagesWithMapsMultiTagConflictToAClearerMessage pins F6: by-id
// removal of a 2+-tag image permanently conflicts ("must be forced" --
// moby only skips that specific soft conflict when removing by id AND
// the image has at most one reference; see imageIDPattern's own doc for
// the same by-id resolution moby does). pruneUnused never sets Force,
// so this isn't transient -- map it to a clearer message, but keep the
// raw daemon string so nothing's lost.
func TestPruneImagesWithMapsMultiTagConflictToAClearerMessage(t *testing.T) {
	raw := "conflict: unable to delete deadbeefcafe (must be forced) - image is referenced in multiple repositories"
	unused := []ImageInfo{{ID: "sha256:multi", RepoTags: []string{"app:v1", "app:v2"}, SizeBytes: 100}}
	removeOne := func(string) error { return errdefs.Conflict(errors.New(raw)) }

	result := pruneImagesWith(unused, removeOne)

	require.Empty(t, result.Deleted)
	require.Len(t, result.Errors, 1)
	require.Contains(t, result.Errors[0], "skipped: image has multiple tags (untag manually)")
	require.Contains(t, result.Errors[0], raw, "the raw daemon string must still be present, not discarded")
}

// TestPruneImagesWithLeavesOtherConflictsAloneAndNonConflictErrorsAlone
// guards the mapping in the test above against over-firing: a
// DIFFERENT conflict (e.g. a container started using the image between
// classification and removal) and a plain non-conflict error must both
// keep their own raw message verbatim, never the multi-tag wording.
func TestPruneImagesWithLeavesOtherConflictsAloneAndNonConflictErrorsAlone(t *testing.T) {
	containerConflict := "conflict: unable to delete deadbeefcafe (cannot be forced) - image is being used by running container abc123"
	unused := []ImageInfo{
		{ID: "sha256:race", RepoTags: []string{"app:v1"}, SizeBytes: 100},
		{ID: "sha256:plain", RepoTags: []string{"app:v2"}, SizeBytes: 200},
	}
	removeOne := func(id string) error {
		if id == "sha256:race" {
			return errdefs.Conflict(errors.New(containerConflict))
		}
		return fmt.Errorf("some other failure")
	}

	result := pruneImagesWith(unused, removeOne)

	require.Equal(t, []string{
		"sha256:race: " + containerConflict,
		"sha256:plain: some other failure",
	}, result.Errors)
}

// fakeImagesClient is a hand-rolled imagesClient double -- injected via
// Collector's own imgCli field (see imagesClient's own doc) so
// pruneDangling/RemoveImages' real wrapper calls (not just the pure
// orchestration functions above, already covered without any client at
// all) can be pinned without a daemon.
type fakeImagesClient struct {
	imageListReturn     []image.Summary
	containerListReturn []container.Summary

	imageRemoveIDs     []string
	imageRemoveOptions []image.RemoveOptions
}

func (f *fakeImagesClient) ImageList(_ context.Context, _ image.ListOptions) ([]image.Summary, error) {
	return f.imageListReturn, nil
}

func (f *fakeImagesClient) ContainerList(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
	return f.containerListReturn, nil
}

func (f *fakeImagesClient) ImageRemove(_ context.Context, imageID string, options image.RemoveOptions) ([]image.DeleteResponse, error) {
	f.imageRemoveIDs = append(f.imageRemoveIDs, imageID)
	f.imageRemoveOptions = append(f.imageRemoveOptions, options)
	return nil, nil
}

// TestPruneDanglingNeverPassesADigestPinnedUnusedImageToImageRemove pins
// N1: a digest-pinned image (RepoDigests set, no RepoTags) classifies
// "unused" under classifyImages, not "dangling" -- see classifyImages'
// own doc. The daemon's own dangling=true filter disagrees about that
// on Unraid's classic image store (prunes anything without a
// NamedTagged ref, tags or no), which is exactly why pruneDangling must
// remove by Gantry's own fresh classification instead of delegating:
// only the true dangling image (neither RepoTags nor RepoDigests) may
// ever reach ImageRemove.
func TestPruneDanglingNeverPassesADigestPinnedUnusedImageToImageRemove(t *testing.T) {
	digestPinned := image.Summary{ID: "sha256:" + fmt.Sprintf("%064x", 1), RepoDigests: []string{"redis@sha256:deadbeef"}, Size: 100}
	trueDangling := image.Summary{ID: "sha256:" + fmt.Sprintf("%064x", 2), Size: 200}
	fc := &fakeImagesClient{imageListReturn: []image.Summary{digestPinned, trueDangling}}
	c := &Collector{imgCli: fc}

	result, err := c.pruneDangling(context.Background())

	require.NoError(t, err)
	require.Equal(t, []string{trueDangling.ID}, fc.imageRemoveIDs, "a digest-pinned image must never reach ImageRemove via prune dangling")
	require.Equal(t, []image.RemoveOptions{{Force: false, PruneChildren: true}}, fc.imageRemoveOptions)
	require.Equal(t, []DeletedImage{{ID: trueDangling.ID, SizeBytes: 200}}, result.Deleted)
	require.Equal(t, int64(200), result.ReclaimedBytes)
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
