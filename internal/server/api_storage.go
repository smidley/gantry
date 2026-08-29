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
// Mounts and Devices are always non-nil, so they marshal as "[]", never
// "null", even when empty.
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
	// Placement (additive, optional -- kind=share only) names the pool a
	// share is actually stored on, so a mount that only ever showed "the
	// downloads share is used" can also answer "which drive is that share
	// ON" (Scott: "we need to connect the dots"). nil for a non-share
	// mount, or a share whose shares.ini section has no useCache field at
	// all -- omitted from the response entirely (omitempty), not a
	// zero-value object, so a caller can tell "unknown" apart from a real
	// (if unlikely) empty mode string.
	Placement *SharePlacementDTO `json:"placement,omitempty"`
}

// SharePlacementDTO mirrors unraid.SharePlacement: Mode is useCache's own
// wire value ("yes" | "no" | "only" | "prefer") verbatim, straight
// through with no relabeling -- the frontend's own copy for each mode
// lives in containerStorage.ts, not here. Pool is cachePool's own pool
// name, omitted (never "") when Mode is "no" (see unraid.SharePlacement.
// Pool's own doc for why "no" never carries one).
type SharePlacementDTO struct {
	Mode string `json:"mode"`
	Pool string `json:"pool,omitempty"`
}

// DeviceIODTO is this container's current IO rate against one backing
// block device (a live-ring-only sample -- see store.Live.
// LatestByMetricPrefix's own doc). Label/Kind are unraid.
// ResolveDeviceLabel's own output (main wiring's job to fill in, same
// separation as Storage's Kind/Name -- see StorageDTO's own doc): Label
// is always populated (Device itself when nothing friendlier is known);
// Kind is "" unless DiskMeta placed Device in a known array/pool/flash
// slot.
type DeviceIODTO struct {
	Device   string  `json:"device"`
	Label    string  `json:"label"`
	Kind     string  `json:"kind"`
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
