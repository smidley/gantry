package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAppendAndQueryEvents(t *testing.T) {
	s := newTestStore(t, func() time.Time { return at("12:00:00") })

	_, err := s.AppendEvent(Event{Kind: "container.start", Entity: "jellyfin"})
	require.NoError(t, err)
	_, err = s.AppendEvent(Event{TS: at("12:01:00").Unix(), Kind: "container.oom", Entity: "jellyfin", Severity: "alert", Detail: "oom-killed"})
	require.NoError(t, err)
	_, err = s.AppendEvent(Event{TS: at("12:02:00").Unix(), Kind: "array.state", Entity: "array", Detail: "STARTED"})
	require.NoError(t, err)

	all, err := s.QueryEvents(EventFilter{})
	require.NoError(t, err)
	require.Len(t, all, 3)
	require.Equal(t, "array.state", all[0].Kind) // newest first

	jelly, err := s.QueryEvents(EventFilter{Entity: "jellyfin"})
	require.NoError(t, err)
	require.Len(t, jelly, 2)

	ooms, err := s.QueryEvents(EventFilter{Kinds: []string{"container.oom"}})
	require.NoError(t, err)
	require.Len(t, ooms, 1)
	require.Equal(t, "alert", ooms[0].Severity)

	windowed, err := s.QueryEvents(EventFilter{From: at("12:00:30").Unix(), To: at("12:01:30").Unix()})
	require.NoError(t, err)
	require.Len(t, windowed, 1)
	require.Equal(t, "container.oom", windowed[0].Kind)
}
