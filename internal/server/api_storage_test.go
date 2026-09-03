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

// storageCall records one Options.Storage invocation's argument, for
// asserting the path value reaches the closure the same way logsCall
// does for /api/containers/{name}/logs.
type storageCall struct {
	Name string
}

// handBuiltStorage returns a fake Options.Storage that records every
// call and answers ok for exactly one container name, the same
// single-fixture-name convention capturingLogs uses.
func handBuiltStorage(calls *[]storageCall, knownName string, dto StorageDTO) func(string) (StorageDTO, bool) {
	return func(name string) (StorageDTO, bool) {
		*calls = append(*calls, storageCall{Name: name})
		if name != knownName {
			return StorageDTO{}, false
		}
		return dto, true
	}
}

func TestStorageEndpointReturnsAssembledDTO(t *testing.T) {
	var calls []storageCall
	dto := StorageDTO{
		Mounts: []MountDTO{
			{Source: "/mnt/user/appdata/jellyfin", Destination: "/config", RW: true, Storage: StorageRefDTO{Kind: "share", Name: "appdata"}},
			{Source: "/mnt/cache/transcode", Destination: "/tmp", RW: true, Storage: StorageRefDTO{Kind: "pool", Name: "cache"}},
		},
		Devices: []DeviceIODTO{
			{Device: "sda", Label: "sda", Kind: "", ReadBps: 1024.5, WriteBps: 512},
			{Device: "nvme0n1", Label: "rocket_pool", Kind: "nvme", ReadBps: 2048, WriteBps: 256},
		},
	}
	s := New(Options{Version: "test-1", Started: time.Now(), Storage: handBuiltStorage(&calls, "jellyfin", dto)})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers/jellyfin/storage")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var got StorageDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, dto, got)

	require.Equal(t, []storageCall{{Name: "jellyfin"}}, calls)
}

// TestStorageEndpointUnknownContainerReturns404JSON pins the ok=false
// path: a name the closure doesn't recognize gets a 404 with an "error"
// body naming the container, same shape as every other unknown-name
// route in this package.
func TestStorageEndpointUnknownContainerReturns404JSON(t *testing.T) {
	var calls []storageCall
	s := New(Options{Version: "test-1", Started: time.Now(), Storage: handBuiltStorage(&calls, "jellyfin", StorageDTO{})})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers/ghost/storage")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Contains(t, body["error"], "ghost")
}

// TestStorageEndpointNilOptionReturns404 pins the fake-mode/not-wired
// contract, mirroring TestLogsEndpointNilOptionReturns404: with no
// docker.Collector wired at all (Options.Storage left nil), the route
// degrades to 404 rather than a panic or 5xx.
func TestStorageEndpointNilOptionReturns404(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()}) // Storage left nil
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers/anything/storage")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestStorageEndpointEmptyMountsAndDevicesMarshalAsEmptyArrays pins the
// nil-vs-empty-slice JSON shape: a container with no mounts and no
// per-device samples yet must serialize "mounts":[] / "devices":[], not
// "null" -- a client that unconditionally .length()s or .map()s the
// arrays shouldn't need a null check.
func TestStorageEndpointEmptyMountsAndDevicesMarshalAsEmptyArrays(t *testing.T) {
	var calls []storageCall
	s := New(Options{Version: "test-1", Started: time.Now(), Storage: handBuiltStorage(&calls, "bare", StorageDTO{Mounts: []MountDTO{}, Devices: []DeviceIODTO{}})})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers/bare/storage")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.JSONEq(t, `{"mounts":[],"devices":[]}`, string(body))
}

// TestStorageEndpointCarriesArrayRowsAndTheShfsFlag pins the two array
// additions on the wire: an array disk's Live IO row rides the same
// DeviceIODTO shape as a pool one (a slot Label plus its Kind, keyed by
// the slot's physical device, since that's the name host diskio.<dev>.*
// series use), and a share mount fronted by Unraid's shfs FUSE layer
// carries "shfs": true so the frontend can say why that mount's IO shows
// up in no device row at all. A mount whose IO IS attributable omits the
// key entirely rather than sending false.
func TestStorageEndpointCarriesArrayRowsAndTheShfsFlag(t *testing.T) {
	var calls []storageCall
	dto := StorageDTO{
		Mounts: []MountDTO{
			{Source: "/mnt/user/Movies", Destination: "/movies", Storage: StorageRefDTO{
				Kind: "share", Name: "Movies", Placement: &SharePlacementDTO{Mode: "no"}, Shfs: true}},
			{Source: "/mnt/disk7/media", Destination: "/media", Storage: StorageRefDTO{Kind: "disk", Name: "disk7"}},
		},
		Devices: []DeviceIODTO{
			{Device: "sdf", Label: "disk7", Kind: "hdd", ReadBps: 700, WriteBps: 1208},
		},
	}
	s := New(Options{Version: "test-1", Started: time.Now(), Storage: handBuiltStorage(&calls, "jellyfin", dto)})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/containers/jellyfin/storage")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), `"shfs":true`)
	require.NotContains(t, string(body), `"shfs":false`, "omitempty -- an attributable mount carries no flag at all")

	var got StorageDTO
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, dto, got)
}
