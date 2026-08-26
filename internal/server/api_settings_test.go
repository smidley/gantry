package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeSettings is a minimal in-memory SettingsIface: Get/Set operate on
// the same map[string]int a real store-backed adapter would resolve
// from config, plus a fixed set of env-overridden field names — the
// test double every /api/settings test in this file wires in place of
// main's real store+config adapter.
type fakeSettings struct {
	values     map[string]int
	overridden map[string]bool
	setCalls   []struct {
		Field string
		Value int
	}
	setErr error
}

func newFakeSettings() *fakeSettings {
	return &fakeSettings{
		values: map[string]int{
			"r1_hours":    48,
			"r2_days":     30,
			"r3_days":     390,
			"size_cap_mb": 512,
		},
		overridden: map[string]bool{},
	}
}

func (f *fakeSettings) Get() (RetentionSettings, map[string]bool) {
	return RetentionSettings{
		R1Hours:   f.values["r1_hours"],
		R2Days:    f.values["r2_days"],
		R3Days:    f.values["r3_days"],
		SizeCapMB: f.values["size_cap_mb"],
	}, f.overridden
}

func (f *fakeSettings) Set(field string, value int) error {
	f.setCalls = append(f.setCalls, struct {
		Field string
		Value int
	}{field, value})
	if f.setErr != nil {
		return f.setErr
	}
	f.values[field] = value
	return nil
}

func putSettings(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewBufferString(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

// validRetentionBody is a full, in-range retention object matching
// newFakeSettings' defaults — the baseline every mutation test tweaks
// one field of.
const validRetentionBody = `{"retention":{"r1_hours":48,"r2_days":30,"r3_days":390,"size_cap_mb":512}}`

func TestSettingsGetReturnsCurrentRetentionAndEnvOverridden(t *testing.T) {
	fs := newFakeSettings()
	fs.overridden["r1_hours"] = true
	s := New(Options{Version: "test-1", Started: time.Now(), Settings: fs})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/settings")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Retention     RetentionSettings `json:"retention"`
		EnvOverridden []string          `json:"env_overridden"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, RetentionSettings{R1Hours: 48, R2Days: 30, R3Days: 390, SizeCapMB: 512}, body.Retention)
	require.Equal(t, []string{"r1_hours"}, body.EnvOverridden)
}

func TestSettingsPutRoundtripsThroughGet(t *testing.T) {
	fs := newFakeSettings()
	s := New(Options{Version: "test-1", Started: time.Now(), Settings: fs})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/settings", `{"retention":{"r1_hours":72,"r2_days":14,"r3_days":400,"size_cap_mb":1024}}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	getResp, err := http.Get(ts.URL + "/api/settings")
	require.NoError(t, err)
	defer func() { _ = getResp.Body.Close() }()
	var body struct {
		Retention RetentionSettings `json:"retention"`
	}
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&body))
	require.Equal(t, RetentionSettings{R1Hours: 72, R2Days: 14, R3Days: 400, SizeCapMB: 1024}, body.Retention)

	require.Len(t, fs.setCalls, 4, "all four whitelisted fields must be persisted via Set")
}

func TestSettingsPutRejectsUnknownFieldInBody(t *testing.T) {
	fs := newFakeSettings()
	s := New(Options{Version: "test-1", Started: time.Now(), Settings: fs})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// "port" is a real top-level config key elsewhere, but not one of
	// the four whitelisted retention fields -- a client mistakenly
	// trying to change it through this endpoint must be rejected, not
	// silently ignored.
	resp := putSettings(t, ts.URL+"/api/settings", `{"retention":{"r1_hours":48,"r2_days":30,"r3_days":390,"size_cap_mb":512},"port":9999}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fs.setCalls, "an unknown field must reject the whole PUT before anything is persisted")
}

func TestSettingsPutRejectsUnknownFieldNestedInRetention(t *testing.T) {
	fs := newFakeSettings()
	s := New(Options{Version: "test-1", Started: time.Now(), Settings: fs})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/settings", `{"retention":{"r1_hours":48,"r2_days":30,"r3_days":390,"size_cap_mb":512,"extra_field":1}}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fs.setCalls)
}

func TestSettingsPutRangeValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"r1_hours too low", `{"retention":{"r1_hours":0,"r2_days":30,"r3_days":390,"size_cap_mb":512}}`},
		{"r1_hours too high", `{"retention":{"r1_hours":169,"r2_days":30,"r3_days":390,"size_cap_mb":512}}`},
		{"r2_days too low", `{"retention":{"r1_hours":48,"r2_days":0,"r3_days":390,"size_cap_mb":512}}`},
		{"r2_days too high", `{"retention":{"r1_hours":48,"r2_days":91,"r3_days":390,"size_cap_mb":512}}`},
		{"r3_days too low", `{"retention":{"r1_hours":48,"r2_days":30,"r3_days":29,"size_cap_mb":512}}`},
		{"r3_days too high", `{"retention":{"r1_hours":48,"r2_days":30,"r3_days":1096,"size_cap_mb":512}}`},
		{"size_cap_mb too low", `{"retention":{"r1_hours":48,"r2_days":30,"r3_days":390,"size_cap_mb":63}}`},
		{"size_cap_mb too high", `{"retention":{"r1_hours":48,"r2_days":30,"r3_days":390,"size_cap_mb":4097}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := newFakeSettings()
			s := New(Options{Version: "test-1", Started: time.Now(), Settings: fs})
			ts := httptest.NewServer(s.Handler())
			defer ts.Close()

			resp := putSettings(t, ts.URL+"/api/settings", tc.body)
			defer func() { _ = resp.Body.Close() }()
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			require.Empty(t, fs.setCalls, "an out-of-range field must reject the whole PUT")

			var errBody struct {
				Error  string            `json:"error"`
				Fields map[string]string `json:"fields"`
			}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&errBody))
			require.NotEmpty(t, errBody.Fields, "a range failure must name the offending field")
		})
	}
}

func TestSettingsPutBoundaryValuesAreAccepted(t *testing.T) {
	fs := newFakeSettings()
	s := New(Options{Version: "test-1", Started: time.Now(), Settings: fs})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/settings", `{"retention":{"r1_hours":1,"r2_days":1,"r3_days":30,"size_cap_mb":64}}`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode, "the documented range minimums must be accepted, not rejected")

	fs2 := newFakeSettings()
	s2 := New(Options{Version: "test-1", Started: time.Now(), Settings: fs2})
	ts2 := httptest.NewServer(s2.Handler())
	defer ts2.Close()

	resp2 := putSettings(t, ts2.URL+"/api/settings", `{"retention":{"r1_hours":168,"r2_days":90,"r3_days":1095,"size_cap_mb":4096}}`)
	defer func() { _ = resp2.Body.Close() }()
	require.Equal(t, http.StatusOK, resp2.StatusCode, "the documented range maximums must be accepted, not rejected")
}

func TestSettingsPutRejectsEnvOverriddenFieldsWith409(t *testing.T) {
	fs := newFakeSettings()
	fs.overridden["r1_hours"] = true
	fs.overridden["size_cap_mb"] = true
	s := New(Options{Version: "test-1", Started: time.Now(), Settings: fs})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/settings", validRetentionBody)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	require.Empty(t, fs.setCalls, "an env-locked field must reject the whole PUT before anything is persisted")

	var body struct {
		Error         string   `json:"error"`
		EnvOverridden []string `json:"env_overridden"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.ElementsMatch(t, []string{"r1_hours", "size_cap_mb"}, body.EnvOverridden, "the 409 body must name every locked field")
}

func TestSettingsGetNilOptionReturnsEmptyNotPanic(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()}) // Settings left nil
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/settings")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Retention     RetentionSettings `json:"retention"`
		EnvOverridden []string          `json:"env_overridden"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, RetentionSettings{}, body.Retention)
	require.Empty(t, body.EnvOverridden)
}

func TestSettingsPutNilOptionReturns404(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()}) // Settings left nil
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/settings", validRetentionBody)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestSettingsPutMalformedBodyReturns400(t *testing.T) {
	fs := newFakeSettings()
	s := New(Options{Version: "test-1", Started: time.Now(), Settings: fs})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/settings", `not json`)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Empty(t, fs.setCalls)
}

// TestSettingsPutPropagatesSetError proves a Set failure surfaces as a
// 500 rather than a false-success 200 -- the same "don't swallow the
// backing store's own error" contract every other handler in this
// package follows for its optional closures.
func TestSettingsPutPropagatesSetError(t *testing.T) {
	fs := newFakeSettings()
	fs.setErr = fmt.Errorf("disk full")
	s := New(Options{Version: "test-1", Started: time.Now(), Settings: fs})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := putSettings(t, ts.URL+"/api/settings", validRetentionBody)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}
