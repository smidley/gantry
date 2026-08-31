package auth

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestLimiter() (*loginLimiter, *time.Time) {
	now := time.Unix(1_000_000, 0)
	l := newLoginLimiter(func() time.Time { return now })
	return l, &now
}

func TestLimiterAllowsBurstThenBlocksPerIP(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < loginPerIPCapacity; i++ {
		require.True(t, l.allow("10.0.0.1"), "attempt %d within the burst must pass", i+1)
	}
	require.False(t, l.allow("10.0.0.1"), "attempt over the per-IP burst must be limited")
	require.True(t, l.allow("10.0.0.2"), "a different IP has its own bucket")
}

func TestLimiterRefillsOverTime(t *testing.T) {
	l, now := newTestLimiter()
	for i := 0; i < loginPerIPCapacity; i++ {
		require.True(t, l.allow("10.0.0.1"))
	}
	require.False(t, l.allow("10.0.0.1"))

	// 5/min refill => one token every 12s.
	*now = now.Add(13 * time.Second)
	require.True(t, l.allow("10.0.0.1"), "one token must have refilled after 13s")
	require.False(t, l.allow("10.0.0.1"), "and only one")
}

func TestLimiterGlobalBucketCapsAcrossIPs(t *testing.T) {
	l, _ := newTestLimiter()
	granted := 0
	// Spray from many distinct IPs: per-IP buckets never run out, so
	// only the global bucket can stop this.
	for i := 0; i < loginGlobalCapacity+10; i++ {
		if l.allow(fmt.Sprintf("10.0.%d.%d", i/250, i%250)) {
			granted++
		}
	}
	require.Equal(t, loginGlobalCapacity, granted, "the global bucket must cap total throughput across IPs")
}

func TestLimiterDenyDoesNotSpendTheOtherBucket(t *testing.T) {
	l, _ := newTestLimiter()
	// Exhaust one IP's bucket (spends loginPerIPCapacity global tokens).
	for i := 0; i < loginPerIPCapacity; i++ {
		require.True(t, l.allow("10.0.0.1"))
	}
	// Hammer the exhausted IP: every denial must leave the global
	// bucket alone, or one abusive IP could starve everyone else.
	for i := 0; i < 100; i++ {
		require.False(t, l.allow("10.0.0.1"))
	}
	remaining := 0
	for i := 0; i < loginGlobalCapacity; i++ {
		if l.allow(fmt.Sprintf("10.0.1.%d", i)) {
			remaining++
		}
	}
	require.Equal(t, loginGlobalCapacity-loginPerIPCapacity, remaining,
		"denied attempts must not have consumed global tokens")
}

func TestLimiterPrunesIdleIPBucketsPastTheCap(t *testing.T) {
	l, now := newTestLimiter()
	for i := 0; i < limiterMapMax+50; i++ {
		l.allow(fmt.Sprintf("10.%d.%d.%d", i/65000, (i/250)%250, i%250))
		// Advance so the global bucket refills and old buckets go full
		// (and thus prunable) again.
		*now = now.Add(time.Minute)
	}
	l.mu.Lock()
	size := len(l.perIP)
	l.mu.Unlock()
	require.LessOrEqual(t, size, limiterMapMax, "the per-IP map must not grow without bound")
}
