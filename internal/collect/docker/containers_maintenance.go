package docker

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/docker/docker/api/types/container"
)

// ContainerMaintenanceInfo is one non-running container's identity and
// lifecycle state -- GET /api/containers/maintenance's per-container data
// (see server.ContainerMaintenanceInfo for the wire DTO main wiring
// adapts this into). State is one of "exited", "created" (never
// started), or "dead" -- classifyContainers' own doc explains why this is
// an exhaustive, hand-picked set rather than "every State that isn't
// running": paused is running-adjacent (a paused container can be
// unpaused straight back into service, unlike a stopped one) and
// deliberately excluded, and a transient state (restarting, removing)
// is excluded the same way for not being a stable, mistake-safe removal
// target.
//
// ExitCode/FinishedAt are only populated for State "exited", and only
// when the Collector's own enrichment inspect succeeds (see
// ContainersMaintenance) -- ContainerList's own Summary type carries
// neither field, unlike everything else here. Both are nil, not a zero
// value, when absent: an exit code of 0 is a real, common value (a clean
// exit), so a plain int/omitempty pairing could never tell "exited
// cleanly" apart from "not populated".
//
// RestartPolicy is populated the same way (exited-only, enrichment-only)
// but as a plain string, not a pointer: unlike ExitCode, there's no real
// value that collides with "absent" -- "" already means "no restart
// policy configured" (docker's own RestartPolicyMode "no", or the type's
// own zero value), so there's nothing a pointer would ever need to
// disambiguate. Exists so the UI can warn before removing an exited
// container a restart policy (always/unless-stopped/on-failure) would
// otherwise bring back on its own -- the same "probably don't mean to"
// warning Managed already gives for a dockerman/compose-owned one.
type ContainerMaintenanceInfo struct {
	ID            string
	Name          string
	Image         string
	State         string
	ExitCode      *int
	Created       int64
	FinishedAt    *int64
	Managed       string
	RestartPolicy string
}

// ContainerMaintenanceSummary is GET /api/containers/maintenance's
// aggregate counts, keyed by the same three ContainerMaintenanceInfo.
// State values.
type ContainerMaintenanceSummary struct {
	Exited  int
	Created int
	Dead    int
}

// ContainerMaintenanceReport is Collector.ContainersMaintenance'/
// fake.Generator.ContainersMaintenance's shared return shape: every
// non-running container plus the aggregate summary over them.
type ContainerMaintenanceReport struct {
	Containers []ContainerMaintenanceInfo
	Summary    ContainerMaintenanceSummary
}

// ContainerRemoveResult is one requested id's outcome from
// Collector.RemoveContainers/fake.Generator.RemoveContainers. Name/Image
// are only populated when OK -- same reasoning as images.
// ImageRemoveResult's own doc: they exist purely so a caller can log a
// name/image-detailed event for the removal, and once a container is
// gone, nothing can inspect it again to recover that detail.
type ContainerRemoveResult struct {
	ID    string
	OK    bool
	Error string
	Name  string
	Image string
}

// DeletedContainer is one container a prune actually deleted -- Name/
// Image ride along for the same event-logging reason ContainerRemove
// Result's own doc gives.
type DeletedContainer struct {
	ID    string
	Name  string
	Image string
}

// ContainerPruneResult is Collector.PruneContainers'/fake.Generator.
// PruneContainers' shared return shape.
type ContainerPruneResult struct {
	Deleted []DeletedContainer
	Errors  []string
}

// dockermanManagedLabel is the label Unraid's dockerman UI sets on every
// container it manages directly (its own templates, not a plain `docker
// run`) -- presence alone is the signal (the value carries no meaning
// gantry uses), and it takes priority over composeProjectLabel (docker.
// go's own const, shared verbatim with metaFromInspect's Meta.
// ComposeProject extraction) below: a container can only ever be
// managed by one system in practice, and dockerman's own claim is the
// one that matters most here, since it's the one the spec calls out as
// "the user probably wants to KEEP".
const dockermanManagedLabel = "net.unraid.docker.managed"

// managedHint reports what -- if anything -- besides a bare `docker run`
// or `docker create` is responsible for a container, so the UI can warn
// before letting a user remove something they probably don't mean to:
// "dockerman" (an Unraid template), a compose project's own name, or ""
// when neither label is present.
func managedHint(labels map[string]string) string {
	if _, ok := labels[dockermanManagedLabel]; ok {
		return "dockerman"
	}
	if proj := labels[composeProjectLabel]; proj != "" {
		return proj
	}
	return ""
}

// isMaintenanceState reports whether state belongs in GET
// /api/containers/maintenance's list -- see ContainerMaintenanceInfo's
// own doc for why this is an explicit, hand-picked set rather than
// simply "state != running".
func isMaintenanceState(state string) bool {
	switch state {
	case container.StateExited, container.StateCreated, container.StateDead:
		return true
	default:
		return false
	}
}

// containerDisplayName returns ct's primary name with docker's own
// leading "/" stripped, falling back to its id when Names is somehow
// empty -- mirrors classifyImages' identical id-fallback convention for
// the same reason: a name is never absent in practice, but nothing here
// should panic or produce an empty string if it ever were.
func containerDisplayName(ct container.Summary) string {
	if len(ct.Names) > 0 {
		return normalizeName(ct.Names[0])
	}
	return ct.ID
}

// classifyContainers filters ContainerList's own output down to
// isMaintenanceState's three states and shapes each into a
// ContainerMaintenanceInfo -- pure, so it's unit-testable without a real
// daemon. ExitCode/FinishedAt are deliberately left nil here: neither
// field is on container.Summary (see ContainerMaintenanceInfo's own
// doc), so enriching them needs a per-container ContainerInspect, which
// only ContainersMaintenance (the Collector method, one layer up) has a
// client to make.
func classifyContainers(containers []container.Summary) ContainerMaintenanceReport {
	out := ContainerMaintenanceReport{Containers: make([]ContainerMaintenanceInfo, 0, len(containers))}
	for _, ct := range containers {
		if !isMaintenanceState(ct.State) {
			continue
		}
		out.Containers = append(out.Containers, ContainerMaintenanceInfo{
			ID:      ct.ID,
			Name:    containerDisplayName(ct),
			Image:   ct.Image,
			State:   ct.State,
			Created: ct.Created,
			Managed: managedHint(ct.Labels),
		})
		switch ct.State {
		case container.StateExited:
			out.Summary.Exited++
		case container.StateCreated:
			out.Summary.Created++
		case container.StateDead:
			out.Summary.Dead++
		}
	}
	sort.Slice(out.Containers, func(i, j int) bool { return out.Containers[i].ID < out.Containers[j].ID })
	return out
}

// removeContainersWith is RemoveContainers' pure orchestration, with the
// actual per-id removal call injected as removeOne (the real
// c.ctrCli.ContainerRemove in production; a fake in tests) -- mirrors
// removeImagesWith's shape exactly: one id's failure doesn't abort the
// rest, and a success is enriched from pre. Unlike images, a container
// removal conflict is passed through VERBATIM (no describeImageRemove
// Error-style rewriting): images have exactly one recurring, permanent
// conflict worth a clearer message (multi-tag, untag manually); a
// running container's own conflict is already self-explanatory and,
// unlike the image case, is never permanent -- stopping the container
// resolves it, so there's nothing to rephrase.
func removeContainersWith(ids []string, pre map[string]container.Summary, removeOne func(id string) error) []ContainerRemoveResult {
	out := make([]ContainerRemoveResult, 0, len(ids))
	for _, id := range ids {
		res := ContainerRemoveResult{ID: id}
		if err := removeOne(id); err != nil {
			res.Error = err.Error()
		} else {
			res.OK = true
			if ct, ok := pre[id]; ok {
				res.Name = containerDisplayName(ct)
				res.Image = ct.Image
			}
		}
		out = append(out, res)
	}
	return out
}

// pruneContainersWith is PruneContainers' shared pure orchestration --
// see removeContainersWith for why the removal call is injected. cts is
// whichever set selectPruneTargets already narrowed the caller's mode
// (and optional age filter) down to; this has no opinion of its own on
// what belongs in that set.
func pruneContainersWith(cts []ContainerMaintenanceInfo, removeOne func(id string) error) ContainerPruneResult {
	var out ContainerPruneResult
	for _, ct := range cts {
		if err := removeOne(ct.ID); err != nil {
			out.Errors = append(out.Errors, ct.ID+": "+err.Error())
			continue
		}
		out.Deleted = append(out.Deleted, DeletedContainer{ID: ct.ID, Name: ct.Name, Image: ct.Image})
	}
	return out
}

// selectPruneTargets narrows cts (already classifyContainers' own
// output) down to one prune call's actual targets: mode "exited" or
// "created" selects exactly that State; "all-stopped" selects both --
// but deliberately never "dead", even though it's broader than either
// single mode. A dead container usually means the daemon itself already
// failed to clean it up (see moby's own State docs), so folding it into
// a broad, one-click convenience sweep would just convert that earlier
// failure into a per-id prune error instead of surfacing it as the
// deliberate, one-off action removing a dead container should be --
// still possible, just via POST .../remove instead.
//
// olderThanHours, when positive, further requires the container's own
// relevant timestamp to be strictly before now-olderThanHours: FinishedAt
// for an exited container (when the state actually turns "stopped", not
// when it was originally created) and Created for a created one (which
// never ran at all, so Created is the only lifecycle timestamp it has).
// 0 means no age filter, matching every other optional-filter default in
// this codebase.
//
// An exited container with no trustworthy FinishedAt (nil -- either the
// Collector's own enrichment inspect never ran/succeeded, or it parsed a
// Go zero-time value; see ContainersMaintenance's own doc for both) is
// SKIPPED whenever an age filter is active, never matched by falling
// back to Created: Created only reflects when the container was
// originally started, which can be arbitrarily older than when it
// actually stopped, so guessing from it risks deleting something that
// only just exited. Age-filterless calls are unaffected -- mode
// selection alone decides membership then, with no timestamp involved.
func selectPruneTargets(cts []ContainerMaintenanceInfo, mode string, olderThanHours int, now time.Time) []ContainerMaintenanceInfo {
	var cutoff time.Time
	if olderThanHours > 0 {
		cutoff = now.Add(-time.Duration(olderThanHours) * time.Hour)
	}

	var out []ContainerMaintenanceInfo
	for _, ct := range cts {
		switch mode {
		case "exited":
			if ct.State != container.StateExited {
				continue
			}
		case "created":
			if ct.State != container.StateCreated {
				continue
			}
		case "all-stopped":
			if ct.State != container.StateExited && ct.State != container.StateCreated {
				continue
			}
		default:
			continue
		}
		if !cutoff.IsZero() {
			ts := ct.Created
			if ct.State == container.StateExited {
				if ct.FinishedAt == nil {
					continue // untrustworthy age -- never delete on a guess
				}
				ts = *ct.FinishedAt
			}
			if !time.Unix(ts, 0).Before(cutoff) {
				continue
			}
		}
		out = append(out, ct)
	}
	return out
}

// containersClient is the narrow slice of *client.Client this file's own
// Collector methods call -- Collector.ctrCli's declared type, mirroring
// imagesClient's own doc/purpose exactly, just for the container-
// maintenance surface instead of images. *client.Client already
// implements this; New sets ctrCli to the same value as cli.
type containersClient interface {
	ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error)
	ContainerInspect(ctx context.Context, containerID string) (container.InspectResponse, error)
	ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error
}

// containersInspectFanOutTimeout bounds ContainersMaintenance's own
// per-exited-container ContainerInspect fan-out to this long IN TOTAL,
// across every inspect the call makes, not per-inspect -- a daemon
// under load, or an exited-container count well past a typical box's
// handful, could otherwise leave GET /api/containers/maintenance
// hanging for as long as N sequential inspects take, with nothing
// capping N. Tests pin the "deadline already blew" path with an
// already-cancelled parent context (which propagates into
// context.WithTimeout's child immediately), never by shrinking this.
const containersInspectFanOutTimeout = 10 * time.Second

// containersInspectFanOutCap bounds the same fan-out by COUNT, on top of
// containersInspectFanOutTimeout's bound by TIME -- an unusually large
// exited-container count could still blow through many inspects well
// inside 10s each, so the two bounds are independent; neither covers
// for the other.
//
// Both degrade the SAME way: an exited container past either bound
// simply keeps its zero-value ExitCode/FinishedAt/RestartPolicy, exactly
// like a single failed inspect already does (see the loop's own
// tolerance below) -- and, combined with selectPruneTargets' own
// nil-FinishedAt skip (see its doc), a container that misses enrichment
// this way is never mistaken for "old enough to prune" by an active age
// filter, only ever left out of an age-filtered sweep entirely.
const containersInspectFanOutCap = 50

// ContainersMaintenance lists every non-running container (ContainerList
// All:true, filtered/shaped by classifyContainers), then enriches each
// "exited" entry's ExitCode/FinishedAt via one ContainerInspect apiece --
// the one pair of fields the list response doesn't carry (see
// ContainerMaintenanceInfo's own doc). created/dead entries are never
// inspected: neither field means anything for a container that never
// ran (created) or that the daemon itself couldn't clean up (dead), so
// spending an API call per container on every poll would be pure waste.
//
// The enrichment fan-out itself is bounded by both
// containersInspectFanOutTimeout (shared across every inspect this call
// makes) and containersInspectFanOutCap (a hard count) -- see their own
// docs for why both exist independently and how they degrade.
//
// A failed inspect (the container vanished between ContainerList and
// ContainerInspect, or any other transient error) is tolerated exactly
// like RemoveImages' own best-effort pre-fetch: that one entry simply
// keeps its zero-value ExitCode/FinishedAt rather than failing the
// whole request over a single race.
func (c *Collector) ContainersMaintenance(ctx context.Context) (ContainerMaintenanceReport, error) {
	if c.ctrCli == nil {
		return ContainerMaintenanceReport{}, fmt.Errorf("docker client: invalid socket path %s", c.sockPath)
	}
	cts, err := c.ctrCli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return ContainerMaintenanceReport{}, fmt.Errorf("list containers: %w", err)
	}

	report := classifyContainers(cts)
	inspectCtx, cancel := context.WithTimeout(ctx, containersInspectFanOutTimeout)
	defer cancel()
	inspected := 0
	for i := range report.Containers {
		if report.Containers[i].State != container.StateExited {
			continue
		}
		if inspected >= containersInspectFanOutCap || inspectCtx.Err() != nil {
			break // beyond either bound -- the rest degrade to unenriched
		}
		inspected++
		insp, err := c.ctrCli.ContainerInspect(inspectCtx, report.Containers[i].ID)
		// ContainerJSONBase is a nil-able embedded pointer (InspectResponse's
		// own definition) -- checked before insp.State, which is a promoted
		// field through it: reading insp.State first would itself panic
		// whenever ContainerJSONBase is nil, the exact "no useful data back"
		// case this is meant to tolerate.
		if err != nil || insp.ContainerJSONBase == nil || insp.State == nil {
			continue
		}
		code := insp.State.ExitCode
		report.Containers[i].ExitCode = &code
		// !t.IsZero() rejects Go's own zero time ("0001-01-01T00:00:00Z"),
		// which dockerd can report and which time.Parse accepts without
		// error -- .Unix() on it is a huge negative number that would
		// otherwise satisfy virtually any older_than_hours filter (see
		// selectPruneTargets' own doc on why an untrustworthy timestamp
		// must never be stored as a real one).
		if t, err := time.Parse(time.RFC3339Nano, insp.State.FinishedAt); err == nil && !t.IsZero() {
			ts := t.Unix()
			report.Containers[i].FinishedAt = &ts
		}
		// IsNone covers both RestartPolicyMode "no" and HostConfig's own
		// zero value ("") -- either way that's "not configured", which
		// RestartPolicy's own doc promises surfaces as "", never the
		// literal string "no".
		if insp.HostConfig != nil && !insp.HostConfig.RestartPolicy.IsNone() {
			report.Containers[i].RestartPolicy = string(insp.HostConfig.RestartPolicy.Name)
		}
	}
	return report, nil
}

// RemoveContainers deletes each of ids (Force:false -- a running
// container's own conflict error passes through per-item, via
// removeContainersWith, never forced past; RemoveVolumes:false always --
// a volume holds actual data no maintenance sweep should ever touch,
// unlike a container's own writable layer).
func (c *Collector) RemoveContainers(ctx context.Context, ids []string) ([]ContainerRemoveResult, error) {
	if c.ctrCli == nil {
		return nil, fmt.Errorf("docker client: invalid socket path %s", c.sockPath)
	}
	// Fetched BEFORE removing anything -- see ContainerRemoveResult's own
	// doc for why: once an id is gone, no second call could recover this.
	pre := map[string]container.Summary{}
	if cts, err := c.ctrCli.ContainerList(ctx, container.ListOptions{All: true}); err == nil {
		for _, ct := range cts {
			pre[ct.ID] = ct
		}
	}
	return removeContainersWith(ids, pre, func(id string) error {
		return c.ctrCli.ContainerRemove(ctx, id, container.RemoveOptions{Force: false, RemoveVolumes: false})
	}), nil
}

// PruneContainers deletes every container selectPruneTargets currently
// selects for mode ("exited", "created", or "all-stopped") and
// olderThanHours (0 = no age filter) -- any other mode is a caller bug,
// not a runtime condition (the HTTP handler already whitelists these
// three values before ever reaching here).
//
// Deliberately re-classifies from a fresh ContainerList on every call
// (via ContainersMaintenance) and removes one container at a time,
// rather than ever calling the daemon's own ContainersPrune: that API
// has its own until-filter/status semantics for "what counts as
// prunable", which is exactly the same one-source-of-truth problem
// images.go's pruneDangling/pruneUnused already ran into (see
// pruneDangling's own doc) -- acting on Gantry's own classification,
// never the daemon's, is what keeps "what's prunable" from having two
// disagreeing answers.
func (c *Collector) PruneContainers(ctx context.Context, mode string, olderThanHours int) (ContainerPruneResult, error) {
	if mode != "exited" && mode != "created" && mode != "all-stopped" {
		return ContainerPruneResult{}, fmt.Errorf("unknown prune mode %q", mode)
	}
	report, err := c.ContainersMaintenance(ctx)
	if err != nil {
		return ContainerPruneResult{}, err
	}
	targets := selectPruneTargets(report.Containers, mode, olderThanHours, time.Now())
	return pruneContainersWith(targets, func(id string) error {
		return c.ctrCli.ContainerRemove(ctx, id, container.RemoveOptions{Force: false, RemoveVolumes: false})
	}), nil
}
