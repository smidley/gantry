package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/smidley/gantry/internal/store"
)

// ImageInfo is one entry in GET /api/images -- see Options.Images' own
// doc for how it's assembled. ID is docker's own 12-character short id
// (see shortImageID), display-only; FullID is the same image's full
// "sha256:"+64-hex digest -- every mutating route below takes FullID,
// never ID (see imageIDPattern's own doc for why the short form can
// never be accepted there).
type ImageInfo struct {
	ID          string   `json:"id"`
	FullID      string   `json:"full_id"`
	RepoTags    []string `json:"repo_tags"`
	RepoDigests []string `json:"repo_digests,omitempty"`
	SizeBytes   int64    `json:"size_bytes"`
	Created     int64    `json:"created"`
	State       string   `json:"state"`
	Containers  []string `json:"containers,omitempty"`
}

// ImagesSummary is GET /api/images' aggregate counts plus a reclaimable-
// bytes estimate. Note always carries the same fixed upper-bound caveat
// (see reclaimableBytesNote) -- part of the struct, not a separate
// endpoint, so a UI can render it right next to the number it qualifies.
type ImagesSummary struct {
	InUse            int    `json:"in_use"`
	Unused           int    `json:"unused"`
	Dangling         int    `json:"dangling"`
	ReclaimableBytes int64  `json:"reclaimable_bytes"`
	Note             string `json:"note"`
}

// ImagesDTO is GET /api/images' response shape.
type ImagesDTO struct {
	Images  []ImageInfo   `json:"images"`
	Summary ImagesSummary `json:"summary"`
}

// reclaimableBytesNote is ImagesSummary.Note's fixed value: docker's
// layers are shared across images, so deleting an unused/dangling image
// doesn't necessarily free its whole reported size if another image
// still holds some of the same layers -- reclaimable_bytes is only ever
// "up to", never a promise.
const reclaimableBytesNote = "docker's shared image layers make this an upper bound: up to reclaimable_bytes may actually be freed, and could be less"

// ImageRemoveResult is one entry in POST /api/images/remove's per-id
// result array -- exactly {id,ok,error} on the wire, per spec.
// RepoTags/SizeBytes ride along unexported from JSON (json:"-") purely
// so handleImagesRemove can log a tag/size-detailed event for each
// success: once an image is gone, there's no second call that could
// recover what it was.
type ImageRemoveResult struct {
	ID        string   `json:"id"`
	OK        bool     `json:"ok"`
	Error     string   `json:"error,omitempty"`
	RepoTags  []string `json:"-"`
	SizeBytes int64    `json:"-"`
}

// DeletedImage is one entry in POST /api/images/prune's "deleted" array.
type DeletedImage struct {
	ID        string   `json:"id"`
	RepoTags  []string `json:"repo_tags"`
	SizeBytes int64    `json:"size_bytes"`
}

// ImagePruneResult is POST /api/images/prune's response shape.
type ImagePruneResult struct {
	Deleted []DeletedImage `json:"deleted"`
	// ReclaimedBytes sums per-image sizes, so shared layers make it an
	// upper bound ("up to"), same as the GET summary's reclaimable note.
	ReclaimedBytes int64    `json:"reclaimed_bytes"`
	Errors         []string `json:"errors"`
}

// shortImageID trims a full "sha256:..." digest to docker's own
// 12-character short-id convention (matching `docker images`' IMAGE ID
// column) -- GET /api/images' id field is display-only; every internal
// join/removal path (docker.Collector, fake.Generator, and the two
// mutating routes below) keeps using the full id throughout.
func shortImageID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		id = id[:12]
	}
	return id
}

// digestRefsOrNone is handleImagesList's RepoTags fallback for an image
// with no tags: "<none>" only when it's truly nameless (no RepoDigests
// either -- see classifyImages' own doc on why that's the only actual
// dangling case). A digest-pinned image instead shows its own digest
// ref(s) as "repo@algo:<12-hex>" -- the same 12-char truncation
// shortImageID uses for a bare id, but keeping the "algo:" label here
// since, unlike the id column, there's no other field naming the
// algorithm.
func digestRefsOrNone(digests []string) []string {
	if len(digests) == 0 {
		return []string{"<none>"}
	}
	refs := make([]string, len(digests))
	for i, d := range digests {
		repo, dig, ok := strings.Cut(d, "@")
		if !ok {
			refs[i] = d
			continue
		}
		algo, hex, ok := strings.Cut(dig, ":")
		if !ok {
			refs[i] = repo + "@" + dig
			continue
		}
		if len(hex) > 12 {
			hex = hex[:12]
		}
		refs[i] = repo + "@" + algo + ":" + hex
	}
	return refs
}

// gantryConfirmHeader/gantryConfirmValue are the write-path guardrail
// requireMutationAllowed checks for. Gantry has no auth, so without
// this, any website open in the LAN user's browser could fire a blind
// cross-origin form POST straight at the daemon and have it just work;
// requiring a custom header forces the browser to CORS-preflight the
// request first, which a plain form POST can never trigger.
const (
	gantryConfirmHeader = "X-Gantry-Confirm"
	gantryConfirmValue  = "images"
)

// mutationMaxRequestBytes caps every mutating /api/images and
// /api/containers/maintenance route's request body (via
// http.MaxBytesReader) -- Gantry has no auth (see gantryConfirmHeader's
// own doc), so nothing else stops a request from forcing an unbounded
// read into memory before validation even runs. mutationMaxIDs caps
// POST /api/images/remove's and POST /api/containers/maintenance/
// remove's ids array for the same reason, one layer further in: a body
// under the byte cap could still carry an unreasonable number of short
// ids.
const (
	mutationMaxRequestBytes = 1 << 20 // 1MB
	mutationMaxIDs          = 100
)

// requireMutationAllowed enforces the write-path guardrails shared by
// every mutating route across both /api/images and
// /api/containers/maintenance, writing the rejection response itself
// when either fails. confirmValue is the caller's resource-specific
// X-Gantry-Confirm value (gantryConfirmValue for images,
// containersConfirmValue for containers -- see the latter's own doc for
// why the value is scoped per-resource); ReadOnly, unlike confirmValue,
// is a single global kill switch with no per-resource variant. Checked
// before the request body is even decoded, so both are independently
// testable without a wired backend.
func (s *Server) requireMutationAllowed(w http.ResponseWriter, r *http.Request, confirmValue string) bool {
	if r.Header.Get(gantryConfirmHeader) != confirmValue {
		writeError(w, http.StatusPreconditionRequired, "missing or invalid "+gantryConfirmHeader+" header")
		return false
	}
	if s.opts.ReadOnly {
		writeError(w, http.StatusForbidden, "read-only mode")
		return false
	}
	return true
}

// writeDecodeError writes dec.Decode's error as the appropriate 4xx --
// shared by every mutating /api/images and /api/containers/maintenance
// route, all of which wrap their body in http.MaxBytesReader
// (mutationMaxRequestBytes). A body over that cap surfaces here as a
// *http.MaxBytesError, which is
// a distinct problem from a malformed one (413, not 400: the body may
// well have been well-formed JSON that simply never got to finish
// arriving); anything else decode can fail on -- bad JSON, an unknown
// field, wrong types -- keeps 400.
func writeDecodeError(w http.ResponseWriter, err error) {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
		return
	}
	writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
}

// logImageRemoved appends one image.removed event, when Options.
// AppendEvent is wired -- shared by handleImagesRemove (one per
// successful id) and handleImagesPrune (one per deleted image).
func (s *Server) logImageRemoved(id string, repoTags []string, sizeBytes int64) {
	if s.opts.AppendEvent == nil {
		return
	}
	tags := strings.Join(repoTags, ",")
	if tags == "" {
		tags = "<none>"
	}
	if _, err := s.opts.AppendEvent(store.Event{
		Kind:     "image.removed",
		Entity:   shortImageID(id),
		Severity: "info",
		Detail:   fmt.Sprintf("%s %s (%d bytes)", id, tags, sizeBytes),
	}); err != nil {
		log.Printf("images: append event: %v", err)
	}
}

// handleImagesList serves GET /api/images. Options.Images is nil in
// tests that don't wire one -- an empty images list and a zeroed
// summary (Note still set: it's a fixed caveat about the field, not
// data from the backend) is the harmless response in that case,
// matching Containers' own nil->empty convention.
func (s *Server) handleImagesList(w http.ResponseWriter, r *http.Request) {
	if s.opts.Images == nil {
		writeJSON(w, ImagesDTO{Images: []ImageInfo{}, Summary: ImagesSummary{Note: reclaimableBytesNote}})
		return
	}
	dto, err := s.opts.Images(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if dto.Images == nil {
		dto.Images = []ImageInfo{}
	}
	for i, im := range dto.Images {
		dto.Images[i].FullID = im.ID
		dto.Images[i].ID = shortImageID(im.ID)
		if len(im.RepoTags) == 0 {
			dto.Images[i].RepoTags = digestRefsOrNone(im.RepoDigests)
		}
	}
	dto.Summary.Note = reclaimableBytesNote
	writeJSON(w, dto)
}

// imageIDPattern is a docker content-addressable image id -- a bare or
// "sha256:"-prefixed run of EXACTLY 64 lowercase hex digits, never fewer.
// This is moby's own digest length, not an arbitrary choice: its
// reference parser (distribution/reference.ParseAnyReference) only
// recognizes a BARE string as a digest -- skipping name resolution
// entirely -- when it's anchored at exactly 64 hex chars. Anything
// shorter falls through to ParseNormalizedNamed and is resolved as a
// repository NAME first, only falling back to id-prefix search if no
// image happens to be tagged with that literal string. A 12-64 char
// range would let an image tagged with a name equal to another image's
// short id shadow it -- silently deleting the wrong image -- so this
// only ever accepts the one form moby itself treats as unambiguous.
var imageIDPattern = regexp.MustCompile(`^(sha256:)?[0-9a-f]{64}$`)

type imagesRemoveRequest struct {
	IDs []string `json:"ids"`
}

// handleImagesRemove serves POST /api/images/remove.
func (s *Server) handleImagesRemove(w http.ResponseWriter, r *http.Request) {
	if !s.requireMutationAllowed(w, r, gantryConfirmValue) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, mutationMaxRequestBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body imagesRemoveRequest
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
		if !imageIDPattern.MatchString(id) {
			writeError(w, http.StatusBadRequest, "not an image id: "+id)
			return
		}
	}

	if s.opts.RemoveImages == nil {
		writeError(w, http.StatusNotFound, "image removal unavailable")
		return
	}
	results, err := s.opts.RemoveImages(r.Context(), body.IDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, res := range results {
		if res.OK {
			s.logImageRemoved(res.ID, res.RepoTags, res.SizeBytes)
		}
	}
	writeJSON(w, results)
}

type imagesPruneRequest struct {
	Mode string `json:"mode"`
}

// handleImagesPrune serves POST /api/images/prune.
func (s *Server) handleImagesPrune(w http.ResponseWriter, r *http.Request) {
	if !s.requireMutationAllowed(w, r, gantryConfirmValue) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, mutationMaxRequestBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body imagesPruneRequest
	if err := dec.Decode(&body); err != nil {
		writeDecodeError(w, err)
		return
	}
	if body.Mode != "dangling" && body.Mode != "unused" {
		writeError(w, http.StatusBadRequest, `mode must be "dangling" or "unused"`)
		return
	}

	if s.opts.PruneImages == nil {
		writeError(w, http.StatusNotFound, "image prune unavailable")
		return
	}
	result, err := s.opts.PruneImages(r.Context(), body.Mode)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, d := range result.Deleted {
		s.logImageRemoved(d.ID, d.RepoTags, d.SizeBytes)
	}
	if result.Deleted == nil {
		result.Deleted = []DeletedImage{}
	}
	if result.Errors == nil {
		result.Errors = []string{}
	}
	writeJSON(w, result)
}
