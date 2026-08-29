package fake

import (
	"context"
	"fmt"

	"github.com/smidley/gantry/internal/collect/docker"
)

// fakeContainersBaseCreated anchors fakeContainerMaintenanceSeed's
// timestamps the same way fakeImagesBaseCreated anchors the image
// seed's -- a fixed, deterministic epoch rather than time.Now(), so a
// test asserting on it never flakes. Given its own name (rather than
// reusing fakeImagesBaseCreated directly) so a reader never has to
// wonder why a container's age would depend on "images" anything --
// both seeds just happen to share the same reference instant.
const fakeContainersBaseCreated = 1_735_000_000

// fakeContainerID mints a plausible-looking, fixed-per-n 64-hex
// container id -- mirrors fakeImageID's own reasoning (n in the FIRST
// two hex digits, so every seed entry's 12-char short id, see server.
// shortContainerID, is already distinct), but with no "sha256:" prefix
// at all: a container id is never content-addressed (see
// server.containerIDPattern's own doc), so there's no alternate form to
// mimic here.
func fakeContainerID(n int64) string { return fmt.Sprintf("%02x%062x", n, 0) }

// fakeContainerMaintenanceSeed is the fake box's own non-running
// container inventory, mirroring the SHAPE of Scott's real box (7
// exited + 17 created, zero dead -- see
// TestFakeContainerMaintenanceSeedNeverIncludesDead) rather than its
// exact count, the same "shape not count" convention
// fakeImageSeed documents. duplicati/watchtower/prowlarr/vaultwarden
// are exited (varied exit codes: a clean 0, a crash, and an OOM-kill
// convention 137, so the UI has more than one exit-code shape to
// render), with a managed-hint spread across all three cases
// (dockerman, a compose project, and plain unmanaged) so every branch
// of the "should the UI warn before removing this" hint has a fixture.
// The two github-runner entries are "created" (never started) with
// deliberately different ages, modeling Scott's own real box's
// ephemeral GitHub-runner spawns and giving
// older_than_hours something to actually filter between in fake mode
// (see PruneContainers' own doc on why "now" is this fixed epoch, not
// wall-clock time, for exactly that reason).
var fakeContainerMaintenanceSeed = []docker.ContainerMaintenanceInfo{
	{
		ID: fakeContainerID(1), Name: "duplicati", Image: "lscr.io/linuxserver/duplicati:latest",
		State: "exited", ExitCode: ptrTo(0), Created: fakeContainersBaseCreated - 10*86400,
		FinishedAt: ptrTo(int64(fakeContainersBaseCreated - 2*3600)), Managed: "dockerman",
	},
	{
		ID: fakeContainerID(2), Name: "watchtower", Image: "containrrr/watchtower:latest",
		State: "exited", ExitCode: ptrTo(0), Created: fakeContainersBaseCreated - 30*86400,
		FinishedAt: ptrTo(int64(fakeContainersBaseCreated - 6*3600)),
	},
	{
		ID: fakeContainerID(3), Name: "prowlarr", Image: "lscr.io/linuxserver/prowlarr:latest",
		State: "exited", ExitCode: ptrTo(1), Created: fakeContainersBaseCreated - 5*86400,
		FinishedAt: ptrTo(int64(fakeContainersBaseCreated - 86400)), Managed: "media",
	},
	{
		ID: fakeContainerID(4), Name: "vaultwarden", Image: "vaultwarden/server:latest",
		State: "exited", ExitCode: ptrTo(137), Created: fakeContainersBaseCreated - 15*86400,
		FinishedAt: ptrTo(int64(fakeContainersBaseCreated - 4*3600)), Managed: "dockerman",
	},

	{
		ID: fakeContainerID(5), Name: "github-runner-a1c9f2", Image: "ghcr.io/actions/actions-runner:latest",
		State: "created", Created: fakeContainersBaseCreated - 2*3600,
	},
	{
		ID: fakeContainerID(6), Name: "github-runner-77bd0e", Image: "ghcr.io/actions/actions-runner:latest",
		State: "created", Created: fakeContainersBaseCreated - 20*60,
	},
}

// ptrTo returns a pointer to a copy of v -- shared by every
// ExitCode/FinishedAt seed literal above, both of which are pointer
// fields specifically so a real (non-nil) zero value stays
// distinguishable from "not populated" (see docker.
// ContainerMaintenanceInfo's own doc).
func ptrTo[T any](v T) *T { return &v }

// summarizeContainersMaintenance tallies ContainerMaintenanceSummary
// from a slice's own already-set State fields -- unlike the real docker
// package's classifyContainers, fake containers carry a hand-authored
// State from construction, so this only ever aggregates, never
// classifies. Mirrors summarizeImages exactly.
func summarizeContainersMaintenance(containers []docker.ContainerMaintenanceInfo) docker.ContainerMaintenanceReport {
	out := docker.ContainerMaintenanceReport{Containers: containers}
	for _, ct := range containers {
		switch ct.State {
		case "exited":
			out.Summary.Exited++
		case "created":
			out.Summary.Created++
		case "dead":
			out.Summary.Dead++
		}
	}
	return out
}

// ContainersMaintenance returns the current fake non-running-container
// inventory. Summarizes a COPY of g.containers, not the slice itself --
// mirrors Images' own doc exactly: RemoveContainers/PruneContainers
// mutate that same backing array in place, so a caller iterating what
// an earlier call handed back would otherwise be reading memory a
// concurrent mutation is rewriting out from under it, after this call
// has already unlocked.
func (g *Generator) ContainersMaintenance(_ context.Context) (docker.ContainerMaintenanceReport, error) {
	g.containersMu.Lock()
	defer g.containersMu.Unlock()
	return summarizeContainersMaintenance(append([]docker.ContainerMaintenanceInfo(nil), g.containers...)), nil
}

// indexOfFakeContainer finds id in containers by an exact full-id match
// only -- unlike indexOfFakeImage, there's no short-prefix fallback:
// a container id has no alternate bare-vs-prefixed textual form to
// reconcile the way an image digest does, and server.containerIDPattern
// never lets anything but the exact full id reach a real
// RemoveContainers call in the first place, so that extra resolution
// logic would be pure dead code here.
func indexOfFakeContainer(containers []docker.ContainerMaintenanceInfo, id string) int {
	for i, ct := range containers {
		if ct.ID == id {
			return i
		}
	}
	return -1
}

// fakeRunningConflictContainerID names watchtower's own seed id (see
// fakeContainerMaintenanceSeed) -- singled out so RemoveContainers can
// manufacture a running-container conflict for it, mirroring
// RemoveImages' own in-use conflict (see its own doc) so fake mode's UI
// has SOME id that always exercises the removal-refusal path, not just
// the successful one.
//
// watchtower is a fitting pick precisely because its real job is
// restarting containers: the scenario this simulates is a container
// that genuinely was "exited" at GET-list time racing back to running
// (its own restart policy, or a human) by the time a remove call
// actually reaches it -- a real possibility docker.RemoveContainers' own
// conflict passthrough exists to handle (see its own doc), unlike
// RemoveImages' conflict, which reflects a real, persistent State this
// package already tracks. Never actually removed: State stays "exited"
// and visible in every GET/prune sweep exactly as before, the same way
// a real container racing back to running would still show up in the
// NEXT list poll.
var fakeRunningConflictContainerID = fakeContainerID(2)

// fakeRunningConflictError is the manufactured conflict
// fakeRunningConflictContainerID's own removal attempts always get --
// worded like a real docker daemon conflict (see docker package's own
// TestRemoveContainersWithPassesRunningConflictErrorThroughVerbatim for
// the shape a real one takes), never images' rewritten "skipped: ..."
// style: a running-container conflict has no permanent-conflict
// rewrite the way images' multi-tag one does (see the container
// maintenance carry-ins bullet).
const fakeRunningConflictError = `conflict: cannot remove container "watchtower": container is running: stop the container before removing or force remove`

// RemoveContainers deletes each of ids from the fake inventory. Unlike
// RemoveImages, there's no persistent "in-use" State to refuse against
// here: this inventory only ever holds non-running containers by
// construction (ContainersMaintenance's own contract). Fake mode
// instead simulates the OTHER real conflict source docker.
// RemoveContainers' own conflict passthrough exists for -- a container
// racing back to running between list and remove -- via one designated
// id (see fakeRunningConflictContainerID's own doc); every other id
// either matches a stopped entry (removed) or it doesn't (error).
func (g *Generator) RemoveContainers(_ context.Context, ids []string) ([]docker.ContainerRemoveResult, error) {
	g.containersMu.Lock()
	defer g.containersMu.Unlock()

	out := make([]docker.ContainerRemoveResult, 0, len(ids))
	for _, id := range ids {
		if id == fakeRunningConflictContainerID {
			out = append(out, docker.ContainerRemoveResult{ID: id, Error: fakeRunningConflictError})
			continue
		}
		idx := indexOfFakeContainer(g.containers, id)
		if idx < 0 {
			out = append(out, docker.ContainerRemoveResult{ID: id, Error: "no such container: " + id})
			continue
		}
		ct := g.containers[idx]
		g.containers = append(g.containers[:idx], g.containers[idx+1:]...)
		out = append(out, docker.ContainerRemoveResult{ID: id, OK: true, Name: ct.Name, Image: ct.Image})
	}
	return out, nil
}

// PruneContainers deletes every currently-matching fake container for
// mode ("exited", "created", or "all-stopped", excluding dead the same
// way docker.PruneContainers' own selectPruneTargets does -- see its
// doc), further filtered by olderThanHours when positive.
//
// now is fakeContainersBaseCreated, not time.Now(): every seed entry's
// Created/FinishedAt is authored as a fixed offset BEFORE that epoch
// (see fakeContainerMaintenanceSeed's own doc), not relative to
// wall-clock time, so an age filter has to measure against that same
// fixed reference point to mean anything here -- against a real
// time.Now(), every seed entry would already read as years old and
// older_than_hours could never distinguish between them.
func (g *Generator) PruneContainers(_ context.Context, mode string, olderThanHours int) (docker.ContainerPruneResult, error) {
	if mode != "exited" && mode != "created" && mode != "all-stopped" {
		return docker.ContainerPruneResult{}, fmt.Errorf("unknown prune mode %q", mode)
	}

	g.containersMu.Lock()
	defer g.containersMu.Unlock()

	now := fakeContainersBaseCreated
	cutoff := int64(0)
	hasCutoff := olderThanHours > 0
	if hasCutoff {
		cutoff = int64(now) - int64(olderThanHours)*3600
	}

	var kept []docker.ContainerMaintenanceInfo
	var out docker.ContainerPruneResult
	for _, ct := range g.containers {
		matches := ct.State == mode || (mode == "all-stopped" && (ct.State == "exited" || ct.State == "created"))
		if !matches {
			kept = append(kept, ct)
			continue
		}
		age := ct.Created
		if ct.FinishedAt != nil {
			age = *ct.FinishedAt
		}
		if hasCutoff && age >= cutoff {
			kept = append(kept, ct)
			continue
		}
		out.Deleted = append(out.Deleted, docker.DeletedContainer{ID: ct.ID, Name: ct.Name, Image: ct.Image})
	}
	g.containers = kept
	return out, nil
}
