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
	DiskMeta      map[string]DiskMetaDTO        `json:"disk_meta"` // slot -> device+type meta (strings can't ride Disks' float64 map)
	Unraid        map[string]map[string]float64 `json:"unraid"`    // entity ("array"|"docker") -> metric -> value
	GPU           map[string]map[string]float64 `json:"gpu"`
	Sources       map[string]string             `json:"sources"` // collector name -> "ok" | unavailability detail
}

// DiskMetaDTO is one disk slot's device name and classified type
// (hdd|ssd|nvme|usb, see unraid.DiskKind) — the frontend's type-badge
// source of truth; rotational alone can't tell a USB flash stick or an
// NVMe pool member apart from a plain spinning/SATA-SSD disk.
type DiskMetaDTO struct {
	Device string `json:"device"`
	Kind   string `json:"kind"`
}

// ContainerDTO is one container's inventory metadata plus its latest
// metric values, keyed by container name in SnapshotDTO.Containers.
//
// UpdateStatus is "available" | "current" | "" (unknown: no unraid-
// update-status.json reader wired, the file was unreadable this poll,
// or this image has no entry in it). ChangelogURL/ProjectURL are each
// independently "" when nothing could be derived for that one field —
// see docker.changelogAndProjectURLs' own doc for the derivation rules.
//
// WebUIURL is the net.unraid.docker.webui label's RAW value, completely
// unresolved -- a template placeholder like "http://[IP]:[PORT:8096]/",
// not a usable URL. There's no backend resolution: the frontend is
// responsible for substituting [IP] -> window.location.hostname and
// [PORT:x] -> x itself, the same way Unraid's own WebUI does, since only
// the browser making the request knows the right host. Once resolved,
// the frontend must scheme-allowlist the result (http/https only)
// before it ever lands in an href, and must never render it via
// Svelte's {@html} -- container labels are attacker-controllable (a
// hostile Community Applications template), so a value that isn't even
// a well-formed http(s) URL must not silently become a script-capable
// one.
//
// Created/UpdateStatus/ChangelogURL/ProjectURL/WebUIURL/Networks/Ports
// all carry "omitempty": this frame ships every currently-running
// container on every tick (SSE included), so omitting an empty
// collection/absent value entirely is cheaper than an endpoint DTO like
// StorageDTO (server.StorageDTO's own doc), whose Mounts/Devices are
// deliberately always non-nil "[]" -- a per-request, one-container
// response has no such multiplied cost.
type ContainerDTO struct {
	State        string             `json:"state"`
	Health       string             `json:"health"`
	Image        string             `json:"image"`
	Icon         string             `json:"icon"`
	Created      int64              `json:"created,omitempty"`
	UpdateStatus string             `json:"update_status,omitempty"`
	ChangelogURL string             `json:"changelog_url,omitempty"`
	ProjectURL   string             `json:"project_url,omitempty"`
	WebUIURL     string             `json:"webui_url,omitempty"`
	Networks     []NetworkInfoDTO   `json:"networks,omitempty"`
	Ports        []PortInfoDTO      `json:"ports,omitempty"`
	Metrics      map[string]float64 `json:"metrics"`
}

// NetworkInfoDTO is one docker network a container is attached to. IP
// is "" for a network that assigns none to this container and for the
// synthetic {Name: "host"} entry host-network containers report.
type NetworkInfoDTO struct {
	Name string `json:"name"`
	IP   string `json:"ip,omitempty"`
}

// PortInfoDTO is one container-port binding. HostIP/HostPort are both
// their zero value for an exposed-but-unpublished port (EXPOSE with no
// -p) -- itself useful information, not an absence to filter out.
type PortInfoDTO struct {
	ContainerPort int    `json:"container_port"`
	Proto         string `json:"proto"`
	HostIP        string `json:"host_ip,omitempty"`
	HostPort      int    `json:"host_port,omitempty"`
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
