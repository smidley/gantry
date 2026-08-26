package server

import "net/http"

// SnapshotDTO is the /api/live/snapshot response shape: the latest value
// of every live series, grouped the way the UI consumes them (this is
// also the SSE seed frame in Phase 3 — not throwaway). Assembly (from
// store.Live() + the docker/unraid collectors) happens in main wiring via
// Options.Snapshot; the server itself never touches store.Live directly.
type SnapshotDTO struct {
	TS            int64                         `json:"ts"`
	Host          map[string]float64            `json:"host"`       // metric -> latest
	Containers    map[string]ContainerDTO       `json:"containers"` // name -> meta+metrics
	Disks         map[string]map[string]float64 `json:"disks"`
	Unraid        map[string]float64            `json:"unraid"`
	UnraidVersion string                        `json:"unraid_version"`
	GPU           map[string]map[string]float64 `json:"gpu"`
}

// ContainerDTO is one container's inventory metadata plus its latest
// metric values, keyed by container name in SnapshotDTO.Containers.
type ContainerDTO struct {
	State   string             `json:"state"`
	Health  string             `json:"health"`
	Image   string             `json:"image"`
	Metrics map[string]float64 `json:"metrics"`
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

// handleContainers serves just the containers slice of the same snapshot
// (name -> state/health/image/metrics) — a narrower view for callers that
// only care about the fleet, not host/disk/gpu/unraid data.
func (s *Server) handleContainers(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Snapshot == nil {
		writeJSON(w, map[string]ContainerDTO{})
		return
	}
	containers := s.opts.Snapshot().Containers
	if containers == nil {
		containers = map[string]ContainerDTO{}
	}
	writeJSON(w, containers)
}
