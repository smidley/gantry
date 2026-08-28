package fake

import (
	"context"
	"fmt"
	"strings"

	"github.com/smidley/gantry/internal/collect/docker"
)

// fakeImagesBaseCreated is an arbitrary but fixed unix timestamp
// fakeImageSeed's Created values stagger backwards from -- deterministic
// across runs, unlike time.Now(), so a test asserting on it never flakes.
const fakeImagesBaseCreated = 1_735_000_000

// fakeImageID mints a plausible-looking, fixed-per-n sha256 digest
// string -- distinct per seed entry, valid length, no real crypto/rand
// needed for data that's entirely hand-authored anyway.
func fakeImageID(n int64) string { return fmt.Sprintf("sha256:%064x", n) }

// fakeImageSeed is the fake box's dozen-image inventory, mirroring the
// SHAPE (not the exact count) of Scott's own real box's numbers: five
// in-use, three unused (a superseded tag no container references any
// more), four dangling (untagged leftover layers). Deliberately its own
// self-contained dataset rather than derived from the fleet var (this
// package's metrics/container-identity demo data) -- images and
// containers are unrelated concerns here the way GET /api/images and
// GET /api/containers are unrelated endpoints in the real app.
var fakeImageSeed = []docker.ImageInfo{
	{ID: fakeImageID(1), RepoTags: []string{"jellyfin/jellyfin:latest"}, SizeBytes: 950_000_000, Created: fakeImagesBaseCreated - 1*86400, State: "in-use", Containers: []string{"jellyfin"}},
	{ID: fakeImageID(2), RepoTags: []string{"lscr.io/linuxserver/radarr:latest"}, SizeBytes: 420_000_000, Created: fakeImagesBaseCreated - 2*86400, State: "in-use", Containers: []string{"radarr"}},
	{ID: fakeImageID(3), RepoTags: []string{"lscr.io/linuxserver/sonarr:latest"}, SizeBytes: 410_000_000, Created: fakeImagesBaseCreated - 3*86400, State: "in-use", Containers: []string{"sonarr"}},
	{ID: fakeImageID(4), RepoTags: []string{"postgres:16"}, SizeBytes: 380_000_000, Created: fakeImagesBaseCreated - 4*86400, State: "in-use", Containers: []string{"postgres"}},
	{ID: fakeImageID(5), RepoTags: []string{"nginx:1.27"}, SizeBytes: 187_000_000, Created: fakeImagesBaseCreated - 5*86400, State: "in-use", Containers: []string{"nginx", "reverse-proxy"}},

	{ID: fakeImageID(6), RepoTags: []string{"lscr.io/linuxserver/radarr:1.2.3-legacy"}, SizeBytes: 415_000_000, Created: fakeImagesBaseCreated - 40*86400, State: "unused"},
	{ID: fakeImageID(7), RepoTags: []string{"postgres:15"}, SizeBytes: 375_000_000, Created: fakeImagesBaseCreated - 90*86400, State: "unused"},
	{ID: fakeImageID(8), RepoTags: []string{"redis:7-alpine"}, SizeBytes: 42_000_000, Created: fakeImagesBaseCreated - 12*86400, State: "unused"},

	{ID: fakeImageID(9), SizeBytes: 950_000_000, Created: fakeImagesBaseCreated - 6*86400, State: "dangling"},
	{ID: fakeImageID(10), SizeBytes: 410_000_000, Created: fakeImagesBaseCreated - 7*86400, State: "dangling"},
	{ID: fakeImageID(11), SizeBytes: 128_000_000, Created: fakeImagesBaseCreated - 20*86400, State: "dangling"},
	{ID: fakeImageID(12), SizeBytes: 65_000_000, Created: fakeImagesBaseCreated - 21*86400, State: "dangling"},
}

// summarizeImages tallies ImagesSummary from a slice's own already-set
// State fields -- unlike the real docker package's classifyImages, fake
// images carry a hand-authored State from construction, so this only
// ever aggregates, never classifies.
func summarizeImages(images []docker.ImageInfo) docker.ImagesReport {
	out := docker.ImagesReport{Images: images}
	for _, im := range images {
		switch im.State {
		case "in-use":
			out.Summary.InUse++
		case "unused":
			out.Summary.Unused++
			out.Summary.ReclaimableBytes += im.SizeBytes
		case "dangling":
			out.Summary.Dangling++
			out.Summary.ReclaimableBytes += im.SizeBytes
		}
	}
	return out
}

// Images returns the current fake image inventory.
func (g *Generator) Images(_ context.Context) (docker.ImagesReport, error) {
	g.imagesMu.Lock()
	defer g.imagesMu.Unlock()
	return summarizeImages(g.images), nil
}

// RemoveImages deletes each of ids from the fake inventory. A real
// daemon refuses to delete an in-use image on its own; fake mode has no
// daemon to do that for it, so this checks State itself and manufactures
// a docker-shaped conflict error, keeping the "in-use removal refusal"
// contract identical in both modes.
func (g *Generator) RemoveImages(_ context.Context, ids []string) ([]docker.ImageRemoveResult, error) {
	g.imagesMu.Lock()
	defer g.imagesMu.Unlock()

	out := make([]docker.ImageRemoveResult, 0, len(ids))
	for _, id := range ids {
		idx := indexOfFakeImage(g.images, id)
		switch {
		case idx < 0:
			out = append(out, docker.ImageRemoveResult{ID: id, Error: "no such image: " + id})
		case g.images[idx].State == "in-use":
			im := g.images[idx]
			out = append(out, docker.ImageRemoveResult{
				ID: id,
				Error: fmt.Sprintf("conflict: unable to remove repository reference %q (must force) - container %s is using its referenced image %s",
					firstOrID(im), strings.Join(im.Containers, ", "), shortFakeID(id)),
			})
		default:
			im := g.images[idx]
			g.images = append(g.images[:idx], g.images[idx+1:]...)
			out = append(out, docker.ImageRemoveResult{ID: id, OK: true, RepoTags: im.RepoTags, SizeBytes: im.SizeBytes})
		}
	}
	return out, nil
}

// PruneImages deletes every currently-"dangling" (mode "dangling") or
// "unused" (mode "unused") fake image -- the same two modes
// Collector.PruneImages accepts, kept in lockstep so fake mode exercises
// the identical request contract a real daemon would.
func (g *Generator) PruneImages(_ context.Context, mode string) (docker.ImagePruneResult, error) {
	if mode != "dangling" && mode != "unused" {
		return docker.ImagePruneResult{}, fmt.Errorf("unknown prune mode %q", mode)
	}

	g.imagesMu.Lock()
	defer g.imagesMu.Unlock()

	var kept []docker.ImageInfo
	var out docker.ImagePruneResult
	for _, im := range g.images {
		if im.State != mode {
			kept = append(kept, im)
			continue
		}
		out.Deleted = append(out.Deleted, docker.DeletedImage{ID: im.ID, RepoTags: im.RepoTags, SizeBytes: im.SizeBytes})
		out.ReclaimedBytes += im.SizeBytes
	}
	g.images = kept
	return out, nil
}

func indexOfFakeImage(images []docker.ImageInfo, id string) int {
	for i, im := range images {
		if im.ID == id {
			return i
		}
	}
	return -1
}

// firstOrID returns an image's first repo tag, falling back to its own
// id when untagged -- only used to word RemoveImages' synthetic conflict
// error plausibly; a dangling image can never actually reach this path
// (dangling is never "in-use"), but unused-turned-in-use is impossible
// here too since nothing ever changes a fake image's State after
// construction -- this is purely cosmetic wording, never a behavior
// branch.
func firstOrID(im docker.ImageInfo) string {
	if len(im.RepoTags) > 0 {
		return im.RepoTags[0]
	}
	return im.ID
}

func shortFakeID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		id = id[:12]
	}
	return id
}
