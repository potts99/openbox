// SPDX-License-Identifier: AGPL-3.0-only

package acceptance_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/httpapi"
	"github.com/openbox-dev/openbox/internal/httpapi/generated"
	"github.com/openbox-dev/openbox/internal/operations"
	"github.com/openbox-dev/openbox/internal/persistence/migrations"
	"github.com/openbox-dev/openbox/internal/persistence/sqlite"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/runtime/fake"
	sandboxpool "github.com/openbox-dev/openbox/internal/sandbox/pool"
	_ "modernc.org/sqlite"
)

func TestOperatorOpsMigratesRestoredFixtureAndReportsHealth(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fixturePath := filepath.Join(root, "pre-phase-5.db")
	writePreWebhookFixture(t, fixturePath)

	migrated, err := sqlite.Open(ctx, fixturePath)
	if err != nil {
		t.Fatalf("migrate fixture: %v", err)
	}
	now := time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC)
	if err := migrated.CreateOwner(ctx, domain.Owner{ID: "owner-ops", Name: "Operator", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	createFixtureOperation(t, ctx, migrated, "pending", domain.OperationPending, now)
	createFixtureOperation(t, ctx, migrated, "failed", domain.OperationFailed, now)
	if err := migrated.Close(); err != nil {
		t.Fatal(err)
	}

	restoredPath := filepath.Join(root, "restored.db")
	copyFixture(t, fixturePath, restoredPath)
	restored, err := sqlite.Open(ctx, restoredPath)
	if err != nil {
		t.Fatalf("open restored fixture: %v", err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	pending, failed, err := restored.OperationCounts(ctx)
	if err != nil || pending != 1 || failed != 1 {
		t.Fatalf("restored operation counts pending=%d failed=%d err=%v", pending, failed, err)
	}

	runtime := fake.New(runtimeapi.Capabilities{
		Architecture: "x86_64", Containers: true, VirtualMachines: true, KVM: true,
		VMAvailability: runtimeapi.VMAvailable,
	})
	service, err := instances.New(runtime, restored, instances.Options{NetworkPolicy: nopPolicy{}})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := sandboxpool.New(runtime, sandboxpool.Options{Config: sandboxpool.Config{Enabled: false}})
	if err != nil {
		t.Fatal(err)
	}
	mode := &operations.Mode{}
	mode.SetDegraded(true)
	handler, err := httpapi.New(service, httpapi.Options{
		OwnerID: "owner-ops", Mode: mode, Operations: restored, SandboxPool: pool,
	})
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", response.Code, response.Body.String())
	}
	var health generated.Health
	if err := jsonDecode(response, &health); err != nil {
		t.Fatal(err)
	}
	if health.Degraded == nil || !*health.Degraded || health.Kvm == nil || !*health.Kvm {
		t.Fatalf("health signals=%+v", health)
	}
	if health.Operations == nil || health.Operations.Pending != 1 || health.Operations.Failed != 1 {
		t.Fatalf("health operations=%+v", health.Operations)
	}
	if health.Pool == nil || health.Pool.Enabled || health.Pool.Substrate != "" {
		t.Fatalf("health pool=%+v", health.Pool)
	}
}

func writePreWebhookFixture(t *testing.T, path string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(entries)
	for _, name := range entries {
		if strings.HasPrefix(filepath.Base(name), "015_") {
			break
		}
		body, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(string(body)); err != nil {
			t.Fatalf("apply fixture migration %s: %v", name, err)
		}
		version := strings.TrimSuffix(filepath.Base(name), ".sql")
		checksum := sha256.Sum256(body)
		if _, err := database.Exec(`INSERT INTO schema_migrations(version,checksum,applied_at) VALUES(?,?,?)`, version, fmt.Sprintf("%x", checksum), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
}

func createFixtureOperation(t *testing.T, ctx context.Context, store *sqlite.Store, suffix string, status domain.OperationStatus, now time.Time) {
	t.Helper()
	instance, err := domain.NewInstance(domain.InstanceID("instance-"+suffix), "owner-ops", "fixture-"+suffix, domain.KindSandbox, now)
	if err != nil {
		t.Fatal(err)
	}
	operation := domain.Operation{
		ID:             domain.OperationID("operation-" + suffix),
		OwnerID:        "owner-ops",
		Type:           "create",
		TargetType:     "instance",
		TargetID:       string(instance.ID),
		Status:         status,
		Stage:          "queued",
		IdempotencyKey: "fixture-" + suffix,
		RequestHash:    "fixture-" + suffix,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if _, _, err := store.CreateInstance(ctx, instance, operation); err != nil {
		t.Fatal(err)
	}
}

func copyFixture(t *testing.T, source, destination string) {
	t.Helper()
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func jsonDecode(response *httptest.ResponseRecorder, target any) error {
	return json.NewDecoder(response.Body).Decode(target)
}
