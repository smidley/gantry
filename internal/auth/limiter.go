package auth

import (
	"sync"
	"time"
)

// Login brute-force limits: the classic token-bucket refill math the
// alert dispatcher's notify bucket already uses (internal/alert
// dispatch.go, takeToken), applied twice per attempt -- a per-IP bucket
// so one client can't hammer, and a global bucket so a botnet of
// spoofed/many LAN sources can't multiply that by its size. 5/min per
// IP and 20/min globally: at ~28k guesses/day absolute worst case, an
// 8+ character non-dictionary password stays out of reach while a
// fat-fingered human never notices the limiter exists.
//
// There is deliberately NO lockout escalation beyond these buckets: a
// hard lockout on a LAN appliance is a self-DoS lever (anything on the
// network could lock the owner out of their own dashboard forever),
// whereas a refilling bucket bounds the guess rate just as well and
// recovers on its own.
const (
	loginPerIPCapacity         = 5
	loginPerIPRefillPerSecond  = 5.0 / 60
	loginGlobalCapacity        = 20
	loginGlobalRefillPerSecond = 20.0 / 60

	// limiterMapMax bounds the per-IP bucket map: when exceeded, buckets
	// that have refilled to full (i.e. idle long enough to hold no
	// state worth keeping) are dropped. On a LAN this is unreachable in
	// practice; it exists so a burst of many distinct source addresses
	// can't grow the map without bound.
	limiterMapMax = 512
)

type lbucket struct {
	tokens float64
	stamp  time.Time
}

// refill advances b to now at rate tokens/second, capped at capacity.
// A zero stamp means "never used": start full.
func (b *lbucket) refill(now time.Time, rate, capacity float64) {
	if b.stamp.IsZero() {
		b.tokens = capacity
		b.stamp = now
		return
	}
	if now.After(b.stamp) {
		b.tokens += now.Sub(b.stamp).Seconds() * rate
		if b.tokens > capacity {
			b.tokens = capacity
		}
		b.stamp = now
	}
}

type loginLimiter struct {
	mu     sync.Mutex
	now    func() time.Time
	perIP  map[string]*lbucket
	global lbucket
}

func newLoginLimiter(now func() time.Time) *loginLimiter {
	if now == nil {
		now = time.Now
	}
	return &loginLimiter{now: now, perIP: make(map[string]*lbucket)}
}

// allow reports whether one login attempt from ip may proceed, spending
// one token from BOTH buckets when it does. Both are checked before
// either is spent: a denial must cost nothing, or an IP already over
// its own limit would keep draining the global budget everyone else
// shares.
func (l *loginLimiter) allow(ip string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.perIP[ip]
	if !ok {
		if len(l.perIP) >= limiterMapMax {
			l.pruneLocked(now)
		}
		b = &lbucket{}
		l.perIP[ip] = b
	}
	b.refill(now, loginPerIPRefillPerSecond, loginPerIPCapacity)
	l.global.refill(now, loginGlobalRefillPerSecond, loginGlobalCapacity)

	if b.tokens < 1 || l.global.tokens < 1 {
		return false
	}
	b.tokens--
	l.global.tokens--
	return true
}

// pruneLocked drops per-IP buckets that would refill to full right now
// -- an entry indistinguishable from a brand-new one holds no state
// worth keeping. Caller holds l.mu.
func (l *loginLimiter) pruneLocked(now time.Time) {
	for ip, b := range l.perIP {
		refilled := b.tokens
		if now.After(b.stamp) {
			refilled += now.Sub(b.stamp).Seconds() * loginPerIPRefillPerSecond
		}
		if refilled >= loginPerIPCapacity {
			delete(l.perIP, ip)
		}
	}
}
