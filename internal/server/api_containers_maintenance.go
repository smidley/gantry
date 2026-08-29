package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"

	"github.com/smidley/gantry/internal/store"
)

// ContainerMaintenanceInfo is one entry in GET
// /api/containers/maintenance -- see handleContainersMaintenanceList's
// own doc for how it's assembled. ID is docker's own 12-character short
// id (see shortContainerID), display-only; FullID is the full 64-hex
// container id -- every mutating route below takes FullID, never ID
// (see containerIDPattern's own doc for why the short form can never be
// accepted there).
//
// ExitCode/FinishedAt are pointers, not plain values: both are only
// meaningful for State "exited", and even then only when the backend's
// own inspect enrichment succeeded (see docker.ContainersMaintenance's
// own doc) -- omitempty on a pointer omits exactly the nil case, unlike
// a plain int/int64 where 0 is itself a real, common exit code/timestamp
// and could never be told apart from "not populated".
//
// RestartPolicy carries the same exited/enrichment-only scoping as
// ExitCode/FinishedAt, but as a plain omitempty string: "" already means
// "not configured" with nothing else it could be confused with, so
// there's no pointer needed here the way ExitCode's real zero value
// forces one. Lets the UI warn before removing an exited container that
// would actually come right back (always/unless-stopped/on-failure),
// the same way Managed warns about a dockerman/compose-owned one.
type ContainerMaintenanceInfo struct {
	ID            string `json:"id"`
	FullID        string `json:"full_id"`
	Name          string `json:"name"`
	Image         string `json:"image"`
	State         string `json:"state"`
	ExitCode      *int   `json:"exit_code,omitempty"`
	Created       int64  `json:"created"`
	FinishedAt    *int64 `json:"finished_at,omitempty"`
	Managed       string `json:"managed,omitempty"`
	RestartPolicy string `json:"restart_policy,omitempty"`
}

// ContainerMaintenanceSummary is GET /api/containers/maintenance's
// aggregate counts, one per ContainerMaintenanceInfo.State value.
type ContainerMaintenanceSummary struct {
	Exited  int `json:"exited"`
	Created int `json:"created"`
	Dead    int `json:"dead"`
}

// ContainerMaintenanceDTO is GET /api/containers/maintenance's response
// shape.
type ContainerMaintenanceDTO struct {
	Containers []ContainerMaintenanceInfo  `json:"containers"`
	Summary    ContainerMaintenanceSummary `json:"summary"`
}

// ContainerRemoveResult is one entry in POST
// /api/containers/maintenance/remove's per-id result array -- exactly
// {id,ok,error} on the wire, per spec, same shape as images'
// ImageRemoveResult. Name/Image ride along unexported from JSON
// (json:"-") purely so handleContainersMaintenanceRemove can log a
// name/image-detailed event for each success: once a container is gone,
// there's no second call that could recover what it was.
type ContainerRemoveResult struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Name  string `json:"-"`
	Image string `json:"-"`
}

// DeletedContainer is one entry in POST
// /api/containers/maintenance/prune's "deleted" array.
type DeletedContainer struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Image string `json:"image"`
}

// ContainerPruneResult is POST /api/containers/maintenance/prune's
// response shape.
type ContainerPruneResult struct {
	Deleted []DeletedContainer `json:"deleted"`
	Errors  []string           `json:"errors"`
}

// shortContainerID trims a full container id to docker's own 12-
// character short-id convention (matching `docker ps`'s CONTAINER ID
// column) -- GET /api/containers/maintenance's id field is display-only;
// every mutating route takes FullID. Unlike shortImageID, there's no
// "sha256:" prefix to strip: a container id is always a bare 64-hex
// string (see containerIDPattern's own doc).
func shortContainerID(id string) string {
	if len(id) > 12 {
		id = id[:12]
	}
	return id
}

// containersConfirmValue is requireMutationAllowed's expected
// X-Gantry-Confirm value for every /api/containers/maintenance mutating
// route -- gantryConfirmHeader itself (the header NAME) is shared with
// /api/images (see its own doc for why the header exists at all); the
// value is scoped per-resource so a confirm header crafted for one
// write surface can't accidentally also satisfy another.
const containersConfirmValue = "containers"

// containerIDPattern is a docker container id -- a bare, EXACTLY
// 64-character lowercase-hex string, matching dockerd's own
// stringid.GenerateRandomID output (32 random bytes, hex-encoded).
// Unlike an image id (see imageIDPattern's own doc), a container id is
// never content-addressed and never carries a "sha256:" prefix -- but
// the same discipline applies for the same reason: only the exact,
// unambiguous full id is accepted here, never a short prefix or a
// container NAME, either of which dockerd's own container lookup
// resolves server-side and could hit the wrong container.
var containerIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// logContainerRemoved appends one container.removed event, when
// Options.AppendEvent is wired -- shared by
// handleContainersMaintenanceRemove (one per successful id) and
// handleContainersMaintenancePrune (one per deleted container),
// mirroring logImageRemoved's own shape. Detail carries name+image+full
// id together (per spec) rather than the tags+bytes pairing
// image.removed uses -- the two events describe different resources,
// not one being more or less complete than the other.
func (s *Server) logContainerRemoved(id, name, image string) {
	if s.opts.AppendEvent == nil {
		return
	}
	if _, err := s.opts.AppendEvent(store.Event{
		Kind:     "container.removed",
		Entity:   shortContainerID(id),
		Severity: "info",
		Detail:   fmt.Sprintf("%s %s (%s)", name, image, id),
	}); err != nil {
		log.Printf("containers: append event: %v", err)
	}
}

// handleContainersMaintenanceList serves GET
// /api/containers/maintenance. Options.ContainersMaintenance is nil in
// tests that don't wire one -- an empty containers list is the harmless
// response in that case, matching Images' own nil->empty convention.
func (s *Server) handleContainersMaintenanceList(w http.ResponseWriter, r *http.Request) {
	if s.opts.ContainersMaintenance == nil {
		writeJSON(w, ContainerMaintenanceDTO{Containers: []ContainerMaintenanceInfo{}})
		return
	}
	dto, err := s.opts.ContainersMaintenance(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if dto.Containers == nil {
		dto.Containers = []ContainerMaintenanceInfo{}
	}
	for i, ct := range dto.Containers {
		dto.Containers[i].FullID = ct.ID
		dto.Containers[i].ID = shortContainerID(ct.ID)
	}
	writeJSON(w, dto)
}

type containersMaintenanceRemoveRequest struct {
	IDs []string `json:"ids"`
}

// handleContainersMaintenanceRemove serves POST
// /api/containers/maintenance/remove.
func (s *Server) handleContainersMaintenanceRemove(w http.ResponseWriter, r *http.Request) {
	if !s.requireMutationAllowed(w, r, containersConfirmValue) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, mutationMaxRequestBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body containersMaintenanceRemoveRequest
	if err := dec.Decode(&body); err != nil {
		writeDecodeError(w, err)
		return
	}
	if len(body.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids must not be empty")
		return
	}
	if len(body.IDs) > mutationMaxIDs {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("too many ids: %d (max %d)", len(body.IDs), mutationMaxIDs))
		return
	}
	for _, id := range body.IDs {
		if !containerIDPattern.MatchString(id) {
			writeError(w, http.StatusBadRequest, "not a container id: "+id)
			return
		}
	}

	if s.opts.RemoveContainers == nil {
		writeError(w, http.StatusNotFound, "container removal unavailable")
		return
	}
	results, err := s.opts.RemoveContainers(r.Context(), body.IDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, res := range results {
		if res.OK {
			s.logContainerRemoved(res.ID, res.Name, res.Image)
		}
	}
	writeJSON(w, results)
}

type containersMaintenancePruneRequest struct {
	Mode           string `json:"mode"`
	OlderThanHours int    `json:"older_than_hours"`
}

// handleContainersMaintenancePrune serves POST
// /api/containers/maintenance/prune.
func (s *Server) handleContainersMaintenancePrune(w http.ResponseWriter, r *http.Request) {
	if !s.requireMutationAllowed(w, r, containersConfirmValue) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, mutationMaxRequestBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body containersMaintenancePruneRequest
	if err := dec.Decode(&body); err != nil {
		writeDecodeError(w, err)
		return
	}
	if body.Mode != "exited" && body.Mode != "created" && body.Mode != "all-stopped" {
		writeError(w, http.StatusBadRequest, `mode must be "exited", "created", or "all-stopped"`)
		return
	}
	if body.OlderThanHours < 0 {
		writeError(w, http.StatusBadRequest, "older_than_hours must not be negative")
		return
	}

	if s.opts.PruneContainers == nil {
		writeError(w, http.StatusNotFound, "container prune unavailable")
		return
	}
	result, err := s.opts.PruneContainers(r.Context(), body.Mode, body.OlderThanHours)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, d := range result.Deleted {
		s.logContainerRemoved(d.ID, d.Name, d.Image)
	}
	if result.Deleted == nil {
		result.Deleted = []DeletedContainer{}
	}
	if result.Errors == nil {
		result.Errors = []string{}
	}
	writeJSON(w, result)
}
