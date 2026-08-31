package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/smidley/gantry/internal/store"
)

const (
	// minAckHours/maxAckHours bound POST /api/acks' snooze window to the
	// UI control's own presets (1h/24h/7d), tolerant of any value in
	// between -- the minSilenceHours precedent, with a deliberately
	// SHORTER ceiling than a silence's 30 days: an ack quiets a
	// frame-derived attention row that regenerates from live state every
	// tick, so a week is the longest "stop showing me this" that still
	// reads as a snooze rather than a permanent mute nobody remembers.
	minAckHours = 1
	maxAckHours = 168 // 7 days
)

// AcksIface is the minimal store surface GET/POST /api/acks and DELETE
// /api/acks/{id} need -- main wires a small adapter over *store.Store
// (acksAdapter), the exact AlertsIface precedent for keeping this
// package store-shape-agnostic. Nil in tests that don't wire one: GET
// reports an empty list (meaningful empty, matching Alerts' own
// convention); POST/DELETE 404, matching Settings' PUT.
type AcksIface interface {
	// Acks returns every ack not yet expired (store.Acks, with "now"
	// resolved by the adapter), for GET /api/acks.
	Acks(ctx context.Context) ([]store.OverviewAck, error)
	// AddAck inserts a new ack for POST /api/acks, returning it with its
	// generated ID.
	AddAck(a store.OverviewAck) (store.OverviewAck, error)
	// DeleteAck lifts an ack for DELETE /api/acks/{id} -- a no-op, not
	// an error, for an id that's already gone (store.DeleteAck's own doc).
	DeleteAck(id int64) error
}

// ackableKinds is the closed vocabulary POST /api/acks accepts: exactly
// the frame-derived anomaly kinds overviewStatus.ts can produce (each
// naming a concrete entity -- container name, disk slot, the literal
// "array", a source name). Two absences are the point, not gaps:
// "alert" never lands here because acknowledging an alert-backed callout
// IS an alert silence (POST /api/alerts/silences -- one mechanism per
// system), and "stopped" no longer exists as an anomaly at all (a
// stopped container is not something that needs you; the fleet sentence
// still states it as a fact).
var ackableKinds = map[string]bool{
	"unhealthy":       true,
	"disk-usage":      true,
	"disk-errors":     true,
	"array-stopped":   true,
	"source-critical": true,
}

// OverviewAckDTO mirrors store.OverviewAck's own columns -- the plain
// field-for-field wire shape, same convention as SilenceDTO (minus its
// computed Scope: acks have no "any" scope to label).
type OverviewAckDTO struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Entity    string `json:"entity"`
	Until     int64  `json:"until"`
	CreatedAt int64  `json:"created_at"`
}

func toOverviewAckDTO(a store.OverviewAck) OverviewAckDTO {
	return OverviewAckDTO{ID: a.ID, Kind: a.Kind, Entity: a.Entity, Until: a.Until, CreatedAt: a.CreatedAt}
}

type acksGetResponse struct {
	Acks []OverviewAckDTO `json:"acks"`
}

// ackCreateRequest is POST /api/acks' body. Unlike silenceCreateRequest
// there is no Scope field to widen anything: kind and entity are both
// required and concrete, always -- see 005_overview_acks.sql for why no
// global ack shape exists.
type ackCreateRequest struct {
	Kind   string `json:"kind"`
	Entity string `json:"entity"`
	Hours  int    `json:"hours"`
}

// handleAcksGet serves GET /api/acks: every not-yet-expired ack.
// Options.Acks is nil in tests that don't wire one -- an empty list is
// the harmless response, matching Alerts' own nil->empty convention.
func (s *Server) handleAcksGet(w http.ResponseWriter, r *http.Request) {
	if s.opts.Acks == nil {
		writeJSON(w, acksGetResponse{Acks: []OverviewAckDTO{}})
		return
	}
	acks, err := s.opts.Acks.Acks(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dtos := make([]OverviewAckDTO, len(acks))
	for i, a := range acks {
		dtos[i] = toOverviewAckDTO(a)
	}
	writeJSON(w, acksGetResponse{Acks: dtos})
}

// handleAcksPost serves POST /api/acks: acknowledge one concrete
// (kind, entity) attention concern for hours (1-168, i.e. up to 7 days).
// Not READ_ONLY-gated and no X-Gantry-Confirm -- config-shaped noise
// reduction, the exact POST /api/alerts/silences precedent (an ack, like
// a silence, only ever reduces what's shown, never touches docker or
// anything else destructive).
func (s *Server) handleAcksPost(w http.ResponseWriter, r *http.Request) {
	if s.opts.Acks == nil {
		writeError(w, http.StatusNotFound, "acks unavailable")
		return
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body ackCreateRequest
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if body.Hours < minAckHours || body.Hours > maxAckHours {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("hours must be between %d and %d", minAckHours, maxAckHours))
		return
	}
	if !ackableKinds[body.Kind] {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("kind must be one of %s", strings.Join(sortedAckableKinds(), ", ")))
		return
	}
	if body.Entity == "" {
		writeError(w, http.StatusBadRequest, "entity is required: an ack always names one concrete concern")
		return
	}

	now := time.Now().Unix()
	created, err := s.opts.Acks.AddAck(store.OverviewAck{
		Kind: body.Kind, Entity: body.Entity,
		Until: now + int64(body.Hours)*3600, CreatedAt: now,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, toOverviewAckDTO(created))
}

// handleAcksDelete serves DELETE /api/acks/{id}: 204 whether or not id
// existed (store.DeleteAck's own "already lifted or pruned" no-op
// convention) -- lifting an ack early is naturally idempotent from the
// caller's point of view, same as lifting a silence.
func (s *Server) handleAcksDelete(w http.ResponseWriter, r *http.Request) {
	if s.opts.Acks == nil {
		writeError(w, http.StatusNotFound, "acks unavailable")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad ack id")
		return
	}
	if err := s.opts.Acks.DeleteAck(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sortedAckableKinds renders the closed vocabulary for the 400 message
// above, in a stable order (map iteration isn't), so the same bad
// request always gets the same message.
func sortedAckableKinds() []string {
	kinds := make([]string, 0, len(ackableKinds))
	for k := range ackableKinds {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}
