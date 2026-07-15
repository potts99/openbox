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
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM owner_credentials`).Scan(&credentials); err != nil {
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
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM owner_credentials`).Scan(&credentials); err != nil {
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

func (s *Store) ConsumeBootstrap(ctx context.Context, secretHash []byte, passwordHash string, now time.Time) (domain.OwnerID, error) {
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var owner domain.OwnerID
	var owners int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM owners`).Scan(&owners); err != nil || owners != 1 {
		return "", auth.ErrBootstrapUnavailable
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM owners LIMIT 1`).Scan(&owner); err != nil {
		return "", auth.ErrBootstrapUnavailable
	}
	var stored []byte
	err = tx.QueryRowContext(ctx, `SELECT secret_hash FROM bootstrap_challenges WHERE consumed_at IS NULL AND expires_at>? LIMIT 1`, formatTime(now)).Scan(&stored)
	if err != nil || !constantBytes(stored, secretHash) {
		return "", auth.ErrBootstrapUnavailable
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM owner_credentials`).Scan(&count); err != nil || count != 0 {
		return "", auth.ErrBootstrapUnavailable
	}
	result, err := tx.ExecContext(ctx, `UPDATE bootstrap_challenges SET consumed_at=? WHERE consumed_at IS NULL AND expires_at>?`, formatTime(now), formatTime(now))
	if err != nil {
		return "", err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return "", auth.ErrBootstrapUnavailable
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO owner_credentials(owner_id,password_hash,updated_at) VALUES(?,?,?)`, owner, passwordHash, formatTime(now)); err != nil {
		return "", auth.ErrBootstrapUnavailable
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return owner, nil
}
func (s *Store) OwnerCredential(ctx context.Context) (domain.OwnerID, string, error) {
	var o domain.OwnerID
	var h string
	err := s.db.QueryRowContext(ctx, `SELECT owner_id,password_hash FROM owner_credentials LIMIT 1`).Scan(&o, &h)
	return o, h, err
}
func (s *Store) UpdateCredential(ctx context.Context, o domain.OwnerID, h string, now time.Time) error {
	r, e := s.db.ExecContext(ctx, `UPDATE owner_credentials SET password_hash=?,updated_at=? WHERE owner_id=?`, h, formatTime(now), o)
	if e != nil {
		return e
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) CreateSession(ctx context.Context, id []byte, o domain.OwnerID, csrf []byte, created, expires time.Time) error {
	_, e := s.db.ExecContext(ctx, `INSERT INTO auth_sessions(id_hash,owner_id,csrf_hash,created_at,expires_at) VALUES(?,?,?,?,?)`, id, o, csrf, formatTime(created), formatTime(expires))
	return mapWriteError(e)
}
func (s *Store) RotateSession(ctx context.Context, oldID, newID []byte, owner domain.OwnerID, csrf []byte, created, expires time.Time) error {
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
	result, err := tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at=? WHERE id_hash=? AND owner_id=? AND revoked_at IS NULL AND expires_at>?`, formatTime(created), oldID, owner, formatTime(created))
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_sessions(id_hash,owner_id,csrf_hash,created_at,expires_at) VALUES(?,?,?,?,?)`, newID, owner, csrf, formatTime(created), formatTime(expires)); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Session(ctx context.Context, id []byte, now time.Time) (domain.OwnerID, []byte, time.Time, error) {
	var o domain.OwnerID
	var csrf []byte
	var raw string
	e := s.db.QueryRowContext(ctx, `SELECT owner_id,csrf_hash,expires_at FROM auth_sessions WHERE id_hash=? AND revoked_at IS NULL AND expires_at>?`, id, formatTime(now)).Scan(&o, &csrf, &raw)
	if e != nil {
		return "", nil, time.Time{}, e
	}
	exp, e := parseTime(raw)
	return o, csrf, exp, e
}
func (s *Store) RevokeSession(ctx context.Context, id []byte, now time.Time) error {
	_, e := s.db.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at=COALESCE(revoked_at,?) WHERE id_hash=?`, formatTime(now), id)
	return e
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

// AuthorizeSSHKey resolves the single owner for an exact OpenSSH fingerprint.
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
