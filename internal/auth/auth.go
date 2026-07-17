// SPDX-License-Identifier: AGPL-3.0-only

// Package auth implements OpenBox's organization-scoped authentication boundary.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"golang.org/x/crypto/ssh"
)

const (
	SessionCookie = "openbox_session"
	CSRFHeader    = "X-CSRF-Token"
	// CSRFQuery is the browser-deliverable CSRF channel for WebSocket upgrades
	// (browsers cannot set custom headers on the WebSocket constructor).
	CSRFQuery = "csrf"

	// ScopeOwner preserves the pre-organization token behavior. It is a
	// compatibility super-scope for local owner tokens and cannot be combined
	// with a least-privilege scope.
	ScopeOwner          = "owner"
	ScopeInstancesRead  = "instances:read"
	ScopeInstancesWrite = "instances:write"
	ScopeOperationsRead = "operations:read"
	ScopeAuditRead      = "audit:read"
	ScopeWebhooksRead   = "webhooks:read"
	ScopeWebhooksWrite  = "webhooks:write"
	ScopeRoutesRead     = "routes:read"
	ScopeRoutesWrite    = "routes:write"
	ScopeProfilesRead   = "profiles:read"
	ScopeProfilesWrite  = "profiles:write"
	ScopeArtifactsRead  = "artifacts:read"
	ScopeArtifactsWrite = "artifacts:write"

	DefaultBootstrapTTL = 20 * time.Minute
	DefaultSessionTTL   = 12 * time.Hour
)

var ErrUnauthenticated = errors.New("authentication required")
var ErrForbidden = errors.New("authentication forbidden")
var ErrBootstrapUnavailable = errors.New("bootstrap unavailable")
var ErrRateLimited = errors.New("authentication rate limited")

type BootstrapStatus struct {
	Required  bool       `json:"required"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}
type Session struct {
	OwnerID   domain.OwnerID `json:"owner_id"`
	UserID    string         `json:"user_id"`
	Username  string         `json:"username"`
	Role      string         `json:"role"`
	ExpiresAt time.Time      `json:"expires_at"`
	CSRFToken string         `json:"csrf_token,omitempty"`
}
type User struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}
type Membership struct {
	OwnerID domain.OwnerID
	User    User
}
type Token struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	Secret     string     `json:"secret,omitempty"`
}

// Principal is the authenticated token identity. It keeps bearer scopes
// separate from owner identity so the HTTP transport can enforce them before
// calling application services.
type Principal struct {
	OwnerID domain.OwnerID
	UserID  string
	Role    string
	Scopes  []string
}
type SSHKey struct {
	ID          string    `json:"id"`
	Label       string    `json:"label"`
	Fingerprint string    `json:"fingerprint"`
	PublicKey   string    `json:"public_key"`
	CreatedAt   time.Time `json:"created_at"`
}

type Store interface {
	EnsureBootstrap(context.Context, []byte, time.Time, time.Time) (bool, error)
	BootstrapStatus(context.Context, time.Time) (BootstrapStatus, error)
	MatchBootstrap(context.Context, []byte, time.Time) error
	ConsumeBootstrap(context.Context, []byte, string, time.Time) (Membership, error)
	UserCredential(context.Context, string) (Membership, string, error)
	UpdateCredential(context.Context, string, string, time.Time) error
	CreateSession(context.Context, []byte, Membership, []byte, time.Time, time.Time) error
	RotateSession(context.Context, []byte, []byte, Membership, []byte, time.Time, time.Time) error
	UpdateSessionCSRF(context.Context, []byte, []byte, time.Time) error
	Session(context.Context, []byte, time.Time) (Membership, []byte, time.Time, error)
	RevokeSession(context.Context, []byte, time.Time) error
	CreateUser(context.Context, domain.OwnerID, User, string, time.Time) error
	ListUsers(context.Context, domain.OwnerID) ([]User, error)
	CreateToken(context.Context, Token, domain.OwnerID, []byte) error
	ListTokens(context.Context, domain.OwnerID) ([]Token, error)
	TokenOwner(context.Context, []byte, time.Time) (domain.OwnerID, []string, error)
	RevokeToken(context.Context, domain.OwnerID, string, time.Time) error
	CreateSSHKey(context.Context, SSHKey, domain.OwnerID) error
	ListSSHKeys(context.Context, domain.OwnerID) ([]SSHKey, error)
	UpdateSSHKey(context.Context, domain.OwnerID, string, string, time.Time) (SSHKey, error)
	DeleteSSHKey(context.Context, domain.OwnerID, string) error
}

type Manager struct {
	store        Store
	now          func() time.Time
	limiter      *Limiter
	hashPassword func(string, PasswordParams) (string, error)
}

func New(store Store) (*Manager, error) {
	if store == nil {
		return nil, errors.New("auth store is required")
	}
	return &Manager{store: store, now: func() time.Time { return time.Now().UTC() }, limiter: NewLimiter(5, 15*time.Minute, 1024), hashPassword: HashPassword}, nil
}
func (m *Manager) WithClock(now func() time.Time) *Manager { m.now = now; return m }
func (m *Manager) WithPasswordHasher(hash func(string, PasswordParams) (string, error)) *Manager {
	if hash != nil {
		m.hashPassword = hash
	}
	return m
}

func (m *Manager) EnsureBootstrap(ctx context.Context) (string, error) {
	secret, raw, err := randomSecret(32)
	if err != nil {
		return "", err
	}
	now := m.now()
	created, err := m.store.EnsureBootstrap(ctx, digest(raw), now, now.Add(DefaultBootstrapTTL))
	if err != nil || !created {
		return "", err
	}
	return secret, nil
}
func (m *Manager) BootstrapStatus(ctx context.Context) (BootstrapStatus, error) {
	return m.store.BootstrapStatus(ctx, m.now())
}
func (m *Manager) Bootstrap(ctx context.Context, key, secret, password string) (Session, string, error) {
	if !m.limiter.Allow(key, m.now()) {
		return Session{}, "", ErrRateLimited
	}
	if err := validatePassword(password); err != nil {
		return Session{}, "", err
	}
	raw, err := decodeSecret(secret)
	if err != nil {
		return Session{}, "", ErrBootstrapUnavailable
	}
	if err := m.store.MatchBootstrap(ctx, digest(raw), m.now()); err != nil {
		return Session{}, "", err
	}
	hash, err := m.hashPassword(password, DefaultPasswordParams)
	if err != nil {
		return Session{}, "", err
	}
	membership, err := m.store.ConsumeBootstrap(ctx, digest(raw), hash, m.now())
	if err != nil {
		return Session{}, "", err
	}
	m.limiter.Reset(key)
	return m.issueSession(ctx, membership)
}
func (m *Manager) Login(ctx context.Context, key, username, password string) (Session, string, error) {
	if !m.limiter.Allow(key, m.now()) {
		return Session{}, "", ErrRateLimited
	}
	membership, encoded, err := m.store.UserCredential(ctx, username)
	if err != nil {
		return Session{}, "", ErrUnauthenticated
	}
	valid, rehash, err := VerifyPassword(encoded, password)
	if err != nil || !valid {
		return Session{}, "", ErrUnauthenticated
	}
	m.limiter.Reset(key)
	if rehash {
		if upgraded, hashErr := HashPassword(password, DefaultPasswordParams); hashErr == nil {
			_ = m.store.UpdateCredential(ctx, membership.User.ID, upgraded, m.now())
		}
	}
	return m.issueSession(ctx, membership)
}
func (m *Manager) issueSession(ctx context.Context, membership Membership) (Session, string, error) {
	secret, raw, err := randomSecret(32)
	if err != nil {
		return Session{}, "", err
	}
	csrf, csrfRaw, err := randomSecret(32)
	if err != nil {
		return Session{}, "", err
	}
	now, expires := m.now(), m.now().Add(DefaultSessionTTL)
	if err := m.store.CreateSession(ctx, digest(raw), membership, digest(csrfRaw), now, expires); err != nil {
		return Session{}, "", err
	}
	return sessionFromMembership(membership, expires, csrf), secret, nil
}
func (m *Manager) AuthenticateSession(ctx context.Context, secret, csrf string, mutation bool) (domain.OwnerID, error) {
	principal, err := m.AuthenticateSessionPrincipal(ctx, secret, csrf, mutation)
	if err != nil {
		return "", err
	}
	return principal.OwnerID, nil
}
func (m *Manager) AuthenticateSessionPrincipal(ctx context.Context, secret, csrf string, mutation bool) (Principal, error) {
	raw, err := decodeSecret(secret)
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	membership, csrfHash, _, err := m.store.Session(ctx, digest(raw), m.now())
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	if mutation {
		c, err := decodeSecret(csrf)
		if err != nil || !equalDigest(csrfHash, digest(c)) {
			return Principal{}, ErrForbidden
		}
	}
	return Principal{OwnerID: membership.OwnerID, UserID: membership.User.ID, Role: membership.User.Role}, nil
}
func (m *Manager) RotateSession(ctx context.Context, oldSecret string, owner domain.OwnerID) (Session, string, error) {
	oldRaw, err := decodeSecret(oldSecret)
	if err != nil {
		return Session{}, "", ErrUnauthenticated
	}
	membership, _, expires, err := m.store.Session(ctx, digest(oldRaw), m.now())
	if err != nil || membership.OwnerID != owner {
		return Session{}, "", ErrUnauthenticated
	}
	secret, raw, err := randomSecret(32)
	if err != nil {
		return Session{}, "", err
	}
	csrf, csrfRaw, err := randomSecret(32)
	if err != nil {
		return Session{}, "", err
	}
	now := m.now()
	if err := m.store.RotateSession(ctx, digest(oldRaw), digest(raw), membership, digest(csrfRaw), now, expires); err != nil {
		return Session{}, "", ErrUnauthenticated
	}
	return sessionFromMembership(membership, expires, csrf), secret, nil
}
func (m *Manager) RefreshCSRF(ctx context.Context, secret string) (Session, error) {
	raw, err := decodeSecret(secret)
	if err != nil {
		return Session{}, ErrUnauthenticated
	}
	membership, _, expires, err := m.store.Session(ctx, digest(raw), m.now())
	if err != nil {
		return Session{}, ErrUnauthenticated
	}
	csrf, csrfRaw, err := randomSecret(32)
	if err != nil {
		return Session{}, err
	}
	if err := m.store.UpdateSessionCSRF(ctx, digest(raw), digest(csrfRaw), m.now()); err != nil {
		return Session{}, ErrUnauthenticated
	}
	return sessionFromMembership(membership, expires, csrf), nil
}
func (m *Manager) CookieMaxAge(expires time.Time) int {
	seconds := int(math.Ceil(expires.Sub(m.now()).Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}
func (m *Manager) Logout(ctx context.Context, secret string) error {
	raw, err := decodeSecret(secret)
	if err != nil {
		return nil
	}
	return m.store.RevokeSession(ctx, digest(raw), m.now())
}
func (m *Manager) CreateToken(ctx context.Context, owner domain.OwnerID, name string, scopes []string, expires *time.Time) (Token, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return Token{}, errors.New("invalid token name")
	}
	normalized, err := normalizeScopes(scopes)
	if err != nil {
		return Token{}, err
	}
	if expires != nil && !expires.After(m.now()) {
		return Token{}, errors.New("token expiry must be in the future")
	}
	secret, raw, err := randomSecret(32)
	if err != nil {
		return Token{}, err
	}
	id, _, err := randomSecret(12)
	if err != nil {
		return Token{}, err
	}
	t := Token{ID: "tok_" + id, Name: name, Scopes: normalized, CreatedAt: m.now(), ExpiresAt: expires, Secret: "obx_" + secret}
	if err := m.store.CreateToken(ctx, t, owner, digest(raw)); err != nil {
		return Token{}, err
	}
	return t, nil
}
func (m *Manager) AuthenticateBearer(ctx context.Context, value string) (domain.OwnerID, error) {
	principal, err := m.AuthenticateBearerPrincipal(ctx, value)
	if err != nil {
		return "", err
	}
	if len(principal.Scopes) != 1 || principal.Scopes[0] != ScopeOwner {
		return "", ErrForbidden
	}
	return principal.OwnerID, nil
}
func (m *Manager) AuthenticateBearerPrincipal(ctx context.Context, value string) (Principal, error) {
	if !strings.HasPrefix(value, "obx_") {
		return Principal{}, ErrUnauthenticated
	}
	raw, err := decodeSecret(strings.TrimPrefix(value, "obx_"))
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	owner, scopes, err := m.store.TokenOwner(ctx, digest(raw), m.now())
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	normalized, err := normalizeScopes(scopes)
	if err != nil {
		return Principal{}, ErrForbidden
	}
	return Principal{OwnerID: owner, Scopes: normalized}, nil
}
func (m *Manager) ListTokens(ctx context.Context, owner domain.OwnerID) ([]Token, error) {
	return m.store.ListTokens(ctx, owner)
}
func (m *Manager) RevokeToken(ctx context.Context, owner domain.OwnerID, id string) error {
	return m.store.RevokeToken(ctx, owner, id, m.now())
}
func (m *Manager) AddUser(ctx context.Context, owner domain.OwnerID, username, displayName, password string) (User, error) {
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)
	if !validUsername(username) {
		return User{}, errors.New("invalid username")
	}
	if displayName == "" {
		displayName = username
	}
	if len(displayName) > 100 {
		return User{}, errors.New("invalid display name")
	}
	hash, err := m.hashPassword(password, DefaultPasswordParams)
	if err != nil {
		return User{}, err
	}
	id, _, err := randomSecret(12)
	if err != nil {
		return User{}, err
	}
	user := User{ID: "usr_" + id, Username: username, DisplayName: displayName, Role: "member", CreatedAt: m.now()}
	if err := m.store.CreateUser(ctx, owner, user, hash, m.now()); err != nil {
		return User{}, err
	}
	return user, nil
}
func (m *Manager) ListUsers(ctx context.Context, owner domain.OwnerID) ([]User, error) {
	return m.store.ListUsers(ctx, owner)
}
func (m *Manager) AddSSHKey(ctx context.Context, owner domain.OwnerID, label, value string) (SSHKey, error) {
	label = strings.TrimSpace(label)
	if label == "" || len(label) > 100 || len(value) > 16*1024 {
		return SSHKey{}, errors.New("invalid SSH key")
	}
	key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(value)))
	if err != nil || len(options) != 0 || len(strings.TrimSpace(string(rest))) != 0 {
		return SSHKey{}, errors.New("malformed SSH authorized key")
	}
	id, _, err := randomSecret(12)
	if err != nil {
		return SSHKey{}, err
	}
	normalized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	item := SSHKey{ID: "key_" + id, Label: label, Fingerprint: ssh.FingerprintSHA256(key), PublicKey: normalized, CreatedAt: m.now()}
	if err := m.store.CreateSSHKey(ctx, item, owner); err != nil {
		return SSHKey{}, err
	}
	return item, nil
}
func (m *Manager) ListSSHKeys(ctx context.Context, owner domain.OwnerID) ([]SSHKey, error) {
	return m.store.ListSSHKeys(ctx, owner)
}
func (m *Manager) UpdateSSHKey(ctx context.Context, owner domain.OwnerID, id, label string) (SSHKey, error) {
	label = strings.TrimSpace(label)
	if label == "" || len(label) > 100 {
		return SSHKey{}, errors.New("invalid SSH key label")
	}
	return m.store.UpdateSSHKey(ctx, owner, id, label, m.now())
}
func (m *Manager) DeleteSSHKey(ctx context.Context, owner domain.OwnerID, id string) error {
	return m.store.DeleteSSHKey(ctx, owner, id)
}

func normalizeScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return []string{ScopeOwner}, nil
	}
	normalized := append([]string(nil), scopes...)
	seen := make(map[string]struct{}, len(normalized))
	for _, scope := range normalized {
		if _, ok := supportedScopes[scope]; !ok {
			return nil, errors.New("unsupported token scope")
		}
		if _, ok := seen[scope]; ok {
			return nil, errors.New("duplicate token scope")
		}
		seen[scope] = struct{}{}
	}
	if _, owner := seen[ScopeOwner]; owner && len(seen) != 1 {
		return nil, errors.New("owner scope cannot be combined")
	}
	sort.Strings(normalized)
	return normalized, nil
}

func sessionFromMembership(membership Membership, expires time.Time, csrf string) Session {
	return Session{
		OwnerID: membership.OwnerID, UserID: membership.User.ID, Username: membership.User.Username,
		Role: membership.User.Role, ExpiresAt: expires, CSRFToken: csrf,
	}
}

func validUsername(value string) bool {
	if len(value) < 1 || len(value) > 100 || strings.ContainsAny(value, " \t\r\n") {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-@+", r)) {
			return false
		}
	}
	return true
}

var supportedScopes = map[string]struct{}{
	ScopeOwner: {}, ScopeInstancesRead: {}, ScopeInstancesWrite: {},
	ScopeOperationsRead: {}, ScopeAuditRead: {}, ScopeWebhooksRead: {},
	ScopeWebhooksWrite: {}, ScopeRoutesRead: {}, ScopeRoutesWrite: {},
	ScopeProfilesRead: {}, ScopeProfilesWrite: {}, ScopeArtifactsRead: {},
	ScopeArtifactsWrite: {},
}

func digest(raw []byte) []byte { sum := sha256.Sum256(raw); return sum[:] }
func equalDigest(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
func randomSecret(size int) (string, []byte, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generate secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), raw, nil
}
func decodeSecret(value string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) < 16 {
		return nil, errors.New("invalid secret")
	}
	return raw, nil
}

type attempt struct {
	count int
	first time.Time
	seq   uint64
}
type Limiter struct {
	mu       sync.Mutex
	max      int
	window   time.Duration
	capacity int
	nextSeq  uint64
	entries  map[string]attempt
}

func NewLimiter(max int, window time.Duration, capacity int) *Limiter {
	return &Limiter{max: max, window: window, capacity: capacity, entries: make(map[string]attempt)}
}
func (l *Limiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.entries[key]
	if ok && now.Sub(a.first) >= l.window {
		delete(l.entries, key)
		ok = false
	}
	if !ok {
		if len(l.entries) >= l.capacity {
			var oldest string
			var oldestSeq uint64
			oldestSet := false
			for k, v := range l.entries {
				if !oldestSet || v.seq < oldestSeq {
					oldest, oldestSeq = k, v.seq
					oldestSet = true
				}
			}
			delete(l.entries, oldest)
		}
		l.nextSeq++
		l.entries[key] = attempt{count: 1, first: now, seq: l.nextSeq}
		return true
	}
	if a.count >= l.max {
		return false
	}
	a.count++
	l.nextSeq++
	a.seq = l.nextSeq
	l.entries[key] = a
	return true
}
func (l *Limiter) Reset(key string) { l.mu.Lock(); delete(l.entries, key); l.mu.Unlock() }
