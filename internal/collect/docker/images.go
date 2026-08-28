package docker

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/errdefs"
)

// ImageInfo is one image's identity, size, and usage classification --
// GET /api/images' per-image data (see server.ImageInfo for the wire DTO
// main wiring adapts this into). State is one of "in-use" (Containers
// lists every referencing container, running or stopped), "unused"
// (tagged, or digest-pinned, but no container currently references it),
// or "dangling" (neither RepoTags nor RepoDigests -- truly nameless) --
// see classifyImages' own doc for the join rule that assigns these.
type ImageInfo struct {
	ID          string
	RepoTags    []string
	RepoDigests []string
	SizeBytes   int64
	Created     int64
	State       string
	Containers  []string
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

// ImageRemoveResult is one requested id's outcome from
// Collector.RemoveImages/fake.Generator.RemoveImages. RepoTags/SizeBytes
// are only populated when OK -- they exist purely so a caller can log a
// tag/size-detailed event for the removal: once an image is gone,
// nothing can inspect it again to recover that detail.
type ImageRemoveResult struct {
	ID        string
	OK        bool
	Error     string
	RepoTags  []string
	SizeBytes int64
}

// DeletedImage is one image a prune actually deleted -- RepoTags/
// SizeBytes ride along for the same event-logging reason
// ImageRemoveResult's own doc gives.
type DeletedImage struct {
	ID        string
	RepoTags  []string
	SizeBytes int64
}

// ImagePruneResult is Collector.PruneImages'/fake.Generator.PruneImages'
// shared return shape.
type ImagePruneResult struct {
	Deleted        []DeletedImage
	ReclaimedBytes int64
	Errors         []string
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
// "dangling" only if it has neither RepoTags nor RepoDigests -- a
// digest-pinned image (pulled by "repo@sha256:...", so RepoTags is
// empty) is not garbage just because it was never tagged, and must
// classify "unused" like any other named-but-unreferenced image, not
// "dangling" (which the daemon's own dangling=true filter, used by
// pruneDangling, would never delete either). ReclaimableBytes sums every
// unused+dangling image's size (see ImagesSummary's own doc on why
// that's an upper bound).
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
		info := ImageInfo{ID: im.ID, RepoTags: im.RepoTags, RepoDigests: im.RepoDigests, SizeBytes: im.Size, Created: im.Created}
		switch {
		case len(referencedBy[im.ID]) > 0:
			info.State = "in-use"
			info.Containers = referencedBy[im.ID]
			out.Summary.InUse++
		case len(im.RepoTags) == 0 && len(im.RepoDigests) == 0:
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

// removeImagesWith is RemoveImages' pure orchestration, with the actual
// per-id removal call injected as removeOne (the real
// c.cli.ImageRemove in production; a fake in tests) so the interesting
// behavior -- one id's failure doesn't abort the rest, and a success is
// enriched from pre -- is fully unit-testable without a daemon. pre is
// best-effort tag/size enrichment (see ImageRemoveResult's own doc for
// why): a missing entry still succeeds, just without RepoTags/SizeBytes.
func removeImagesWith(ids []string, pre map[string]image.Summary, removeOne func(id string) error) []ImageRemoveResult {
	out := make([]ImageRemoveResult, 0, len(ids))
	for _, id := range ids {
		res := ImageRemoveResult{ID: id}
		if err := removeOne(id); err != nil {
			res.Error = err.Error()
		} else {
			res.OK = true
			if im, ok := pre[id]; ok {
				res.RepoTags = im.RepoTags
				res.SizeBytes = im.Size
			}
		}
		out = append(out, res)
	}
	return out
}

// mergeDanglingPrune builds pruneDangling's result from the daemon's own
// PruneReport plus a same-filter pre-fetch's id->size map (fetched
// before pruning, for the same reason removeImagesWith's pre is). A
// moby prune response names a removed image via Deleted (the content
// itself removed) or, if only its last tag was removed, Untagged --
// either way it's the id this entry is about.
func mergeDanglingPrune(report image.PruneReport, sizeByID map[string]int64) ImagePruneResult {
	out := ImagePruneResult{ReclaimedBytes: int64(report.SpaceReclaimed)}
	for _, d := range report.ImagesDeleted {
		id := d.Deleted
		if id == "" {
			id = d.Untagged
		}
		if id == "" {
			continue
		}
		out.Deleted = append(out.Deleted, DeletedImage{ID: id, SizeBytes: sizeByID[id]})
	}
	return out
}

// multiTagConflictText is the one piece of a moby by-id removal
// conflict's message that's unique to the "2+ tags" case (see
// describePruneUnusedError's own doc) -- verbatim from moby's own
// imageDeleteConflict message, daemon/images/image_delete.go.
const multiTagConflictText = "image is referenced in multiple repositories"

// describePruneUnusedError maps removeOne's error to pruneUnusedWith's
// per-id message, for the one conflict that's actually permanent here:
// removing a 2+-tag image by id -- which is all pruneUnused ever does,
// see PruneImages' own doc -- conflicts unless Force is set (never is,
// same as RemoveImages), and retrying changes nothing. errdefs.IsConflict
// alone can't tell this apart from an unrelated conflict (e.g. a
// container started using the image between classification and
// removal, same HTTP 409 either way) so this also checks for the one
// message substring unique to the multi-tag case; anything else keeps
// its own raw message untouched.
func describePruneUnusedError(err error) string {
	if errdefs.IsConflict(err) && strings.Contains(err.Error(), multiTagConflictText) {
		return "skipped: image has multiple tags (untag manually) (" + err.Error() + ")"
	}
	return err.Error()
}

// pruneUnusedWith is pruneUnused's pure orchestration -- see
// removeImagesWith for why the removal call is injected rather than
// called directly.
func pruneUnusedWith(unused []ImageInfo, removeOne func(id string) error) ImagePruneResult {
	var out ImagePruneResult
	for _, im := range unused {
		if err := removeOne(im.ID); err != nil {
			out.Errors = append(out.Errors, im.ID+": "+describePruneUnusedError(err))
			continue
		}
		out.Deleted = append(out.Deleted, DeletedImage{ID: im.ID, RepoTags: im.RepoTags, SizeBytes: im.SizeBytes})
		out.ReclaimedBytes += im.SizeBytes
	}
	return out
}

// imagesClient is the narrow slice of *client.Client this file's own
// Collector methods call -- Collector.imgCli's declared type, so tests
// can inject a fake there and pin exactly what RemoveImages/pruneDangling/
// pruneUnused send the daemon (filter args, remove options) without a
// real one. *client.Client already implements this; New sets imgCli to
// the same value as cli.
type imagesClient interface {
	ImageList(ctx context.Context, options image.ListOptions) ([]image.Summary, error)
	ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error)
	ImageRemove(ctx context.Context, imageID string, options image.RemoveOptions) ([]image.DeleteResponse, error)
	ImagesPrune(ctx context.Context, pruneFilters filters.Args) (image.PruneReport, error)
}

// Images lists every image on the daemon (ImageList All:true) joined
// against every container (ContainerList All:true) -- see
// classifyImages for the join/classification rule itself, kept as a
// pure function so it's unit-testable without a real daemon.
func (c *Collector) Images(ctx context.Context) (ImagesReport, error) {
	if c.imgCli == nil {
		return ImagesReport{}, fmt.Errorf("docker client: invalid socket path %s", c.sockPath)
	}
	imgs, err := c.imgCli.ImageList(ctx, image.ListOptions{All: true})
	if err != nil {
		return ImagesReport{}, fmt.Errorf("list images: %w", err)
	}
	cts, err := c.imgCli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return ImagesReport{}, fmt.Errorf("list containers: %w", err)
	}
	return classifyImages(imgs, cts), nil
}

// RemoveImages deletes each of ids (Force:false -- an in-use image's own
// conflict error passes through per-item, via removeImagesWith, never
// forced past). PruneChildren:true also removes any now-unreferenced
// parent layers, the same default `docker rmi` uses.
func (c *Collector) RemoveImages(ctx context.Context, ids []string) ([]ImageRemoveResult, error) {
	if c.imgCli == nil {
		return nil, fmt.Errorf("docker client: invalid socket path %s", c.sockPath)
	}
	// Fetched BEFORE removing anything -- see ImageRemoveResult's own doc
	// for why: once an id is gone, no second call could recover this.
	pre := map[string]image.Summary{}
	if imgs, err := c.imgCli.ImageList(ctx, image.ListOptions{All: true}); err == nil {
		for _, im := range imgs {
			pre[im.ID] = im
		}
	}
	return removeImagesWith(ids, pre, func(id string) error {
		_, err := c.imgCli.ImageRemove(ctx, id, image.RemoveOptions{Force: false, PruneChildren: true})
		return err
	}), nil
}

// PruneImages deletes either every dangling image (mode "dangling") or
// every image this same package's own classifyImages currently calls
// "unused" (mode "unused") -- any other mode is a caller bug, not a
// runtime condition (the HTTP handler already whitelists these two
// values before ever reaching here).
func (c *Collector) PruneImages(ctx context.Context, mode string) (ImagePruneResult, error) {
	switch mode {
	case "dangling":
		return c.pruneDangling(ctx)
	case "unused":
		return c.pruneUnused(ctx)
	default:
		return ImagePruneResult{}, fmt.Errorf("unknown prune mode %q", mode)
	}
}

// pruneDangling delegates to the daemon's own ImagesPrune(dangling=true)
// rather than looping ImageRemove -- unlike "unused" (this package's own
// container-join concept), "dangling" (no RepoTags at all) is an
// unambiguous, daemon-agreed definition, so handing the whole operation
// to the daemon is safe and lets it be atomic.
func (c *Collector) pruneDangling(ctx context.Context) (ImagePruneResult, error) {
	danglingFilter := filters.NewArgs(filters.Arg("dangling", "true"))
	pre, _ := c.imgCli.ImageList(ctx, image.ListOptions{Filters: danglingFilter}) // best-effort size enrichment; see mergeDanglingPrune's own doc
	sizeByID := make(map[string]int64, len(pre))
	for _, im := range pre {
		sizeByID[im.ID] = im.Size
	}

	report, err := c.imgCli.ImagesPrune(ctx, danglingFilter)
	if err != nil {
		return ImagePruneResult{}, fmt.Errorf("prune dangling images: %w", err)
	}
	return mergeDanglingPrune(report, sizeByID), nil
}

// pruneUnused deletes this package's own computed "unused" set via
// per-image ImageRemove -- deliberately NOT ImagesPrune with
// dangling=false (docker's own `-a`/all-unused prune), which has subtly
// different semantics than classifyImages' container-join rule. Running
// both would give two different answers to "what's unused"; this keeps
// one source of truth.
func (c *Collector) pruneUnused(ctx context.Context) (ImagePruneResult, error) {
	report, err := c.Images(ctx)
	if err != nil {
		return ImagePruneResult{}, err
	}
	var unused []ImageInfo
	for _, im := range report.Images {
		if im.State == "unused" {
			unused = append(unused, im)
		}
	}
	return pruneUnusedWith(unused, func(id string) error {
		_, err := c.imgCli.ImageRemove(ctx, id, image.RemoveOptions{Force: false, PruneChildren: true})
		return err
	}), nil
}
