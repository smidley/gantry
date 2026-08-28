package server

import "net/http"

// StorageDTO is the /api/containers/{name}/storage response shape: this
// container's mounts (each resolved to the Unraid storage system backing
// it) plus its current per-backing-device IO rates. Assembly (docker
// Meta.Mounts + unraid.ResolveStoragePath + store.Live's live:io.* rates)
// happens in main wiring via Options.Storage, the same separation
// buildSnapshot already keeps for SnapshotDTO -- this package stays
// store/collector-shape-agnostic.
//
// A container's writable-layer size (docker inspect/DiskUsage's SizeRw)
// is deliberately not a field here: the docker-disk collector's
// DiskUsage poll only retains an aggregate total across every
// container's SizeRw, never a per-container breakdown (see diskusage.go)
// -- surfacing it would mean adding a new, heavier per-container poll,
// which is out of scope for this groundwork (see docs/superpowers/
// backlog.md's "Per-container storage panel" design notes).
type StorageDTO struct {
	Mounts  []MountDTO    `json:"mounts"`
	Devices []DeviceIODTO `json:"devices"`
}

// MountDTO is one container mount plus the storage system its host
// Source path resolves to.
type MountDTO struct {
	Source      string        `json:"source"`
	Destination string        `json:"destination"`
	RW          bool          `json:"rw"`
	Storage     StorageRefDTO `json:"storage"`
}

// StorageRefDTO names the storage system backing one mount. Kind is one
// of "share" | "pool" | "disk" | "flash" | "other"; Name is the share/
// pool/disk-slot name -- "" for "flash" and "other".
type StorageRefDTO struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// DeviceIODTO is this container's current IO rate against one backing
// block device (a live-ring-only sample -- see store.Live.
// LatestByMetricPrefix's own doc).
type DeviceIODTO struct {
	Device   string  `json:"device"`
	ReadBps  float64 `json:"read_bps"`
	WriteBps float64 `json:"write_bps"`
}

// handleStorage serves GET /api/containers/{name}/storage. Options.
// Storage is nil only in tests that don't wire one -- main wiring always
// sets it, fake-data mode included, where buildContainerStorage falls
// back to fakeMetas for names dc's registry doesn't know (see its own
// doc). A name neither source recognizes still 404s the same way an
// unwired closure would (ok == false), the same shape Logs' unknown-name
// case uses.
func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.opts.Storage == nil {
		writeError(w, http.StatusNotFound, "unknown container "+name)
		return
	}
	dto, ok := s.opts.Storage(name)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown container "+name)
		return
	}
	writeJSON(w, dto)
}
