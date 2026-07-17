// SPDX-License-Identifier: AGPL-3.0-only

package auth_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/auth"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/persistence/sqlite"
	"golang.org/x/crypto/ssh"
)

func TestPasswordHashIsVersionedBoundedAndVerifies(t *testing.T) {
	hash, err := auth.HashPassword("a sufficiently long password", auth.DefaultPasswordParams)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$") {
		t.Fatalf("unexpected hash format %q", hash)
	}
	valid, rehash, err := auth.VerifyPassword(hash, "a sufficiently long password")
	if err != nil || !valid || rehash {
		t.Fatalf("valid=%v rehash=%v err=%v", valid, rehash, err)
	}
	valid, _, err = auth.VerifyPassword(hash, "this is the wrong password")
	if err != nil || valid {
		t.Fatalf("wrong password valid=%v err=%v", valid, err)
	}
	if _, err := auth.HashPassword(strings.Repeat("x", auth.MaxPasswordBytes+1), auth.DefaultPasswordParams); err == nil {
		t.Fatal("oversized password accepted")
	}
}

func TestPasswordRehashOnlyUpgradesWeakerPolicy(t *testing.T) {
	weak := auth.DefaultPasswordParams
	weak.Memory = 8 * 1024
	weak.Iterations = 1
	hash, err := auth.HashPassword("a sufficiently long password", weak)
	if err != nil {
		t.Fatal(err)
	}
	valid, rehash, err := auth.VerifyPassword(hash, "a sufficiently long password")
	if err != nil || !valid || !rehash {
		t.Fatalf("weak valid=%v rehash=%v err=%v", valid, rehash, err)
	}
	strong := auth.DefaultPasswordParams
	strong.Memory = 20 * 1024
	strong.Iterations = 3
	hash, err = auth.HashPassword("a sufficiently long password", strong)
	if err != nil {
		t.Fatal(err)
	}
	valid, rehash, err = auth.VerifyPassword(hash, "a sufficiently long password")
	if err != nil || !valid || rehash {
		t.Fatalf("strong valid=%v rehash=%v err=%v", valid, rehash, err)
	}
}

func TestBootstrapRejectsInvalidUsernameBeforeHashingPassword(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir()+"/auth.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	if err := store.CreateOwner(ctx, domain.Owner{ID: "owner-local", Name: "Owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	manager, _ := auth.New(store)
	manager.WithClock(func() time.Time { return now })
	hashCalls := 0
	manager.WithPasswordHasher(func(password string, params auth.PasswordParams) (string, error) {
		hashCalls++
		return auth.HashPassword(password, params)
	})
	if _, _, err := manager.Bootstrap(ctx, "client", "not valid", "a sufficiently long password"); !errors.Is(err, auth.ErrInvalidUser) {
		t.Fatalf("error=%v", err)
	}
	if hashCalls != 0 {
		t.Fatalf("password hashed %d times before rejecting bootstrap username", hashCalls)
	}
}

func TestBootstrapIsRateLimitedLikeLogin(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir()+"/auth.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	if err := store.CreateOwner(ctx, domain.Owner{ID: "owner-local", Name: "Owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	manager, _ := auth.New(store)
	manager.WithClock(func() time.Time { return now })
	for i := 0; i < 5; i++ {
		if _, _, err := manager.Bootstrap(ctx, "attacker", "admin", "short"); !errors.Is(err, auth.ErrInvalidUser) {
			t.Fatalf("attempt %d error=%v", i, err)
		}
	}
	if _, _, err := manager.Bootstrap(ctx, "attacker", "admin", "a sufficiently long password"); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("expected rate limit, got %v", err)
	}
}

func TestBootstrapCreatesFirstAdminOnlyOnce(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir()+"/auth.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	if err := store.CreateOwner(ctx, domain.Owner{ID: "owner-local", Name: "Owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	manager, _ := auth.New(store)
	manager.WithClock(func() time.Time { return now })
	session, cookie, err := manager.Bootstrap(ctx, "loopback", "admin", "a sufficiently long password")
	if err != nil || cookie == "" || session.OwnerID != "owner-local" || session.Username != "admin" {
		t.Fatalf("session=%+v cookie=%q err=%v", session, cookie, err)
	}
	if _, _, err := manager.Bootstrap(ctx, "different-client", "another-admin", "a sufficiently long password"); !errors.Is(err, auth.ErrBootstrapUnavailable) {
		t.Fatalf("second bootstrap error=%v", err)
	}
}

func TestSessionCSRFTokenRevocationAndSSHKeyValidation(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir()+"/auth.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 15, 11, 0, 0, 0, time.UTC)
	_ = store.CreateOwner(ctx, domain.Owner{ID: "owner-local", Name: "Owner", CreatedAt: now, UpdatedAt: now})
	m, _ := auth.New(store)
	m.WithClock(func() time.Time { return now })
	session, cookie, err := m.Bootstrap(ctx, "loopback", "admin", "a sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AuthenticateSession(ctx, cookie, "", true); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("missing CSRF err=%v", err)
	}
	if owner, err := m.AuthenticateSession(ctx, cookie, session.CSRFToken, true); err != nil || owner != "owner-local" {
		t.Fatalf("owner=%q err=%v", owner, err)
	}
	rotated, newCookie, err := m.RotateSession(ctx, cookie, "owner-local")
	if err != nil {
		t.Fatal(err)
	}
	if newCookie == cookie || rotated.CSRFToken == session.CSRFToken {
		t.Fatal("session rotation reused credentials")
	}
	if !rotated.ExpiresAt.Equal(session.ExpiresAt) {
		t.Fatalf("rotation extended absolute expiry: got %v want %v", rotated.ExpiresAt, session.ExpiresAt)
	}
	if _, err := m.AuthenticateSession(ctx, cookie, session.CSRFToken, true); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("old session survived rotation: %v", err)
	}
	cookie, session = newCookie, rotated
	now = session.ExpiresAt.Add(time.Second)
	if _, err := m.AuthenticateSession(ctx, cookie, "", false); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("rotated session outlived absolute expiry: %v", err)
	}
	now = time.Date(2026, 7, 15, 11, 0, 0, 0, time.UTC)
	token, err := m.CreateToken(ctx, "owner-local", "automation", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if owner, err := m.AuthenticateBearer(ctx, token.Secret); err != nil || owner != "owner-local" {
		t.Fatalf("owner=%q err=%v", owner, err)
	}
	if err := m.RevokeToken(ctx, "owner-local", token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AuthenticateBearer(ctx, token.Secret); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("revoked token err=%v", err)
	}
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	sshPublic, _ := ssh.NewPublicKey(public)
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublic))) + " operator@example"
	key, err := m.AddSSHKey(ctx, "owner-local", "laptop", authorized)
	if err != nil {
		t.Fatal(err)
	}
	if key.Fingerprint == "" || strings.Contains(key.PublicKey, "operator@example") {
		t.Fatalf("key not normalized: %+v", key)
	}
	updated, err := m.UpdateSSHKey(ctx, "owner-local", key.ID, "work laptop")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Label != "work laptop" || updated.Fingerprint != key.Fingerprint || updated.PublicKey != key.PublicKey {
		t.Fatalf("label update changed key material: %+v", updated)
	}
	if _, err := m.AddSSHKey(ctx, "owner-local", "duplicate", authorized); err == nil {
		t.Fatal("duplicate SSH key accepted")
	}
	if _, err := m.AddSSHKey(ctx, "owner-local", "bad", "not a key"); err == nil {
		t.Fatal("malformed SSH key accepted")
	}
	if _, err := m.AddSSHKey(ctx, "owner-local", "options", `command="false" `+strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublic)))); err == nil {
		t.Fatal("SSH key options were silently discarded")
	}
	if err := m.Logout(ctx, cookie); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AuthenticateSession(ctx, cookie, "", false); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("logged out session err=%v", err)
	}
}

func TestScopedTokenPrincipalAndOwnerCompatibility(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir()+"/auth.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC)
	if err := store.CreateOwner(ctx, domain.Owner{ID: "owner-local", Name: "Owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	manager, err := auth.New(store)
	if err != nil {
		t.Fatal(err)
	}
	manager.WithClock(func() time.Time { return now })

	token, err := manager.CreateToken(ctx, "owner-local", "reader", []string{auth.ScopeOperationsRead, auth.ScopeInstancesRead}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(token.Scopes, ","), auth.ScopeInstancesRead+","+auth.ScopeOperationsRead; got != want {
		t.Fatalf("scopes=%q want %q", got, want)
	}
	principal, err := manager.AuthenticateBearerPrincipal(ctx, token.Secret)
	if err != nil || principal.OwnerID != "owner-local" || strings.Join(principal.Scopes, ",") != strings.Join(token.Scopes, ",") {
		t.Fatalf("principal=%+v err=%v", principal, err)
	}
	if _, err := manager.AuthenticateBearer(ctx, token.Secret); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("scoped token authenticated as full owner: %v", err)
	}
	if _, err := manager.CreateToken(ctx, "owner-local", "mixed", []string{auth.ScopeOwner, auth.ScopeInstancesRead}, nil); err == nil {
		t.Fatal("owner scope combined with scoped capability")
	}
	if _, err := manager.CreateToken(ctx, "owner-local", "unknown", []string{"admin"}, nil); err == nil {
		t.Fatal("unknown scope accepted")
	}
}

func TestLoginLimiterIsBounded(t *testing.T) {
	l := auth.NewLimiter(2, time.Minute, 2)
	now := time.Now()
	if !l.Allow("a", now) || !l.Allow("a", now) || l.Allow("a", now) {
		t.Fatal("per-key limit not enforced")
	}
	_ = l.Allow("b", now)
	_ = l.Allow("c", now)
	if !l.Allow("a", now) {
		t.Fatal("oldest entry was not evicted at capacity")
	}
}
