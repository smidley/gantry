package docker

import "strings"

// normalizeImageRef appends the implicit ":latest" tag docker elides
// from Config.Image when a container was created without one, so it
// matches the explicit-tag form dockerMan always writes as an
// unraid-update-status.json key. Only the LAST path segment's own
// absence of a colon (or "@" digest marker) counts as "no tag": a
// registry host of the form "host:port/repo" carries a colon that is a
// port separator, not a tag separator.
func normalizeImageRef(image string) string {
	tail := image
	if i := strings.LastIndex(image, "/"); i >= 0 {
		tail = image[i+1:]
	}
	if strings.ContainsAny(tail, ":@") {
		return image
	}
	return image + ":latest"
}

// joinUpdateStatus resolves one container's update_status DTO value:
// Meta.Image (normalized for the implicit tag), matched against the
// unraid-update-status.json snapshot's own keys (dockerMan always
// writes an explicit one, e.g. "ghcr.io/advplyr/audiobookshelf:latest").
// "" covers both "no reader wired" (statuses is nil) and "no entry for
// this image" -- both read as unknown to the DTO.
func joinUpdateStatus(image string, statuses map[string]string) string {
	if statuses == nil {
		return ""
	}
	return statuses[normalizeImageRef(image)]
}
