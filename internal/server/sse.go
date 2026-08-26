package server

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

const (
	// broadcasterMaxClients caps concurrent /api/live connections; the
	// 33rd Register() call fails and the handler answers 503.
	broadcasterMaxClients = 32
	// clientBufferCap is each client's per-connection frame buffer. Once
	// full, Publish drops that client's frame rather than blocking.
	clientBufferCap = 4
	// defaultPingInterval is how often a connection gets a ": ping\n\n"
	// keepalive comment absent an override on PingInterval.
	defaultPingInterval = 15 * time.Second
)

// Broadcaster fans a stream of pre-marshaled frame bytes out to every
// connected /api/live client. Publish never blocks: a client whose
// buffer (cap clientBufferCap) is already full has that frame dropped
// for it, rather than stalling every other client -- or the publisher --
// behind one slow reader.
type Broadcaster struct {
	// PingInterval overrides defaultPingInterval when positive; tests
	// inject a short interval, main wiring leaves it at the
	// NewBroadcaster default.
	PingInterval time.Duration

	mu      sync.Mutex
	clients map[int]chan []byte
	nextID  int
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{clients: make(map[int]chan []byte), PingInterval: defaultPingInterval}
}

// Register adds a new client, returning a receive-only channel of frames,
// a cancel func the caller must call exactly once to deregister, and
// ok=false (nil channel, no-op cancel) once broadcasterMaxClients are
// already registered.
func (b *Broadcaster) Register() (<-chan []byte, func(), bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.clients) >= broadcasterMaxClients {
		return nil, func() {}, false
	}
	id := b.nextID
	b.nextID++
	ch := make(chan []byte, clientBufferCap)
	b.clients[id] = ch
	return ch, func() { b.deregister(id) }, true
}

func (b *Broadcaster) deregister(id int) {
	b.mu.Lock()
	delete(b.clients, id)
	b.mu.Unlock()
}

// Publish fans frame out to every registered client. A client whose
// buffer is already full (a slow reader that hasn't kept up) has this
// frame dropped for it -- Publish itself never blocks on a client.
func (b *Broadcaster) Publish(frame []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.clients {
		select {
		case ch <- frame:
		default: // slow client: drop rather than block the publisher
		}
	}
}

// handleLive serves GET /api/live: an SSE stream of snapshot frames. A nil
// Options.Live degrades to 503 rather than the "empty" convention the rest
// of this package uses for optional closures -- there's no meaningful empty
// stream, and main wiring always supplies a real Broadcaster.
func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	if s.opts.Live == nil {
		writeError(w, http.StatusServiceUnavailable, "live stream not available")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	ch, cancel, ok := s.opts.Live.Register()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "too many live clients")
		return
	}
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	if s.opts.Current != nil {
		writeSSEFrame(w, s.opts.Current())
		flusher.Flush()
	}

	interval := s.opts.Live.PingInterval
	if interval <= 0 {
		interval = defaultPingInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-ch:
			writeSSEFrame(w, frame)
			flusher.Flush()
		case <-ticker.C:
			_, _ = fmt.Fprint(w, ": ping\n\n") // write failure here means the client already disconnected
			flusher.Flush()
		}
	}
}

// writeSSEFrame writes one "event: frame" SSE event carrying data as its
// payload. Callers still need to Flush() -- writing alone can sit in a
// buffer.
func writeSSEFrame(w http.ResponseWriter, data []byte) {
	_, _ = fmt.Fprintf(w, "event: frame\ndata: %s\n\n", data) // write failure here means the client already disconnected
}
