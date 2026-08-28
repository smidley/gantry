package docker

import (
	"sort"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
)

// ImageInfo is one image's identity, size, and usage classification --
// GET /api/images' per-image data (see server.ImageInfo for the wire DTO
// main wiring adapts this into). State is one of "in-use" (Containers
// lists every referencing container, running or stopped), "unused"
// (tagged, but no container currently references it), or "dangling" (no
// RepoTags at all) -- see classifyImages' own doc for the join rule that
// assigns these.
type ImageInfo struct {
	ID         string
	RepoTags   []string
	SizeBytes  int64
	Created    int64
	State      string
	Containers []string
}

// ImagesSummary is GET /api/images' aggregate counts, keyed by the same
// three ImageInfo.State values, plus a reclaimable-bytes total over just
// the unused+dangling images (in-use images are never reclaimable). It's
// an upper bound, not a guarantee -- docker's layers are shared across
// images, so deleting one unused/dangling image doesn't necessarily free
// its whole SizeBytes if another image still holds some of the same
// layers.
type ImagesSummary struct {
	InUse            int
	Unused           int
	Dangling         int
	ReclaimableBytes int64
}

// ImagesReport is Collector.Images'/fake.Generator.Images' shared return
// shape: every image plus the aggregate summary over them.
type ImagesReport struct {
	Images  []ImageInfo
	Summary ImagesSummary
}

// classifyImages joins every image against every container to decide
// each image's usage state. The join is by id, not name/tag directly: a
// container's Image field records whatever ref (tag or id) it was
// created with, which classifyImages first tries to resolve through the
// CURRENT tag->id mapping (a container referencing tag X pins whichever
// image holds tag X right now, even if that's not the image X pointed to
// when the container was created -- this deliberately matches how a
// stale tag reads today, not a historical snapshot); if that ref isn't a
// tag any current image holds, the container's own ImageID is used
// instead (covers a container created by bare id, or one whose tag has
// since moved or been removed entirely). A ref that resolves to neither
// is simply not attributed to any image -- not an error.
//
// An image with any attributing container is "in-use". Otherwise it's
// "dangling" if it has no RepoTags at all, or "unused" if it does.
// ReclaimableBytes sums every unused+dangling image's size (see
// ImagesSummary's own doc on why that's an upper bound).
func classifyImages(imgs []image.Summary, containers []container.Summary) ImagesReport {
	byID := make(map[string]image.Summary, len(imgs))
	tagToID := make(map[string]string)
	for _, im := range imgs {
		byID[im.ID] = im
		for _, tag := range im.RepoTags {
			tagToID[tag] = im.ID
		}
	}

	referencedBy := map[string][]string{}
	for _, ct := range containers {
		id, ok := tagToID[ct.Image]
		if !ok {
			id = ct.ImageID
		}
		if _, known := byID[id]; !known {
			continue
		}
		name := ct.ID
		if len(ct.Names) > 0 {
			name = normalizeName(ct.Names[0])
		}
		referencedBy[id] = append(referencedBy[id], name)
	}
	for id := range referencedBy {
		sort.Strings(referencedBy[id])
	}

	out := ImagesReport{Images: make([]ImageInfo, 0, len(imgs))}
	for _, im := range imgs {
		info := ImageInfo{ID: im.ID, RepoTags: im.RepoTags, SizeBytes: im.Size, Created: im.Created}
		switch {
		case len(referencedBy[im.ID]) > 0:
			info.State = "in-use"
			info.Containers = referencedBy[im.ID]
			out.Summary.InUse++
		case len(im.RepoTags) == 0:
			info.State = "dangling"
			out.Summary.Dangling++
			out.Summary.ReclaimableBytes += im.Size
		default:
			info.State = "unused"
			out.Summary.Unused++
			out.Summary.ReclaimableBytes += im.Size
		}
		out.Images = append(out.Images, info)
	}
	sort.Slice(out.Images, func(i, j int) bool { return out.Images[i].ID < out.Images[j].ID })
	return out
}
