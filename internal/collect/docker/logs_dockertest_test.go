//go:build dockertest

// Integration test against a real local docker daemon — see docker_test.go
// for the tag's rationale and invocation.
package docker

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/smidley/gantry/internal/server"
	"github.com/stretchr/testify/require"
)

// TestContainerLogsEndpointAgainstRealDaemon exercises Task 9's full
// path -- real daemon, real *Collector, wired through server.Options.Logs
// exactly the way main.go wires it -- over real HTTP, not just
// StreamLogs called directly.
func TestContainerLogsEndpointAgainstRealDaemon(t *testing.T) {
	rawCli, err := client.NewClientWithOpts(client.WithHost("unix:///var/run/docker.sock"), client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := rawCli.Ping(ctx); err != nil {
		t.Skip("no local docker daemon reachable: " + err.Error())
	}

	ensureImage(t, ctx, rawCli, dockertestImage)

	const name = "gantry-dockertest-logs"
	created, err := rawCli.ContainerCreate(ctx,
		&container.Config{Image: dockertestImage, Cmd: []string{"sh", "-c", "echo line1; echo line2; echo line3; sleep 30"}},
		&container.HostConfig{AutoRemove: true},
		nil, nil, name)
	require.NoError(t, err)
	require.NoError(t, rawCli.ContainerStart(ctx, created.ID, container.StartOptions{}))
	t.Cleanup(func() {
		timeout := 1
		_ = rawCli.ContainerStop(context.Background(), created.ID, container.StopOptions{Timeout: &timeout})
	})

	sink := newFakeSink()
	dc := New(sink, &fakeEventSink{}, func(string, string) {}, "/var/run/docker.sock")
	t.Cleanup(dc.Drain)

	st := dc.Probe(ctx)
	require.True(t, st.Available, "probe: %s", st.Detail)

	require.Eventually(t, func() bool {
		require.NoError(t, dc.Tick(ctx, time.Now()))
		_, ok := dc.LookupByName(name)
		return ok
	}, 20*time.Second, 500*time.Millisecond, "%s never appeared in the registry", name)

	srv := server.New(server.Options{Version: "test", Started: time.Now(), Logs: dc.StreamLogs})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var status int
	var contentType, body string
	require.Eventually(t, func() bool {
		resp, err := http.Get(ts.URL + "/api/containers/" + name + "/logs?tail=500")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		status = resp.StatusCode
		contentType = resp.Header.Get("Content-Type")
		body = string(b)

		lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
		return status == http.StatusOK && len(lines) >= 2
	}, 10*time.Second, 200*time.Millisecond, "logs endpoint for %s never returned at least two lines", name)

	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "text/plain; charset=utf-8", contentType)
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	require.GreaterOrEqual(t, len(lines), 2, "expected at least two lines, got: %q", body)
	require.Contains(t, body, "line1")
	require.Contains(t, body, "line2")
}

// tickingLoggerCmd is the shell one-liner both follow tests run: one
// line a second, counter starting at zero. The reset back to "line 0" is
// the point -- it makes "these lines came from the run AFTER the
// interruption" unambiguous in the captured stream, with no clock or
// ordering inference needed.
var tickingLoggerCmd = []string{"sh", "-c", "i=0; while true; do echo line $i; i=$((i+1)); sleep 1; done"}

// startTickingLogger creates and starts one ticking-logger container
// under name, returning its id. Deliberately NOT AutoRemove: the restart
// test needs the container to survive its own exit.
func startTickingLogger(t *testing.T, ctx context.Context, cli *client.Client, name string) string {
	t.Helper()
	created, err := cli.ContainerCreate(ctx,
		&container.Config{Image: dockertestImage, Cmd: tickingLoggerCmd},
		&container.HostConfig{}, nil, nil, name)
	require.NoError(t, err)
	require.NoError(t, cli.ContainerStart(ctx, created.ID, container.StartOptions{}))
	return created.ID
}

// removeTickingLogger force-removes name, tolerating its absence -- both
// a cleanup and (in the re-create test) a deliberate step.
func removeTickingLogger(cli *client.Client, name string) {
	_ = cli.ContainerRemove(context.Background(), name, container.RemoveOptions{Force: true})
}

// keepRegistryFresh runs the collector's inventory refresh on a tight
// loop for the rest of the test. Production does this on Tick's own 10s
// inventoryInterval, which a test that re-creates a container mid-follow
// would otherwise have to sit through before the replacement's new id
// became resolvable at all.
func keepRegistryFresh(t *testing.T, ctx context.Context, dc *Collector) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		tk := time.NewTicker(300 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				_ = dc.refreshInventory(ctx, time.Now())
			}
		}
	}()
	t.Cleanup(func() { <-done })
}

// captureStream drains rc in the background, returning a func that
// reports everything received so far. Polling a snapshot rather than
// blocking on a read is what lets these tests use require.Eventually
// generously: a follow stream legitimately has nothing to say for a
// second at a time, and on a loaded machine rather longer.
func captureStream(t *testing.T, rc io.ReadCloser) func() string {
	t.Helper()
	got, _ := readWithin(t, rc)
	return got
}

// eachLineOnce asserts that no line repeats inside one run's slice of a
// capture. Since the counter restarts at zero on every run, a repeat
// WITHIN a run can only mean the re-attach re-delivered records the
// caller already had -- exactly what the Since boundary exists to
// prevent.
func eachLineOnce(t *testing.T, label, segment string) {
	t.Helper()
	seen := map[string]int{}
	for _, line := range strings.Split(strings.TrimRight(segment, "\n"), "\n") {
		if line == "" || line == strings.TrimRight(restartMarker, "\n") {
			continue
		}
		seen[line]++
	}
	require.NotEmpty(t, seen, "%s: expected at least one line", label)
	for line, n := range seen {
		require.Equal(t, 1, n, "%s: %q delivered %d times in a single run", label, line, n)
	}
}

// followUntilFirstLines opens a follow stream on name and waits until
// the capture shows the container's first two lines, so the caller knows
// the pre-interruption half of the stream is genuinely established
// before it interrupts anything.
func followUntilFirstLines(t *testing.T, ctx context.Context, dc *Collector, name string) (func() string, io.ReadCloser) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, ok := dc.LookupByName(name)
		return ok
	}, 30*time.Second, 200*time.Millisecond, "%s never appeared in the registry", name)

	rc, err := dc.StreamLogs(ctx, name, true, 100)
	require.NoError(t, err)

	got := captureStream(t, rc)
	require.Eventually(t, func() bool {
		return strings.Contains(got(), "line 0\n") && strings.Contains(got(), "line 1\n")
	}, 30*time.Second, 200*time.Millisecond, "%s never produced its first lines; got %q", name, got())
	return got, rc
}

// followCollector builds a probed collector with a live registry, the
// same wiring main.go gives the real one minus the metric sinks nothing
// here reads.
func followCollector(t *testing.T, ctx context.Context) *Collector {
	t.Helper()
	dc := New(newFakeSink(), &fakeEventSink{}, func(string, string) {}, "/var/run/docker.sock")
	t.Cleanup(dc.Drain)
	require.True(t, dc.Probe(ctx).Available)
	keepRegistryFresh(t, ctx, dc)
	return dc
}

// TestFollowLogsSurvivesContainerRestartAgainstRealDaemon is the
// user-reported bug, end to end against a real daemon: the daemon ends a
// follow stream when the container exits, so before the re-attach loop
// this stream went silent at the restart and never came back. Lines from
// both runs must arrive on the SAME reader, in order, with the boundary
// re-delivering nothing.
func TestFollowLogsSurvivesContainerRestartAgainstRealDaemon(t *testing.T) {
	rawCli, err := client.NewClientWithOpts(client.WithHost("unix:///var/run/docker.sock"), client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if _, err := rawCli.Ping(ctx); err != nil {
		t.Skip("no local docker daemon reachable: " + err.Error())
	}
	ensureImage(t, ctx, rawCli, dockertestImage)

	const name = "gantry-dockertest-logrestart"
	removeTickingLogger(rawCli, name)
	id := startTickingLogger(t, ctx, rawCli, name)
	t.Cleanup(func() { removeTickingLogger(rawCli, name) })

	dc := followCollector(t, ctx)
	got, rc := followUntilFirstLines(t, ctx, dc, name)
	defer func() { _ = rc.Close() }()

	beforeRestart := got()
	timeout := 1
	require.NoError(t, rawCli.ContainerRestart(ctx, id, container.StopOptions{Timeout: &timeout}))

	require.Eventually(t, func() bool {
		_, after, found := strings.Cut(got(), restartMarker)
		return found && strings.Contains(after, "line 0\n")
	}, 60*time.Second, 250*time.Millisecond, "the stream never resumed after the restart; got %q", got())

	out := got()
	before, after, _ := strings.Cut(out, restartMarker)
	require.Contains(t, before, "line 0\n", "the pre-restart run's output must still be there")
	require.Contains(t, before, "line 1\n")
	require.Contains(t, beforeRestart, "line 0\n", "sanity: the capture really did have output before the restart was issued")
	require.Contains(t, after, "line 0\n", "the post-restart run starts its counter over, which is what marks it as the new run")
	require.Equal(t, 1, strings.Count(out, restartMarker), "exactly one resume marker for one restart: %q", out)
	eachLineOnce(t, "pre-restart run", before)
	eachLineOnce(t, "post-restart run", after)
}

// TestFollowLogsSurvivesContainerRecreateAgainstRealDaemon covers the
// secondary defect the same way: an Unraid container UPDATE re-creates
// the container under the same name with a NEW id, so a re-attach keyed
// on the id the follow first resolved would target a container that no
// longer exists. Only re-resolving the NAME every attach survives this.
func TestFollowLogsSurvivesContainerRecreateAgainstRealDaemon(t *testing.T) {
	rawCli, err := client.NewClientWithOpts(client.WithHost("unix:///var/run/docker.sock"), client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if _, err := rawCli.Ping(ctx); err != nil {
		t.Skip("no local docker daemon reachable: " + err.Error())
	}
	ensureImage(t, ctx, rawCli, dockertestImage)

	const name = "gantry-dockertest-logrecreate"
	removeTickingLogger(rawCli, name)
	oldID := startTickingLogger(t, ctx, rawCli, name)
	t.Cleanup(func() { removeTickingLogger(rawCli, name) })

	dc := followCollector(t, ctx)
	got, rc := followUntilFirstLines(t, ctx, dc, name)
	defer func() { _ = rc.Close() }()

	// Remove and re-run under the same name: same identity to the user,
	// an entirely different container to the daemon.
	removeTickingLogger(rawCli, name)
	newID := startTickingLogger(t, ctx, rawCli, name)
	require.NotEqual(t, oldID, newID)

	require.Eventually(t, func() bool {
		_, after, found := strings.Cut(got(), restartMarker)
		return found && strings.Contains(after, "line 0\n")
	}, 60*time.Second, 250*time.Millisecond, "the stream never resumed onto the re-created container; got %q", got())

	m, ok := dc.LookupByName(name)
	require.True(t, ok)
	require.Equal(t, newID, m.ID, "the registry must have moved to the new id -- the follow resumed by re-resolving this name")

	out := got()
	before, after, _ := strings.Cut(out, restartMarker)
	eachLineOnce(t, "pre-recreate container", before)
	eachLineOnce(t, "post-recreate container", after)
}
