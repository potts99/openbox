// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/openbox-dev/openbox/internal/cloudinit"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

func (a *Adapter) createVM(ctx context.Context, request runtimeapi.CreateRequest) (runtimeapi.Instance, error) {
	if request.Ref == "" || request.Image == "" {
		return runtimeapi.Instance{}, errors.New("VM ref and immutable image fingerprint are required")
	}
	if request.Unprivileged {
		return runtimeapi.Instance{}, fmt.Errorf("unprivileged is a container-only option: %w", runtimeapi.ErrUnsupported)
	}
	if a.storagePool == "" {
		return runtimeapi.Instance{}, fmt.Errorf("VM root disk requires configured storage pool: %w", runtimeapi.ErrUnsupported)
	}
	var image imageRecord
	if err := a.request(ctx, http.MethodGet, "/1.0/images/"+url.PathEscape(request.Image), url.Values{"project": {a.project}}, nil, &image); err != nil {
		return runtimeapi.Instance{}, fmt.Errorf("inspect pinned VM image: %w", err)
	}
	if image.Fingerprint != request.Image {
		return runtimeapi.Instance{}, errors.New("VM image reference is not an immutable fingerprint")
	}
	if image.Type != "virtual-machine" {
		return runtimeapi.Instance{}, fmt.Errorf("image is not virtual-machine compatible: %w", runtimeapi.ErrUnsupported)
	}
	if !imageSupportsCloudInit(image.Properties) {
		return runtimeapi.Instance{}, fmt.Errorf("VM image does not advertise cloud-init compatibility: %w", runtimeapi.ErrUnsupported)
	}

	config := make(map[string]string, len(request.Metadata)+3)
	for key, value := range request.Metadata {
		if !strings.HasPrefix(key, "user.openbox.") {
			return runtimeapi.Instance{}, fmt.Errorf("unsupported instance metadata key %q", key)
		}
		config[key] = value
	}
	if config[ManagedLabel] != "true" || config[ResourceLabel] != "instance" || config[InstanceIDLabel] == "" || config[OwnerIDLabel] == "" {
		return runtimeapi.Instance{}, errors.New("complete OpenBox ownership metadata is required")
	}
	if request.Resources.VCPUs > 0 {
		config["limits.cpu"] = strconv.Itoa(request.Resources.VCPUs)
	}
	if request.Resources.MemoryBytes > 0 {
		config["limits.memory"] = strconv.FormatInt(request.Resources.MemoryBytes, 10) + "B"
	}
	userData, err := cloudinit.OwnerKey(request.OwnerPublicKey)
	if err != nil {
		return runtimeapi.Instance{}, fmt.Errorf("build VM owner cloud-init data: %w", err)
	}
	config["cloud-init.user-data"] = userData
	root := map[string]string{"type": "disk", "path": "/", "pool": a.storagePool}
	if request.Resources.DiskBytes > 0 {
		root["size"] = strconv.FormatInt(request.Resources.DiskBytes, 10) + "B"
	}
	devices := map[string]map[string]string{
		"root":       root,
		"cloud-init": {"type": "disk", "source": "cloud-init:config"},
		"eth0":       {"type": "nic", "network": a.network, "name": "eth0"},
	}
	body := struct {
		Name     string                       `json:"name"`
		Type     string                       `json:"type"`
		Source   map[string]string            `json:"source"`
		Profiles []string                     `json:"profiles"`
		Config   map[string]string            `json:"config"`
		Devices  map[string]map[string]string `json:"devices"`
	}{
		Name: request.Ref, Type: "virtual-machine",
		Source:   map[string]string{"type": "image", "fingerprint": request.Image},
		Profiles: []string{a.vmProfile}, Config: config, Devices: devices,
	}
	if err := a.request(ctx, http.MethodPost, "/1.0/instances", url.Values{"project": {a.project}}, body, nil); err != nil {
		var httpErr *HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusConflict {
			return runtimeapi.Instance{}, runtimeapi.ErrAlreadyExists
		}
		return runtimeapi.Instance{}, fmt.Errorf("create Incus VM: %w", err)
	}
	return a.InspectInstance(ctx, request.Ref)
}

type instanceStateRecord struct {
	Status  string                          `json:"status"`
	CPU     instanceStateCPU                `json:"cpu"`
	Memory  instanceStateMemory             `json:"memory"`
	Disk    map[string]instanceStateDisk    `json:"disk"`
	Network map[string]instanceStateNetwork `json:"network"`
}

type instanceStateCPU struct {
	Usage int64 `json:"usage"`
}

type instanceStateMemory struct {
	Usage int64 `json:"usage"`
}

type instanceStateDisk struct {
	Usage int64 `json:"usage"`
}

type instanceStateNetwork struct {
	Addresses []instanceStateAddress `json:"addresses"`
	Counters  instanceStateCounters  `json:"counters"`
}

type instanceStateAddress struct {
	Family  string `json:"family"`
	Address string `json:"address"`
	Scope   string `json:"scope"`
}

type instanceStateCounters struct {
	BytesReceived int64 `json:"bytes_received"`
	BytesSent     int64 `json:"bytes_sent"`
}

func (a *Adapter) WaitInstanceReady(ctx context.Context, request runtimeapi.ReadinessRequest) error {
	if request.Ref == "" {
		return errors.New("VM readiness ref is required")
	}
	ctx, cancel := context.WithTimeout(ctx, a.readinessTimeout)
	defer cancel()
	stage := "waiting_for_agent"
	if request.Stage != nil {
		if err := request.Stage(stage); err != nil {
			return err
		}
	}
	var lastErr error
	for {
		agentReady, agentErr := a.probeAgent(ctx, request.Ref)
		if errors.Is(agentErr, runtimeapi.ErrNotFound) {
			return agentErr
		}
		if agentErr != nil {
			lastErr = agentErr
		}
		address, found, err := a.vmAddress(ctx, request.Ref)
		if err != nil {
			if errors.Is(err, runtimeapi.ErrNotFound) {
				return err
			}
			lastErr = err
		}
		if agentReady && found {
			stage = "waiting_for_ssh"
			if request.Stage != nil {
				if err := request.Stage(stage); err != nil {
					return err
				}
			}
			for {
				ready, probeErr := a.probeSSH(ctx, address)
				if probeErr != nil {
					lastErr = probeErr
				}
				if ready {
					return nil
				}
				if err := waitPoll(ctx, a.readinessPoll); err != nil {
					return readinessError(stage, err, lastErr)
				}
			}
		}
		if err := waitPoll(ctx, a.readinessPoll); err != nil {
			return readinessError(stage, err, lastErr)
		}
	}
}

func (a *Adapter) probeAgent(ctx context.Context, ref string) (bool, error) {
	body := struct {
		Command          []string `json:"command"`
		Interactive      bool     `json:"interactive"`
		WaitForWebsocket bool     `json:"wait-for-websocket"`
	}{Command: []string{"/bin/true"}}
	err := a.request(ctx, http.MethodPost, "/1.0/instances/"+url.PathEscape(ref)+"/exec", url.Values{"project": {a.project}}, body, nil)
	if isNotFound(err) {
		return false, runtimeapi.ErrNotFound
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (a *Adapter) vmAddress(ctx context.Context, ref string) (string, bool, error) {
	var state instanceStateRecord
	err := a.request(ctx, http.MethodGet, "/1.0/instances/"+url.PathEscape(ref)+"/state", url.Values{"project": {a.project}}, nil, &state)
	if isNotFound(err) {
		return "", false, runtimeapi.ErrNotFound
	}
	if err != nil {
		return "", false, err
	}
	address, found := selectInstanceAddress(state, false)
	return address, found, nil
}

func selectInstanceAddress(state instanceStateRecord, privateOnly bool) (string, bool) {
	var fallback string
	for name, network := range state.Network {
		if name == "lo" {
			continue
		}
		for _, address := range network.Addresses {
			ip := net.ParseIP(address.Address)
			if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || address.Scope == "link" {
				continue
			}
			if ip.IsPrivate() {
				return address.Address, true
			}
			if !privateOnly && fallback == "" {
				fallback = address.Address
			}
		}
	}
	return fallback, fallback != ""
}

// InstanceSSHAddress returns a managed instance's private address for host-side
// gateway access. The same Incus state endpoint works for containers and VMs.
func (a *Adapter) InstanceSSHAddress(ctx context.Context, ref string) (string, error) {
	var managed resource
	if err := a.request(ctx, http.MethodGet, "/1.0/networks/"+url.PathEscape(a.network), url.Values{"project": {a.project}}, nil, &managed); err != nil {
		return "", fmt.Errorf("inspect managed instance network: %w", err)
	}
	_, subnet, err := net.ParseCIDR(managed.Config["ipv4.address"])
	if err != nil || subnet == nil || !subnet.IP.IsPrivate() {
		return "", errors.New("managed instance network has no private IPv4 subnet")
	}
	var state instanceStateRecord
	err = a.request(ctx, http.MethodGet, "/1.0/instances/"+url.PathEscape(ref)+"/state", url.Values{"project": {a.project}}, nil, &state)
	if isNotFound(err) {
		return "", runtimeapi.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	address, found := selectInstanceAddressInNetwork(state, subnet)
	if !found {
		return "", errors.New("instance has no managed private SSH address")
	}
	return address, nil
}

func selectInstanceAddressInNetwork(state instanceStateRecord, subnet *net.IPNet) (string, bool) {
	if subnet == nil {
		return "", false
	}
	for name, network := range state.Network {
		if name == "lo" {
			continue
		}
		for _, address := range network.Addresses {
			ip := net.ParseIP(address.Address)
			if ip != nil && ip.IsPrivate() && !ip.IsUnspecified() && !ip.IsLoopback() && !ip.IsMulticast() && !ip.IsLinkLocalUnicast() && address.Scope != "link" && subnet.Contains(ip) {
				return address.Address, true
			}
		}
	}
	return "", false
}

func (a *Adapter) probeSSH(ctx context.Context, address string) (bool, error) {
	if a.sshProbe != nil {
		return a.sshProbe(ctx, address)
	}
	dialer := net.Dialer{Timeout: a.timeout}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(address, "22"))
	if err != nil {
		return false, err
	}
	_ = connection.Close()
	return true, nil
}

func waitPoll(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func readinessError(stage string, cause, last error) error {
	if last != nil {
		return fmt.Errorf("VM readiness timed out during %s (%v): %w", stage, last, cause)
	}
	return fmt.Errorf("VM readiness timed out during %s: %w", stage, cause)
}
