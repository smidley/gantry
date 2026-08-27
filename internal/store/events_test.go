package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAppendAndQueryEvents(t *testing.T) {
	s := newTestStore(t, func() time.Time { return at("12:00:00") })
	ctx := context.Background()

	_, err := s.AppendEvent(Event{Kind: "container.start", Entity: "jellyfin"})
	require.NoError(t, err)
	_, err = s.AppendEvent(Event{TS: at("12:01:00").Unix(), Kind: "container.oom", Entity: "jellyfin", Severity: "alert", Detail: "oom-killed"})
	require.NoError(t, err)
	_, err = s.AppendEvent(Event{TS: at("12:02:00").Unix(), Kind: "array.state", Entity: "array", Detail: "STARTED"})
	require.NoError(t, err)

	all, err := s.QueryEvents(ctx, EventFilter{})
	require.NoError(t, err)
	require.Len(t, all, 3)
	require.Equal(t, "array.state", all[0].Kind) // newest first

	jelly, err := s.QueryEvents(ctx, EventFilter{Entity: "jellyfin"})
	require.NoError(t, err)
	require.Len(t, jelly, 2)

	ooms, err := s.QueryEvents(ctx, EventFilter{Kinds: []string{"container.oom"}})
	require.NoError(t, err)
	require.Len(t, ooms, 1)
	require.Equal(t, "alert", ooms[0].Severity)

	windowed, err := s.QueryEvents(ctx, EventFilter{From: at("12:00:30").Unix(), To: at("12:01:30").Unix()})
	require.NoError(t, err)
	require.Len(t, windowed, 1)
	require.Equal(t, "container.oom", windowed[0].Kind)
}

// TestQueryEventsCancelledContextReturnsPromptly is the I2 regression
// guard: QueryEvents must actually observe ctx (via QueryContext on the
// read pool), not just accept and ignore it -- a context already
// cancelled before the call reaches the driver is the simplest way to
// prove that without racing a slow query against a timeout.
func TestQueryEventsCancelledContextReturnsPromptly(t *testing.T) {
	s := newTestStore(t, nil)
	_, err := s.AppendEvent(Event{Kind: "container.start", Entity: "jellyfin"})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = s.QueryEvents(ctx, EventFilter{})
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}
