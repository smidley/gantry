package unraid

import (
	"encoding/json"
	"os"
	"sync"
)

// UpdateStatusReader reads dockerMan's own record of pending image
// updates (normally bind-mounted read-only from
// /var/lib/docker/unraid-update-status.json), self-healing on every
// call: dockerMan rewrites the file in place via PHP's
// file_put_contents (same inode, so a single-file ro bind-mount stays
// live across a rewrite), but occasionally @unlink's it when its own
// scan produces empty output. Statuses re-stats the file fresh every
// call rather than trusting a cached Stat, so a genuinely missing file
// degrades to nil immediately and a later rewrite is picked up on the
// very next call, with no stuck bad state in between.
//
// The one exception is a TORN read: file_put_contents' rewrite isn't
// atomic from a concurrent reader's point of view, so a call can land
// mid-write and see a file that EXISTS but doesn't parse as valid
// JSON. Statuses tells that apart from a real @unlink (Stat itself
// fails) and serves last -- the most recent successfully parsed
// snapshot -- instead of blinking to "unknown" for that one tick.
type UpdateStatusReader struct {
	path string

	mu   sync.Mutex
	last map[string]string // last successfully parsed snapshot; served on a torn read only, see Statuses' own doc
}

// NewUpdateStatusReader constructs a reader for the file at path (the
// in-container path of the ro bind mount, e.g.
// "/updates/unraid-update-status.json" -- configurable so the mount
// point isn't hardcoded).
func NewUpdateStatusReader(path string) *UpdateStatusReader {
	return &UpdateStatusReader{path: path}
}

// maxUpdateStatusFileSize caps how large a file Statuses will ever read
// into memory. dockerMan's own file is a handful of KB even on a
// fifty-container box (one small JSON object per image); anything past
// this is either corruption or hostile (the file is bind-mounted in
// from outside the container Statuses' caller runs in), and a bare
// os.ReadFile has no size limit of its own to protect against either.
const maxUpdateStatusFileSize = 4 << 20 // 4MB

// Statuses returns the current image->status snapshot. It returns nil
// immediately -- clearing any cached snapshot from before -- when the
// file is absent (Stat fails) or larger than maxUpdateStatusFileSize;
// callers treat nil the same as "no reader wired at all"
// (docker.Collector.UpdateStatuses' default), so that's a quiet
// feature-off, not an error. When the file EXISTS but fails to parse (a
// torn read, see the reader's own doc), it instead serves the last
// successfully parsed snapshot rather than nil.
func (r *UpdateStatusReader) Statuses() map[string]string {
	fi, err := os.Stat(r.path)
	if err != nil {
		r.clearLast()
		return nil
	}
	if fi.Size() > maxUpdateStatusFileSize {
		return nil
	}
	data, err := os.ReadFile(r.path)
	if err != nil {
		// Disappeared between Stat and ReadFile -- a race with dockerMan's
		// own unlink, not a torn read (there's no content to have torn).
		r.clearLast()
		return nil
	}

	parsed := ParseUpdateStatus(data)
	r.mu.Lock()
	defer r.mu.Unlock()
	if parsed == nil {
		return r.last // torn read: the file exists but this read caught it mid-rewrite
	}
	r.last = parsed
	return parsed
}

func (r *UpdateStatusReader) clearLast() {
	r.mu.Lock()
	r.last = nil
	r.mu.Unlock()
}

// updateStatusEntry is the one field ParseUpdateStatus needs from each
// value in dockerMan's real-box shape: {"<image-ref>": {"local":
// "sha256:...", "remote": "sha256:...", "status": "true"|"false"|...}}.
// local/remote are unneeded here and simply ignored by json.Unmarshal.
type updateStatusEntry struct {
	Status string `json:"status"`
}

// ParseUpdateStatus parses dockerMan's unraid-update-status.json into
// image ref -> "current"|"available", translating its own "true"/
// "false" status vocabulary into the DTO's. An entry whose status is
// neither -- an unrecognized value, or the key missing entirely -- is
// omitted rather than guessed at; the docker package's join
// (joinUpdateStatus) already treats a missing map entry as unknown, so
// there's no need for a third sentinel value here. Malformed JSON
// returns nil, the same "can't tell you anything" signal Statuses uses
// for an unreadable file.
func ParseUpdateStatus(data []byte) map[string]string {
	var raw map[string]updateStatusEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	out := make(map[string]string, len(raw))
	for imageRef, entry := range raw {
		switch entry.Status {
		case "true":
			out[imageRef] = "current"
		case "false":
			out[imageRef] = "available"
		}
	}
	return out
}
