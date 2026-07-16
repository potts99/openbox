// SPDX-License-Identifier: AGPL-3.0-only

package pool

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/openbox-dev/openbox/internal/cloudinit"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/images"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

var ErrMiss = errors.New("sandbox pool miss")

// Claim is a reserved warm-pool slot awaiting personalization.
type Claim struct {
	Ref      string
	Running  bool
}

// AssignRequest personalizes a claimed slot into a user instance runtime ref.
type AssignRequest struct {
	SlotRef        string
	TargetRef      string
	OwnerPublicKey string
	Metadata       map[string]string
	WasRunning     bool
}

// Stats summarizes current pool depth for logging and diagnostics.
type Stats struct {
	GoldenReady bool
	Stopped     int
	Running     int
	Claiming    int
	CoWStorage  bool
	Substrate   Substrate
}

// Manager maintains the hybrid warm pool in Incus and memory.
type Manager struct {
	runtime Runtime
	config  Config
	now     func() time.Time
	newID   func() string

	mu        sync.Mutex
	image     string
	substrate Substrate
	stopped   []string
	running   []string
	claiming  map[string]time.Time
}

// Options configures a warm-pool manager.
type Options struct {
	Config  Config
	Now     func() time.Time
	NewID   func() string
	Catalog images.Catalog
}

// New constructs a pool manager. Image resolution happens during Bootstrap.
func New(runtime Runtime, options Options) (*Manager, error) {
	if runtime == nil {
		return nil, errors.New("runtime is required")
	}
	cfg := options.Config
	if cfg.StoppedTarget < 0 || cfg.RunningTarget < 0 {
		return nil, errors.New("pool targets must not be negative")
	}
	if cfg.ReplenishInterval <= 0 {
		cfg.ReplenishInterval = DefaultConfig().ReplenishInterval
	}
	if cfg.ClaimFenceTimeout <= 0 {
		cfg.ClaimFenceTimeout = DefaultConfig().ClaimFenceTimeout
	}
	if cfg.SSHReadyTimeout <= 0 {
		cfg.SSHReadyTimeout = DefaultConfig().SSHReadyTimeout
	}
	if cfg.SSHReadyPoll <= 0 {
		cfg.SSHReadyPoll = DefaultConfig().SSHReadyPoll
	}
	if cfg.ClaimTimeout <= 0 {
		cfg.ClaimTimeout = DefaultConfig().ClaimTimeout
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = randomID
	}
	return &Manager{
		runtime:  runtime,
		config:   cfg,
		now:      options.Now,
		newID:    options.NewID,
		claiming: map[string]time.Time{},
	}, nil
}

func (m *Manager) Enabled() bool {
	return m.config.Enabled
}

// Substrate reports the active pool runtime shape (empty when disabled).
func (m *Manager) Substrate() Substrate {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.substrate
}

// SetImageForTest pins the sandbox image fingerprint in unit tests.
func (m *Manager) SetImageForTest(fingerprint string) {
	m.image = fingerprint
}

// SetSubstrateForTest pins the pool substrate in unit tests.
func (m *Manager) SetSubstrateForTest(substrate Substrate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.substrate = substrate
}

func (m *Manager) Stats(ctx context.Context) (Stats, error) {
	if !m.config.Enabled {
		return Stats{}, nil
	}
	m.mu.Lock()
	stats := Stats{Stopped: len(m.stopped), Running: len(m.running), Claiming: len(m.claiming), Substrate: m.substrate}
	m.mu.Unlock()
	if _, err := m.runtime.InspectInstance(ctx, GoldenRef); err == nil {
		stats.GoldenReady = true
	}
	driver, driverErr := m.runtime.StoragePoolDriver(ctx)
	if driverErr == nil {
		stats.CoWStorage = strings.EqualFold(strings.TrimSpace(driver), "zfs")
	}
	return stats, driverErr
}

// Bootstrap selects the host substrate, then ensures the golden template and snapshot exist.
func (m *Manager) Bootstrap(ctx context.Context) error {
	if !m.config.Enabled {
		return nil
	}
	if err := m.selectSubstrate(ctx); err != nil {
		return err
	}
	if !m.config.Enabled {
		return nil
	}
	if err := m.resolveSandboxImage(ctx); err != nil {
		return err
	}
	golden, err := m.runtime.InspectInstance(ctx, GoldenRef)
	if errors.Is(err, runtimeapi.ErrNotFound) {
		return m.buildGolden(ctx)
	}
	if err != nil {
		return fmt.Errorf("inspect golden template: %w", err)
	}
	wantVM := m.Substrate() == SubstrateVM
	if golden.IsVM != wantVM {
		log.Printf("openboxd: sandbox pool golden substrate mismatch (have_vm=%v want_vm=%v); rebuilding", golden.IsVM, wantVM)
		if err := m.resetPoolInstances(ctx); err != nil {
			return err
		}
		if err := m.resolveSandboxImage(ctx); err != nil {
			return err
		}
		return m.buildGolden(ctx)
	}
	if err := m.runtime.CreateSnapshot(ctx, GoldenRef, GoldenSnapshot); err != nil && !errors.Is(err, runtimeapi.ErrAlreadyExists) {
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return m.Reconcile(ctx)
		}
		return m.ensureGoldenSnapshot(ctx)
	}
	return m.Reconcile(ctx)
}

func (m *Manager) ensureGoldenSnapshot(ctx context.Context) error {
	instance, err := m.runtime.InspectInstance(ctx, GoldenRef)
	if err != nil {
		return err
	}
	if instance.State != runtimeapi.StateRunning {
		if err := m.runtime.StartInstance(ctx, GoldenRef); err != nil {
			return fmt.Errorf("start golden template: %w", err)
		}
		readyCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if err := m.waitSSH(readyCtx, GoldenRef); err != nil {
			return fmt.Errorf("golden template SSH readiness: %w", err)
		}
	}
	if err := m.runtime.StopInstance(ctx, GoldenRef); err != nil {
		return fmt.Errorf("stop golden template: %w", err)
	}
	return m.snapshotGolden(ctx)
}

// Reconcile rebuilds in-memory queues from Incus labels and reclaims stale claims.
func (m *Manager) Reconcile(ctx context.Context) error {
	if !m.config.Enabled {
		return nil
	}
	instances, err := m.runtime.ListInstances(ctx)
	if err != nil {
		return fmt.Errorf("list instances for pool reconcile: %w", err)
	}
	stopped := make([]string, 0, m.config.StoppedTarget)
	running := make([]string, 0, m.config.RunningTarget)
	claiming := map[string]time.Time{}
	now := m.now().UTC()
	for _, instance := range instances {
		role := instance.Metadata[RoleLabel]
		if role != RoleSlot {
			continue
		}
		ref := instance.Ref
		switch instance.Metadata[StateLabel] {
		case StateRunning:
			if instance.State == runtimeapi.StateRunning {
				running = append(running, ref)
			} else {
				stopped = append(stopped, ref)
			}
		case StateStopped:
			stopped = append(stopped, ref)
		case StateClaiming:
			claimedAt := now
			if raw := strings.TrimSpace(instance.Metadata[ClaimedAtLabel]); raw != "" {
				if parsed, parseErr := time.Parse(time.RFC3339Nano, raw); parseErr == nil {
					claimedAt = parsed
				}
			}
			if now.Sub(claimedAt) > m.config.ClaimFenceTimeout {
				_ = m.runtime.DeleteInstance(ctx, ref)
				continue
			}
			claiming[ref] = claimedAt
		}
	}
	m.mu.Lock()
	m.stopped = stopped
	m.running = running
	m.claiming = claiming
	m.mu.Unlock()
	return nil
}

// Replenish fills the pool toward configured targets.
func (m *Manager) Replenish(ctx context.Context) {
	if !m.config.Enabled {
		return
	}
	if err := m.Bootstrap(ctx); err != nil {
		log.Printf("openboxd: sandbox pool bootstrap: %v", err)
		return
	}
	if err := m.reconcileStaleClaims(ctx); err != nil {
		log.Printf("openboxd: sandbox pool claim fence: %v", err)
	}
	for {
		m.mu.Lock()
		stoppedNeeded := m.config.StoppedTarget - len(m.stopped)
		runningNeeded := m.config.RunningTarget - len(m.running)
		m.mu.Unlock()
		if stoppedNeeded <= 0 && runningNeeded <= 0 {
			return
		}
		if stoppedNeeded > 0 {
			if err := m.addStoppedSlot(ctx); err != nil {
				log.Printf("openboxd: sandbox pool replenish stopped: %v", err)
				return
			}
			continue
		}
		if runningNeeded > 0 {
			if err := m.promoteRunningSlot(ctx); err != nil {
				log.Printf("openboxd: sandbox pool replenish running: %v", err)
				return
			}
		}
	}
}

// Claim reserves a warm slot for sandbox create.
func (m *Manager) Claim(ctx context.Context) (Claim, error) {
	if !m.config.Enabled {
		return Claim{}, ErrMiss
	}
	claimCtx, cancel := context.WithTimeout(ctx, m.config.ClaimTimeout)
	defer cancel()
	for {
		ref, running, ok := m.popSlot()
		if !ok {
			return Claim{}, ErrMiss
		}
		if err := m.markClaiming(claimCtx, ref); err != nil {
			_ = m.runtime.DeleteInstance(claimCtx, ref)
			continue
		}
		return Claim{Ref: ref, Running: running}, nil
	}
}

// Assign personalizes a claimed slot into the final user runtime identity.
func (m *Manager) Assign(ctx context.Context, request AssignRequest) error {
	if request.SlotRef == "" || request.TargetRef == "" || request.OwnerPublicKey == "" {
		return errors.New("slot ref, target ref, and owner public key are required")
	}
	ref := request.SlotRef
	config := map[string]string{
		RoleLabel:              "",
		StateLabel:             "",
		ClaimedAtLabel:         "",
		"user.openbox.resource": "instance",
	}
	for key, value := range request.Metadata {
		config[key] = value
	}
	if !request.WasRunning {
		if err := m.stopIfRunning(ctx, ref); err != nil {
			return err
		}
		// Golden CoW slots already finished first boot, so this is best-effort only.
		userData, err := cloudinit.OwnerKey(request.OwnerPublicKey)
		if err != nil {
			return fmt.Errorf("build owner cloud-init: %w", err)
		}
		config["cloud-init.user-data"] = userData
	}
	if err := m.runtime.UpdateInstanceConfig(ctx, ref, config); err != nil {
		return fmt.Errorf("personalize pool slot: %w", err)
	}
	if ref != request.TargetRef {
		if err := m.stopIfRunning(ctx, ref); err != nil {
			return err
		}
		if err := m.runtime.RenameInstance(ctx, ref, request.TargetRef); err != nil {
			return fmt.Errorf("rename pool slot: %w", err)
		}
		ref = request.TargetRef
	}
	if err := m.runtime.StartInstance(ctx, ref); err != nil {
		return fmt.Errorf("start assigned sandbox: %w", err)
	}
	readyCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	// SSH readiness (TCP :22) must succeed before agent-backed file writes on VMs.
	if err := m.waitSSH(readyCtx, ref); err != nil {
		return fmt.Errorf("wait for sandbox SSH: %w", err)
	}
	// Authoritative key install for both stopped and running slots. The Incus
	// guest agent can lag SSH briefly after start, so retry agent-not-ready.
	if err := m.writeOwnerKeys(readyCtx, ref, request.OwnerPublicKey); err != nil {
		return err
	}
	m.forgetSlot(request.SlotRef)
	return nil
}

func (m *Manager) writeOwnerKeys(ctx context.Context, ref, ownerPublicKey string) error {
	var body strings.Builder
	for _, line := range strings.Split(ownerPublicKey, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	payload := body.String()
	var last error
	for {
		err := m.runtime.WriteFile(ctx, runtimeapi.WriteFileRequest{
			Ref:  ref,
			Path: "/root/.ssh/authorized_keys",
			Body: strings.NewReader(payload),
			Mode: 0o600,
		})
		if err == nil {
			return nil
		}
		last = err
		if !strings.Contains(strings.ToLower(err.Error()), "agent") {
			return err
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("write owner keys: %w (%v)", err, last)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("write owner keys: %w (%v)", ctx.Err(), last)
		case <-timer.C:
		}
	}
}

// Discard deletes a failed pool slot and triggers replenishment asynchronously.
func (m *Manager) Discard(ctx context.Context, ref string) {
	if ref == "" {
		return
	}
	m.forgetSlot(ref)
	_ = m.runtime.DeleteInstance(ctx, ref)
}

func (m *Manager) selectSubstrate(ctx context.Context) error {
	caps, err := m.runtime.DiscoverCapabilities(ctx)
	if err != nil {
		return fmt.Errorf("discover capabilities for pool substrate: %w", err)
	}
	driver, err := m.runtime.StoragePoolDriver(ctx)
	if err != nil {
		return fmt.Errorf("inspect storage pool for pool substrate: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(driver), "zfs") {
		log.Printf("openboxd: sandbox pool disabled: configured storage pool driver is %q (ZFS required)", driver)
		m.config.Enabled = false
		return nil
	}
	vmUsable := caps.VirtualMachines && caps.VMAvailability == runtimeapi.VMAvailable
	m.mu.Lock()
	defer m.mu.Unlock()
	if vmUsable {
		m.substrate = SubstrateVM
		containerDefaults := DefaultConfig()
		vmDefaults := VMConfig()
		if m.config.StoppedTarget == containerDefaults.StoppedTarget {
			m.config.StoppedTarget = vmDefaults.StoppedTarget
		}
		if m.config.RunningTarget == containerDefaults.RunningTarget {
			m.config.RunningTarget = vmDefaults.RunningTarget
		}
		if m.config.SSHReadyTimeout == containerDefaults.SSHReadyTimeout {
			m.config.SSHReadyTimeout = vmDefaults.SSHReadyTimeout
		}
		return nil
	}
	if !caps.Containers {
		log.Printf("openboxd: sandbox pool disabled: neither KVM VMs nor containers are available")
		m.config.Enabled = false
		return nil
	}
	m.substrate = SubstrateContainer
	return nil
}

func (m *Manager) resolveSandboxImage(ctx context.Context) error {
	if m.image != "" {
		return nil
	}
	caps, err := m.runtime.DiscoverCapabilities(ctx)
	if err != nil {
		return fmt.Errorf("discover capabilities for pool image: %w", err)
	}
	arch := caps.Architecture
	if arch == "" {
		arch = "x86_64"
	}
	runtimeType := "container"
	m.mu.Lock()
	substrate := m.substrate
	m.mu.Unlock()
	if substrate == SubstrateVM {
		runtimeType = "virtual-machine"
	}
	manifest, err := images.DefaultCatalog().DefaultFor(domain.KindSandbox, arch, runtimeType)
	if err != nil {
		return err
	}
	available, err := m.runtime.ListImages(ctx)
	if err != nil {
		return fmt.Errorf("list runtime images: %w", err)
	}
	image, err := images.ResolveForType(manifest.Alias, runtimeType, available)
	if err != nil {
		return fmt.Errorf("resolve sandbox pool image: %w", err)
	}
	m.image = image.Fingerprint
	return nil
}

func (m *Manager) buildGolden(ctx context.Context) error {
	placeholderKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG9wZW5ib3gtcG9vbC1wb29sLWdvbGRlbi1pbnRlcm5hbA="
	m.mu.Lock()
	substrate := m.substrate
	m.mu.Unlock()
	_, err := m.runtime.CreatePoolContainer(ctx, PoolCreateRequest{
		Ref: GoldenRef, Image: m.image, OwnerPublicKey: placeholderKey, VM: substrate == SubstrateVM,
		Metadata: map[string]string{
			"user.openbox.managed":  "true",
			"user.openbox.resource": "pool",
			RoleLabel:               RoleGolden,
			StateLabel:              StateStopped,
		},
	})
	if err != nil && !errors.Is(err, runtimeapi.ErrAlreadyExists) {
		return fmt.Errorf("create golden template: %w", err)
	}
	if err := m.runtime.StartInstance(ctx, GoldenRef); err != nil {
		return fmt.Errorf("start golden template: %w", err)
	}
	if err := m.runtime.EnableBootstrapEgress(ctx, GoldenRef); err != nil {
		return fmt.Errorf("enable golden bootstrap egress: %w", err)
	}
	timeout := 2 * time.Minute
	if substrate == SubstrateVM {
		timeout = 5 * time.Minute
	}
	readyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := m.waitSSH(readyCtx, GoldenRef); err != nil {
		return fmt.Errorf("golden template SSH readiness: %w", err)
	}
	if err := m.runtime.StopInstance(ctx, GoldenRef); err != nil {
		return fmt.Errorf("stop golden template: %w", err)
	}
	return m.snapshotGolden(ctx)
}

func (m *Manager) snapshotGolden(ctx context.Context) error {
	if err := m.runtime.CreateSnapshot(ctx, GoldenRef, GoldenSnapshot); err != nil && !errors.Is(err, runtimeapi.ErrAlreadyExists) {
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return m.Reconcile(ctx)
		}
		return fmt.Errorf("snapshot golden template: %w", err)
	}
	return m.Reconcile(ctx)
}

func (m *Manager) addStoppedSlot(ctx context.Context) error {
	ref := RefPrefix + m.newID()
	_, err := m.runtime.CopyInstance(ctx, runtimeapi.CopyRequest{
		SourceRef: GoldenRef, Snapshot: GoldenSnapshot, TargetRef: ref,
		Metadata: slotMetadata(StateStopped),
	})
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.stopped = append(m.stopped, ref)
	m.mu.Unlock()
	return nil
}

func (m *Manager) promoteRunningSlot(ctx context.Context) error {
	ref := ""
	m.mu.Lock()
	if len(m.stopped) > 0 {
		ref = m.stopped[0]
		m.stopped = m.stopped[1:]
	}
	m.mu.Unlock()
	if ref == "" {
		if err := m.addStoppedSlot(ctx); err != nil {
			return err
		}
		m.mu.Lock()
		ref = m.stopped[0]
		m.stopped = m.stopped[1:]
		m.mu.Unlock()
	}
	if err := m.runtime.UpdateInstanceConfig(ctx, ref, map[string]string{StateLabel: StateRunning}); err != nil {
		return err
	}
	if err := m.runtime.StartInstance(ctx, ref); err != nil {
		return err
	}
	readyCtx, cancel := context.WithTimeout(ctx, m.config.SSHReadyTimeout)
	defer cancel()
	if err := m.waitSSH(readyCtx, ref); err != nil {
		_ = m.runtime.DeleteInstance(ctx, ref)
		return err
	}
	m.mu.Lock()
	m.running = append(m.running, ref)
	m.mu.Unlock()
	return nil
}

func (m *Manager) popSlot() (ref string, running bool, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.running) > 0 {
		ref = m.running[0]
		m.running = m.running[1:]
		return ref, true, true
	}
	if len(m.stopped) > 0 {
		ref = m.stopped[0]
		m.stopped = m.stopped[1:]
		return ref, false, true
	}
	return "", false, false
}

func (m *Manager) markClaiming(ctx context.Context, ref string) error {
	now := m.now().UTC()
	values := map[string]string{
		StateLabel:     StateClaiming,
		ClaimedAtLabel: now.Format(time.RFC3339Nano),
	}
	if err := m.runtime.UpdateInstanceConfig(ctx, ref, values); err != nil {
		return err
	}
	m.mu.Lock()
	m.claiming[ref] = now
	m.mu.Unlock()
	return nil
}

func (m *Manager) forgetSlot(ref string) {
	m.mu.Lock()
	delete(m.claiming, ref)
	m.mu.Unlock()
}

func (m *Manager) reconcileStaleClaims(ctx context.Context) error {
	m.mu.Lock()
	stale := make([]string, 0)
	now := m.now().UTC()
	for ref, claimedAt := range m.claiming {
		if now.Sub(claimedAt) > m.config.ClaimFenceTimeout {
			stale = append(stale, ref)
		}
	}
	m.mu.Unlock()
	for _, ref := range stale {
		_ = m.runtime.DeleteInstance(ctx, ref)
		m.forgetSlot(ref)
	}
	if len(stale) > 0 {
		return m.Reconcile(ctx)
	}
	return nil
}

func (m *Manager) stopIfRunning(ctx context.Context, ref string) error {
	instance, err := m.runtime.InspectInstance(ctx, ref)
	if err != nil {
		return err
	}
	if instance.State == runtimeapi.StateRunning {
		return m.runtime.StopInstance(ctx, ref)
	}
	return nil
}

func (m *Manager) waitSSH(ctx context.Context, ref string) error {
	return m.runtime.WaitSSHReady(ctx, ref)
}

func slotMetadata(state string) map[string]string {
	return map[string]string{
		"user.openbox.managed":  "true",
		"user.openbox.resource": "pool",
		RoleLabel:               RoleSlot,
		StateLabel:              state,
	}
}

func randomID() string {
	return strings.ToLower(fmt.Sprintf("%016x", time.Now().UnixNano()))
}

func (m *Manager) resetPoolInstances(ctx context.Context) error {
	instances, err := m.runtime.ListInstances(ctx)
	if err != nil {
		return fmt.Errorf("list instances for pool reset: %w", err)
	}
	for _, instance := range instances {
		role := instance.Metadata[RoleLabel]
		if role != RoleSlot && role != RoleGolden && instance.Ref != GoldenRef {
			continue
		}
		if err := m.runtime.DeleteInstance(ctx, instance.Ref); err != nil && !errors.Is(err, runtimeapi.ErrNotFound) {
			return fmt.Errorf("delete pool instance %s: %w", instance.Ref, err)
		}
	}
	m.mu.Lock()
	m.stopped = nil
	m.running = nil
	m.claiming = map[string]time.Time{}
	m.mu.Unlock()
	return nil
}
