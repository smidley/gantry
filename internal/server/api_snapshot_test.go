package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// handBuiltSnapshot returns a fixed two-container snapshot closure, the
// same hand-assembled shape main wiring would build from store.Live() +
// the docker/unraid collectors — but with no store involved, so this
// package's tests stay decoupled from store's actual schema.
func handBuiltSnapshot() func() SnapshotDTO {
	return func() SnapshotDTO {
		return SnapshotDTO{
			TS: 1735689600,
			Host: map[string]float64{
				"cpu.total":    12.5,
				"mem.used_pct": 60,
			},
			Containers: map[string]ContainerDTO{
				"jellyfin": {
					State:  "running",
					Health: "healthy",
					Image:  "jellyfin/jellyfin:latest",
					Metrics: map[string]float64{
						"cpu.pct":   4.2,
						"mem.bytes": 900e6,
					},
				},
				"radarr": {
					State:  "exited",
					Health: "",
					Image:  "linuxserver/radarr:latest",
					Metrics: map[string]float64{
						"cpu.pct": 0,
					},
				},
			},
			Disks: map[string]map[string]float64{
				"sda": {"used_pct": 42.0},
			},
			Unraid:        map[string]float64{"parity.progress_pct": 0},
			UnraidVersion: "6.12.10",
			GPU: map[string]map[string]float64{
				"gpu0": {"engine.render.busy_pct": 5.5},
			},
		}
	}
}

func TestSnapshotEndpointReturnsAssembledDTO(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now(), Snapshot: handBuiltSnapshot()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/live/snapshot")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var got SnapshotDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))

	require.Equal(t, int64(1735689600), got.TS)
	require.Equal(t, 12.5, got.Host["cpu.total"])
	require.Equal(t, 60.0, got.Host["mem.used_pct"])
	require.Len(t, got.Containers, 2)
	require.Equal(t, "running", got.Containers["jellyfin"].State)
	require.Equal(t, "healthy", got.Containers["jellyfin"].Health)
	require.Equal(t, "jellyfin/jellyfin:latest", got.Containers["jellyfin"].Image)
	require.Equal(t, 4.2, got.Containers["jellyfin"].Metrics["cpu.pct"])
	require.Equal(t, "exited", got.Containers["radarr"].State)
	require.Equal(t, 42.0, got.Disks["sda"]["used_pct"])
	require.Equal(t, "6.12.10", got.UnraidVersion)
	require.Equal(t, 5.5, got.GPU["gpu0"]["engine.render.busy_pct"])
}

func TestSnapshotEndpointEmptyObjectWhenNotWired(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()}) // Snapshot left nil, as Phase 1 callers do
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/live/snapshot")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var got map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Empty(t, got)
}

func TestContainersEndpointDerivesFromSnapshot(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now(), Snapshot: handBuiltSnapshot()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var got map[string]ContainerDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got, 2)
	require.Equal(t, "running", got["jellyfin"].State)
	require.Equal(t, "exited", got["radarr"].State)
	require.Equal(t, 0.0, got["radarr"].Metrics["cpu.pct"])
}

func TestContainersEndpointEmptyObjectWhenNotWired(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got map[string]ContainerDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Empty(t, got)
}

func TestHealthzSourcesPassthrough(t *testing.T) {
	sources := func() map[string]string {
		return map[string]string{
			"host":   "ok",
			"docker": "mount the docker socket read-only at /var/run/docker.sock",
		}
	}
	s := New(Options{Version: "test-1", Started: time.Now(), Sources: sources})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Sources map[string]string `json:"sources"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, sources(), body.Sources)
}

func TestHealthzSourcesEmptyMapWhenNotWired(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()}) // Sources left nil
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()

	var body struct {
		Sources map[string]string `json:"sources"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.NotNil(t, body.Sources)
	require.Empty(t, body.Sources)
}
