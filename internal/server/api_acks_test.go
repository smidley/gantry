package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

// fakeAcks is a minimal in-memory AcksIface double -- the fakeAlerts
// convention (api_alerts_test.go), just small enough for three routes.
type fakeAcks struct {
	acks   []store.OverviewAck
	nextID int64

	acksErr, addErr, deleteErr error

	deleted []int64
}

func newFakeAcks() *fakeAcks { return &fakeAcks{nextID: 1} }

func (f *fakeAcks) Acks(context.Context) ([]store.OverviewAck, error) {
	return f.acks, f.acksErr
}

func (f *fakeAcks) AddAck(a store.OverviewAck) (store.OverviewAck, error) {
	if f.addErr != nil {
		return store.OverviewAck{}, f.addErr
	}
	a.ID = f.nextID
	f.nextID++
	f.acks = append(f.acks, a)
	return a, nil
}

func (f *fakeAcks) DeleteAck(id int64) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, id)
	return nil
}

func postAck(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, url+"/api/acks", bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(requestedWithHeader, requestedWithValue) // gate.go's cross-site check, satisfied the way the SPA does
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func TestAcksGetNilOptionsReturnsEmptyList(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/acks")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out struct {
		Acks []OverviewAckDTO `json:"acks"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.NotNil(t, out.Acks, "empty must be a real [] on the wire, not null")
	require.Empty(t, out.Acks)
}

func TestAcksPostAndGetRoundTrip(t *testing.T) {
	fa := newFakeAcks()
	s := New(Options{Version: "test-1", Started: time.Now(), Acks: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	before := time.Now().Unix()
	resp := postAck(t, ts.URL, map[string]any{"kind": "disk-usage", "entity": "disk3", "hours": 24})
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created OverviewAckDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	require.Equal(t, int64(1), created.ID)
	require.Equal(t, "disk-usage", created.Kind)
	require.Equal(t, "disk3", created.Entity)
	// until = created_at + 24h exactly, and created_at is "now" as the
	// HANDLER resolved it (never client-supplied).
	require.GreaterOrEqual(t, created.CreatedAt, before)
	require.Equal(t, created.CreatedAt+24*3600, created.Until)

	getResp, err := http.Get(ts.URL + "/api/acks")
	require.NoError(t, err)
	defer func() { _ = getResp.Body.Close() }()
	var out struct {
		Acks []OverviewAckDTO `json:"acks"`
	}
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&out))
	require.Equal(t, []OverviewAckDTO{created}, out.Acks)
}

// TestAcksPostRejectsBadRequests pins every 400 path: hour bounds (the
// 1h-7d UI presets' own range), the closed kind vocabulary ("alert" is
// deliberately NOT in it -- an alert callout's ack is a silence, one
// mechanism per system), and the no-global-shape rule (entity required,
// kind+entity both blank has no "scope":"all" escape hatch the way
// silences do -- the shape simply does not exist).
func TestAcksPostRejectsBadRequests(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
	}{
		{"hours too small", map[string]any{"kind": "unhealthy", "entity": "sonarr", "hours": 0}},
		{"hours too large", map[string]any{"kind": "unhealthy", "entity": "sonarr", "hours": maxAckHours + 1}},
		{"unknown kind", map[string]any{"kind": "not-a-kind", "entity": "sonarr", "hours": 1}},
		{"alert kind routes to silences, never here", map[string]any{"kind": "alert", "entity": "sonarr", "hours": 1}},
		{"stopped is no longer an anomaly kind", map[string]any{"kind": "stopped", "entity": "sonarr", "hours": 1}},
		{"empty entity", map[string]any{"kind": "unhealthy", "entity": "", "hours": 1}},
		{"empty kind and entity (no global ack shape)", map[string]any{"kind": "", "entity": "", "hours": 1}},
		{"unknown field", map[string]any{"kind": "unhealthy", "entity": "sonarr", "hours": 1, "scope": "all"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fa := newFakeAcks()
			s := New(Options{Version: "test-1", Started: time.Now(), Acks: fa})
			ts := httptest.NewServer(s.Handler())
			defer ts.Close()

			resp := postAck(t, ts.URL, tc.body)
			defer func() { _ = resp.Body.Close() }()
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			require.Empty(t, fa.acks, "a rejected request must never reach AddAck")
		})
	}
}

func TestAcksPostNilOptionsReturns404(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postAck(t, ts.URL, map[string]any{"kind": "unhealthy", "entity": "sonarr", "hours": 1})
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestAcksDelete(t *testing.T) {
	fa := newFakeAcks()
	s := New(Options{Version: "test-1", Started: time.Now(), Acks: fa})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/acks/%d", ts.URL, 7), nil)
	require.NoError(t, err)
	req.Header.Set(requestedWithHeader, requestedWithValue)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Equal(t, []int64{7}, fa.deleted)

	badReq, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/acks/not-a-number", nil)
	require.NoError(t, err)
	badReq.Header.Set(requestedWithHeader, requestedWithValue)
	badResp, err := http.DefaultClient.Do(badReq)
	require.NoError(t, err)
	defer func() { _ = badResp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, badResp.StatusCode)
}
