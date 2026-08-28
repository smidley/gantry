package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"
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
