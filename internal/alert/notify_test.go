package alert

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWriteNotifyFormat(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_756_000_000, 0)
	path, err := WriteNotify(dir, Notification{
		Event:       "Gantry",
		Subject:     "Container jellyfin unhealthy",
		Description: "health check failing for 5m",
		Importance:  "alert",
		Link:        "/Docker",
	}, now)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(path, filepath.Join(dir, "unread")+string(os.PathSeparator)))
	require.True(t, strings.HasSuffix(path, ".notify"))

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t,
		"timestamp=1756000000\n"+
			"event=Gantry\n"+
			"subject=Container jellyfin unhealthy\n"+
			"description=health check failing for 5m\n"+
			"importance=alert\n"+
			"link=/Docker\n",
		string(body))
}

func TestWriteNotifyValidatesImportanceAndStripsNewlines(t *testing.T) {
	dir := t.TempDir()
	_, err := WriteNotify(dir, Notification{Event: "x", Subject: "s", Importance: "urgent"}, time.Now())
	require.Error(t, err)

	path, err := WriteNotify(dir, Notification{
		Event: "Gantry", Subject: "line1\nline2", Description: "d\r\nd2", Importance: "normal",
	}, time.Unix(1, 0))
	require.NoError(t, err)
	body, _ := os.ReadFile(path)
	require.NotContains(t, strings.TrimSuffix(string(body), "\n"), "\r")
	require.Contains(t, string(body), "subject=line1 line2\n")
}

func TestWriteNotifyUniqueNames(t *testing.T) {
	dir := t.TempDir()
	n := Notification{Event: "Gantry", Subject: "s", Importance: "normal"}
	p1, err := WriteNotify(dir, n, time.Unix(1, 0))
	require.NoError(t, err)
	p2, err := WriteNotify(dir, n, time.Unix(1, 0))
	require.NoError(t, err)
	require.NotEqual(t, p1, p2)
}
