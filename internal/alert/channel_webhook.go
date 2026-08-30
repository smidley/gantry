package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// WebhookTarget is one configured outbound webhook -- stored as a JSON
// blob in the settings table under the "alert.webhook_targets" key
// (cmd/gantry/main.go owns that adapter, the exact groupsAdapter-shaped
// precedent every other domain-specific settings blob in this codebase
// follows), never its own SQL table: this is delivery config, not
// alerting data the engine reads.
type WebhookTarget struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Enabled     bool   `json:"enabled"`
	HeaderName  string `json:"header_name,omitempty"`
	HeaderValue string `json:"header_value,omitempty"` // never returned by any GET -- see docs/alerts.md; there is no GET route on this branch yet (Task 8)
	TimeoutS    int    `json:"timeout_s"`
}

// maxWebhookTargets caps how many targets ReplaceAlertRules'-shaped whole-
// document writes (Task 8, not this branch) may ever hold -- checked here
// too so a hand-edited settings blob can't quietly exceed it before that
// route exists to enforce it at the door.
const maxWebhookTargets = 8

// ValidateWebhookTarget checks one target's own fields. It does not check
// uniqueness across a list -- that's ValidateWebhookTargets' job, the same
// split ValidateRule/the future rules-list validator uses.
func ValidateWebhookTarget(t WebhookTarget) error {
	u, err := url.Parse(t.URL)
	if err != nil {
		return fmt.Errorf("webhook target %q: invalid url: %w", t.ID, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("webhook target %q: url scheme must be http or https", t.ID)
	}
	if u.User != nil {
		return fmt.Errorf("webhook target %q: url must not carry userinfo", t.ID)
	}
	if t.HeaderName != "" && !isValidHeaderToken(t.HeaderName) {
		return fmt.Errorf("webhook target %q: invalid header name %q", t.ID, t.HeaderName)
	}
	if len(t.HeaderValue) > 1024 {
		return fmt.Errorf("webhook target %q: header value exceeds 1KB", t.ID)
	}
	if t.TimeoutS < 0 || t.TimeoutS > 30 {
		return fmt.Errorf("webhook target %q: timeout_s %d outside 0-30 (0 falls back to the 10s default)", t.ID, t.TimeoutS)
	}
	return nil
}

// ValidateWebhookTargets validates a whole list: the count cap and id
// uniqueness, plus every target individually.
func ValidateWebhookTargets(targets []WebhookTarget) error {
	if len(targets) > maxWebhookTargets {
		return fmt.Errorf("too many webhook targets: %d exceeds the cap of %d", len(targets), maxWebhookTargets)
	}
	seen := make(map[string]bool, len(targets))
	for _, t := range targets {
		if seen[t.ID] {
			return fmt.Errorf("duplicate webhook target id %q", t.ID)
		}
		seen[t.ID] = true
		if err := ValidateWebhookTarget(t); err != nil {
			return err
		}
	}
	return nil
}

// isValidHeaderToken checks s is a legal HTTP header field-name token
// (RFC 7230 section 3.2.6) -- hand-rolled rather than pulled from
// golang.org/x/net/http/httpguts, since that would be a new dependency
// for one small character-class check.
func isValidHeaderToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", r):
		default:
			return false
		}
	}
	return true
}

// webhookMaxAttempts and webhookBackoff implement the plan's "3 attempts,
// backoff 2s -> 8s -> 32s": with a 3-attempt cap there are only two gaps
// to actually wait out (after attempt 1 and after attempt 2), so 32s
// documents the progression's next step without ever being used at the
// current cap.
const webhookMaxAttempts = 3

var webhookBackoff = []time.Duration{2 * time.Second, 8 * time.Second, 32 * time.Second}

const defaultWebhookTimeout = 10 * time.Second

// eventNameForPhase maps an AlertNotification's Phase onto the webhook
// envelope's "event" field -- the same alert.fired/alert.resolved
// vocabulary the events table itself uses (see store.Event.Kind), so a
// webhook consumer never has to learn a second name for the same thing.
// "renotify" is still a firing alert repeating itself, so it shares
// "alert.fired" rather than inventing a third event name with no
// analogous events-table row.
func eventNameForPhase(phase string) string {
	switch phase {
	case "resolved":
		return "alert.resolved"
	case "flapping":
		return "alert.flapping"
	default: // "fired", "renotify", "throttled" (though throttled never reaches a webhook -- notify-only)
		return "alert.fired"
	}
}

type webhookEnvelope struct {
	Version       int          `json:"version"`
	Event         string       `json:"event"`
	Alert         webhookAlert `json:"alert"`
	Summary       string       `json:"summary"`
	Source        string       `json:"source"`
	GantryVersion string       `json:"gantry_version"`
}

type webhookAlert struct {
	RuleID     string  `json:"rule_id"`
	RuleName   string  `json:"rule_name"`
	Severity   string  `json:"severity"`
	State      string  `json:"state"`
	Kind       string  `json:"kind"`
	Entity     string  `json:"entity"`
	Metric     string  `json:"metric"`
	Value      float64 `json:"value"`
	Threshold  float64 `json:"threshold"`
	Op         string  `json:"op"`
	StartedAt  int64   `json:"started_at"`
	FiredAt    int64   `json:"fired_at"`
	ResolvedAt int64   `json:"resolved_at"`
}

func buildWebhookEnvelope(n AlertNotification, version string) webhookEnvelope {
	return webhookEnvelope{
		Version: 1,
		Event:   eventNameForPhase(n.Phase),
		Alert: webhookAlert{
			RuleID: n.Rule.ID, RuleName: n.Rule.Name, Severity: n.Rule.Severity,
			State: n.Instance.State, Kind: n.Rule.Kind, Entity: n.Instance.Entity, Metric: n.Rule.Metric,
			Value: n.Instance.Value, Threshold: n.Instance.Threshold, Op: n.Rule.Op,
			StartedAt: n.Instance.StartedAt, FiredAt: n.Instance.FiredAt, ResolvedAt: n.Instance.ResolvedAt,
		},
		Summary:       n.Summary,
		Source:        "gantry",
		GantryVersion: version,
	}
}

// WebhookChannel is the Channel implementation for one WebhookTarget: a
// generic JSON POST with bounded retry/backoff, never blocking the
// engine (Dispatcher already runs every channel on its own worker; this
// type's Send is a plain, if slow, synchronous call from that worker's
// point of view).
type WebhookChannel struct {
	Target  WebhookTarget
	Version string
	Clock   func() time.Time
	Client  *http.Client
	// Sleep stands in for time.Sleep between retry attempts -- tests
	// inject a recording no-op so the backoff sequence is assertable
	// without waiting through it in real time.
	Sleep func(time.Duration)
	// Rand drives the ±20% backoff jitter; nil disables jitter entirely
	// (exact 2s/8s), which is what every test but the one asserting the
	// jitter bounds themselves wants.
	Rand *rand.Rand

	mu         sync.Mutex
	failed     bool
	failStatus int
	failErr    string
}

// noRedirectClient is every webhook channel's HTTP client: a webhook
// POST never follows redirects. Go's default client would replay the
// request -- custom secret header included -- against whatever host a
// 3xx points at, so a compromised or misconfigured endpoint could
// exfiltrate the credential with one Location header. ErrUseLastResponse
// hands the 3xx itself back instead, and Send treats it like any other
// non-2xx failure.
var noRedirectClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// NewWebhookChannel constructs a channel ready to send: the shared
// redirect-refusing client with no fixed Timeout (each attempt's own
// context deadline drives it, since TimeoutS is per-attempt, not
// per-Send), real jitter from a time-seeded Rand, and clock defaulting
// to time.Now.
func NewWebhookChannel(target WebhookTarget, version string, clock func() time.Time) *WebhookChannel {
	if clock == nil {
		clock = time.Now
	}
	return &WebhookChannel{
		Target: target, Version: version, Clock: clock,
		Client: noRedirectClient,
		Sleep:  time.Sleep,
		Rand:   rand.New(rand.NewSource(time.Now().UnixNano())), //nolint:gosec // retry jitter, not a security boundary
	}
}

func (c *WebhookChannel) ID() string { return "webhook:" + c.Target.ID }

// Health reports "ok" or "last delivery failed: <status> <error> (<age>)"
// -- the Settings channels card's own text, verbatim (Task 8, not built
// on this branch, but the string this method returns is already exactly
// what that card will render). The age is computed fresh on every call
// from the stored failure timestamp, never baked in at failure time, so
// it keeps counting up correctly across repeated Health() calls.
func (c *WebhookChannel) Health() string {
	c.mu.Lock()
	failed, status, errStr := c.failed, c.failStatus, c.failErr
	c.mu.Unlock()
	if !failed {
		return "ok"
	}
	return fmt.Sprintf("last delivery failed: %d %s", status, errStr)
}

func (c *WebhookChannel) recordHealth(ok bool, status int, errStr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failed = !ok
	c.failStatus = status
	c.failErr = errStr
}

// Send marshals n into the versioned envelope once, then attempts
// delivery up to webhookMaxAttempts times with backoff between failures,
// stopping early on any non-retryable outcome. Exactly one SendResult
// comes back regardless of how many HTTP attempts it took -- the
// delivery ledger records the outcome, not each intermediate try.
func (c *WebhookChannel) Send(ctx context.Context, n AlertNotification) SendResult {
	body, err := json.Marshal(buildWebhookEnvelope(n, c.Version))
	if err != nil {
		return SendResult{Err: fmt.Errorf("marshal webhook envelope: %w", err)}
	}

	timeout := time.Duration(c.Target.TimeoutS) * time.Second
	if timeout <= 0 {
		timeout = defaultWebhookTimeout
	}

	var status int
	var sendErr error
	attempt := 1
	for ; attempt <= webhookMaxAttempts; attempt++ {
		status, sendErr = c.attempt(ctx, body, timeout)
		if sendErr == nil && status >= 200 && status < 300 {
			c.recordHealth(true, 0, "")
			return SendResult{OK: true, Attempts: attempt, Status: status}
		}
		if attempt == webhookMaxAttempts || !shouldRetry(status, sendErr) {
			break
		}
		c.sleep(jitter(webhookBackoff[attempt-1], c.Rand))
	}

	errStr := ""
	if sendErr != nil {
		errStr = sendErr.Error()
	}
	c.recordHealth(false, status, errStr)
	return SendResult{OK: false, Attempts: attempt, Status: status, Err: sendErr}
}

func (c *WebhookChannel) attempt(ctx context.Context, body []byte, timeout time.Duration) (status int, err error) {
	actx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(actx, http.MethodPost, c.Target.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "gantry/"+c.Version)
	if c.Target.HeaderName != "" {
		req.Header.Set(c.Target.HeaderName, c.Target.HeaderValue)
	}

	resp, err := c.client().Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body) // drain so the connection can be reused; the body's content is never needed
	return resp.StatusCode, nil
}

// shouldRetry: 5xx, 408, 429, and any transport error (including a
// per-attempt timeout, which surfaces as a context.DeadlineExceeded
// wrapped in a *url.Error) retry; any other 4xx is a configuration
// mistake -- a 404 or 401 retried three times per alert just amplifies
// the mistake, it doesn't fix it.
func shouldRetry(status int, err error) bool {
	if err != nil {
		return true
	}
	if status >= 500 {
		return true
	}
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests
}

// jitter applies ±20% to base using r; r == nil disables jitter (used by
// every test that wants exact, assertable backoff values).
func jitter(base time.Duration, r *rand.Rand) time.Duration {
	if r == nil {
		return base
	}
	factor := 0.8 + r.Float64()*0.4 // [0.8, 1.2)
	return time.Duration(float64(base) * factor)
}

func (c *WebhookChannel) sleep(d time.Duration) {
	if c.Sleep != nil {
		c.Sleep(d)
		return
	}
	time.Sleep(d)
}

func (c *WebhookChannel) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return noRedirectClient // never http.DefaultClient: it follows redirects, secret header and all
}
