// SPDX-License-Identifier: AGPL-3.0-only

package routes_test

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/routes"
)

func TestServiceCreateIsPrivateAndDoesNotAutoPublish(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	repo.instances["inst-1"] = domain.Instance{ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "incus-1"}
	svc := newTestService(t, repo)

	route, err := svc.Create(context.Background(), "owner-1", routes.CreateInput{
		InstanceID: "inst-1", Hostname: "dev.openbox.example", TargetPort: 3000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if route.Visibility != domain.RoutePrivate {
		t.Fatalf("visibility=%q, want private", route.Visibility)
	}
	if route.TLSState != routes.TLSStateNone {
		t.Fatalf("tls_state=%q, want %q", route.TLSState, routes.TLSStateNone)
	}
	if len(repo.routes) != 1 {
		t.Fatalf("created %d routes", len(repo.routes))
	}
}

func TestServiceCreateRejectsHostGatewayUnmanagedAndForeign(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		instance *domain.Instance
		wantCode domain.ErrorCode
	}{
		{name: "missing", instance: nil, wantCode: domain.CodeNotFound},
		{name: "host", instance: &domain.Instance{ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "host"}, wantCode: domain.CodeInvalidArgument},
		{name: "gateway", instance: &domain.Instance{ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "gateway"}, wantCode: domain.CodeInvalidArgument},
		{name: "unmanaged", instance: &domain.Instance{ID: "inst-1", OwnerID: "owner-1", RuntimeRef: ""}, wantCode: domain.CodeRuntimeMissing},
		{name: "foreign", instance: &domain.Instance{ID: "inst-1", OwnerID: "other", RuntimeRef: "incus-1"}, wantCode: domain.CodeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := newFakeRepo()
			if tt.instance != nil && tt.instance.OwnerID == "owner-1" {
				repo.instances[tt.instance.ID] = *tt.instance
			}
			svc := newTestService(t, repo)
			_, err := svc.Create(context.Background(), "owner-1", routes.CreateInput{
				InstanceID: "inst-1", Hostname: "dev.openbox.example", TargetPort: 3000,
			})
			assertCode(t, err, tt.wantCode)
			if len(repo.routes) != 0 {
				t.Fatalf("policy rejection created routes: %+v", repo.routes)
			}
		})
	}
}

func TestServicePublishIsExplicit(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	repo.instances["inst-1"] = domain.Instance{ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "incus-1"}
	svc := newTestService(t, repo)
	created, err := svc.Create(context.Background(), "owner-1", routes.CreateInput{
		InstanceID: "inst-1", Hostname: "dev.openbox.example", TargetPort: 3000,
	})
	if err != nil {
		t.Fatal(err)
	}
	published, err := svc.Publish(context.Background(), "owner-1", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if published.Visibility != domain.RoutePublic {
		t.Fatalf("visibility=%q, want public", published.Visibility)
	}
}

func TestServiceSuggestPortsNeverCreatesRoutes(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	repo.instances["inst-1"] = domain.Instance{ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "incus-1"}
	svc := newTestService(t, repo)

	ports, err := svc.SuggestPorts(context.Background(), "owner-1", "inst-1")
	if err != nil {
		t.Fatal(err)
	}
	if ports == nil || len(ports) != 0 {
		t.Fatalf("ports=%v, want empty non-nil slice", ports)
	}
	if len(repo.routes) != 0 {
		t.Fatalf("suggestions created routes: %+v", repo.routes)
	}
	listed, err := svc.List(context.Background(), "owner-1")
	if err != nil || len(listed) != 0 {
		t.Fatalf("list after suggest=%+v err=%v", listed, err)
	}
}

func TestServiceCRUDRoundTrip(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	repo.instances["inst-1"] = domain.Instance{ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "incus-1"}
	svc := newTestService(t, repo)

	created, err := svc.Create(context.Background(), "owner-1", routes.CreateInput{
		InstanceID: "inst-1", Hostname: "dev.openbox.example", TargetPort: 3000,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(context.Background(), "owner-1", created.ID)
	if err != nil || got.ID != created.ID {
		t.Fatalf("get=%+v err=%v", got, err)
	}
	port := 8080
	hostname := "app.openbox.example"
	updated, err := svc.Update(context.Background(), "owner-1", created.ID, routes.UpdateInput{
		Hostname: &hostname, TargetPort: &port,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Hostname != hostname || updated.TargetPort != port || updated.Visibility != domain.RoutePrivate {
		t.Fatalf("updated=%+v", updated)
	}
	if err := svc.Delete(context.Background(), "owner-1", created.ID); err != nil {
		t.Fatal(err)
	}
	_, err = svc.Get(context.Background(), "owner-1", created.ID)
	assertCode(t, err, domain.CodeNotFound)
}

func TestServiceCreateRejectsDuplicateHostname(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	repo.instances["inst-1"] = domain.Instance{ID: "inst-1", OwnerID: "owner-1", RuntimeRef: "incus-1"}
	svc := newTestService(t, repo)
	_, err := svc.Create(context.Background(), "owner-1", routes.CreateInput{
		InstanceID: "inst-1", Hostname: "dev.openbox.example", TargetPort: 3000,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Create(context.Background(), "owner-1", routes.CreateInput{
		InstanceID: "inst-1", Hostname: "dev.openbox.example", TargetPort: 3001,
	})
	assertCode(t, err, domain.CodeConflict)
}

func newTestService(t *testing.T, repo *fakeRepo) *routes.Service {
	t.Helper()
	ids := 0
	svc, err := routes.New(repo, routes.Options{
		Now: func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) },
		NewID: func() string {
			ids++
			return fmt.Sprintf("route-%d", ids)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

type fakeRepo struct {
	instances   map[domain.InstanceID]domain.Instance
	routes      map[domain.RouteID]domain.Route
	routeTokens map[string]storedRouteToken
}

type storedRouteToken struct {
	token   routes.RouteToken
	digest  []byte
	revoked *time.Time
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		instances:   map[domain.InstanceID]domain.Instance{},
		routes:      map[domain.RouteID]domain.Route{},
		routeTokens: map[string]storedRouteToken{},
	}
}

func (f *fakeRepo) CreateRoute(_ context.Context, route domain.Route) error {
	f.routes[route.ID] = route
	return nil
}
func (f *fakeRepo) GetRoute(_ context.Context, owner domain.OwnerID, id domain.RouteID) (domain.Route, error) {
	route, ok := f.routes[id]
	if !ok || route.OwnerID != owner {
		return domain.Route{}, &domain.Error{Code: domain.CodeNotFound, Field: "route"}
	}
	return route, nil
}
func (f *fakeRepo) ListRoutes(_ context.Context, owner domain.OwnerID) ([]domain.Route, error) {
	out := make([]domain.Route, 0)
	for _, route := range f.routes {
		if route.OwnerID == owner {
			out = append(out, route)
		}
	}
	return out, nil
}
func (f *fakeRepo) UpdateRoute(_ context.Context, route domain.Route) error {
	if _, ok := f.routes[route.ID]; !ok {
		return &domain.Error{Code: domain.CodeNotFound, Field: "route"}
	}
	f.routes[route.ID] = route
	return nil
}
func (f *fakeRepo) DeleteRoute(_ context.Context, owner domain.OwnerID, id domain.RouteID) error {
	route, ok := f.routes[id]
	if !ok || route.OwnerID != owner {
		return &domain.Error{Code: domain.CodeNotFound, Field: "route"}
	}
	delete(f.routes, id)
	return nil
}
func (f *fakeRepo) FindRouteByHostname(_ context.Context, hostname string) (domain.Route, bool, error) {
	for _, route := range f.routes {
		if strings.EqualFold(route.Hostname, hostname) {
			return route, true, nil
		}
	}
	return domain.Route{}, false, nil
}
func (f *fakeRepo) GetInstance(_ context.Context, owner domain.OwnerID, id domain.InstanceID) (domain.Instance, error) {
	instance, ok := f.instances[id]
	if !ok || instance.OwnerID != owner {
		return domain.Instance{}, &domain.Error{Code: domain.CodeNotFound, Field: "instance"}
	}
	return instance, nil
}

func (f *fakeRepo) CreateRouteToken(_ context.Context, token routes.RouteToken, digest []byte) error {
	stored := token
	stored.Secret = ""
	f.routeTokens[token.ID] = storedRouteToken{token: stored, digest: append([]byte(nil), digest...)}
	return nil
}

func (f *fakeRepo) ListRouteTokens(_ context.Context, owner domain.OwnerID, routeID domain.RouteID) ([]routes.RouteToken, error) {
	out := make([]routes.RouteToken, 0)
	for _, item := range f.routeTokens {
		if item.revoked != nil || item.token.OwnerID != owner || item.token.RouteID != routeID {
			continue
		}
		out = append(out, item.token)
	}
	return out, nil
}

func (f *fakeRepo) RevokeRouteToken(_ context.Context, owner domain.OwnerID, routeID domain.RouteID, id string, at time.Time) error {
	item, ok := f.routeTokens[id]
	if !ok || item.token.OwnerID != owner || item.token.RouteID != routeID || item.revoked != nil {
		return &domain.Error{Code: domain.CodeNotFound, Field: "route_token"}
	}
	item.revoked = &at
	f.routeTokens[id] = item
	return nil
}

func (f *fakeRepo) FindRouteToken(_ context.Context, digest []byte, _ time.Time) (routes.RouteToken, error) {
	for _, item := range f.routeTokens {
		if item.revoked != nil {
			continue
		}
		if subtle.ConstantTimeCompare(item.digest, digest) == 1 {
			return item.token, nil
		}
	}
	return routes.RouteToken{}, &domain.Error{Code: domain.CodeNotFound, Field: "route_token"}
}

func assertCode(t *testing.T, err error, code domain.ErrorCode) {
	t.Helper()
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != code {
		t.Fatalf("got %v, want code %s", err, code)
	}
}
