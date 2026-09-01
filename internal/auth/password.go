// Package auth implements Gantry's optional single-user password gate:
// argon2id password storage, cookie-session lifecycle, and login
// brute-force limiting. It owns all policy; internal/store persists,
// internal/server enforces per-route.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id defaults, stored per-hash inside the PHC string (see
// encodePHC) so they can change here without invalidating anything
// already written. RFC 9106's second recommended profile is m=64MiB,
// t=3, p=4; memory and time are kept as recommended, parallelism is
// dropped to 1 so one login attempt occupies one core of a modest NAS
// CPU instead of four -- logins are rare and ~100ms of one core is an
// acceptable price, four cores at once on a box that's busy doing
// parity math is not. The verify path additionally serializes hashing
// (Manager.hashMu) so concurrent attempts can't stack 64MiB
// allocations.
const (
	argonMemoryKiB = 64 * 1024
	argonTime      = 3
	argonThreads   = 1
	argonSaltLen   = 16
	argonKeyLen    = 32

	// maxStoredMemoryKiB bounds the m= a stored PHC string may ask for
	// before verification refuses to allocate it -- the settings table
	// is trusted, but a hash is long-lived data crossing versions, and
	// "allocate whatever the row says" is exactly the kind of latent
	// foot-gun a corrupted or hand-edited row shouldn't get to pull.
	maxStoredMemoryKiB = 512 * 1024
)

type params struct {
	memoryKiB uint32
	time      uint32
	threads   uint8
}

func defaultParams() params {
	return params{memoryKiB: argonMemoryKiB, time: argonTime, threads: argonThreads}
}

func deriveKey(password string, salt []byte, p params) []byte {
	return argon2.IDKey([]byte(password), salt, p.time, p.memoryKiB, p.threads, argonKeyLen)
}

// encodePHC renders the standard PHC string form
// $argon2id$v=19$m=...,t=...,p=...$<b64 salt>$<b64 key> (RawStdEncoding,
// no padding, per the PHC spec) -- the format argon2 CLI tools and other
// implementations interoperate on, so a future "verify my hash"
// debugging session doesn't need Gantry itself.
func encodePHC(p params, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memoryKiB, p.time, p.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))
}

// parsePHC is the strict inverse of encodePHC: exactly six $-separated
// fields, argon2id only, current argon2 version only, all three params
// present and sane. Anything else errors -- a malformed stored hash is
// a bug or corruption to surface, never something to quietly treat as
// "wrong password".
func parsePHC(phc string) (p params, salt, key []byte, err error) {
	fields := strings.Split(phc, "$")
	// Leading '$' makes fields[0] == "".
	if len(fields) != 6 || fields[0] != "" {
		return p, nil, nil, fmt.Errorf("auth: malformed password hash: want 6 fields, got %d", len(fields))
	}
	if fields[1] != "argon2id" {
		return p, nil, nil, fmt.Errorf("auth: unsupported hash variant %q", fields[1])
	}
	var version int
	if _, err := fmt.Sscanf(fields[2], "v=%d", &version); err != nil || version != argon2.Version {
		return p, nil, nil, fmt.Errorf("auth: unsupported argon2 version %q", fields[2])
	}
	if _, err := fmt.Sscanf(fields[3], "m=%d,t=%d,p=%d", &p.memoryKiB, &p.time, &p.threads); err != nil {
		return p, nil, nil, fmt.Errorf("auth: malformed params %q", fields[3])
	}
	// Sscanf tolerates unconsumed trailing input; require the field to
	// round-trip exactly so "p=1junk" can't parse as p=1.
	if fmt.Sprintf("m=%d,t=%d,p=%d", p.memoryKiB, p.time, p.threads) != fields[3] {
		return p, nil, nil, fmt.Errorf("auth: malformed params %q", fields[3])
	}
	if p.memoryKiB == 0 || p.time == 0 || p.threads == 0 {
		return p, nil, nil, fmt.Errorf("auth: zero-cost params %q", fields[3])
	}
	if p.memoryKiB > maxStoredMemoryKiB {
		return p, nil, nil, fmt.Errorf("auth: stored hash asks for %d KiB, over the %d KiB verify cap", p.memoryKiB, maxStoredMemoryKiB)
	}
	salt, err = base64.RawStdEncoding.DecodeString(fields[4])
	if err != nil {
		return p, nil, nil, fmt.Errorf("auth: malformed salt: %w", err)
	}
	key, err = base64.RawStdEncoding.DecodeString(fields[5])
	if err != nil {
		return p, nil, nil, fmt.Errorf("auth: malformed key: %w", err)
	}
	return p, salt, key, nil
}

// HashPassword derives an argon2id hash of password under a fresh
// random salt and returns it as a PHC string -- the only form the
// password is ever persisted in. The password itself must never be
// logged, stored, or echoed by any caller.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: salt: %w", err)
	}
	p := defaultParams()
	return encodePHC(p, salt, deriveKey(password, salt, p)), nil
}

// VerifyPassword reports whether password matches the stored PHC hash,
// re-deriving with the params the stored string itself carries (so
// hashes written under older defaults keep verifying). The comparison
// is constant-time over the derived keys; the argon2 derivation itself
// runs in full for every candidate, right or wrong, so a failure costs
// the same as a success.
func VerifyPassword(phc, password string) (bool, error) {
	p, salt, key, err := parsePHC(phc)
	if err != nil {
		return false, err
	}
	derived := deriveKey(password, salt, p)
	return subtle.ConstantTimeCompare(derived, key) == 1, nil
}
