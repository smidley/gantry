package unraid

import (
	"encoding/json"
	"os"
)

// UpdateStatusReader reads dockerMan's own record of pending image
// updates (normally bind-mounted read-only from
// /var/lib/docker/unraid-update-status.json), self-healing on every
// call: dockerMan rewrites the file in place via PHP's
// file_put_contents (same inode, so a single-file ro bind-mount stays
// live across a rewrite), but occasionally @unlink's it when its own
// scan produces empty output. Statuses re-stats/re-reads the file fresh
// every call rather than caching, so a missing/unreadable file degrades
// to nil immediately and a later rewrite is picked up on the very next
// call, with no stuck bad state in between.
type UpdateStatusReader struct {
	path string
}

// NewUpdateStatusReader constructs a reader for the file at path (the
// in-container path of the ro bind mount, e.g.
// "/updates/unraid-update-status.json" -- configurable so the mount
// point isn't hardcoded).
func NewUpdateStatusReader(path string) *UpdateStatusReader {
	return &UpdateStatusReader{path: path}
}

// Statuses returns the current image->status snapshot, or nil when the
// file is absent, unreadable, or not valid JSON -- callers treat nil the
// same as "no reader wired at all" (docker.Collector.UpdateStatuses'
// default), so an absent file is a quiet feature-off, not an error.
func (r *UpdateStatusReader) Statuses() map[string]string {
	data, err := os.ReadFile(r.path)
	if err != nil {
		return nil
	}
	return ParseUpdateStatus(data)
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
