package server

import "net/http"

// SnapshotDTO is the /api/live/snapshot response shape: the latest value
// of every live series, grouped the way the UI consumes them (this is
// also the SSE seed frame in Phase 3 — not throwaway). Assembly (from
// store.Live() + the docker/unraid collectors) happens in main wiring via
// Options.Snapshot; the server itself never touches store.Live directly.
//
// v2: Unraid gained an entity dimension (entity -> metric -> value, was a
// flat metric->value map) so "array" (parity/mover/ups/shares) and
// "docker" (docker.img usage) provenance survives into the DTO instead of
// colliding into one bag. Sources moved into the frame from healthz-only,
// so an SSE client sees a collector degrading live, not just on its next
// healthz poll.
type SnapshotDTO struct {
	TS            int64                         `json:"ts"`
	UnraidVersion string                        `json:"unraid_version"`
	Host          map[string]float64            `json:"host"`       // metric -> latest
	Containers    map[string]ContainerDTO       `json:"containers"` // name -> meta+metrics
	Disks         map[string]map[string]float64 `json:"disks"`
	Unraid        map[string]map[string]float64 `json:"unraid"` // entity ("array"|"docker") -> metric -> value
	GPU           map[string]map[string]float64 `json:"gpu"`
	Sources       map[string]string             `json:"sources"` // collector name -> "ok" | unavailability detail
}

// ContainerDTO is one container's inventory metadata plus its latest
// metric values, keyed by container name in SnapshotDTO.Containers.
type ContainerDTO struct {
	State   string             `json:"state"`
	Health  string             `json:"health"`
	Image   string             `json:"image"`
	Icon    string             `json:"icon"`
	Metrics map[string]float64 `json:"metrics"`
}

// ContainerInfo is the /api/containers response shape: inventory facts
// only, straight from the docker collector's Running() (no metrics, no
// snapshot/DTO detour — a container's stats live in SnapshotDTO.Containers
// for callers that need them).
type ContainerInfo struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Health string `json:"health"`
	Image  string `json:"image"`
	Icon   string `json:"icon"`
}

// handleSnapshot serves the assembled snapshot. Options.Snapshot is nil in
// tests that don't wire one (and can't be in Phase 1 tests, which predate
// this route) — an empty JSON object is a valid, harmless response in
// that case, not an error.
func (s *Server) handleSnapshot(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Snapshot == nil {
		writeJSON(w, map[string]any{})
		return
	}
	writeJSON(w, s.opts.Snapshot())
}

// handleContainers serves the fleet list directly from Options.Containers
// (main wiring calls dc.Running(), no snapshot/DTO involved) — a narrower,
// cheaper view for callers that only care about identity/health, not
// metrics. Options.Containers is nil in tests that don't wire one; an
// empty JSON array is the harmless response in that case, matching the
// slice shape rather than an empty object.
func (s *Server) handleContainers(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Containers == nil {
		writeJSON(w, []ContainerInfo{})
		return
	}
	containers := s.opts.Containers()
	if containers == nil {
		containers = []ContainerInfo{}
	}
	writeJSON(w, containers)
}
