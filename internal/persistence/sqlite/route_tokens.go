// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/routes"
)

func (s *Store) CreateRouteToken(ctx context.Context, token routes.RouteToken, digest []byte) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO route_tokens(id,owner_id,route_id,name,token_hash,created_at)
		VALUES(?,?,?,?,?,?)`,
		token.ID, token.OwnerID, token.RouteID, token.Name, digest, formatTime(token.CreatedAt))
	return mapWriteError(err)
}

func (s *Store) ListRouteTokens(ctx context.Context, ownerID domain.OwnerID, routeID domain.RouteID) ([]routes.RouteToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,owner_id,route_id,name,created_at FROM route_tokens
		WHERE owner_id=? AND route_id=? AND revoked_at IS NULL ORDER BY created_at, id`, ownerID, routeID)
	if err != nil {
		return nil, fmt.Errorf("list route tokens: %w", err)
	}
	defer rows.Close()
	out := make([]routes.RouteToken, 0)
	for rows.Next() {
		var token routes.RouteToken
		var created string
		if err := rows.Scan(&token.ID, &token.OwnerID, &token.RouteID, &token.Name, &created); err != nil {
			return nil, err
		}
		if token.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		out = append(out, token)
	}
	return out, rows.Err()
}

func (s *Store) RevokeRouteToken(ctx context.Context, ownerID domain.OwnerID, routeID domain.RouteID, id string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE route_tokens SET revoked_at=?
		WHERE owner_id=? AND route_id=? AND id=? AND revoked_at IS NULL`,
		formatTime(at), ownerID, routeID, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return &domain.Error{Code: domain.CodeNotFound, Field: "route_token"}
	}
	return nil
}

func (s *Store) FindRouteToken(ctx context.Context, digest []byte, _ time.Time) (routes.RouteToken, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,owner_id,route_id,name,created_at FROM route_tokens
		WHERE token_hash=? AND revoked_at IS NULL`, digest)
	var token routes.RouteToken
	var created string
	err := row.Scan(&token.ID, &token.OwnerID, &token.RouteID, &token.Name, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return routes.RouteToken{}, &domain.Error{Code: domain.CodeNotFound, Field: "route_token"}
	}
	if err != nil {
		return routes.RouteToken{}, err
	}
	if token.CreatedAt, err = parseTime(created); err != nil {
		return routes.RouteToken{}, err
	}
	return token, nil
}
