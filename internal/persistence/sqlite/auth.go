// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/openbox-dev/openbox/internal/auth"
	"github.com/openbox-dev/openbox/internal/domain"
)

func (s *Store) EnsureBootstrap(ctx context.Context, secretHash []byte, now, expires time.Time) (bool, error) {
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var credentials int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_credentials`).Scan(&credentials); err != nil {
		return false, err
	}
	if credentials != 0 {
		return false, nil
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM bootstrap_challenges WHERE consumed_at IS NULL AND expires_at > ?`, formatTime(now)).Scan(&active); err != nil {
		return false, err
	}
	if active != 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE bootstrap_challenges SET consumed_at=? WHERE consumed_at IS NULL`, formatTime(now)); err != nil {
		return false, err
	}
	id := hex.EncodeToString(secretHash[:12])
	if _, err := tx.ExecContext(ctx, `INSERT INTO bootstrap_challenges(id,secret_hash,expires_at) VALUES(?,?,?)`, id, secretHash, formatTime(expires)); err != nil {
		return false, mapWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) BootstrapStatus(ctx context.Context, now time.Time) (auth.BootstrapStatus, error) {
	var credentials int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_credentials`).Scan(&credentials); err != nil {
		return auth.BootstrapStatus{}, err
	}
	if credentials != 0 {
		return auth.BootstrapStatus{Required: false}, nil
	}
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT expires_at FROM bootstrap_challenges WHERE consumed_at IS NULL AND expires_at>? ORDER BY expires_at DESC LIMIT 1`, formatTime(now)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.BootstrapStatus{Required: true}, nil
	}
	if err != nil {
		return auth.BootstrapStatus{}, err
	}
	expires, err := parseTime(raw)
	if err != nil {
		return auth.BootstrapStatus{}, err
	}
	return auth.BootstrapStatus{Required: true, ExpiresAt: &expires}, nil
}

func (s *Store) MatchBootstrap(ctx context.Context, secretHash []byte, now time.Time) error {
	var credentials int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_credentials`).Scan(&credentials); err != nil || credentials != 0 {
		return auth.ErrBootstrapUnavailable
	}
	var stored []byte
	err := s.db.QueryRowContext(ctx, `SELECT secret_hash FROM bootstrap_challenges WHERE consumed_at IS NULL AND expires_at>? LIMIT 1`, formatTime(now)).Scan(&stored)
	if err != nil || !constantBytes(stored, secretHash) {
		return auth.ErrBootstrapUnavailable
	}
	return nil
}

func (s *Store) ConsumeBootstrap(ctx context.Context, secretHash []byte, passwordHash string, now time.Time) (auth.Membership, error) {
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return auth.Membership{}, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return auth.Membership{}, err
	}
	defer tx.Rollback()
	var owner domain.OwnerID
	var owners int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM owners`).Scan(&owners); err != nil || owners != 1 {
		return auth.Membership{}, auth.ErrBootstrapUnavailable
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM owners LIMIT 1`).Scan(&owner); err != nil {
		return auth.Membership{}, auth.ErrBootstrapUnavailable
	}
	var stored []byte
	err = tx.QueryRowContext(ctx, `SELECT secret_hash FROM bootstrap_challenges WHERE consumed_at IS NULL AND expires_at>? LIMIT 1`, formatTime(now)).Scan(&stored)
	if err != nil || !constantBytes(stored, secretHash) {
		return auth.Membership{}, auth.ErrBootstrapUnavailable
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_credentials`).Scan(&count); err != nil || count != 0 {
		return auth.Membership{}, auth.ErrBootstrapUnavailable
	}
	result, err := tx.ExecContext(ctx, `UPDATE bootstrap_challenges SET consumed_at=? WHERE consumed_at IS NULL AND expires_at>?`, formatTime(now), formatTime(now))
	if err != nil {
		return auth.Membership{}, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return auth.Membership{}, auth.ErrBootstrapUnavailable
	}
	user := auth.User{ID: "usr_" + string(owner), Username: string(owner), DisplayName: "Owner", Role: "admin", CreatedAt: now}
	if _, err := tx.ExecContext(ctx, `INSERT INTO users(id,username,display_name,created_at,updated_at) VALUES(?,?,?,?,?)`, user.ID, user.Username, user.DisplayName, formatTime(now), formatTime(now)); err != nil {
		return auth.Membership{}, auth.ErrBootstrapUnavailable
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO org_memberships(owner_id,user_id,role,created_at) VALUES(?,?,?,?)`, owner, user.ID, user.Role, formatTime(now)); err != nil {
		return auth.Membership{}, auth.ErrBootstrapUnavailable
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_credentials(user_id,password_hash,updated_at) VALUES(?,?,?)`, user.ID, passwordHash, formatTime(now)); err != nil {
		return auth.Membership{}, auth.ErrBootstrapUnavailable
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO owner_credentials(owner_id,password_hash,updated_at) VALUES(?,?,?)`, owner, passwordHash, formatTime(now)); err != nil {
		return auth.Membership{}, auth.ErrBootstrapUnavailable
	}
	if err := tx.Commit(); err != nil {
		return auth.Membership{}, err
	}
	return auth.Membership{OwnerID: owner, User: user}, nil
}
func (s *Store) UserCredential(ctx context.Context, username string) (auth.Membership, string, error) {
	var membership auth.Membership
	var h string
	var created string
	var err error
	if strings.TrimSpace(username) == "" {
		err = s.db.QueryRowContext(ctx, `SELECT m.owner_id,u.id,u.username,u.display_name,m.role,u.created_at,c.password_hash
			FROM user_credentials c JOIN users u ON u.id=c.user_id JOIN org_memberships m ON m.user_id=u.id
			WHERE (SELECT COUNT(*) FROM user_credentials)=1 LIMIT 1`).Scan(
			&membership.OwnerID, &membership.User.ID, &membership.User.Username, &membership.User.DisplayName, &membership.User.Role, &created, &h)
	} else {
		err = s.db.QueryRowContext(ctx, `SELECT m.owner_id,u.id,u.username,u.display_name,m.role,u.created_at,c.password_hash
			FROM user_credentials c JOIN users u ON u.id=c.user_id JOIN org_memberships m ON m.user_id=u.id
			WHERE u.username=? LIMIT 1`, username).Scan(
			&membership.OwnerID, &membership.User.ID, &membership.User.Username, &membership.User.DisplayName, &membership.User.Role, &created, &h)
	}
	if err != nil {
		return auth.Membership{}, "", err
	}
	membership.User.CreatedAt, err = parseTime(created)
	return membership, h, err
}
func (s *Store) UpdateCredential(ctx context.Context, userID, h string, now time.Time) error {
	r, e := s.db.ExecContext(ctx, `UPDATE user_credentials SET password_hash=?,updated_at=? WHERE user_id=?`, h, formatTime(now), userID)
	if e != nil {
		return e
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) CreateSession(ctx context.Context, id []byte, membership auth.Membership, csrf []byte, created, expires time.Time) error {
	_, e := s.db.ExecContext(ctx, `INSERT INTO auth_sessions(id_hash,owner_id,user_id,csrf_hash,created_at,expires_at) VALUES(?,?,?,?,?,?)`, id, membership.OwnerID, membership.User.ID, csrf, formatTime(created), formatTime(expires))
	return mapWriteError(e)
}
func (s *Store) RotateSession(ctx context.Context, oldID, newID []byte, membership auth.Membership, csrf []byte, created, expires time.Time) error {
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at=? WHERE id_hash=? AND owner_id=? AND user_id=? AND revoked_at IS NULL AND expires_at>?`, formatTime(created), oldID, membership.OwnerID, membership.User.ID, formatTime(created))
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_sessions(id_hash,owner_id,user_id,csrf_hash,created_at,expires_at) VALUES(?,?,?,?,?,?)`, newID, membership.OwnerID, membership.User.ID, csrf, formatTime(created), formatTime(expires)); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) UpdateSessionCSRF(ctx context.Context, id, csrf []byte, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE auth_sessions SET csrf_hash=? WHERE id_hash=? AND revoked_at IS NULL AND expires_at>?`, csrf, id, formatTime(now))
	if err != nil {
		return mapWriteError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return sql.ErrNoRows
	}
	return nil
}
func (s *Store) Session(ctx context.Context, id []byte, now time.Time) (auth.Membership, []byte, time.Time, error) {
	var membership auth.Membership
	var csrf []byte
	var raw, created string
	e := s.db.QueryRowContext(ctx, `SELECT s.owner_id,u.id,u.username,u.display_name,m.role,u.created_at,s.csrf_hash,s.expires_at
		FROM auth_sessions s JOIN users u ON u.id=s.user_id JOIN org_memberships m ON m.owner_id=s.owner_id AND m.user_id=s.user_id
		WHERE s.id_hash=? AND s.revoked_at IS NULL AND s.expires_at>?`, id, formatTime(now)).Scan(
		&membership.OwnerID, &membership.User.ID, &membership.User.Username, &membership.User.DisplayName, &membership.User.Role, &created, &csrf, &raw)
	if e != nil {
		return auth.Membership{}, nil, time.Time{}, e
	}
	membership.User.CreatedAt, e = parseTime(created)
	if e != nil {
		return auth.Membership{}, nil, time.Time{}, e
	}
	exp, e := parseTime(raw)
	return membership, csrf, exp, e
}
func (s *Store) RevokeSession(ctx context.Context, id []byte, now time.Time) error {
	_, e := s.db.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at=COALESCE(revoked_at,?) WHERE id_hash=?`, formatTime(now), id)
	return e
}

func (s *Store) CreateUser(ctx context.Context, owner domain.OwnerID, user auth.User, passwordHash string, now time.Time) error {
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO users(id,username,display_name,created_at,updated_at) VALUES(?,?,?,?,?)`,
		user.ID, user.Username, user.DisplayName, formatTime(user.CreatedAt), formatTime(now)); err != nil {
		return mapWriteError(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO org_memberships(owner_id,user_id,role,created_at) VALUES(?,?,?,?)`,
		owner, user.ID, user.Role, formatTime(now)); err != nil {
		return mapWriteError(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_credentials(user_id,password_hash,updated_at) VALUES(?,?,?)`,
		user.ID, passwordHash, formatTime(now)); err != nil {
		return mapWriteError(err)
	}
	return tx.Commit()
}

func (s *Store) ListUsers(ctx context.Context, owner domain.OwnerID) ([]auth.User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT u.id,u.username,u.display_name,m.role,u.created_at
		FROM org_memberships m JOIN users u ON u.id=m.user_id
		WHERE m.owner_id=? ORDER BY u.created_at,u.id`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []auth.User{}
	for rows.Next() {
		var user auth.User
		var created string
		if err := rows.Scan(&user.ID, &user.Username, &user.DisplayName, &user.Role, &created); err != nil {
			return nil, err
		}
		var err error
		user.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) CreateToken(ctx context.Context, t auth.Token, o domain.OwnerID, hash []byte) error {
	_, e := s.db.ExecContext(ctx, `INSERT INTO api_tokens(id,owner_id,name,token_hash,scopes,created_at,expires_at) VALUES(?,?,?,?,?,?,?)`, t.ID, o, t.Name, hash, strings.Join(t.Scopes, " "), formatTime(t.CreatedAt), nullableTime(t.ExpiresAt))
	return mapWriteError(e)
}
func (s *Store) ListTokens(ctx context.Context, o domain.OwnerID) ([]auth.Token, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT id,name,scopes,created_at,expires_at,revoked_at,last_used_at FROM api_tokens WHERE owner_id=? ORDER BY created_at DESC`, o)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []auth.Token{}
	for rows.Next() {
		var t auth.Token
		var scopes, created string
		var expires, revoked, used sql.NullString
		if e := rows.Scan(&t.ID, &t.Name, &scopes, &created, &expires, &revoked, &used); e != nil {
			return nil, e
		}
		t.Scopes = strings.Fields(scopes)
		t.CreatedAt, e = parseTime(created)
		if e != nil {
			return nil, e
		}
		t.ExpiresAt, e = parseNullableTime(expires)
		if e != nil {
			return nil, e
		}
		t.RevokedAt, e = parseNullableTime(revoked)
		if e != nil {
			return nil, e
		}
		t.LastUsedAt, e = parseNullableTime(used)
		if e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) TokenOwner(ctx context.Context, hash []byte, now time.Time) (domain.OwnerID, []string, error) {
	var o domain.OwnerID
	var scopes string
	e := s.db.QueryRowContext(ctx, `SELECT owner_id,scopes FROM api_tokens WHERE token_hash=? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>?)`, hash, formatTime(now)).Scan(&o, &scopes)
	if e != nil {
		return "", nil, e
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at=? WHERE token_hash=?`, formatTime(now), hash)
	return o, strings.Fields(scopes), nil
}
func (s *Store) RevokeToken(ctx context.Context, o domain.OwnerID, id string, now time.Time) error {
	r, e := s.db.ExecContext(ctx, `UPDATE api_tokens SET revoked_at=COALESCE(revoked_at,?) WHERE owner_id=? AND id=?`, formatTime(now), o, id)
	if e != nil {
		return e
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return &domain.Error{Code: domain.CodeNotFound, Field: "token"}
	}
	return nil
}

func (s *Store) CreateSSHKey(ctx context.Context, k auth.SSHKey, o domain.OwnerID) error {
	_, e := s.db.ExecContext(ctx, `INSERT INTO ssh_keys(id,owner_id,fingerprint,public_key,label,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, k.ID, o, k.Fingerprint, k.PublicKey, k.Label, formatTime(k.CreatedAt), formatTime(k.CreatedAt))
	return mapWriteError(e)
}
func (s *Store) ListSSHKeys(ctx context.Context, o domain.OwnerID) ([]auth.SSHKey, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT id,label,fingerprint,public_key,created_at FROM ssh_keys WHERE owner_id=? ORDER BY created_at`, o)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []auth.SSHKey{}
	for rows.Next() {
		var k auth.SSHKey
		var raw string
		if e := rows.Scan(&k.ID, &k.Label, &k.Fingerprint, &k.PublicKey, &raw); e != nil {
			return nil, e
		}
		k.CreatedAt, e = parseTime(raw)
		if e != nil {
			return nil, e
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// AuthorizeSSHKey resolves the organization owner for an exact OpenSSH fingerprint.
// It deliberately returns no public-key material to the SSH transport.
func (s *Store) AuthorizeSSHKey(ctx context.Context, fingerprint string) (domain.OwnerID, bool, error) {
	if fingerprint == "" {
		return "", false, nil
	}
	var owner domain.OwnerID
	err := s.db.QueryRowContext(ctx, `SELECT owner_id FROM ssh_keys WHERE fingerprint=?`, fingerprint).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return owner, true, nil
}
func (s *Store) UpdateSSHKey(ctx context.Context, owner domain.OwnerID, id, label string, updated time.Time) (auth.SSHKey, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE ssh_keys SET label=?,updated_at=? WHERE owner_id=? AND id=?`, label, formatTime(updated), owner, id)
	if err != nil {
		return auth.SSHKey{}, mapWriteError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return auth.SSHKey{}, &domain.Error{Code: domain.CodeNotFound, Field: "ssh_key"}
	}
	var key auth.SSHKey
	var created string
	err = s.db.QueryRowContext(ctx, `SELECT id,label,fingerprint,public_key,created_at FROM ssh_keys WHERE owner_id=? AND id=?`, owner, id).Scan(&key.ID, &key.Label, &key.Fingerprint, &key.PublicKey, &created)
	if err != nil {
		return auth.SSHKey{}, err
	}
	key.CreatedAt, err = parseTime(created)
	return key, err
}
func (s *Store) DeleteSSHKey(ctx context.Context, o domain.OwnerID, id string) error {
	r, e := s.db.ExecContext(ctx, `DELETE FROM ssh_keys WHERE owner_id=? AND id=?`, o, id)
	if e != nil {
		return e
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return &domain.Error{Code: domain.CodeNotFound, Field: "ssh_key"}
	}
	return nil
}

func constantBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
