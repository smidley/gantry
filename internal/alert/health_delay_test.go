package alert

import (
	"context"
	"testing"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

func TestHealthDelaySuppressesBackupInterruptions(t *testing.T) {
	for _, tc := range []struct {
		name       string
		state      string
		health     string
		clearEvent bool
	}{
		{name: "healthy event", state: "running", health: "healthy", clearEvent: true},
		{name: "healthy inventory", state: "running", health: "healthy"},
		{name: "stopped for backup", state: "exited", health: "unhealthy"},
		{name: "restarting", state: "restarting", health: "unhealthy"},
		{name: "removed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := unhealthyRule()
			rule.ForSeconds = 60
			st := newFakeStore(rule)
			clk := &clockAt{t: 2_000_000_000}
			members := []FleetMember{{Name: "sonarr", State: "running", Health: "healthy"}}
			var notes []string
			eng := New(st, nil, nil, func() []FleetMember { return members },
				func(n AlertNotification) { notes = append(notes, n.Phase) }, clk.now)
			require.NoError(t, eng.Tick(context.Background()))

			members[0].Health = "unhealthy"
			_, err := st.AppendEvent(store.Event{Kind: "container.health", Entity: "sonarr", Severity: "warning"})
			require.NoError(t, err)
			require.NoError(t, eng.Tick(context.Background()))
			require.Equal(t, "pending", st.soleActive(t).State)
			require.Empty(t, notes)

			clk.t += 30
			members = nil
			if tc.state != "" {
				members = []FleetMember{{Name: "sonarr", State: tc.state, Health: tc.health}}
			}
			if tc.clearEvent {
				_, err = st.AppendEvent(store.Event{Kind: "container.health", Entity: "sonarr", Severity: "info"})
				require.NoError(t, err)
			}
			require.NoError(t, eng.Tick(context.Background()))
			clk.t += 90
			require.NoError(t, eng.Tick(context.Background()))
			require.Zero(t, st.activeCount())
			require.Empty(t, notes, "neither an unhealthy nor a recovery notification should be sent")
			require.NotContains(t, st.eventKinds(), "alert.fired")
			require.NotContains(t, st.eventKinds(), "alert.resolved")

			// A later failure must start a fresh window, not inherit the
			// elapsed time of the interrupted backup alert.
			members = []FleetMember{{Name: "sonarr", State: "running", Health: "unhealthy"}}
			_, err = st.AppendEvent(store.Event{Kind: "container.health", Entity: "sonarr", Severity: "warning"})
			require.NoError(t, err)
			require.NoError(t, eng.Tick(context.Background()))
			require.Equal(t, "pending", st.soleActive(t).State)
			clk.t += 59
			require.NoError(t, eng.Tick(context.Background()))
			require.Empty(t, notes)
			clk.t++
			require.NoError(t, eng.Tick(context.Background()))
			require.Equal(t, []string{"fired"}, notes)
		})
	}
}

func TestHealthDelayNotifiesSustainedFailureAndRecovery(t *testing.T) {
	rule := unhealthyRule()
	rule.ForSeconds = 60
	st := newFakeStore(rule)
	clk := &clockAt{t: 2_000_000_000}
	members := []FleetMember{{Name: "sonarr", State: "running", Health: "unhealthy"}}
	var notes []string
	eng := New(st, nil, nil, func() []FleetMember { return members },
		func(n AlertNotification) { notes = append(notes, n.Phase) }, clk.now)
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, "pending", st.soleActive(t).State)
	clk.t += 59
	require.NoError(t, eng.Tick(context.Background()))
	require.Empty(t, notes)
	clk.t++
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, "firing", st.soleActive(t).State)
	require.Equal(t, []string{"fired"}, notes)
	clk.t += 10
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, []string{"fired"}, notes, "sustained health must not act like restart probation")
	members[0].Health = "healthy"
	_, err := st.AppendEvent(store.Event{Kind: "container.health", Entity: "sonarr", Severity: "info"})
	require.NoError(t, err)
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, []string{"fired", "resolved"}, notes)
}

func TestHealthDelayDoesNotCountGantryDowntime(t *testing.T) {
	rule := unhealthyRule()
	rule.ForSeconds = 60
	st := newFakeStore(rule)
	clk := &clockAt{t: 2_000_000_000}
	fleet := func() []FleetMember { return []FleetMember{{Name: "sonarr", State: "running", Health: "unhealthy"}} }
	var notes []string
	dispatch := func(n AlertNotification) { notes = append(notes, n.Phase) }
	eng := New(st, nil, nil, fleet, dispatch, clk.now)
	require.NoError(t, eng.Tick(context.Background()))
	clk.t += 3600
	eng = New(st, nil, nil, fleet, dispatch, clk.now)
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, "pending", st.soleActive(t).State)
	require.Equal(t, clk.t, st.soleActive(t).StartedAt)
	require.Empty(t, notes)
	clk.t += 60
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, []string{"fired"}, notes)
}

func TestStoppedContainerDoesNotNotifyEvenWithZeroHealthDelay(t *testing.T) {
	st := newFakeStore(unhealthyRule())
	clk := &clockAt{t: 2_000_000_000}
	var notes []string
	eng := New(st, nil, nil, func() []FleetMember {
		return []FleetMember{{Name: "sonarr", State: "exited", Health: "unhealthy"}}
	}, func(n AlertNotification) { notes = append(notes, n.Phase) }, clk.now)
	require.NoError(t, eng.Tick(context.Background()))
	_, err := st.AppendEvent(store.Event{Kind: "container.health", Entity: "sonarr", Severity: "warning"})
	require.NoError(t, err)
	require.NoError(t, eng.Tick(context.Background()))
	require.Zero(t, st.activeCount())
	require.Empty(t, notes)
	require.NotContains(t, st.eventKinds(), "alert.fired")
}

func TestHealthFailureAndRecoveryInSameBatchStayQuiet(t *testing.T) {
	st := newFakeStore(unhealthyRule())
	clk := &clockAt{t: 2_000_000_000}
	var notes []string
	eng := New(st, nil, nil, nil, func(n AlertNotification) { notes = append(notes, n.Phase) }, clk.now)
	require.NoError(t, eng.Tick(context.Background()))
	for _, severity := range []string{"warning", "info"} {
		_, err := st.AppendEvent(store.Event{Kind: "container.health", Entity: "sonarr", Severity: severity})
		require.NoError(t, err)
	}
	require.NoError(t, eng.Tick(context.Background()))
	require.Zero(t, st.activeCount())
	require.Empty(t, notes)
	require.NotContains(t, st.eventKinds(), "alert.resolved")
}

func TestSilencedHealthAlertDoesNotSendOrphanRecovery(t *testing.T) {
	rule := unhealthyRule()
	st := newFakeStore(rule)
	st.silences = []store.Silence{{RuleID: rule.ID}}
	clk := &clockAt{t: 2_000_000_000}
	var notes []string
	eng := New(st, nil, nil, nil, func(n AlertNotification) { notes = append(notes, n.Phase) }, clk.now)
	require.NoError(t, eng.Tick(context.Background()))
	_, err := st.AppendEvent(store.Event{Kind: "container.health", Entity: "sonarr", Severity: "warning"})
	require.NoError(t, err)
	require.NoError(t, eng.Tick(context.Background()))
	require.Equal(t, "firing", st.soleActive(t).State)
	require.Zero(t, st.soleActive(t).NotifyCount)
	st.silences = nil
	clk.t++
	_, err = st.AppendEvent(store.Event{Kind: "container.health", Entity: "sonarr", Severity: "info"})
	require.NoError(t, err)
	require.NoError(t, eng.Tick(context.Background()))
	require.Empty(t, notes)
	require.Contains(t, st.eventKinds(), "alert.resolved", "the recovery is still recorded in history")
}
