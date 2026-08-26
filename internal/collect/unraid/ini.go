// Package unraid parses Unraid's emhttp state files (mounted read-only
// from /var/local/emhttp) for array/parity state, per-disk stats, shares,
// mover activity, and best-effort UPS status. See ini.go for the tolerant
// dialect parser shared by every file, var.go/disks.go/shares.go/mover.go
// for each file's own interpretation.
package unraid

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// ParseINI parses the tolerant emhttp ini dialect: key="value" assignment
// lines (values are double-quoted; the quotes are stripped), optional
// [section] or ["section"] header lines that switch the current section
// (quotes stripped there too), and headerless keys collected under
// section "". Any line that doesn't match one of those two shapes —
// blank, a key=value pair whose value isn't properly double-quoted, or
// anything else unrecognized — is skipped silently: this parser only
// ever fails on a genuine reader error, never on file content, so a
// newer emhttp adding keys or sections this code doesn't know about
// can't break it.
func ParseINI(r io.Reader) (map[string]map[string]string, error) {
	result := map[string]map[string]string{"": {}}
	section := ""

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = unquote(line[1 : len(line)-1])
			if _, ok := result[section]; !ok {
				result[section] = map[string]string{}
			}
			continue
		}

		if key, value, ok := splitAssignment(line); ok {
			result[section][key] = value
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// splitAssignment parses one key="value" line: key is everything before
// the first '=', trimmed; value must be wrapped in double quotes (which
// are stripped) or the whole line is rejected as malformed (ok=false).
func splitAssignment(line string) (key, value string, ok bool) {
	idx := strings.Index(line, "=")
	if idx < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	if key == "" {
		return "", "", false
	}
	rest := strings.TrimSpace(line[idx+1:])
	if len(rest) < 2 || rest[0] != '"' || rest[len(rest)-1] != '"' {
		return "", "", false
	}
	return key, rest[1 : len(rest)-1], true
}

// unquote strips one layer of matching double quotes from s if present;
// a bare, unquoted section name is tolerated too (unlike an unquoted
// value line, which splitAssignment rejects outright).
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// parseFloatOK parses one ini value as a float64. Every numeric field in
// these dialects (positions, sizes, speeds, percentages, error counts) is
// stored as a decimal string, so this one helper covers all of them; ok
// is false for missing/malformed values (including "*", spun-down disks'
// literal temp placeholder) so callers can tell "value present and zero"
// from "value absent or unusable" where that distinction matters.
func parseFloatOK(s string) (float64, bool) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
