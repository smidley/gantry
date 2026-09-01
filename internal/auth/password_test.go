package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHashPasswordProducesArgon2idPHCString(t *testing.T) {
	phc, err := HashPassword("correct horse battery staple")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(phc, "$argon2id$v=19$m=65536,t=3,p=1$"),
		"stored form must be a standard PHC string carrying its own params, got %q", phc)
	require.NotContains(t, phc, "correct horse", "the password itself must never appear in the stored form")

	// Salted: hashing the same password twice must not repeat.
	phc2, err := HashPassword("correct horse battery staple")
	require.NoError(t, err)
	require.NotEqual(t, phc, phc2)
}

func TestVerifyPasswordAcceptsOnlyTheRightPassword(t *testing.T) {
	phc, err := HashPassword("swordfish-123")
	require.NoError(t, err)

	ok, err := VerifyPassword(phc, "swordfish-123")
	require.NoError(t, err)
	require.True(t, ok)

	for _, wrong := range []string{"", "swordfish-12", "swordfish-1234", "SWORDFISH-123", strings.Repeat("x", 300)} {
		ok, err := VerifyPassword(phc, wrong)
		require.NoError(t, err, "a wrong password is a clean false, never an error")
		require.False(t, ok, "must reject %q", wrong)
	}
}

// TestVerifyPasswordHonorsStoredParams pins param agility: verification
// derives with the params ENCODED IN the stored PHC string, not the
// current compile-time defaults -- so hashes written by an older (or
// differently tuned) build keep verifying after the defaults change.
func TestVerifyPasswordHonorsStoredParams(t *testing.T) {
	// A hand-built PHC with cheaper-than-default params (m=8MiB, t=1).
	phc := encodePHC(params{memoryKiB: 8 * 1024, time: 1, threads: 1}, []byte("0123456789abcdef"),
		deriveKey("open sesame", []byte("0123456789abcdef"), params{memoryKiB: 8 * 1024, time: 1, threads: 1}))

	ok, err := VerifyPassword(phc, "open sesame")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = VerifyPassword(phc, "open sesame!")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestVerifyPasswordRejectsMalformedStoredHashes(t *testing.T) {
	cases := []string{
		"",
		"plaintext-not-a-hash",
		"$argon2i$v=19$m=65536,t=3,p=1$c2FsdA$aGFzaA",      // wrong variant
		"$argon2id$v=18$m=65536,t=3,p=1$c2FsdA$aGFzaA",     // wrong version
		"$argon2id$v=19$m=65536,t=3$c2FsdA$aGFzaA",         // missing p
		"$argon2id$v=19$m=65536,t=3,p=1$!!!$aGFzaA",        // bad salt b64
		"$argon2id$v=19$m=65536,t=3,p=1$c2FsdA$!!!",        // bad key b64
		"$argon2id$v=19$m=0,t=3,p=1$c2FsdA$aGFzaA",         // zero memory
		"$argon2id$v=19$m=65536,t=0,p=1$c2FsdA$aGFzaA",     // zero time
		"$argon2id$v=19$m=99999999,t=3,p=1$c2FsdA$aGFzaA",  // absurd memory: refuse to allocate
		"$argon2id$v=19$m=65536,t=3,p=1$c2FsdA",            // missing key field
		"$argon2id$v=19$m=65536,t=3,p=1$c2FsdA$aGFzaA$x=1", // trailing junk field
		"$argon2id$v=19$m=65536,t=3,p=1junk$c2FsdA$aGFzaA", // junk after a param Sscanf would tolerate
	}
	for _, phc := range cases {
		ok, err := VerifyPassword(phc, "whatever")
		require.Error(t, err, "malformed stored hash %q must error, not silently mismatch", phc)
		require.False(t, ok)
	}
}
