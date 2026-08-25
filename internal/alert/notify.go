// Package alert delivers Gantry alerts. This file implements the
// Unraid-native channel: dynamix-format notify files dropped into the
// mounted /tmp/notifications spool.
package alert

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

type Notification struct {
	Event       string
	Subject     string
	Description string
	Importance  string
	Link        string
}

var notifySeq atomic.Uint64

func sanitize(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	return strings.ReplaceAll(s, "\n", " ")
}

// WriteNotify writes the file atomically (temp file + rename) so the
// dynamix poller never reads a partial notification.
func WriteNotify(dir string, n Notification, now time.Time) (string, error) {
	switch n.Importance {
	case "normal", "warning", "alert":
	default:
		return "", fmt.Errorf("invalid importance %q (want normal|warning|alert)", n.Importance)
	}

	unread := filepath.Join(dir, "unread")
	if err := os.MkdirAll(unread, 0o755); err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "timestamp=%d\n", now.Unix())
	fmt.Fprintf(&b, "event=%s\n", sanitize(n.Event))
	fmt.Fprintf(&b, "subject=%s\n", sanitize(n.Subject))
	fmt.Fprintf(&b, "description=%s\n", sanitize(n.Description))
	fmt.Fprintf(&b, "importance=%s\n", n.Importance)
	if n.Link != "" {
		fmt.Fprintf(&b, "link=%s\n", sanitize(n.Link))
	}

	name := fmt.Sprintf("gantry_%d_%d.notify", now.UnixNano(), notifySeq.Add(1))
	tmp := filepath.Join(unread, "."+name+".tmp")
	final := filepath.Join(unread, name)
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return final, nil
}
