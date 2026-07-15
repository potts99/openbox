// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/routes"
)

func TestRouteCRUDPrivateByDefaultAndExplicitPublish(t *testing.T) {
	t.Parallel()
	repo := newRouteTestRepo()
	repo.instances["inst-1"] = domain.Instance{ID: "inst-1", OwnerID: "owner-local", RuntimeRef: "incus-1"}
	handler := newRouteTestHandler(t, repo)

	createBody := `{"instance_id":"inst-1","hostname":"dev.openbox.example","target_port":3000}`
	createReq := httptest.NewRequest(http.MethodPost, "/v1/routes", bytes.NewBufferString(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createRes.Code, createRes.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created["visibility"] != "private" {
		t.Fatalf("visibility=%v, want private", created["visibility"])
	}
	routeID, _ := created["id"].(string)
	if routeID == "" {
		t.Fatal("missing route id")
	}

	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, httptest.NewRequest(http.MethodGet, "/v1/routes", nil))
	if listRes.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRes.Code, listRes.Body.String())
	}
	assertJSONContains(t, listRes.Body.Bytes(), `"hostname":"dev.openbox.example"`, `"visibility":"private"`)

	publishRes := httptest.NewRecorder()
	handler.ServeHTTP(publishRes, httptest.NewRequest(http.MethodPost, "/v1/routes/"+routeID+"/publish", nil))
	if publishRes.Code != http.StatusOK {
		t.Fatalf("publish status=%d body=%s", publishRes.Code, publishRes.Body.String())
	}
	assertJSONContains(t, publishRes.Body.Bytes(), `"visibility":"public"`)

	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, httptest.NewRequest(http.MethodDelete, "/v1/routes/"+routeID, nil))
	if deleteRes.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleteRes.Code, deleteRes.Body.String())
	}
}

func TestRouteCreateRejectsForbiddenTargets(t *testing.T) {
	t.Parallel()
	repo := newRouteTestRepo()
	repo.instances["host-inst"] = domain.Instance{ID: "host-inst", OwnerID: "owner-local", RuntimeRef: "host"}
	handler := newRouteTestHandler(t, repo)

	body := `{"instance_id":"host-inst","hostname":"evil.openbox.example","target_port":22}`
	req := httptest.NewRequest(http.MethodPost, "/v1/routes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	assertJSONContains(t, res.Body.Bytes(), `"code":"invalid_argument"`)
	if len(repo.routes) != 0 {
		t.Fatalf("rejected create still persisted routes: %+v", repo.routes)
	}
}

func TestSuggestedPortsNeverCreateRoutes(t *testing.T) {
	t.Parallel()
	repo := newRouteTestRepo()
	repo.instances["inst-1"] = domain.Instance{ID: "inst-1", OwnerID: "owner-local", RuntimeRef: "incus-1"}
	handler := newRouteTestHandler(t, repo)

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/instances/inst-1/suggested-ports", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	assertJSONContains(t, res.Body.Bytes(), `"items":[]`)
	if len(repo.routes) != 0 {
		t.Fatalf("suggestions created routes: %+v", repo.routes)
	}

	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, httptest.NewRequest(http.MethodGet, "/v1/routes", nil))
	if listRes.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRes.Code, listRes.Body.String())
	}
	assertJSONContains(t, listRes.Body.Bytes(), `"items":[]`)
}

func newRouteTestHandler(t *testing.T, repo *routeTestRepo) http.Handler {
	t.Helper()
	svc, err := routes.New(repo, routes.Options{
		Now:   func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) },
		NewID: func() string { return "route-test-1" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return newTestHandlerWithOptions(t, &fakeService{}, Options{OwnerID: "owner-local", Routes: svc})
}

type routeTestRepo struct {
	instances map[domain.InstanceID]domain.Instance
	routes    map[domain.RouteID]domain.Route
}

func newRouteTestRepo() *routeTestRepo {
	return &routeTestRepo{
		instances: map[domain.InstanceID]domain.Instance{},
		routes:    map[domain.RouteID]domain.Route{},
	}
}

func (f *routeTestRepo) CreateRoute(_ context.Context, route domain.Route) error {
	f.routes[route.ID] = route
	return nil
}
func (f *routeTestRepo) GetRoute(_ context.Context, owner domain.OwnerID, id domain.RouteID) (domain.Route, error) {
	route, ok := f.routes[id]
	if !ok || route.OwnerID != owner {
		return domain.Route{}, &domain.Error{Code: domain.CodeNotFound, Field: "route"}
	}
	return route, nil
}
func (f *routeTestRepo) ListRoutes(_ context.Context, owner domain.OwnerID) ([]domain.Route, error) {
	out := make([]domain.Route, 0)
	for _, route := range f.routes {
		if route.OwnerID == owner {
			out = append(out, route)
		}
	}
	return out, nil
}
func (f *routeTestRepo) UpdateRoute(_ context.Context, route domain.Route) error {
	if _, ok := f.routes[route.ID]; !ok {
		return &domain.Error{Code: domain.CodeNotFound, Field: "route"}
	}
	f.routes[route.ID] = route
	return nil
}
func (f *routeTestRepo) DeleteRoute(_ context.Context, owner domain.OwnerID, id domain.RouteID) error {
	route, ok := f.routes[id]
	if !ok || route.OwnerID != owner {
		return &domain.Error{Code: domain.CodeNotFound, Field: "route"}
	}
	delete(f.routes, id)
	return nil
}
func (f *routeTestRepo) FindRouteByHostname(_ context.Context, hostname string) (domain.Route, bool, error) {
	for _, route := range f.routes {
		if strings.EqualFold(route.Hostname, hostname) {
			return route, true, nil
		}
	}
	return domain.Route{}, false, nil
}
func (f *routeTestRepo) GetInstance(_ context.Context, owner domain.OwnerID, id domain.InstanceID) (domain.Instance, error) {
	instance, ok := f.instances[id]
	if !ok || instance.OwnerID != owner {
		return domain.Instance{}, &domain.Error{Code: domain.CodeNotFound, Field: "instance"}
	}
	return instance, nil
}
