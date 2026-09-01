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

// Modes decide who authenticates. ModeAuto is the default and the whole
// point of this package: Gantry's own gate is ALWAYS on -- a username +
// password credential is required, and with none stored yet the box is
// in first-run setup (not open). ModeProxy (GANTRY_AUTH=proxy) stands
// the built-in gate down for installs whose reverse proxy (authelia,
// SWAG, nginx auth_request, ...) already authenticates every request
// before Gantry sees it. ModeNone (GANTRY_AUTH=none) is the explicit,
// documented "I want this open" opt-out -- the ONLY way to run Gantry
// with no authentication at all. An unknown value fails safe to
// ModeAuto: a typo in an auth setting must land on mandatory auth, never
// on an open box.
const (
	ModeAuto  = "auto"
	ModeProxy = "proxy"
	ModeNone  = "none"
)

// ParseMode maps a GANTRY_AUTH value to a mode. Empty means auto. An
// unrecognized value returns ModeAuto AND an error: the caller logs the
// bad value and runs with the mandatory gate -- a typo in an auth
// setting must fail closed (auth required), never open.
func ParseMode(v string) (string, error) {
	switch {
	case v == "" || strings.EqualFold(v, ModeAuto):
		return ModeAuto, nil
	case strings.EqualFold(v, ModeProxy):
		return ModeProxy, nil
	case strings.EqualFold(v, ModeNone):
		return ModeNone, nil
	}
	return ModeAuto, fmt.Errorf("auth: unknown GANTRY_AUTH value %q (want auto, proxy, or none)", v)
}

// Sentinel errors the HTTP layer maps to statuses. The messages are
// deliberately user-facing (writeError bodies) and never echo any
// password material.
var (
	ErrRateLimited      = errors.New("too many attempts, wait a minute")
	ErrBadCredentials   = errors.New("invalid username or password")
	ErrNoCredential     = errors.New("no credential is set")
	ErrCredentialExists = errors.New("a credential is already set")
	ErrPasswordTooShort = errors.New("password must be at least 8 characters")
	ErrPasswordTooLong  = errors.New("password must be at most 256 characters")
	ErrUsernameEmpty    = errors.New("username must not be empty")
	ErrUsernameTooLong  = errors.New("username must be at most 64 characters")
	ErrUsernameInvalid  = errors.New("username must not contain control characters")
)

const (
	passwordHashKey = "auth.password_hash"
	usernameKey     = "auth.username"
	passwordMinLen  = 8
	passwordMaxLen  = 256
	usernameMaxLen  = 64

	// migratedUsername is the username handed to a pre-0.1.1 install that
	// stored a password with no username (auth was password-only then).
	// On boot we mint "admin" for it rather than lock the owner out --
	// see New's migration block.
	migratedUsername = "admin"

	// Session policy: "until the browser closes". The cookie itself is a
	// session cookie (no Max-Age/Expires -- gate.go), so a normal browser
	// drops it on close. These two server-side backstops exist for the
	// browser that NEVER closes (a kiosk, a pinned wall-display tab): a
	// session slides forward on activity but lapses after
	// SessionIdleTimeout of silence, and can never outlive
	// SessionAbsoluteCap from login regardless of activity. Touches are
	// written at most once per sessionTouchInterval so the 2s SSE/polling
	// cadence doesn't turn every request into a SQLite write.
	SessionIdleTimeout   = 8 * time.Hour
	SessionAbsoluteCap   = 24 * time.Hour
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
// credential. The password hash lives under passwordHashKey and the
// username under usernameKey, both with "" as "not configured" --
// SettingSet has no delete, an argon2id PHC string is never empty, and a
// stored username is trimmed-non-empty by policy, so the sentinels are
// unambiguous.
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

// Manager owns Gantry's auth policy: credential lifecycle (username +
// argon2id password), session lifecycle, login limiting, and the auth.*
// audit events. One instance per process, wired by cmd/gantry and
// consumed by internal/server through its own minimal AuthIface.
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

	// mu guards the cached credential (hash + username), envManaged, and
	// the failure-event throttle map.
	mu             sync.Mutex
	cachedHash     string
	cachedUsername string
	envManaged     bool
	failures       map[string]*failureState

	// setupMu makes Setup's CredentialSet guard and its writes one
	// critical section, so two concurrent first-run calls can't both
	// observe "no credential yet" and both store one (see Setup). It is
	// a separate lock from mu, held for Setup's whole body: mu itself is
	// only ever taken for a single field read/write and released, never
	// held across a call to another method, so CredentialSet/
	// storePassword/storeUsername each taking mu briefly *inside* the
	// setupMu section cannot deadlock against it.
	setupMu sync.Mutex
}

type Options struct {
	Sessions SessionStore
	Settings SettingsStore
	// AppendEvent records the auth.* audit events (main wiring:
	// store.AppendEvent). Nil skips event logging, same convention as
	// server.Options.AppendEvent.
	AppendEvent func(store.Event) (int64, error)
	// Mode is ModeAuto, ModeProxy, or ModeNone -- run through ParseMode
	// first.
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
	username, _, err := o.Settings.SettingGet(usernameKey)
	if err != nil {
		return nil, fmt.Errorf("auth: load username: %w", err)
	}
	// Migration: a pre-0.1.1 install stored a password with no username
	// (auth was password-only). Mint "admin" for it on boot so the upgrade
	// never locks the owner out -- the existing hash keeps verifying
	// unchanged; only the username is added.
	if hash != "" && username == "" {
		if err := o.Settings.SettingSet(usernameKey, migratedUsername); err != nil {
			return nil, fmt.Errorf("auth: migrate password-only credential: %w", err)
		}
		username = migratedUsername
		log.Printf("auth: migrated a password-only credential to username %q -- change it in Settings > Access", migratedUsername)
	}
	m.cachedHash = hash
	m.cachedUsername = username
	return m, nil
}

func (m *Manager) Mode() string { return m.mode }

// CredentialSet reports whether a credential is configured. Any non-empty
// stored password hash counts -- including a corrupt one that can no
// longer verify anything (fail closed: a corrupt hash keeps the box in
// the logged-out state until EnsureEnvCredential or manual surgery
// rewrites it, it never silently drops back to first-run setup as though
// nothing was ever configured).
func (m *Manager) CredentialSet() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cachedHash != ""
}

// Username returns the configured username ("" when no credential is
// set). Not a secret -- the Settings card shows it so the owner knows
// what they're changing, and the status endpoint returns it only to an
// authenticated caller.
func (m *Manager) Username() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cachedUsername
}

// EnvManaged reports whether the current credential came from
// GANTRY_USERNAME/GANTRY_PASSWORD at this boot -- the Settings UI warns
// that in-app changes will be overwritten by the variables on the next
// start.
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

// checkUsernamePolicy trims and validates a username, returning the
// canonical (trimmed) form to store or compare. The username is not
// secret, but it IS stored in the clear and shown back, so it must stay
// a sane single-line label: non-empty after trimming, bounded, and free
// of control characters (which could smuggle newlines or terminal escapes
// into logs and the Events feed).
func checkUsernamePolicy(u string) (string, error) {
	u = strings.TrimSpace(u)
	if u == "" {
		return "", ErrUsernameEmpty
	}
	if len(u) > usernameMaxLen {
		return "", ErrUsernameTooLong
	}
	for _, r := range u {
		if r < 0x20 || r == 0x7f {
			return "", ErrUsernameInvalid
		}
	}
	return u, nil
}

// verify runs the argon2 check against the cached hash, serialized by
// hashMu (see that field's doc). Callers must have confirmed
// CredentialSet first; an empty hash here is a programming error surfaced
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

// Login verifies username + password for one attempt from ip and returns
// a fresh session token. The limiter gates the ATTEMPT, before
// verification and regardless of whether the credential would have been
// right -- so the limiter's answer carries no information about
// correctness.
//
// No user-enumeration timing leak: the argon2 derivation of the supplied
// password against the stored hash runs on EVERY attempt, whether or not
// the username matched, and only then are the two constant-time results
// combined. A wrong username therefore costs exactly what a wrong
// password costs (~one full argon2 derivation), so an attacker can't
// probe which half was wrong by timing. The username itself is not a
// secret; this defends the *shape* of the answer, not the username.
func (m *Manager) Login(ip, username, password string) (string, error) {
	if !m.CredentialSet() {
		return "", ErrNoCredential
	}
	if !m.limiter.allow(ip) {
		m.recordFailure(ip)
		return "", ErrRateLimited
	}
	m.mu.Lock()
	storedUser := m.cachedUsername
	m.mu.Unlock()
	userOK := subtle.ConstantTimeCompare([]byte(strings.TrimSpace(username)), []byte(storedUser)) == 1
	// Always derive -- see the method doc. verify never early-exits, so
	// the cost is identical for right and wrong on either field. Both
	// userOK and pwOK are fully computed above before they are combined
	// here; the || only negates two already-settled bools, it does not
	// skip any work.
	pwOK, err := m.verify(password)
	if err != nil {
		// A corrupt stored hash: surfaced as a server error, never as
		// "wrong credential" -- see TestVerifyPasswordRejectsMalformed.
		return "", err
	}
	if !userOK || !pwOK {
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
		exp := min(now+int64(SessionIdleTimeout/time.Second), absoluteDeadline)
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

// Setup is the one-shot first-run bootstrap: it creates the initial
// username + password credential and returns a session token so the
// browser that set it lands logged in. It succeeds only while NO
// credential exists; once one is set it returns ErrCredentialExists
// forever (the HTTP layer maps that to 409). No current-password check
// and no rate limit -- there's nothing yet to verify against, exactly as
// exposed as every other route on a box that has never been configured,
// and the mux-wide cross-site header still shuts out drive-by pages.
//
// setupMu holds the guard and the writes it gates as one critical
// section, so two callers racing a fresh box can't both pass the check
// before either has stored anything: the second always sees the first's
// credential already in place and gets ErrCredentialExists instead of a
// session of its own. DeleteAllSessions before issuing one is
// defense-in-depth alongside that lock (the same belt-and-suspenders
// UpdateCredential uses): if anything ever let a second caller reach
// this far anyway, only the last one to finish keeps a live session.
func (m *Manager) Setup(ip, username, password string) (string, error) {
	m.setupMu.Lock()
	defer m.setupMu.Unlock()

	if m.CredentialSet() {
		return "", ErrCredentialExists
	}
	u, err := checkUsernamePolicy(username)
	if err != nil {
		return "", err
	}
	if err := checkPasswordPolicy(password); err != nil {
		return "", err
	}
	if err := m.storePassword(password); err != nil {
		return "", err
	}
	if err := m.storeUsername(u); err != nil {
		return "", err
	}
	if _, err := m.sessions.DeleteAllSessions(); err != nil {
		return "", err
	}
	token, err := m.createSession()
	if err != nil {
		return "", err
	}
	m.event("auth.setup", ip, "info", "credential created from "+ip)
	return token, nil
}

// UpdateCredential changes the username and/or password of an existing
// credential. The current password is always required (rate-limited like
// a login -- a borrowed open tab must not be a free brute-force oracle),
// the new username is always applied, and the password changes only when
// newPassword is non-empty (a blank new password is a username-only
// edit). Every existing session is deleted -- "log out other sessions"
// is the change's defining side effect -- and a fresh token for the
// caller is returned so the change doesn't log out the very browser
// making it.
func (m *Manager) UpdateCredential(ip, current, newUsername, newPassword string) (string, error) {
	if !m.CredentialSet() {
		return "", ErrNoCredential
	}
	u, err := checkUsernamePolicy(newUsername)
	if err != nil {
		return "", err
	}
	changePassword := newPassword != ""
	if changePassword {
		if err := checkPasswordPolicy(newPassword); err != nil {
			return "", err
		}
	}
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
	if changePassword {
		if err := m.storePassword(newPassword); err != nil {
			return "", err
		}
	}
	if err := m.storeUsername(u); err != nil {
		return "", err
	}
	// Log out other sessions: every token minted before the change dies
	// with it; the caller alone gets a fresh one below.
	if _, err := m.sessions.DeleteAllSessions(); err != nil {
		return "", err
	}
	token, err := m.createSession()
	if err != nil {
		return "", err
	}
	m.event("auth.credential_changed", ip, "info", "credential changed from "+ip+"; all other sessions logged out")
	return token, nil
}

// EnsureEnvCredential applies GANTRY_USERNAME + GANTRY_PASSWORD at boot
// (main calls it only when BOTH are set): it verifies them against the
// stored credential first and rewrites only on a genuine change, so an
// unchanged pair doesn't wipe sessions on every restart. A stored hash
// too corrupt to verify is rewritten (the env path is the no-shell
// recovery route). Removing the variables later changes nothing -- this
// only ever runs for a fully-specified pair, which is what keeps a
// template edit from silently reopening the box; auth stays mandatory,
// and the stored credential simply persists.
func (m *Manager) EnsureEnvCredential(username, password string) error {
	u, err := checkUsernamePolicy(username)
	if err != nil {
		return err
	}
	if err := checkPasswordPolicy(password); err != nil {
		return err
	}
	if m.CredentialSet() {
		m.mu.Lock()
		sameUser := m.cachedUsername == u
		m.mu.Unlock()
		ok, verr := m.verify(password)
		if verr == nil && ok && sameUser {
			m.mu.Lock()
			m.envManaged = true
			m.mu.Unlock()
			return nil // unchanged: keep sessions
		}
		// verr != nil: corrupt stored hash -- fall through and rewrite.
	}
	if err := m.storePassword(password); err != nil {
		return err
	}
	if err := m.storeUsername(u); err != nil {
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

// createSessionlessWipeEvent is EnsureEnvCredential's post-store tail: no
// session is issued (there's no browser at boot), all existing ones go,
// and the change is audited.
func (m *Manager) createSessionlessWipeEvent() (int64, error) {
	n, err := m.sessions.DeleteAllSessions()
	if err != nil {
		return 0, err
	}
	m.event("auth.credential_changed", "env", "info", "credential applied from GANTRY_USERNAME/GANTRY_PASSWORD; all sessions logged out")
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

// storeUsername persists a (policy-checked, trimmed) username and updates
// the cache. Like storePassword it leaves sessions alone.
func (m *Manager) storeUsername(u string) error {
	if err := m.settings.SettingSet(usernameKey, u); err != nil {
		return err
	}
	m.mu.Lock()
	m.cachedUsername = u
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
		ExpiresAt: now + int64(SessionIdleTimeout/time.Second),
	}
	if err := m.sessions.InsertSession(sess); err != nil {
		return "", err
	}
	return token, nil
}

// recordFailure counts one failed attempt (wrong credential OR rate-
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
