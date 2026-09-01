package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/smidley/gantry/internal/store"
)

// Modes: ModeAuto is the default -- the gate is simply whether a
// password is configured. ModeProxy (GANTRY_AUTH=proxy) switches the
// built-in gate off entirely for installs whose reverse proxy
// (authelia, SWAG, nginx auth_request, ...) already authenticates every
// request before Gantry sees it; it also suppresses the Settings
// "no password set" nudge, which would otherwise nag exactly the people
// who solved this one layer up.
const (
	ModeAuto  = "auto"
	ModeProxy = "proxy"
)

// ParseMode maps a GANTRY_AUTH value to a mode. Empty means auto. An
// unrecognized value returns ModeAuto AND an error: the caller logs the
// bad value and runs with the password-controlled gate -- a typo in an
// auth setting must fail closed (gate stays available), never open.
func ParseMode(v string) (string, error) {
	switch {
	case v == "" || strings.EqualFold(v, ModeAuto):
		return ModeAuto, nil
	case strings.EqualFold(v, ModeProxy):
		return ModeProxy, nil
	}
	return ModeAuto, fmt.Errorf("auth: unknown GANTRY_AUTH value %q (want auto or proxy)", v)
}

// Sentinel errors the HTTP layer maps to statuses. The messages are
// deliberately user-facing (writeError bodies) and never echo any
// password material.
var (
	ErrRateLimited      = errors.New("too many attempts, wait a minute")
	ErrBadCredentials   = errors.New("invalid password")
	ErrNoPassword       = errors.New("no password is set")
	ErrPasswordTooShort = errors.New("password must be at least 8 characters")
	ErrPasswordTooLong  = errors.New("password must be at most 256 characters")
)

const (
	passwordHashKey = "auth.password_hash"
	passwordMinLen  = 8
	passwordMaxLen  = 256

	// Session policy: a browser stays logged in as long as it comes
	// back within SessionSlidingWindow, but never longer than
	// SessionAbsoluteCap after login regardless of activity. Touches
	// are written at most once per sessionTouchInterval so the 2s SSE/
	// polling cadence doesn't turn every request into a SQLite write.
	SessionSlidingWindow = 7 * 24 * time.Hour
	SessionAbsoluteCap   = 30 * 24 * time.Hour
	sessionTouchInterval = 10 * time.Minute

	sessionTokenBytes = 32 // 256-bit random cookie token

	// failureWindow is the auth.login_failed event coalescing window --
	// the alert dispatcher's once-per-entity-per-hour discipline
	// (recordFailureEvent), so a sustained guessing run informs the
	// Events feed instead of flooding it.
	failureWindow = time.Hour
	// failureMapMax bounds the per-IP failure-throttle map the same way
	// limiterMapMax bounds the limiter's.
	failureMapMax = 512
)

// SettingsStore is the slice of *store.Store the manager needs for the
// password hash. The hash is stored under passwordHashKey with "" as
// "not configured" -- SettingSet has no delete, and an argon2id PHC
// string is never empty, so the sentinel is unambiguous.
type SettingsStore interface {
	SettingGet(key string) (string, bool, error)
	SettingSet(key, value string) error
}

// SessionStore is the slice of *store.Store the manager needs for
// sessions (see store/sessions.go -- persistence only, policy here).
type SessionStore interface {
	InsertSession(s store.Session) error
	GetSession(tokenHash string) (store.Session, bool, error)
	TouchSession(tokenHash string, lastSeen, expiresAt int64) error
	DeleteSession(tokenHash string) error
	DeleteAllSessions() (int64, error)
}

type failureState struct {
	windowStart time.Time
	suppressed  int
}

// Manager owns Gantry's auth policy: password lifecycle, session
// lifecycle, login limiting, and the auth.* audit events. One instance
// per process, wired by cmd/gantry and consumed by internal/server
// through its own minimal AuthIface.
type Manager struct {
	sessions    SessionStore
	settings    SettingsStore
	appendEvent func(store.Event) (int64, error)
	now         func() time.Time
	mode        string
	limiter     *loginLimiter

	// hashMu serializes every argon2 derivation: each one allocates
	// argonMemoryKiB (64MiB), so unserialized concurrent logins could
	// stack allocations the global rate limit alone doesn't prevent
	// (it caps sustained rate, not instantaneous concurrency).
	hashMu sync.Mutex

	// mu guards the cached hash, envManaged, and the failure-event
	// throttle map.
	mu         sync.Mutex
	cachedHash string
	envManaged bool
	failures   map[string]*failureState
}

type Options struct {
	Sessions SessionStore
	Settings SettingsStore
	// AppendEvent records the auth.* audit events (main wiring:
	// store.AppendEvent). Nil skips event logging, same convention as
	// server.Options.AppendEvent.
	AppendEvent func(store.Event) (int64, error)
	// Mode is ModeAuto or ModeProxy -- run through ParseMode first.
	Mode string
	// Now is the clock, nil for time.Now -- injected by tests to drive
	// expiry and limiter refill.
	Now func() time.Time
}

func New(o Options) (*Manager, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Mode == "" {
		o.Mode = ModeAuto
	}
	m := &Manager{
		sessions:    o.Sessions,
		settings:    o.Settings,
		appendEvent: o.AppendEvent,
		now:         o.Now,
		mode:        o.Mode,
		limiter:     newLoginLimiter(o.Now),
		failures:    make(map[string]*failureState),
	}
	hash, _, err := o.Settings.SettingGet(passwordHashKey)
	if err != nil {
		return nil, fmt.Errorf("auth: load password hash: %w", err)
	}
	m.cachedHash = hash
	return m, nil
}

func (m *Manager) Mode() string { return m.mode }

// PasswordSet reports whether a password is configured. Any non-empty
// stored value counts -- including a corrupt one that can no longer
// verify anything (fail closed: a corrupt hash locks the gate shut
// until EnsureEnvPassword or manual surgery rewrites it, it never
// silently reopens the box).
func (m *Manager) PasswordSet() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cachedHash != ""
}

// EnvManaged reports whether the current password came from
// GANTRY_PASSWORD at this boot -- the Settings UI warns that in-app
// changes will be overwritten by the variable on the next start.
func (m *Manager) EnvManaged() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.envManaged
}

// HashToken derives a session token's storage key: SHA-256, hex.
// Exported so tests can assert the raw token never lands in the
// sessions table. Hashing before lookup is also the timing defense for
// session validation: the attacker-controlled string is digested before
// any comparison, so equality timing can only leak digest bits, and
// turning those into a usable token is a preimage attack on SHA-256.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func checkPasswordPolicy(pw string) error {
	if len(pw) < passwordMinLen {
		return ErrPasswordTooShort
	}
	if len(pw) > passwordMaxLen {
		return ErrPasswordTooLong
	}
	return nil
}

// verify runs the argon2 check against the cached hash, serialized by
// hashMu (see that field's doc). Callers must have confirmed
// PasswordSet first; an empty hash here is a programming error surfaced
// as a normal mismatch.
func (m *Manager) verify(password string) (bool, error) {
	m.mu.Lock()
	phc := m.cachedHash
	m.mu.Unlock()
	if phc == "" {
		return false, nil
	}
	m.hashMu.Lock()
	defer m.hashMu.Unlock()
	return VerifyPassword(phc, password)
}

// Login verifies password for one attempt from ip and returns a fresh
// session token. The limiter gates the ATTEMPT, before verification and
// regardless of whether the password would have been right -- so the
// limiter's answer carries no information about correctness. A wrong
// password pays the full argon2 cost (VerifyPassword never
// early-exits), so failure and success are indistinguishable by timing.
func (m *Manager) Login(ip, password string) (string, error) {
	if !m.PasswordSet() {
		return "", ErrNoPassword
	}
	if !m.limiter.allow(ip) {
		m.recordFailure(ip)
		return "", ErrRateLimited
	}
	ok, err := m.verify(password)
	if err != nil {
		// A corrupt stored hash: surfaced as a server error, never as
		// "wrong password" -- see TestVerifyPasswordRejectsMalformed.
		return "", err
	}
	if !ok {
		m.recordFailure(ip)
		return "", ErrBadCredentials
	}
	token, err := m.createSession()
	if err != nil {
		return "", err
	}
	m.event("auth.login_ok", ip, "info", "login from "+ip)
	return token, nil
}

// Authenticate reports whether token names a live session, sliding its
// expiry forward (at most once per sessionTouchInterval) when it does.
// Every failure path is a plain false: the HTTP layer answers 401 the
// same way for absent, garbage, expired, and logged-out tokens.
func (m *Manager) Authenticate(token string) bool {
	if token == "" {
		return false
	}
	h := HashToken(token)
	sess, ok, err := m.sessions.GetSession(h)
	if err != nil {
		log.Printf("auth: session lookup: %v", err)
		return false // fail closed
	}
	if !ok {
		return false
	}
	// The primary-key lookup already matched; re-compare constant-time
	// so this path's comparison discipline is locally visible and
	// independent of the storage engine's.
	if subtle.ConstantTimeCompare([]byte(sess.TokenHash), []byte(h)) != 1 {
		return false
	}
	now := m.now().Unix()
	absoluteDeadline := sess.CreatedAt + int64(SessionAbsoluteCap/time.Second)
	if now >= sess.ExpiresAt || now >= absoluteDeadline {
		if err := m.sessions.DeleteSession(h); err != nil {
			log.Printf("auth: expired session delete: %v", err)
		}
		return false
	}
	if now-sess.LastSeen >= int64(sessionTouchInterval/time.Second) {
		exp := min(now+int64(SessionSlidingWindow/time.Second), absoluteDeadline)
		if err := m.sessions.TouchSession(h, now, exp); err != nil {
			log.Printf("auth: session touch: %v", err)
		}
	}
	return true
}

// Logout deletes token's session. Idempotent; garbage tokens are a
// no-op (their digest simply matches nothing).
func (m *Manager) Logout(token string) {
	if token == "" {
		return
	}
	if err := m.sessions.DeleteSession(HashToken(token)); err != nil {
		log.Printf("auth: logout: %v", err)
	}
}

// SetPassword sets (current ignored when none is configured yet) or
// changes (current required and rate-limited like a login attempt --
// a borrowed session must not be a free brute-force oracle for the
// current password) the password. Every existing session is deleted --
// "log out other sessions" is the change's defining side effect -- and
// a fresh token for the caller is returned so the change doesn't log
// out the very browser making it.
func (m *Manager) SetPassword(ip, current, newPassword string) (string, error) {
	if err := checkPasswordPolicy(newPassword); err != nil {
		return "", err
	}
	firstSet := !m.PasswordSet()
	if !firstSet {
		if !m.limiter.allow(ip) {
			m.recordFailure(ip)
			return "", ErrRateLimited
		}
		ok, err := m.verify(current)
		if err != nil {
			return "", err
		}
		if !ok {
			m.recordFailure(ip)
			return "", ErrBadCredentials
		}
	}
	if err := m.storePassword(newPassword); err != nil {
		return "", err
	}
	// Log out other sessions: every token minted under the old password
	// dies with it; the caller alone gets a fresh one below.
	if _, err := m.sessions.DeleteAllSessions(); err != nil {
		return "", err
	}
	token, err := m.createSession()
	if err != nil {
		return "", err
	}
	if firstSet {
		m.event("auth.password_changed", ip, "info", "password set from "+ip)
	} else {
		m.event("auth.password_changed", ip, "info", "password changed from "+ip+"; all other sessions logged out")
	}
	return token, nil
}

// Disable turns the password gate off: requires the current password
// (rate-limited like a login), clears the stored hash, and wipes every
// session. Already-off is a no-op success. This is the ONLY way to
// disable auth -- removing GANTRY_PASSWORD from the template
// deliberately does not (see EnsureEnvPassword).
func (m *Manager) Disable(ip, current string) error {
	if !m.PasswordSet() {
		return nil
	}
	if !m.limiter.allow(ip) {
		m.recordFailure(ip)
		return ErrRateLimited
	}
	ok, err := m.verify(current)
	if err != nil {
		return err
	}
	if !ok {
		m.recordFailure(ip)
		return ErrBadCredentials
	}
	if err := m.settings.SettingSet(passwordHashKey, ""); err != nil {
		return err
	}
	m.mu.Lock()
	m.cachedHash = ""
	m.mu.Unlock()
	if _, err := m.sessions.DeleteAllSessions(); err != nil {
		return err
	}
	m.event("auth.disabled", ip, "warning", "authentication disabled from "+ip)
	return nil
}

// EnsureEnvPassword applies GANTRY_PASSWORD at boot: verifies it
// against the stored hash first and rewrites only on a genuine change,
// so an unchanged variable doesn't wipe sessions on every restart. A
// stored hash too corrupt to verify is rewritten (the env path is the
// no-shell recovery route). Removing the variable later changes
// nothing -- this only ever runs for a non-empty value, which is what
// keeps a template edit from silently reopening the box.
func (m *Manager) EnsureEnvPassword(pw string) error {
	if err := checkPasswordPolicy(pw); err != nil {
		return err
	}
	if m.PasswordSet() {
		ok, err := m.verify(pw)
		if err == nil && ok {
			m.mu.Lock()
			m.envManaged = true
			m.mu.Unlock()
			return nil // unchanged: keep sessions
		}
		// err != nil: corrupt stored hash -- fall through and rewrite.
	}
	if err := m.storePassword(pw); err != nil {
		return err
	}
	if _, err := m.createSessionlessWipeEvent(); err != nil {
		return err
	}
	m.mu.Lock()
	m.envManaged = true
	m.mu.Unlock()
	return nil
}

// createSessionlessWipeEvent is EnsureEnvPassword's post-store tail: no
// session is issued (there's no browser at boot), all existing ones go,
// and the change is audited.
func (m *Manager) createSessionlessWipeEvent() (int64, error) {
	n, err := m.sessions.DeleteAllSessions()
	if err != nil {
		return 0, err
	}
	m.event("auth.password_changed", "env", "info", "password applied from GANTRY_PASSWORD; all sessions logged out")
	return n, nil
}

// storePassword hashes (serialized, see hashMu) and persists newPassword,
// then updates the cache. It deliberately does NOT touch sessions --
// each caller owns its own wipe/reissue choreography.
func (m *Manager) storePassword(newPassword string) error {
	m.hashMu.Lock()
	phc, err := HashPassword(newPassword)
	m.hashMu.Unlock()
	if err != nil {
		return err
	}
	if err := m.settings.SettingSet(passwordHashKey, phc); err != nil {
		return err
	}
	m.mu.Lock()
	m.cachedHash = phc
	m.mu.Unlock()
	return nil
}

func (m *Manager) createSession() (string, error) {
	buf := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	now := m.now().Unix()
	sess := store.Session{
		TokenHash: HashToken(token),
		CreatedAt: now,
		LastSeen:  now,
		ExpiresAt: now + int64(SessionSlidingWindow/time.Second),
	}
	if err := m.sessions.InsertSession(sess); err != nil {
		return "", err
	}
	return token, nil
}

// recordFailure counts one failed attempt (wrong password OR rate-
// limited -- both are attempts) from ip and appends auth.login_failed,
// coalesced to one event per IP per failureWindow: the first failure
// logs immediately; the ones after it inside the window are counted;
// the first failure PAST the window logs again carrying that count.
// Like the dispatcher's own lazy flush, a run that stops mid-window
// leaves its count unsurfaced until the next failure -- acceptable for
// an audit trail whose point is "someone is guessing", already made by
// the first event.
func (m *Manager) recordFailure(ip string) {
	now := m.now()
	m.mu.Lock()
	fs, ok := m.failures[ip]
	switch {
	case !ok:
		if len(m.failures) >= failureMapMax {
			for k, v := range m.failures {
				if now.Sub(v.windowStart) >= failureWindow {
					delete(m.failures, k)
				}
			}
		}
		m.failures[ip] = &failureState{windowStart: now}
		m.mu.Unlock()
		m.event("auth.login_failed", ip, "warning", "failed login from "+ip)
	case now.Sub(fs.windowStart) >= failureWindow:
		suppressed := fs.suppressed
		fs.windowStart = now
		fs.suppressed = 0
		m.mu.Unlock()
		detail := "failed login from " + ip
		if suppressed > 0 {
			detail = fmt.Sprintf("%s (%d more in the last hour not logged individually)", detail, suppressed)
		}
		m.event("auth.login_failed", ip, "warning", detail)
	default:
		fs.suppressed++
		m.mu.Unlock()
	}
}

func (m *Manager) event(kind, entity, severity, detail string) {
	if m.appendEvent == nil {
		return
	}
	if _, err := m.appendEvent(store.Event{Kind: kind, Entity: entity, Severity: severity, Detail: detail}); err != nil {
		log.Printf("auth: append %s: %v", kind, err)
	}
}
