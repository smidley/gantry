package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAddAckRoundTripsEveryField pins AddAck/Acks end to end -- the exact
// TestAddSilenceRoundTripsEveryField / TestAddInsightDismissalRoundTrips-
// EveryField contract.
func TestAddAckRoundTripsEveryField(t *testing.T) {
	s := newTestStore(t, nil)
	want := OverviewAck{Kind: "disk-usage", Entity: "disk3", Until: 2000, CreatedAt: 1000}
	id, err := s.AddAck(want)
	require.NoError(t, err)
	require.Greater(t, id, int64(0))
	want.ID = id

	got, err := s.Acks(context.Background(), 1500) // now < until: not expired
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, want, got[0])
}

// TestAcksExcludesExpired pins the read-side half of expiry (Maintain
// prunes expired rows separately -- this proves a call in between the two
// still never sees one) -- the exact TestSilencesExcludesExpired contract.
func TestAcksExcludesExpired(t *testing.T) {
	s := newTestStore(t, nil)
	expiredID, err := s.AddAck(OverviewAck{Kind: "unhealthy", Entity: "sonarr", Until: 1000, CreatedAt: 0})
	require.NoError(t, err)
	activeID, err := s.AddAck(OverviewAck{Kind: "array-stopped", Entity: "array", Until: 3000, CreatedAt: 0})
	require.NoError(t, err)

	got, err := s.Acks(context.Background(), 2000)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, activeID, got[0].ID)
	require.NotEqual(t, expiredID, got[0].ID)
}

// TestDeleteAckRemovesRowAndIsIdempotent pins the "lift an ack early"
// path: the row goes away, and deleting an id that's already gone is a
// no-op, not an error (the DeleteSilence convention).
func TestDeleteAckRemovesRowAndIsIdempotent(t *testing.T) {
	s := newTestStore(t, nil)
	id, err := s.AddAck(OverviewAck{Kind: "unhealthy", Entity: "sonarr", Until: 3000, CreatedAt: 0})
	require.NoError(t, err)
	require.NoError(t, s.DeleteAck(id))

	got, err := s.Acks(context.Background(), 0)
	require.NoError(t, err)
	require.Empty(t, got)

	require.NoError(t, s.DeleteAck(id), "deleting an already-gone id must be a no-op")
}
