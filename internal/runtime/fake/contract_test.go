// SPDX-License-Identifier: AGPL-3.0-only

package fake_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/runtime/fake"
)

func TestRuntimeContract(t *testing.T) {
	capabilities := runtimeapi.Capabilities{Architecture: "x86_64", Containers: true, KVM: true, VirtualMachines: true}
	r := fake.New(capabilities)
	r.AddImage(runtimeapi.Image{Fingerprint: "sha256:base", Aliases: []string{"base"}, Architecture: "x86_64"})

	gotCapabilities, err := r.DiscoverCapabilities(context.Background())
	if err != nil || !reflect.DeepEqual(gotCapabilities, capabilities) {
		t.Fatalf("capabilities = %#v, %v", gotCapabilities, err)
	}
	images, err := r.ListImages(context.Background())
	if err != nil || len(images) != 1 || images[0].Fingerprint != "sha256:base" {
		t.Fatalf("images = %#v, %v", images, err)
	}

	created, err := r.CreateInstance(context.Background(), runtimeapi.CreateRequest{Ref: "dev", Image: "sha256:base"})
	if err != nil || created.State != runtimeapi.StateStopped {
		t.Fatalf("create = %#v, %v", created, err)
	}
	if _, err := r.CreateInstance(context.Background(), runtimeapi.CreateRequest{Ref: "dev", Image: "sha256:base"}); !errors.Is(err, runtimeapi.ErrAlreadyExists) {
		t.Fatalf("duplicate create error = %v", err)
	}
	if err := r.StartInstance(context.Background(), "dev"); err != nil {
		t.Fatal(err)
	}
	r.SetExecResult("dev", runtimeapi.ExecResult{ExitCode: 7, Stdout: []byte("out"), Stderr: []byte("err")})
	result, err := r.Exec(context.Background(), runtimeapi.ExecRequest{Ref: "dev", Command: []string{"false"}})
	if err != nil || result.ExitCode != 7 || string(result.Stdout) != "out" || string(result.Stderr) != "err" {
		t.Fatalf("exec = %#v, %v", result, err)
	}
	if err := r.CreateSnapshot(context.Background(), "dev", "ready"); err != nil {
		t.Fatal(err)
	}
	copy, err := r.CopyInstance(context.Background(), runtimeapi.CopyRequest{SourceRef: "dev", Snapshot: "ready", TargetRef: "copy"})
	if err != nil || copy.Ref != "copy" || copy.State != runtimeapi.StateRunning {
		t.Fatalf("copy = %#v, %v", copy, err)
	}
	if err := r.StopInstance(context.Background(), "copy"); err != nil {
		t.Fatal(err)
	}
	inspected, err := r.InspectInstance(context.Background(), "copy")
	if err != nil || inspected.State != runtimeapi.StateStopped {
		t.Fatalf("inspect = %#v, %v", inspected, err)
	}
	if err := r.DeleteInstance(context.Background(), "copy"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.InspectInstance(context.Background(), "copy"); !errors.Is(err, runtimeapi.ErrNotFound) {
		t.Fatalf("inspect deleted error = %v", err)
	}
}

func TestCancellationAndFailureInjectionAreDeterministic(t *testing.T) {
	r := fake.New(runtimeapi.Capabilities{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.ListImages(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}
	wanted := errors.New("injected")
	r.FailNext("images.list", wanted)
	if _, err := r.ListImages(context.Background()); !errors.Is(err, wanted) {
		t.Fatalf("first error = %v", err)
	}
	if _, err := r.ListImages(context.Background()); err != nil {
		t.Fatalf("second error = %v", err)
	}
}
