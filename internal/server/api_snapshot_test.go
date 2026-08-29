package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// handBuiltSnapshot returns a fixed two-container snapshot closure, the
// same hand-assembled shape main wiring would build from store.Live() +
// the docker/unraid collectors — but with no store involved, so this
// package's tests stay decoupled from store's actual schema. Unraid
// carries both "array" and "docker" entities to exercise the v2 entity
// dimension; Sources is populated to exercise the frame-carries-sources
// change.
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
					State:        "running",
					Health:       "healthy",
					Image:        "jellyfin/jellyfin:latest",
					Created:      1735600000,
					UpdateStatus: "available",
					ChangelogURL: "https://github.com/jellyfin/jellyfin-packaging/releases",
					ProjectURL:   "https://jellyfin.org",
					WebUIURL:     "http://[IP]:[PORT:8096]/",
					Networks:     []NetworkInfoDTO{{Name: "bridge", IP: "172.17.0.2"}},
					Ports:        []PortInfoDTO{{ContainerPort: 8096, Proto: "tcp", HostIP: "0.0.0.0", HostPort: 8096}},
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
			UnraidVersion: "6.12.10",
			Unraid: map[string]map[string]float64{
				"array":  {"parity.progress_pct": 0},
				"docker": {"docker.images_bytes": 12e9},
			},
			GPU: map[string]map[string]float64{
				"gpu0": {"engine.render.busy_pct": 5.5},
			},
			Sources: map[string]string{
				"host":   "ok",
				"docker": "ok",
			},
		}
	}
}

// handBuiltContainers returns the /api/containers list-only shape
// (name/state/health/image, no metrics) mirroring what main wiring builds
// straight from dc.Running() — deliberately a DIFFERENT container set
// than handBuiltSnapshot's, so tests can't pass by accidentally reading
// the wrong closure.
func handBuiltContainers() func() []ContainerInfo {
	return func() []ContainerInfo {
		return []ContainerInfo{
			{Name: "jellyfin", State: "running", Health: "healthy", Image: "jellyfin/jellyfin:latest"},
			{Name: "sonarr", State: "running", Health: "", Image: "linuxserver/sonarr:latest"},
		}
	}
}

func TestSnapshotEndpointReturnsAssembledDTO(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now(), Snapshot: handBuiltSnapshot()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/live/snapshot")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var got SnapshotDTO
	require.NoError(t, json.Unmarshal(body, &got))

	require.Equal(t, int64(1735689600), got.TS)
	require.Equal(t, 12.5, got.Host["cpu.total"])
	require.Equal(t, 60.0, got.Host["mem.used_pct"])
	require.Len(t, got.Containers, 2)
	require.Equal(t, "running", got.Containers["jellyfin"].State)
	require.Equal(t, "healthy", got.Containers["jellyfin"].Health)
	require.Equal(t, "jellyfin/jellyfin:latest", got.Containers["jellyfin"].Image)
	require.Equal(t, 4.2, got.Containers["jellyfin"].Metrics["cpu.pct"])
	require.Equal(t, int64(1735600000), got.Containers["jellyfin"].Created)
	require.Equal(t, "available", got.Containers["jellyfin"].UpdateStatus)
	require.Equal(t, "https://github.com/jellyfin/jellyfin-packaging/releases", got.Containers["jellyfin"].ChangelogURL)
	require.Equal(t, "https://jellyfin.org", got.Containers["jellyfin"].ProjectURL)
	require.Equal(t, "http://[IP]:[PORT:8096]/", got.Containers["jellyfin"].WebUIURL)
	require.Equal(t, []NetworkInfoDTO{{Name: "bridge", IP: "172.17.0.2"}}, got.Containers["jellyfin"].Networks)
	require.Equal(t, []PortInfoDTO{{ContainerPort: 8096, Proto: "tcp", HostIP: "0.0.0.0", HostPort: 8096}}, got.Containers["jellyfin"].Ports)
	require.Equal(t, "exited", got.Containers["radarr"].State)

	// JSON-level, not just typed-decode: decoding an absent key and
	// decoding a present-but-empty "[]" both leave a Go slice field at
	// its nil zero value, so asserting on got.Containers[...].Networks
	// here can't actually tell "omitted" apart from "[]" -- only the raw
	// bytes can. ContainerDTO's frame fields are documented to omit
	// empty collections entirely (see its own doc comment), unlike an
	// endpoint DTO such as StorageDTO, whose own JSON-level test
	// (TestStorageEndpointEmptyMountsAndDevicesMarshalAsEmptyArrays) pins
	// the opposite convention for the same reason -- omission is cheaper
	// per-container on a frame shipping dozens of them every tick.
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &envelope))
	var containers map[string]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(envelope["containers"], &containers))

	_, present := containers["radarr"]["networks"]
	require.False(t, present, "a container with no wired network info must omit the \"networks\" key entirely, not marshal it as null or []")
	_, present = containers["radarr"]["ports"]
	require.False(t, present, "same omission for \"ports\"")

	jellyfinNetworks, present := containers["jellyfin"]["networks"]
	require.True(t, present, "a container WITH wired network info must carry the key")
	require.JSONEq(t, `[{"name":"bridge","ip":"172.17.0.2"}]`, string(jellyfinNetworks))

	require.Equal(t, 42.0, got.Disks["sda"]["used_pct"])
	require.Equal(t, "6.12.10", got.UnraidVersion)
	require.Equal(t, 5.5, got.GPU["gpu0"]["engine.render.busy_pct"])

	// v2: Unraid is entity-dimensioned -- "array" and "docker" provenance
	// must land in separate buckets, not collide into one flat map.
	require.Equal(t, 0.0, got.Unraid["array"]["parity.progress_pct"])
	require.Equal(t, 12e9, got.Unraid["docker"]["docker.images_bytes"])
	require.Len(t, got.Unraid, 2, "array and docker must stay in separate entity buckets")

	// v2: Sources now rides in the frame itself, not just healthz.
	require.Equal(t, "ok", got.Sources["host"])
	require.Equal(t, "ok", got.Sources["docker"])
}

func TestSnapshotEndpointEmptyObjectWhenNotWired(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()}) // Snapshot left nil, as Phase 1 callers do
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/live/snapshot")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var got map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Empty(t, got)
}

// TestContainersEndpointReturnsWiredList pins the v2 contract: /api/
// containers serves Options.Containers directly (main wiring's dc.
// Running() straight through), as a JSON array of {name,state,health,
// image} -- not a detour through the snapshot DTO.
func TestContainersEndpointReturnsWiredList(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now(), Containers: handBuiltContainers()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var got []ContainerInfo
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, []ContainerInfo{
		{Name: "jellyfin", State: "running", Health: "healthy", Image: "jellyfin/jellyfin:latest"},
		{Name: "sonarr", State: "running", Health: "", Image: "linuxserver/sonarr:latest"},
	}, got)
}

// TestContainersEndpointIgnoresSnapshotWhenBothWired confirms the "no DTO
// detour" half of the contract directly: with both Options.Snapshot and
// Options.Containers wired to deliberately DIFFERENT container sets,
// /api/containers must reflect Containers, never Snapshot().Containers.
func TestContainersEndpointIgnoresSnapshotWhenBothWired(t *testing.T) {
	s := New(Options{
		Version: "test-1", Started: time.Now(),
		Snapshot:   handBuiltSnapshot(),
		Containers: handBuiltContainers(),
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var got []ContainerInfo
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	names := make([]string, len(got))
	for i, c := range got {
		names[i] = c.Name
	}
	require.ElementsMatch(t, []string{"jellyfin", "sonarr"}, names,
		"must come from Options.Containers (sonarr), not Options.Snapshot().Containers (radarr)")
}

func TestContainersEndpointEmptyArrayWhenNotWired(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got []ContainerInfo
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
	defer func() { _ = resp.Body.Close() }()
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
	defer func() { _ = resp.Body.Close() }()

	var body struct {
		Sources map[string]string `json:"sources"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.NotNil(t, body.Sources)
	require.Empty(t, body.Sources)
}
