// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/openbox-dev/openbox/internal/domain"
)

func (s *Store) CreateRoute(ctx context.Context, route domain.Route) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO routes(id,owner_id,instance_id,hostname,target_port,visibility,tls_state,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		route.ID, route.OwnerID, route.InstanceID, route.Hostname, route.TargetPort, route.Visibility, route.TLSState,
		formatTime(route.CreatedAt), formatTime(route.UpdatedAt))
	return mapWriteError(err)
}

func (s *Store) GetRoute(ctx context.Context, ownerID domain.OwnerID, id domain.RouteID) (domain.Route, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,owner_id,instance_id,hostname,target_port,visibility,tls_state,created_at,updated_at
		FROM routes WHERE owner_id=? AND id=?`, ownerID, id)
	return scanRoute(row)
}

func (s *Store) ListRoutes(ctx context.Context, ownerID domain.OwnerID) ([]domain.Route, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,owner_id,instance_id,hostname,target_port,visibility,tls_state,created_at,updated_at
		FROM routes WHERE owner_id=? ORDER BY created_at, id`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("list routes: %w", err)
	}
	defer rows.Close()
	result := make([]domain.Route, 0)
	for rows.Next() {
		route, scanErr := scanRoute(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, route)
	}
	return result, rows.Err()
}

func (s *Store) UpdateRoute(ctx context.Context, route domain.Route) error {
	result, err := s.db.ExecContext(ctx, `UPDATE routes SET hostname=?,target_port=?,visibility=?,tls_state=?,updated_at=?
		WHERE owner_id=? AND id=?`,
		route.Hostname, route.TargetPort, route.Visibility, route.TLSState, formatTime(route.UpdatedAt), route.OwnerID, route.ID)
	if err != nil {
		return mapWriteError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return &domain.Error{Code: domain.CodeNotFound, Field: "route"}
	}
	return nil
}

func (s *Store) DeleteRoute(ctx context.Context, ownerID domain.OwnerID, id domain.RouteID) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM routes WHERE owner_id=? AND id=?`, ownerID, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return &domain.Error{Code: domain.CodeNotFound, Field: "route"}
	}
	return nil
}

func (s *Store) FindRouteByHostname(ctx context.Context, hostname string) (domain.Route, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,owner_id,instance_id,hostname,target_port,visibility,tls_state,created_at,updated_at
		FROM routes WHERE lower(hostname)=lower(?) LIMIT 1`, hostname)
	route, err := scanRoute(row)
	if err != nil {
		var domainErr *domain.Error
		if errors.As(err, &domainErr) && domainErr.Code == domain.CodeNotFound {
			return domain.Route{}, false, nil
		}
		return domain.Route{}, false, err
	}
	return route, true, nil
}

type routeScanner interface {
	Scan(dest ...any) error
}

func scanRoute(row routeScanner) (domain.Route, error) {
	var route domain.Route
	var created, updated string
	err := row.Scan(&route.ID, &route.OwnerID, &route.InstanceID, &route.Hostname, &route.TargetPort,
		&route.Visibility, &route.TLSState, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Route{}, &domain.Error{Code: domain.CodeNotFound, Field: "route"}
	}
	if err != nil {
		return domain.Route{}, err
	}
	if route.CreatedAt, err = parseTime(created); err != nil {
		return domain.Route{}, err
	}
	if route.UpdatedAt, err = parseTime(updated); err != nil {
		return domain.Route{}, err
	}
	return route, nil
}
