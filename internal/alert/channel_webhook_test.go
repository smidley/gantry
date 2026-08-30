package alert

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

func testTarget(url string) WebhookTarget {
	return WebhookTarget{ID: "home", Name: "Home Assistant", URL: url, Enabled: true, TimeoutS: 5}
}

func testNotification() AlertNotification {
	return AlertNotification{
		Phase: "fired",
		Rule: store.AlertRule{
			ID: "disk-temp-high", Name: "Disk temperature high", Severity: "warning",
			Kind: "disk", Metric: "temp.c", Op: ">", Threshold: 55,
		},
		Instance: store.AlertInstance{
			RuleID: "disk-temp-high", Kind: "disk", Entity: "disk3", Metric: "temp.c",
			State: "firing", Value: 57.0, Threshold: 55.0,
			StartedAt: 1756400000, FiredAt: 1756400600,
		},
		Summary: "disk3 is at 57.0 C (over 55.0 C for 10 minutes)",
	}
}

func noJitterChannel(target WebhookTarget) *WebhookChannel {
	c := NewWebhookChannel(target, "v0.1.0-test", nil)
	c.Rand = nil // exact backoff values, no ±20% jitter, for deterministic assertions
	c.Sleep = func(time.Duration) {}
	return c
}

// --- Send: status handling -------------------------------------------------

func TestWebhookChannelSend200IsOneAttemptOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := noJitterChannel(testTarget(srv.URL))
	res := c.Send(context.Background(), testNotification())
	require.True(t, res.OK)
	require.Equal(t, 1, res.Attempts)
	require.Equal(t, http.StatusOK, res.Status)
	require.NoError(t, res.Err)
}

func TestWebhookChannelSend500RetriesThreeTimesThenFails(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := noJitterChannel(testTarget(srv.URL))
	res := c.Send(context.Background(), testNotification())
	require.False(t, res.OK)
	require.Equal(t, 3, res.Attempts)
	require.Equal(t, http.StatusInternalServerError, res.Status)
	require.EqualValues(t, 3, calls.Load())
}

func TestWebhookChannelSend404DoesNotRetry(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := noJitterChannel(testTarget(srv.URL))
	res := c.Send(context.Background(), testNotification())
	require.False(t, res.OK)
	require.Equal(t, 1, res.Attempts)
	require.Equal(t, http.StatusNotFound, res.Status)
	require.EqualValues(t, 1, calls.Load())
}

func TestWebhookChannelSendRetriesOn408And429(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests} {
		var calls atomic.Int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(status)
		}))

		c := noJitterChannel(testTarget(srv.URL))
		res := c.Send(context.Background(), testNotification())
		require.False(t, res.OK)
		require.Equal(t, 3, res.Attempts, "status %d should retry", status)
		srv.Close()
	}
}

func TestWebhookChannelSendHangingHandlerTripsPerAttemptTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // never responds inside the test's timeout budget
	}))
	// defer order matters: srv.Close() blocks until every outstanding
	// handler returns, so the release signal must fire FIRST (registered
	// last) or Close() would wait forever on a handler that's waiting on
	// this same release.
	defer srv.Close()
	defer close(release)

	target := testTarget(srv.URL)
	target.TimeoutS = 1
	c := noJitterChannel(target)

	start := time.Now()
	res := c.Send(context.Background(), testNotification())
	elapsed := time.Since(start)

	require.False(t, res.OK)
	require.Error(t, res.Err)
	require.Equal(t, 3, res.Attempts, "a timeout is a transport error and should retry like any other")
	// 3 attempts at ~1s each, no real backoff (Sleep stubbed to a no-op):
	// generously bounded well under what 3 real hangs plus real 2s/8s
	// backoff would take.
	require.Less(t, elapsed, 6*time.Second)
}

// --- envelope shape ---------------------------------------------------------

type capturedRequest struct {
	headers http.Header
	body    []byte
}

func captureOneRequest(t *testing.T) (*httptest.Server, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured.headers = r.Header.Clone()
		captured.body = body
		w.WriteHeader(http.StatusOK)
	}))
	return srv, captured
}

func TestWebhookChannelEnvelopeShapeForFired(t *testing.T) {
	srv, captured := captureOneRequest(t)
	defer srv.Close()

	c := noJitterChannel(testTarget(srv.URL))
	res := c.Send(context.Background(), testNotification())
	require.True(t, res.OK)

	var env map[string]any
	require.NoError(t, json.Unmarshal(captured.body, &env))
	require.EqualValues(t, 1, env["version"])
	require.Equal(t, "alert.fired", env["event"])
	require.Equal(t, "gantry", env["source"])
	require.Equal(t, "v0.1.0-test", env["gantry_version"])
	require.Equal(t, "disk3 is at 57.0 C (over 55.0 C for 10 minutes)", env["summary"])

	alert, ok := env["alert"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "disk-temp-high", alert["rule_id"])
	require.Equal(t, "Disk temperature high", alert["rule_name"])
	require.Equal(t, "warning", alert["severity"])
	require.Equal(t, "firing", alert["state"])
	require.Equal(t, "disk", alert["kind"])
	require.Equal(t, "disk3", alert["entity"])
	require.Equal(t, "temp.c", alert["metric"])
	require.EqualValues(t, 57.0, alert["value"])
	require.EqualValues(t, 55.0, alert["threshold"])
	require.Equal(t, ">", alert["op"])
	require.EqualValues(t, 1756400000, alert["started_at"])
	require.EqualValues(t, 1756400600, alert["fired_at"])
	require.EqualValues(t, 0, alert["resolved_at"])
}

func TestWebhookChannelEnvelopeEventNameByPhase(t *testing.T) {
	cases := []struct {
		phase, wantEvent string
	}{
		{"fired", "alert.fired"},
		{"renotify", "alert.fired"},
		{"resolved", "alert.resolved"},
		{"flapping", "alert.flapping"},
	}
	for _, tc := range cases {
		srv, captured := captureOneRequest(t)
		c := noJitterChannel(testTarget(srv.URL))
		n := testNotification()
		n.Phase = tc.phase
		res := c.Send(context.Background(), n)
		require.True(t, res.OK, tc.phase)

		var env map[string]any
		require.NoError(t, json.Unmarshal(captured.body, &env))
		require.Equal(t, tc.wantEvent, env["event"], tc.phase)
		srv.Close()
	}
}

func TestWebhookChannelHeaders(t *testing.T) {
	srv, captured := captureOneRequest(t)
	defer srv.Close()

	target := testTarget(srv.URL)
	target.HeaderName = "X-Api-Key"
	target.HeaderValue = "super-secret-value"
	c := noJitterChannel(target)

	res := c.Send(context.Background(), testNotification())
	require.True(t, res.OK)
	require.Equal(t, "application/json", captured.headers.Get("Content-Type"))
	require.Equal(t, "gantry/v0.1.0-test", captured.headers.Get("User-Agent"))
	require.Equal(t, "super-secret-value", captured.headers.Get("X-Api-Key"))
}

func TestWebhookChannelNoHeaderWhenTargetHasNone(t *testing.T) {
	srv, captured := captureOneRequest(t)
	defer srv.Close()

	c := noJitterChannel(testTarget(srv.URL))
	res := c.Send(context.Background(), testNotification())
	require.True(t, res.OK)
	require.Empty(t, captured.headers.Get("X-Api-Key"))
}

// --- redirects never followed ------------------------------------------------

func TestWebhookChannelNeverFollowsRedirect(t *testing.T) {
	var followed atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()

	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer srv.Close()

	target := testTarget(srv.URL)
	target.HeaderName = "Authorization"
	target.HeaderValue = "Bearer super-secret-token-xyz"
	c := noJitterChannel(target)

	res := c.Send(context.Background(), testNotification())
	require.False(t, res.OK, "a 3xx is a non-2xx failure, never a hop to follow")
	require.Equal(t, http.StatusFound, res.Status)
	require.Equal(t, 1, res.Attempts, "3xx is a configuration mistake, not retryable")
	require.EqualValues(t, 0, followed.Load(), "the redirect target must never see a request (it would receive the secret header)")
	require.EqualValues(t, 1, calls.Load())
	require.Contains(t, c.Health(), "302")
}

// --- secret never leaked ----------------------------------------------------

func TestWebhookChannelSecretNeverInErrorStringOnFailure(t *testing.T) {
	target := testTarget("http://127.0.0.1:1/dead") // nothing listens on port 1: guaranteed connection error
	target.HeaderName = "Authorization"
	target.HeaderValue = "Bearer super-secret-token-xyz"
	c := noJitterChannel(target)

	res := c.Send(context.Background(), testNotification())
	require.False(t, res.OK)
	require.Error(t, res.Err)
	require.NotContains(t, res.Err.Error(), "super-secret-token-xyz")

	health := c.Health()
	require.Contains(t, health, "last delivery failed:")
	require.NotContains(t, health, "super-secret-token-xyz")
}

// TestWebhookChannelTargetURLNeverInErrorOrHealth pins the transport-
// error boundary: Discord/Slack/ntfy webhook URLs carry the credential
// in the PATH, and Go's *url.Error stringifies the full URL, so a raw
// transport error would leak it into every surface that renders the
// error. The channel must identify itself by target id only.
func TestWebhookChannelTargetURLNeverInErrorOrHealth(t *testing.T) {
	target := testTarget("http://127.0.0.1:1/api/webhooks/1234/PATH-SECRET-TOKEN") // nothing listens on port 1
	c := noJitterChannel(target)

	res := c.Send(context.Background(), testNotification())
	require.False(t, res.OK)
	require.Error(t, res.Err)
	require.NotContains(t, res.Err.Error(), "PATH-SECRET-TOKEN")
	require.Contains(t, res.Err.Error(), `"home"`, "the target is identified by its id instead")
	require.Contains(t, res.Err.Error(), "connection refused", "the inner transport error survives the unwrap")
	require.NotContains(t, c.Health(), "PATH-SECRET-TOKEN")
}

func TestWebhookChannelSecretNeverInHealthString(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	target := testTarget(srv.URL)
	target.HeaderName = "Authorization"
	target.HeaderValue = "Bearer super-secret-token-xyz"
	c := noJitterChannel(target)

	c.Send(context.Background(), testNotification())
	require.NotContains(t, c.Health(), "super-secret-token-xyz")
}

// --- Health ------------------------------------------------------------------

func TestWebhookChannelHealthOKUntilAFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := noJitterChannel(testTarget(srv.URL))
	require.Equal(t, "ok", c.Health())
	c.Send(context.Background(), testNotification())
	require.Equal(t, "ok", c.Health())
}

func TestWebhookChannelHealthReportsLastFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := noJitterChannel(testTarget(srv.URL))
	c.Send(context.Background(), testNotification())
	require.Contains(t, c.Health(), "last delivery failed:")
	require.Contains(t, c.Health(), "404")
}

func TestWebhookChannelHealthRecoversAfterASubsequentSuccess(t *testing.T) {
	fail := atomic.Bool{}
	fail.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := noJitterChannel(testTarget(srv.URL))
	c.Send(context.Background(), testNotification())
	require.Contains(t, c.Health(), "last delivery failed:")

	fail.Store(false)
	c.Send(context.Background(), testNotification())
	require.Equal(t, "ok", c.Health())
}

func TestWebhookChannelIDIncludesTargetID(t *testing.T) {
	c := NewWebhookChannel(WebhookTarget{ID: "home"}, "v1", nil)
	require.Equal(t, "webhook:home", c.ID())
}

// --- backoff sequence (fake clock / recorded sleeps) ------------------------

func TestWebhookChannelBackoffSequenceBetweenAttempts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewWebhookChannel(testTarget(srv.URL), "v1", nil)
	c.Rand = nil
	var slept []time.Duration
	c.Sleep = func(d time.Duration) { slept = append(slept, d) }

	res := c.Send(context.Background(), testNotification())
	require.False(t, res.OK)
	require.Equal(t, []time.Duration{2 * time.Second, 8 * time.Second}, slept, "two backoff gaps between three attempts")
}

func TestWebhookChannelBackoffJitterStaysWithinTwentyPercent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewWebhookChannel(testTarget(srv.URL), "v1", nil) // real jitter (constructor-seeded Rand)
	var slept []time.Duration
	c.Sleep = func(d time.Duration) { slept = append(slept, d) }

	c.Send(context.Background(), testNotification())
	require.Len(t, slept, 2)
	require.InDelta(t, float64(2*time.Second), float64(slept[0]), float64(2*time.Second)*0.2+1)
	require.InDelta(t, float64(8*time.Second), float64(slept[1]), float64(8*time.Second)*0.2+1)
}

// --- target validation --------------------------------------------------------

func TestValidateWebhookTargetAcceptsHTTPAndHTTPS(t *testing.T) {
	require.NoError(t, ValidateWebhookTarget(WebhookTarget{ID: "a", URL: "http://example.com/hook", TimeoutS: 10}))
	require.NoError(t, ValidateWebhookTarget(WebhookTarget{ID: "a", URL: "https://example.com/hook", TimeoutS: 10}))
}

func TestValidateWebhookTargetRejectsNonHTTPScheme(t *testing.T) {
	err := ValidateWebhookTarget(WebhookTarget{ID: "a", URL: "file:///etc/passwd", TimeoutS: 10})
	require.Error(t, err)
}

func TestValidateWebhookTargetRejectsUserinfoInURL(t *testing.T) {
	err := ValidateWebhookTarget(WebhookTarget{ID: "a", URL: "https://user:pass@example.com/hook", TimeoutS: 10})
	require.Error(t, err)
}

func TestValidateWebhookTargetRejectsInvalidHeaderName(t *testing.T) {
	err := ValidateWebhookTarget(WebhookTarget{ID: "a", URL: "https://example.com", TimeoutS: 10, HeaderName: "bad header"})
	require.Error(t, err)
}

func TestValidateWebhookTargetRejectsOversizedHeaderValue(t *testing.T) {
	err := ValidateWebhookTarget(WebhookTarget{
		ID: "a", URL: "https://example.com", TimeoutS: 10,
		HeaderName: "X-Api-Key", HeaderValue: strings.Repeat("x", 1025),
	})
	require.Error(t, err)
}

func TestValidateWebhookTargetRejectsTimeoutOutOfRange(t *testing.T) {
	require.Error(t, ValidateWebhookTarget(WebhookTarget{ID: "a", URL: "https://example.com", TimeoutS: 31}))
	require.Error(t, ValidateWebhookTarget(WebhookTarget{ID: "a", URL: "https://example.com", TimeoutS: -1}))
}

func TestValidateWebhookTargetsRejectsMoreThanEight(t *testing.T) {
	var targets []WebhookTarget
	for i := 0; i < 9; i++ {
		targets = append(targets, WebhookTarget{ID: string(rune('a' + i)), URL: "https://example.com", TimeoutS: 10})
	}
	require.Error(t, ValidateWebhookTargets(targets))
}

func TestValidateWebhookTargetsRejectsDuplicateIDs(t *testing.T) {
	targets := []WebhookTarget{
		{ID: "a", URL: "https://example.com", TimeoutS: 10},
		{ID: "a", URL: "https://example.org", TimeoutS: 10},
	}
	require.Error(t, ValidateWebhookTargets(targets))
}
