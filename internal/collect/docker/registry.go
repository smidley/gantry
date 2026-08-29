package docker

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/events"
	"github.com/smidley/gantry/internal/store"
)

// Meta is a snapshot of one container's inventory and health state,
// refreshed on the collector's 10s poll. It's the id -> data every other
// collector (cgroup/API stats, per-container net, GPU attribution)
// consumes via Lookup/Running. Metas are immutable once published --
// never mutate a field in place, build a fresh Meta instead.
//
// Alloc carries the HostConfig resource ceiling (allocFromHostConfig) --
// the API-fallback path's primary source of allocation data (of the
// ceilings, only PidsStats.Limit rides along in the stats response
// itself). Like the rest of Meta, it's only as fresh as the last 10s
// inventory poll, unlike the cgroup v2 fast path's own allocation read
// (cgroupv2.go), which is fresh every 2s tick.
type Meta struct {
	ID, Name, Image, Icon, State, Health string
	// ComposeProject is the com.docker.compose.project label docker
	// compose sets on every container it creates, naming the stack it
	// belongs to -- "" for a container not created via compose. Extracted
	// in metaFromInspect exactly like Icon (a label read, nil-Labels-safe,
	// no separate absence check).
	ComposeProject string
	Pid            int
	StartedAt      time.Time
	HostNet        bool
	RestartCount   int
	Alloc          alloc
	Mounts         []MountInfo
}

// MountInfo is one container mount, as reported by docker inspect's
// Mounts -- bind and volume types only (see mountsFromInspect); tmpfs/
// npipe/cluster/image mounts carry no meaningful host storage path and
// are dropped before they ever reach a Meta. Source is the host-side
// path backing the mount: for a volume mount this is already docker's
// real on-disk location under /var/lib/docker/volumes/, not the
// volume's name, which is what the storage-panel path->storage resolver
// (internal/collect/unraid) needs to map this mount onto an Unraid
// storage system.
type MountInfo struct {
	Source, Destination string
	RW                  bool
}

// EventSink is the narrow slice of store.Store the docker collector needs
// to append translated container lifecycle events.
type EventSink interface {
	AppendEvent(store.Event) (int64, error)
}

// registry holds the id->Meta inventory plus the diff/translate logic
// shared by the 10s inventory poll and the live event stream. Mutex-
// guarded: the poll goroutine (via Tick) and the event-stream goroutine
// both mutate it concurrently.
type registry struct {
	mu   sync.Mutex
	byID map[string]Meta
}

func newRegistry() *registry {
	return &registry{byID: make(map[string]Meta)}
}

func (r *registry) lookup(id string) (Meta, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.byID[id]
	return m, ok
}

// lookupByName scans the registry for a Meta with the given name,
// regardless of state -- a merely-exited (not removed) container is
// still "known" here, and stops being known the moment applyInventory or
// applyEvent removes it. Task 4's snapshot filter uses this to tell a
// briefly-stale-but-real container apart from one that's been fully
// removed. Linear scan: registry sizes are a handful of containers, and
// this runs once per snapshot build, not once per tick.
func (r *registry) lookupByName(name string) (Meta, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lookupByNameLocked(name)
}

// lookupByNameLocked is lookupByName's body, for callers that already
// hold r.mu -- applyEvent's removal branch, which must check whether a
// destroyed id's name still lives on under a different, newer id before
// evicting a name that name-keyed eviction would otherwise treat as
// "this name is gone".
func (r *registry) lookupByNameLocked(name string) (Meta, bool) {
	for _, m := range r.byID {
		if m.Name == name {
			return m, true
		}
	}
	return Meta{}, false
}

// running returns a name-sorted snapshot of every Meta currently in state
// "running".
func (r *registry) running() []Meta {
	r.mu.Lock()
	out := make([]Meta, 0, len(r.byID))
	for _, m := range r.byID {
		if m.State == "running" {
			out = append(out, m)
		}
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// all returns a name-sorted snapshot of every Meta the registry currently
// knows about, running or not -- a stopped-but-not-yet-removed container
// stays here (see applyInventory/applyEvent's own removal docs) until it's
// actually gone. Used by the snapshot frame (Task: stopped containers)
// so a container the fleet turned off on purpose still shows up with its
// real state/identity, not just while running.
func (r *registry) all() []Meta {
	r.mu.Lock()
	out := make([]Meta, 0, len(r.byID))
	for _, m := range r.byID {
		out = append(out, m)
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// normalizeName strips docker's leading "/" from a container name.
func normalizeName(name string) string {
	return strings.TrimPrefix(name, "/")
}

// applyInventory replaces the registry's contents with a fresh snapshot,
// diffing against the previous contents to emit "belt" events for
// state/health/restart-count changes the event stream might have missed
// (a duplicate emission alongside a stream-reported transition for the
// same change is an accepted simplification, not a bug — see the phase
// plan's dispatch notes), and evicting names that disappeared entirely
// (containers removed since the last refresh).
//
// Recreation guard: a vanished id only evicts its name when no id in the
// NEW snapshot holds that same name. Eviction is name-keyed (Live.Evict,
// RateTracker.EvictPrefix), so a container destroyed and recreated under
// the same name within one 10s poll interval must not have its
// just-started replacement's series wiped out by the old id's own
// disappearance.
func (r *registry) applyInventory(metas []Meta, sink EventSink, evict func(kind, entity string)) {
	next := make(map[string]Meta, len(metas))
	nextNames := make(map[string]struct{}, len(metas))
	for _, m := range metas {
		next[m.ID] = m
		nextNames[m.Name] = struct{}{}
	}

	var toEmit []store.Event
	var toEvict []string

	r.mu.Lock()
	old := r.byID
	r.byID = next
	for id, newM := range next {
		if oldM, existed := old[id]; existed {
			toEmit = append(toEmit, diffEvents(oldM, newM)...)
		}
	}
	for id, oldM := range old {
		if _, still := next[id]; still {
			continue
		}
		if _, nameLives := nextNames[oldM.Name]; nameLives {
			continue // recreated under the same name elsewhere in this diff
		}
		toEvict = append(toEvict, oldM.Name)
	}
	r.mu.Unlock()

	for _, e := range toEmit {
		if _, err := sink.AppendEvent(e); err != nil {
			log.Printf("events: %v", err)
		}
	}
	for _, name := range toEvict {
		evict("container", name)
	}
}

// diffEvents reproduces the belt events an inventory-diff can detect from
// two Meta snapshots of the same container id: a state flip to/from
// running (start/die), a restart-count bump while state didn't change
// (also treated as a start), and any health change.
func diffEvents(oldM, newM Meta) []store.Event {
	var out []store.Event
	switch {
	case newM.State == "running" && oldM.State != "running":
		out = append(out, store.Event{Kind: "container.start", Entity: newM.Name, Severity: "info"})
	case oldM.State == "running" && newM.State != "running":
		out = append(out, store.Event{Kind: "container.die", Entity: newM.Name, Severity: "info", Detail: "state: " + newM.State})
	case newM.RestartCount != oldM.RestartCount:
		out = append(out, store.Event{Kind: "container.start", Entity: newM.Name, Severity: "info", Detail: fmt.Sprintf("restart count %d", newM.RestartCount)})
	}
	if newM.Health != "" && newM.Health != oldM.Health {
		sev := "info"
		if newM.Health == "unhealthy" {
			sev = "warning"
		}
		out = append(out, store.Event{Kind: "container.health", Entity: newM.Name, Severity: sev, Detail: newM.Health})
	}
	return out
}

// applyEvent updates the registry (on removal) and translates one docker
// event into a store.Event where applicable. name resolution prefers the
// event's own "name" attribute, falling back to whatever the registry
// already knows about the actor id (events don't always carry attributes,
// e.g. a bare OOM notification).
//
// Recreation guard: the event-stream goroutine and the poll goroutine
// (applyInventory) are decoupled -- applyEvent never registers metas,
// only polls do -- so a destroy event for an id can be PROCESSED here
// after a later poll has already registered a same-named replacement
// under a different id (compose redeploys, watchtower routinely produce
// this ordering: destroy(old) delayed in local processing while the next
// 10s poll already discovered new). Mirrors applyInventory's own guard:
// evict only fires when no other id currently in the registry holds this
// name; the dying id is still forgotten either way.
func (r *registry) applyEvent(msg events.Message, sink EventSink, evict func(kind, entity string)) {
	name := normalizeName(msg.Actor.Attributes["name"])
	isRemoval := msg.Action == events.ActionDestroy || msg.Action == events.ActionRemove

	r.mu.Lock()
	if name == "" {
		if m, ok := r.byID[msg.Actor.ID]; ok {
			name = m.Name
		}
	}
	skipEvict := false
	if isRemoval {
		delete(r.byID, msg.Actor.ID)
		_, skipEvict = r.lookupByNameLocked(name)
	}
	r.mu.Unlock()

	if isRemoval {
		if !skipEvict {
			evict("container", name)
		}
		return
	}
	if evt, ok := translateEvent(msg, name); ok {
		if _, err := sink.AppendEvent(evt); err != nil {
			log.Printf("events: %v", err)
		}
	}
}

// translateEvent maps one docker event action into a store.Event. ok is
// false for actions the collector doesn't surface.
func translateEvent(msg events.Message, name string) (store.Event, bool) {
	switch {
	case msg.Action == events.ActionStart:
		return store.Event{Kind: "container.start", Entity: name, Severity: "info"}, true
	case msg.Action == events.ActionDie:
		exitCode := msg.Actor.Attributes["exitCode"]
		sev := "info"
		if exitCode != "" && exitCode != "0" {
			sev = "warning"
		}
		return store.Event{Kind: "container.die", Entity: name, Severity: sev, Detail: "exit code " + exitCode}, true
	case msg.Action == events.ActionOOM:
		return store.Event{Kind: "container.oom", Entity: name, Severity: "alert"}, true
	case strings.HasPrefix(string(msg.Action), string(events.ActionHealthStatus)):
		status := healthFromAction(msg.Action)
		sev := "info"
		if status == "unhealthy" {
			sev = "warning"
		}
		return store.Event{Kind: "container.health", Entity: name, Severity: sev, Detail: status}, true
	default:
		return store.Event{}, false
	}
}

// healthFromAction extracts the status suffix from a "health_status: X"
// action (X is usually healthy/unhealthy/running, or free-form healthcheck
// output on some docker versions). Returns "" for a bare "health_status"
// action with no suffix.
func healthFromAction(action events.Action) string {
	_, status, found := strings.Cut(string(action), ": ")
	if !found {
		return ""
	}
	return status
}
