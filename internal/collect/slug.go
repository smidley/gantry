package collect

import (
	"regexp"
	"strings"
)

var (
	slugInvalidChar   = regexp.MustCompile(`[^a-z0-9_-]`)
	slugUnderscoreRun = regexp.MustCompile(`_+`)
)

// SlugSegment turns arbitrary runtime-discovered text (a hwmon sensor
// label, an Unraid share name, a block device name, a DRM engine name —
// anything a collector reads off the system rather than choosing itself)
// into one safe metric-name segment: lowercased, every character outside
// [a-z0-9_-] replaced with "_", runs of "_" collapsed to one, leading and
// trailing "_" trimmed. An input that slugs away to nothing (e.g. all
// punctuation) becomes "unknown" rather than an empty segment, which
// would otherwise produce a malformed metric name like
// "share..used_bytes".
func SlugSegment(s string) string {
	lower := strings.ToLower(s)
	replaced := slugInvalidChar.ReplaceAllString(lower, "_")
	collapsed := slugUnderscoreRun.ReplaceAllString(replaced, "_")
	trimmed := strings.Trim(collapsed, "_")
	if trimmed == "" {
		return "unknown"
	}
	return trimmed
}
