package docker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/errdefs"
	"github.com/stretchr/testify/require"
)

// summaryOfState builds a minimal container.Summary -- name (docker's own
// leading "/" included, as ContainerList really returns it), state, image
// ref, and labels, the fields classifyContainers actually reads.
func summaryOfState(id, name, state string) container.Summary {
	return container.Summary{ID: id, Names: []string{"/" + name}, Image: "app:latest", State: state}
}

func TestClassifyContainersIncludesExitedCreatedDead(t *testing.T) {
	cts := []container.Summary{
		summaryOfState("a", "one", "exited"),
		summaryOfState("b", "two", "created"),
		summaryOfState("c", "three", "dead"),
	}

	report := classifyContainers(cts)

	require.Len(t, report.Containers, 3)
}

// TestClassifyContainersExcludesRunningAndPaused pins the spec's explicit
// carve-out: paused is running-adjacent and must never appear in this
// list, same as running itself -- see ContainerMaintenanceInfo's own doc.
func TestClassifyContainersExcludesRunningAndPaused(t *testing.T) {
	cts := []container.Summary{
		summaryOfState("a", "running-one", "running"),
		summaryOfState("b", "paused-one", "paused"),
		summaryOfState("c", "exited-one", "exited"),
	}

	report := classifyContainers(cts)

	require.Len(t, report.Containers, 1)
	require.Equal(t, "exited-one", report.Containers[0].Name)
}

// TestClassifyContainersExcludesRestartingAndRemoving guards against
// silently widening the included-state set beyond the three the spec
// names: a transient state must stay excluded even though it's also
// "not running" in the literal sense.
func TestClassifyContainersExcludesRestartingAndRemoving(t *testing.T) {
	cts := []container.Summary{
		summaryOfState("a", "restarting-one", "restarting"),
		summaryOfState("b", "removing-one", "removing"),
	}

	report := classifyContainers(cts)

	require.Empty(t, report.Containers)
}

func TestClassifyContainersSummaryCountsMatchPerContainerStates(t *testing.T) {
	cts := []container.Summary{
		summaryOfState("a", "e1", "exited"),
		summaryOfState("b", "e2", "exited"),
		summaryOfState("c", "cr1", "created"),
		summaryOfState("d", "de1", "dead"),
		summaryOfState("e", "running1", "running"),
	}

	report := classifyContainers(cts)

	require.Equal(t, 2, report.Summary.Exited)
	require.Equal(t, 1, report.Summary.Created)
	require.Equal(t, 1, report.Summary.Dead)
}

func TestClassifyContainersManagedHintDockermanLabelTakesPriority(t *testing.T) {
	ct := summaryOfState("a", "one", "exited")
	ct.Labels = map[string]string{
		"net.unraid.docker.managed":  "true",
		"com.docker.compose.project": "mystack",
	}

	report := classifyContainers([]container.Summary{ct})

	require.Equal(t, "dockerman", report.Containers[0].Managed)
}

func TestClassifyContainersManagedHintComposeProjectLabel(t *testing.T) {
	ct := summaryOfState("a", "one", "exited")
	ct.Labels = map[string]string{"com.docker.compose.project": "mystack"}

	report := classifyContainers([]container.Summary{ct})

	require.Equal(t, "mystack", report.Containers[0].Managed)
}

func TestClassifyContainersManagedHintEmptyWhenNeitherLabelPresent(t *testing.T) {
	ct := summaryOfState("a", "one", "exited")

	report := classifyContainers([]container.Summary{ct})

	require.Empty(t, report.Containers[0].Managed)
}

func TestClassifyContainersNameFallsBackToIDWhenNamesEmpty(t *testing.T) {
	ct := container.Summary{ID: "deadbeef", State: "exited"}

	report := classifyContainers([]container.Summary{ct})

	require.Equal(t, "deadbeef", report.Containers[0].Name)
}

func TestClassifyContainersStripsLeadingSlashFromName(t *testing.T) {
	report := classifyContainers([]container.Summary{summaryOfState("a", "web", "exited")})

	require.Equal(t, "web", report.Containers[0].Name)
}

func TestClassifyContainersSortedByID(t *testing.T) {
	cts := []container.Summary{
		summaryOfState("zzz", "z", "exited"),
		summaryOfState("aaa", "a", "exited"),
	}

	report := classifyContainers(cts)

	require.Equal(t, []string{"aaa", "zzz"}, []string{report.Containers[0].ID, report.Containers[1].ID})
}

func TestClassifyContainersEmptyInputReturnsEmptyReport(t *testing.T) {
	report := classifyContainers(nil)

	require.NotNil(t, report.Containers)
	require.Empty(t, report.Containers)
	require.Zero(t, report.Summary)
}

// TestClassifyContainersNeverSetsExitCodeOrFinishedAt pins the "inspect
// only if a needed field isn't on the list type" split at its source:
// classifyContainers works from ContainerList data alone, which carries
// neither field (see ContainersMaintenance's own doc) -- enrichment is
// the Collector method's job, one layer up.
func TestClassifyContainersNeverSetsExitCodeOrFinishedAt(t *testing.T) {
	report := classifyContainers([]container.Summary{summaryOfState("a", "one", "exited")})

	require.Nil(t, report.Containers[0].ExitCode)
	require.Nil(t, report.Containers[0].FinishedAt)
}

func TestRemoveContainersWithOneFailureDoesNotAbortTheRest(t *testing.T) {
	removeOne := func(id string) error {
		if id == "bad" {
			return fmt.Errorf("conflict: container is running")
		}
		return nil
	}

	results := removeContainersWith([]string{"good", "bad"}, nil, removeOne)

	require.Equal(t, []ContainerRemoveResult{
		{ID: "good", OK: true},
		{ID: "bad", OK: false, Error: "conflict: container is running"},
	}, results)
}

func TestRemoveContainersWithEnrichesSuccessFromPreFetchedMap(t *testing.T) {
	pre := map[string]container.Summary{
		"good": {ID: "good", Names: []string{"/web"}, Image: "app:latest"},
	}
	removeOne := func(string) error { return nil }

	results := removeContainersWith([]string{"good"}, pre, removeOne)

	require.Equal(t, []ContainerRemoveResult{
		{ID: "good", OK: true, Name: "web", Image: "app:latest"},
	}, results)
}

func TestRemoveContainersWithSucceedsWithoutEnrichmentWhenIDMissingFromPre(t *testing.T) {
	removeOne := func(string) error { return nil }

	results := removeContainersWith([]string{"unknown"}, nil, removeOne)

	require.Equal(t, []ContainerRemoveResult{{ID: "unknown", OK: true}}, results)
}

// TestRemoveContainersWithPassesRunningConflictErrorThroughVerbatim pins
// bullet 2's contract: unlike images' multi-tag conflict, a running
// container's own removal conflict gets no special-case rewriting --
// the daemon's own message passes straight through per-id.
func TestRemoveContainersWithPassesRunningConflictErrorThroughVerbatim(t *testing.T) {
	raw := "conflict: cannot remove container \"web\": container is running: stop the container before removing or force remove"
	removeOne := func(string) error { return errdefs.Conflict(fmt.Errorf("%s", raw)) }

	results := removeContainersWith([]string{"running-id"}, nil, removeOne)

	require.Len(t, results, 1)
	require.False(t, results[0].OK)
	require.Equal(t, raw, results[0].Error, "the daemon's own conflict message must pass through unmodified, no image-style rewriting")
}

func TestPruneContainersWithCollectsDeletedAndPerIDErrors(t *testing.T) {
	targets := []ContainerMaintenanceInfo{
		{ID: "a", Name: "one", Image: "app:1"},
		{ID: "b", Name: "two", Image: "app:2"},
	}
	removeOne := func(id string) error {
		if id == "b" {
			return fmt.Errorf("in use")
		}
		return nil
	}

	result := pruneContainersWith(targets, removeOne)

	require.Equal(t, []DeletedContainer{{ID: "a", Name: "one", Image: "app:1"}}, result.Deleted)
	require.Equal(t, []string{"b: in use"}, result.Errors)
}

func intPtr(v int) *int       { return &v }
func int64Ptr(v int64) *int64 { return &v }

func TestSelectPruneTargetsExitedModeOnlySelectsExited(t *testing.T) {
	cts := []ContainerMaintenanceInfo{
		{ID: "a", State: "exited"},
		{ID: "b", State: "created"},
		{ID: "c", State: "dead"},
	}

	out := selectPruneTargets(cts, "exited", 0, time.Now())

	require.Equal(t, []ContainerMaintenanceInfo{{ID: "a", State: "exited"}}, out)
}

func TestSelectPruneTargetsCreatedModeOnlySelectsCreated(t *testing.T) {
	cts := []ContainerMaintenanceInfo{
		{ID: "a", State: "exited"},
		{ID: "b", State: "created"},
	}

	out := selectPruneTargets(cts, "created", 0, time.Now())

	require.Equal(t, []ContainerMaintenanceInfo{{ID: "b", State: "created"}}, out)
}

// TestSelectPruneTargetsAllStoppedSelectsExitedAndCreatedNotDead pins a
// deliberate scope decision: "all-stopped" means every ordinary stopped
// state, not literally everything this package lists -- dead is excluded
// even from the broad mode (see selectPruneTargets' own doc for why).
func TestSelectPruneTargetsAllStoppedSelectsExitedAndCreatedNotDead(t *testing.T) {
	cts := []ContainerMaintenanceInfo{
		{ID: "a", State: "exited"},
		{ID: "b", State: "created"},
		{ID: "c", State: "dead"},
	}

	out := selectPruneTargets(cts, "all-stopped", 0, time.Now())

	require.Len(t, out, 2)
	require.NotContains(t, out, ContainerMaintenanceInfo{ID: "c", State: "dead"})
}

func TestSelectPruneTargetsOlderThanHoursFiltersByFinishedAtForExited(t *testing.T) {
	now := time.Now()
	old := ContainerMaintenanceInfo{ID: "old", State: "exited", FinishedAt: int64Ptr(now.Add(-10 * time.Hour).Unix())}
	recent := ContainerMaintenanceInfo{ID: "recent", State: "exited", FinishedAt: int64Ptr(now.Add(-1 * time.Hour).Unix())}

	out := selectPruneTargets([]ContainerMaintenanceInfo{old, recent}, "exited", 5, now)

	require.Equal(t, []ContainerMaintenanceInfo{old}, out)
}

func TestSelectPruneTargetsOlderThanHoursFiltersByCreatedForCreated(t *testing.T) {
	now := time.Now()
	old := ContainerMaintenanceInfo{ID: "old", State: "created", Created: now.Add(-10 * time.Hour).Unix()}
	recent := ContainerMaintenanceInfo{ID: "recent", State: "created", Created: now.Add(-1 * time.Hour).Unix()}

	out := selectPruneTargets([]ContainerMaintenanceInfo{old, recent}, "created", 5, now)

	require.Equal(t, []ContainerMaintenanceInfo{old}, out)
}

func TestSelectPruneTargetsZeroOlderThanHoursMeansNoAgeFilter(t *testing.T) {
	now := time.Now()
	recent := ContainerMaintenanceInfo{ID: "recent", State: "exited", FinishedAt: int64Ptr(now.Unix())}

	out := selectPruneTargets([]ContainerMaintenanceInfo{recent}, "exited", 0, now)

	require.Equal(t, []ContainerMaintenanceInfo{recent}, out)
}

// TestSelectPruneTargetsSkipsExitedWithNilFinishedAtWhenAgeFilterActive
// pins C1's fix: an exited container with no trustworthy FinishedAt (nil
// -- see ContainersMaintenance's own doc for when that happens) must
// never be matched by falling back to Created. Created only reflects
// when the container was originally started, which can be arbitrarily
// older than when it actually stopped -- guessing from it risks deleting
// something that in reality just exited moments ago.
func TestSelectPruneTargetsSkipsExitedWithNilFinishedAtWhenAgeFilterActive(t *testing.T) {
	now := time.Now()
	untrustworthy := ContainerMaintenanceInfo{
		ID: "untrustworthy", State: "exited", FinishedAt: nil,
		Created: now.Add(-30 * 24 * time.Hour).Unix(), // 30 days old by Created alone
	}

	out := selectPruneTargets([]ContainerMaintenanceInfo{untrustworthy}, "exited", 24, now)

	require.Empty(t, out, "an exited container with no trustworthy FinishedAt must be skipped, never deleted on a Created-based guess")
}

// TestSelectPruneTargetsExitedWithNilFinishedAtIncludedWhenNoAgeFilter
// pins the boundary of C1's fix: the skip only applies once an age
// filter is actually active. With no filter, mode selection alone
// decides membership and no timestamp is ever consulted, so a container
// that never got enriched must still be a normal, includable prune
// target -- exactly PruneContainers' own default (olderThanHours=0)
// behavior already relied on before this fix.
func TestSelectPruneTargetsExitedWithNilFinishedAtIncludedWhenNoAgeFilter(t *testing.T) {
	now := time.Now()
	unenriched := ContainerMaintenanceInfo{ID: "unenriched", State: "exited", FinishedAt: nil}

	out := selectPruneTargets([]ContainerMaintenanceInfo{unenriched}, "exited", 0, now)

	require.Equal(t, []ContainerMaintenanceInfo{unenriched}, out)
}

// fakeContainersClient is a hand-rolled containersClient double --
// injected via Collector's own ctrCli field (see containersClient's own
// doc) so ContainersMaintenance/RemoveContainers/PruneContainers' real
// wrapper calls (not just the pure orchestration functions above) can be
// pinned without a daemon.
type fakeContainersClient struct {
	containerListReturn []container.Summary
	inspectReturn       map[string]container.InspectResponse
	inspectErr          map[string]error
	inspectCalls        []string

	removeIDs     []string
	removeOptions []container.RemoveOptions
	removeErr     map[string]error
}

func (f *fakeContainersClient) ContainerList(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
	return f.containerListReturn, nil
}

func (f *fakeContainersClient) ContainerInspect(_ context.Context, id string) (container.InspectResponse, error) {
	f.inspectCalls = append(f.inspectCalls, id)
	if err, ok := f.inspectErr[id]; ok {
		return container.InspectResponse{}, err
	}
	return f.inspectReturn[id], nil
}

func (f *fakeContainersClient) ContainerRemove(_ context.Context, id string, options container.RemoveOptions) error {
	f.removeIDs = append(f.removeIDs, id)
	f.removeOptions = append(f.removeOptions, options)
	return f.removeErr[id]
}

func inspectWithState(exitCode int, finishedAt string) container.InspectResponse {
	return container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{
		State: &container.State{ExitCode: exitCode, FinishedAt: finishedAt},
	}}
}

// inspectWithRestartPolicy is inspectWithState plus a HostConfig
// carrying the given restart policy name -- used only by the
// RestartPolicy enrichment tests below, which don't care about
// ExitCode/FinishedAt.
func inspectWithRestartPolicy(name container.RestartPolicyMode) container.InspectResponse {
	insp := inspectWithState(0, "2024-12-24T00:00:00Z")
	insp.HostConfig = &container.HostConfig{RestartPolicy: container.RestartPolicy{Name: name}}
	return insp
}

// TestContainersMaintenanceEnrichesExitedWithExitCodeAndFinishedAtViaInspect
// pins the split the spec calls out explicitly: ExitCode/FinishedAt
// aren't on ContainerList's own Summary type, so ContainersMaintenance
// must fall back to ContainerInspect for exactly the containers that
// need it.
func TestContainersMaintenanceEnrichesExitedWithExitCodeAndFinishedAtViaInspect(t *testing.T) {
	fc := &fakeContainersClient{
		containerListReturn: []container.Summary{summaryOfState("exited1", "one", "exited")},
		inspectReturn: map[string]container.InspectResponse{
			"exited1": inspectWithState(137, "2024-12-24T00:00:00Z"),
		},
	}
	c := &Collector{ctrCli: fc}

	report, err := c.ContainersMaintenance(context.Background())

	require.NoError(t, err)
	require.Len(t, report.Containers, 1)
	require.NotNil(t, report.Containers[0].ExitCode)
	require.Equal(t, 137, *report.Containers[0].ExitCode)
	require.NotNil(t, report.Containers[0].FinishedAt)
}

// TestContainersMaintenanceNeverInspectsNonExited pins the same
// efficiency rule as a behavioral assertion, not just a doc comment: a
// created/dead container has no exit code or finish time to enrich, so
// inspecting it would be a wasted API call against every non-running
// container on every list poll.
func TestContainersMaintenanceNeverInspectsNonExited(t *testing.T) {
	fc := &fakeContainersClient{
		containerListReturn: []container.Summary{
			summaryOfState("created1", "one", "created"),
			summaryOfState("dead1", "two", "dead"),
		},
	}
	c := &Collector{ctrCli: fc}

	report, err := c.ContainersMaintenance(context.Background())

	require.NoError(t, err)
	require.Empty(t, fc.inspectCalls, "created/dead containers must never be inspected")
	require.Nil(t, report.Containers[0].ExitCode)
	require.Nil(t, report.Containers[1].ExitCode)
}

// TestContainersMaintenanceInspectFailureLeavesFieldsUnsetNotError
// mirrors RemoveImages' own best-effort tolerance for its pre-fetch: a
// container that vanishes (or a transient inspect error) between
// ContainerList and ContainerInspect must not fail the whole request,
// just leave that one entry's enrichment fields unset.
func TestContainersMaintenanceInspectFailureLeavesFieldsUnsetNotError(t *testing.T) {
	fc := &fakeContainersClient{
		containerListReturn: []container.Summary{summaryOfState("gone", "one", "exited")},
		inspectErr:          map[string]error{"gone": fmt.Errorf("no such container")},
	}
	c := &Collector{ctrCli: fc}

	report, err := c.ContainersMaintenance(context.Background())

	require.NoError(t, err)
	require.Len(t, report.Containers, 1)
	require.Nil(t, report.Containers[0].ExitCode)
	require.Nil(t, report.Containers[0].FinishedAt)
}

// TestContainersMaintenanceZeroTimeFinishedAtLeftUnset pins C1's second
// fix: dockerd can report FinishedAt as Go's own zero time
// ("0001-01-01T00:00:00Z"), which time.Parse(time.RFC3339Nano, ...)
// happily parses without error -- .Unix() on that value is a huge
// negative number (-62135596800), which would otherwise satisfy
// virtually any older_than_hours filter. Must be left nil, the same
// "untrustworthy" bucket a failed parse or a failed inspect already
// fall into, never stored as a real (if absurd-looking) timestamp.
func TestContainersMaintenanceZeroTimeFinishedAtLeftUnset(t *testing.T) {
	fc := &fakeContainersClient{
		containerListReturn: []container.Summary{summaryOfState("exited1", "one", "exited")},
		inspectReturn: map[string]container.InspectResponse{
			"exited1": inspectWithState(137, "0001-01-01T00:00:00Z"),
		},
	}
	c := &Collector{ctrCli: fc}

	report, err := c.ContainersMaintenance(context.Background())

	require.NoError(t, err)
	require.NotNil(t, report.Containers[0].ExitCode, "ExitCode enrichment is independent of FinishedAt's own validity")
	require.Nil(t, report.Containers[0].FinishedAt, "a Go zero-time FinishedAt must never be stored, or any age filter could treat this container as infinitely old")
}

// TestContainersMaintenanceEnrichesExitedWithRestartPolicyFromInspect pins
// C2: a real example (Scott's own box) is an exited container with
// restart=unless-stopped, which reads as ordinary garbage in the
// maintenance list but is actually a service that would come right back
// -- the UI needs the same restart-policy value ContainerInspect's own
// HostConfig carries to be able to warn about it.
func TestContainersMaintenanceEnrichesExitedWithRestartPolicyFromInspect(t *testing.T) {
	fc := &fakeContainersClient{
		containerListReturn: []container.Summary{summaryOfState("exited1", "one", "exited")},
		inspectReturn:       map[string]container.InspectResponse{"exited1": inspectWithRestartPolicy(container.RestartPolicyUnlessStopped)},
	}
	c := &Collector{ctrCli: fc}

	report, err := c.ContainersMaintenance(context.Background())

	require.NoError(t, err)
	require.Equal(t, "unless-stopped", report.Containers[0].RestartPolicy)
}

// TestContainersMaintenanceRestartPolicyEmptyWhenDisabled pins the "empty
// when 'no'/none" half of RestartPolicy's own contract: docker's own
// RestartPolicyMode "no" must surface as "", the same as a container
// with no restart policy configured at all, never as the literal string
// "no".
func TestContainersMaintenanceRestartPolicyEmptyWhenDisabled(t *testing.T) {
	fc := &fakeContainersClient{
		containerListReturn: []container.Summary{summaryOfState("exited1", "one", "exited")},
		inspectReturn:       map[string]container.InspectResponse{"exited1": inspectWithRestartPolicy(container.RestartPolicyDisabled)},
	}
	c := &Collector{ctrCli: fc}

	report, err := c.ContainersMaintenance(context.Background())

	require.NoError(t, err)
	require.Empty(t, report.Containers[0].RestartPolicy)
}

// TestContainersMaintenanceCreatedNeverGetsRestartPolicy pins the other
// half: a created container is never inspected at all (see
// ContainersMaintenance's own doc), so RestartPolicy must stay at its
// zero value just like ExitCode/FinishedAt do for the same reason.
func TestContainersMaintenanceCreatedNeverGetsRestartPolicy(t *testing.T) {
	fc := &fakeContainersClient{
		containerListReturn: []container.Summary{summaryOfState("created1", "one", "created")},
	}
	c := &Collector{ctrCli: fc}

	report, err := c.ContainersMaintenance(context.Background())

	require.NoError(t, err)
	require.Empty(t, fc.inspectCalls)
	require.Empty(t, report.Containers[0].RestartPolicy)
}

// TestRemoveContainersCallsContainerRemoveWithForceFalseAndRemoveVolumesFalse
// pins bullet 2's two non-negotiable options at the real wrapper: never
// forced past a running conflict, and volumes -- actual data -- are
// never removed alongside a container.
func TestRemoveContainersCallsContainerRemoveWithForceFalseAndRemoveVolumesFalse(t *testing.T) {
	fc := &fakeContainersClient{}
	c := &Collector{ctrCli: fc}

	_, err := c.RemoveContainers(context.Background(), []string{"deadbeef"})

	require.NoError(t, err)
	require.Equal(t, []container.RemoveOptions{{Force: false, RemoveVolumes: false}}, fc.removeOptions)
}

// TestPruneContainersModeExitedOnlyRemovesExitedViaFreshClassification
// pins the "never daemon-side ContainersPrune" rule (see PruneContainers'
// own doc) as a behavioral assertion: only ids classifyContainers itself
// currently calls "exited" ever reach ContainerRemove for mode "exited".
func TestPruneContainersModeExitedOnlyRemovesExitedViaFreshClassification(t *testing.T) {
	fc := &fakeContainersClient{
		containerListReturn: []container.Summary{
			summaryOfState("exited1", "one", "exited"),
			summaryOfState("created1", "two", "created"),
		},
	}
	c := &Collector{ctrCli: fc}

	result, err := c.PruneContainers(context.Background(), "exited", 0)

	require.NoError(t, err)
	require.Equal(t, []string{"exited1"}, fc.removeIDs)
	require.Len(t, result.Deleted, 1)
}

func TestPruneContainersUnknownModeIsError(t *testing.T) {
	c := &Collector{ctrCli: &fakeContainersClient{}}

	_, err := c.PruneContainers(context.Background(), "bogus", 0)

	require.Error(t, err)
}
