// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/openbox-dev/openbox/internal/app/metrics"
	"github.com/openbox-dev/openbox/internal/auth"
	"github.com/openbox-dev/openbox/internal/domain"
)

func TestMetricsWebSocketRequiresAuthAndOwnership(t *testing.T) {
	hub := metrics.NewHub(10, 10)
	h, m, bootstrap := newAuthHandlerWithOptions(t, Options{Metrics: hub})
	session, cookie, err := m.Bootstrap(context.Background(), "loopback", bootstrap, "a sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	svc := h.service.(*fakeService)
	svc.instances = []domain.Instance{{
		ID: "inst-owned", OwnerID: "owner-local", Name: "dev", Kind: domain.KindVPS,
		Resources: domain.Resources{VCPUs: 2, MemoryBytes: 1 << 30, DiskBytes: 10 << 30},
	}}

	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	base := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/instances/"

	t.Run("unauthenticated", func(t *testing.T) {
		status, err := dialTerminalHTTP(t, base+"inst-owned/metrics", nil)
		if err == nil {
			t.Fatal("expected rejection")
		}
		if status != http.StatusUnauthorized && status != http.StatusForbidden {
			t.Fatalf("status=%d", status)
		}
	})

	t.Run("unknown instance", func(t *testing.T) {
		status, err := dialTerminalHTTP(t, base+"inst-missing/metrics?"+auth.CSRFQuery+"="+url.QueryEscape(session.CSRFToken), http.Header{
			"Cookie": []string{auth.SessionCookie + "=" + cookie},
			"Origin": []string{server.URL},
		})
		if err == nil {
			t.Fatal("expected rejection")
		}
		if status != http.StatusNotFound {
			t.Fatalf("status=%d", status)
		}
	})

	t.Run("cookie without origin", func(t *testing.T) {
		status, err := dialTerminalHTTP(t, base+"inst-owned/metrics?"+auth.CSRFQuery+"="+url.QueryEscape(session.CSRFToken), http.Header{
			"Cookie": []string{auth.SessionCookie + "=" + cookie},
		})
		if err == nil {
			t.Fatal("expected rejection")
		}
		if status != http.StatusForbidden {
			t.Fatalf("status=%d", status)
		}
	})
}

func TestMetricsWebSocketSnapshotAndSample(t *testing.T) {
	hub := metrics.NewHub(10, 10)
	id := domain.InstanceID("inst-owned")
	cpu := 12.5
	hub.Publish(id, metrics.Limits{VCPUs: 2, MemoryBytes: 1 << 30, DiskBytes: 10 << 30}, metrics.Sample{
		At: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC), MemoryBytes: 100, DiskBytes: 200, CPUPercent: &cpu,
	})

	h, m, bootstrap := newAuthHandlerWithOptions(t, Options{Metrics: hub})
	session, cookie, err := m.Bootstrap(context.Background(), "loopback", bootstrap, "a sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	svc := h.service.(*fakeService)
	svc.instances = []domain.Instance{{
		ID: id, OwnerID: "owner-local", Name: "dev", Kind: domain.KindVPS,
		Resources: domain.Resources{VCPUs: 2, MemoryBytes: 1 << 30, DiskBytes: 10 << 30},
	}}

	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/instances/inst-owned/metrics?" +
		auth.CSRFQuery + "=" + url.QueryEscape(session.CSRFToken)

	conn, err := dialTerminal(t, wsURL, http.Header{
		"Cookie": []string{auth.SessionCookie + "=" + cookie},
		"Origin": []string{server.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var snap metricsFrame
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Type != "snapshot" || snap.Limits == nil || snap.Limits.VCPUs != 2 || len(snap.Samples) != 1 {
		t.Fatalf("snapshot=%+v", snap)
	}

	go func() {
		time.Sleep(20 * time.Millisecond)
		rx := 1024.0
		hub.Publish(id, metrics.Limits{VCPUs: 2, MemoryBytes: 1 << 30, DiskBytes: 10 << 30}, metrics.Sample{
			At: time.Date(2026, 7, 15, 12, 0, 10, 0, time.UTC), MemoryBytes: 110, DiskBytes: 210, NetRxBps: &rx,
		})
	}()

	_, data, err = conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var live metricsFrame
	if err := json.Unmarshal(data, &live); err != nil {
		t.Fatal(err)
	}
	if live.Type != "sample" || live.Sample == nil || live.Sample.MemoryBytes != 110 || live.Sample.NetRxBps == nil {
		t.Fatalf("sample=%+v", live)
	}
}
