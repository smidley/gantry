package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// sseHTTPClient bounds every SSE test read to a generous but finite
// timeout: a bug that makes the handler hang (instead of writing a frame
// or ping, or exiting on disconnect) fails the test promptly instead of
// hanging the whole suite.
var sseHTTPClient = &http.Client{Timeout: 10 * time.Second}

// sseClient is a held-open SSE connection for tests: readFrame consumes
// one event at a time from the stream.
type sseClient struct {
	resp   *http.Response
	reader *bufio.Reader
	cancel context.CancelFunc
}

func openSSE(t *testing.T, url string) *sseClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := sseHTTPClient.Do(req)
	if err != nil {
		cancel()
		require.NoError(t, err)
	}
	return &sseClient{resp: resp, reader: bufio.NewReader(resp.Body), cancel: cancel}
}

func (c *sseClient) close() {
	c.cancel()
	_ = c.resp.Body.Close()
}

// readFrame reads one SSE event (up to the blank line that ends it) and
// returns its "data:" payload, or nil if the event was a bare comment
// (a ": ping" keepalive carries no data line at all).
func (c *sseClient) readFrame(t *testing.T) []byte {
	t.Helper()
	var data []byte
	for {
		line, err := c.reader.ReadString('\n')
		require.NoError(t, err)
		line = strings.TrimRight(line, "\n")
		if line == "" {
			return data
		}
		if rest, ok := strings.CutPrefix(line, "data: "); ok {
			data = []byte(rest)
		}
	}
}

// --- Broadcaster unit tests (no HTTP) --------------------------------------

func TestBroadcasterRegisterCapsAt32ClientsAndFreesOnCancel(t *testing.T) {
	b := NewBroadcaster()
	var cancels []func()
	for i := 0; i < 32; i++ {
		_, cancel, ok := b.Register()
		require.True(t, ok, "client %d should register", i)
		cancels = append(cancels, cancel)
	}

	_, _, ok := b.Register()
	require.False(t, ok, "33rd client must be rejected")

	cancels[0]()
	_, cancel33, ok := b.Register()
	require.True(t, ok, "canceling a client must free a slot")
	cancel33()

	for _, c := range cancels[1:] {
		c()
	}
}

// TestBroadcasterPublishDropsFramesForSlowClient pins the "never blocks"
// contract directly: a registered client that never reads must not stall
// Publish, even once its cap-4 buffer is long overflowing.
func TestBroadcasterPublishDropsFramesForSlowClient(t *testing.T) {
	b := NewBroadcaster()
	_, cancel, ok := b.Register()
	require.True(t, ok)
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 10; i++ {
			b.Publish([]byte("frame"))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a slow client's full buffer instead of dropping")
	}
}

func TestBroadcasterPublishToNoClientsIsANoop(t *testing.T) {
	b := NewBroadcaster()
	b.Publish([]byte("frame")) // must not panic with zero registered clients
}

// --- /api/live over real HTTP ----------------------------------------------

func TestLiveEndpointSetsSSEHeaders(t *testing.T) {
	b := NewBroadcaster()
	s := New(Options{Version: "test-1", Started: time.Now(), Live: b, Current: func() []byte { return []byte(`{}`) }})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	c := openSSE(t, ts.URL+"/api/live")
	defer c.close()

	require.Equal(t, "text/event-stream", c.resp.Header.Get("Content-Type"))
	require.Equal(t, "no-cache", c.resp.Header.Get("Cache-Control"))
	require.Equal(t, "no", c.resp.Header.Get("X-Accel-Buffering"))
	require.Equal(t, http.StatusOK, c.resp.StatusCode)
}

func TestLiveEndpointWritesConnectFrameFromCurrent(t *testing.T) {
	b := NewBroadcaster()
	s := New(Options{Version: "test-1", Started: time.Now(), Live: b, Current: func() []byte { return []byte(`{"seed":true}`) }})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	c := openSSE(t, ts.URL+"/api/live")
	defer c.close()

	require.Equal(t, `{"seed":true}`, string(c.readFrame(t)))
}

func TestLiveEndpointSkipsConnectFrameWhenCurrentNotWired(t *testing.T) {
	b := NewBroadcaster()
	b.PingInterval = 20 * time.Millisecond
	s := New(Options{Version: "test-1", Started: time.Now(), Live: b}) // Current left nil
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	c := openSSE(t, ts.URL+"/api/live")
	defer c.close()

	require.Nil(t, c.readFrame(t), "with no Current wired, the first event must be the ping, not a connect frame")
}

// TestLiveEndpointTwoClientsReceivePublishedFrame is the dispatch's named
// coverage requirement: two independent clients both get the connect frame
// and both get a frame the test then Publishes directly.
func TestLiveEndpointTwoClientsReceivePublishedFrame(t *testing.T) {
	b := NewBroadcaster()
	s := New(Options{Version: "test-1", Started: time.Now(), Live: b, Current: func() []byte { return []byte(`{"seed":true}`) }})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	c1 := openSSE(t, ts.URL+"/api/live")
	defer c1.close()
	c2 := openSSE(t, ts.URL+"/api/live")
	defer c2.close()

	// By the time openSSE returns, headers are already on the wire, which
	// only happens after Register() succeeded -- both clients are
	// guaranteed registered before Publish below, so neither can miss it.
	require.Equal(t, `{"seed":true}`, string(c1.readFrame(t)))
	require.Equal(t, `{"seed":true}`, string(c2.readFrame(t)))

	b.Publish([]byte(`{"tick":1}`))

	require.Equal(t, `{"tick":1}`, string(c1.readFrame(t)))
	require.Equal(t, `{"tick":1}`, string(c2.readFrame(t)))
}

func TestLiveEndpointPingAppearsOnInjectedInterval(t *testing.T) {
	b := NewBroadcaster()
	b.PingInterval = 20 * time.Millisecond
	s := New(Options{Version: "test-1", Started: time.Now(), Live: b, Current: func() []byte { return []byte(`{}`) }})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	c := openSSE(t, ts.URL+"/api/live")
	defer c.close()

	c.readFrame(t) // connect frame
	// Nothing else is being Published, so the very next event -- arriving
	// within the 20ms injected interval -- must be the ping comment.
	require.Nil(t, c.readFrame(t), "expected a ping comment (no data: line) next")
}

// TestLiveEndpointCapAt32ClientsReturns503AndFreesOnDisconnect is the
// dispatch's named coverage requirement over real HTTP: the 33rd
// concurrent client gets 503, and disconnecting one of the 32 frees a slot
// for a new connection -- proving the handler's ctx.Done() path actually
// deregisters, not just Broadcaster.Register() in isolation.
func TestLiveEndpointCapAt32ClientsReturns503AndFreesOnDisconnect(t *testing.T) {
	b := NewBroadcaster()
	s := New(Options{Version: "test-1", Started: time.Now(), Live: b, Current: func() []byte { return []byte(`{}`) }})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	var clients []*sseClient
	defer func() {
		for _, c := range clients {
			c.close()
		}
	}()
	for i := 0; i < 32; i++ {
		c := openSSE(t, ts.URL+"/api/live")
		c.readFrame(t) // consume the connect frame
		clients = append(clients, c)
	}

	resp, err := http.Get(ts.URL + "/api/live")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.NotEmpty(t, body["error"])

	clients[0].close()
	clients = clients[1:]

	require.Eventually(t, func() bool {
		resp2, err := http.Get(ts.URL + "/api/live")
		if err != nil {
			return false
		}
		defer func() { _ = resp2.Body.Close() }()
		return resp2.StatusCode == http.StatusOK
	}, 3*time.Second, 20*time.Millisecond, "a freed slot must let a new client through")
}

func TestLiveEndpointReturns503WhenNotWired(t *testing.T) {
	s := New(Options{Version: "test-1", Started: time.Now()}) // Live left nil
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/live")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}
