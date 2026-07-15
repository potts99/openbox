// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/openbox-dev/openbox/internal/cloudinit"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

const (
	InstanceIDLabel           = "user.openbox.instance_id"
	OwnerIDLabel              = "user.openbox.owner_id"
	CloudInitOverrideProperty = "user.openbox.cloud_init"
)

type imageRecord struct {
	Fingerprint  string            `json:"fingerprint"`
	Architecture string            `json:"architecture"`
	Type         string            `json:"type"`
	Properties   map[string]string `json:"properties"`
	Aliases      []struct {
		Name string `json:"name"`
	} `json:"aliases"`
}

type instanceRecord struct {
	Name           string                       `json:"name"`
	Type           string                       `json:"type"`
	Status         string                       `json:"status"`
	Config         map[string]string            `json:"config"`
	ExpandedConfig map[string]string            `json:"expanded_config"`
	Devices        map[string]map[string]string `json:"devices,omitempty"`
}

func (a *Adapter) ListImages(ctx context.Context) ([]runtimeapi.Image, error) {
	query := url.Values{"project": {a.project}, "recursion": {"1"}}
	var records []imageRecord
	if err := a.request(ctx, http.MethodGet, "/1.0/images", query, nil, &records); err != nil {
		return nil, fmt.Errorf("list Incus images: %w", err)
	}
	result := make([]runtimeapi.Image, 0, len(records))
	for _, record := range records {
		aliases := make([]string, 0, len(record.Aliases))
		for _, alias := range record.Aliases {
			aliases = append(aliases, alias.Name)
		}
		cloudInit := imageSupportsCloudInit(record.Properties)
		result = append(result, runtimeapi.Image{Fingerprint: record.Fingerprint, Aliases: aliases, Architecture: record.Architecture, Type: record.Type, CloudInit: cloudInit})
	}
	return result, nil
}

func imageSupportsCloudInit(properties map[string]string) bool {
	return strings.EqualFold(properties["variant"], "cloud") || strings.EqualFold(properties[CloudInitOverrideProperty], "true")
}

func (a *Adapter) InspectInstance(ctx context.Context, ref string) (runtimeapi.Instance, error) {
	var record instanceRecord
	err := a.request(ctx, http.MethodGet, "/1.0/instances/"+url.PathEscape(ref), url.Values{"project": {a.project}}, nil, &record)
	if isNotFound(err) {
		return runtimeapi.Instance{}, runtimeapi.ErrNotFound
	}
	if err != nil {
		return runtimeapi.Instance{}, fmt.Errorf("inspect Incus instance: %w", err)
	}
	state, err := incusState(record.Status)
	if err != nil {
		return runtimeapi.Instance{}, err
	}
	config := record.ExpandedConfig
	if config == nil {
		config = record.Config
	}
	metadata := make(map[string]string)
	for key, value := range config {
		if strings.HasPrefix(key, "user.openbox.") {
			metadata[key] = value
		}
	}
	privileged := config["security.privileged"] == "true"
	return runtimeapi.Instance{
		Ref: record.Name, Image: config["volatile.base_image"], State: state,
		IsVM: record.Type == "virtual-machine", Metadata: metadata, Privileged: privileged,
	}, nil
}

func (a *Adapter) CreateInstance(ctx context.Context, request runtimeapi.CreateRequest) (runtimeapi.Instance, error) {
	if request.VM {
		return runtimeapi.Instance{}, fmt.Errorf("create VM: %w", runtimeapi.ErrUnsupported)
	}
	if !request.Unprivileged {
		return runtimeapi.Instance{}, fmt.Errorf("privileged containers: %w", runtimeapi.ErrUnsupported)
	}
	if request.Ref == "" || request.Image == "" {
		return runtimeapi.Instance{}, errors.New("container ref and immutable image fingerprint are required")
	}
	config := map[string]string{"security.privileged": "false"}
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
	if request.OwnerPublicKey != "" {
		userData, err := cloudinit.OwnerKey(request.OwnerPublicKey)
		if err != nil {
			return runtimeapi.Instance{}, fmt.Errorf("build owner cloud-init data: %w", err)
		}
		config["cloud-init.user-data"] = userData
	}
	devices := map[string]map[string]string(nil)
	if request.Resources.DiskBytes > 0 {
		if a.storagePool == "" {
			return runtimeapi.Instance{}, fmt.Errorf("disk resource requires configured storage pool: %w", runtimeapi.ErrUnsupported)
		}
		devices = map[string]map[string]string{"root": {"type": "disk", "path": "/", "pool": a.storagePool, "size": strconv.FormatInt(request.Resources.DiskBytes, 10) + "B"}}
	}
	body := struct {
		Name     string                       `json:"name"`
		Type     string                       `json:"type"`
		Source   map[string]string            `json:"source"`
		Profiles []string                     `json:"profiles"`
		Config   map[string]string            `json:"config"`
		Devices  map[string]map[string]string `json:"devices,omitempty"`
	}{Name: request.Ref, Type: "container", Source: map[string]string{"type": "image", "fingerprint": request.Image}, Profiles: []string{a.containerProfile}, Config: config, Devices: devices}
	err := a.request(ctx, http.MethodPost, "/1.0/instances", url.Values{"project": {a.project}}, body, nil)
	if err != nil {
		var httpErr *HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusConflict {
			return runtimeapi.Instance{}, runtimeapi.ErrAlreadyExists
		}
		return runtimeapi.Instance{}, fmt.Errorf("create Incus container: %w", err)
	}
	return a.InspectInstance(ctx, request.Ref)
}

func (a *Adapter) StartInstance(ctx context.Context, ref string) error {
	return a.changeState(ctx, ref, "start")
}

func (a *Adapter) StopInstance(ctx context.Context, ref string) error {
	return a.changeState(ctx, ref, "stop")
}

func (a *Adapter) changeState(ctx context.Context, ref, action string) error {
	body := map[string]any{"action": action, "timeout": 30, "force": false}
	err := a.request(ctx, http.MethodPut, "/1.0/instances/"+url.PathEscape(ref)+"/state", url.Values{"project": {a.project}}, body, nil)
	if isNotFound(err) {
		return runtimeapi.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("%s Incus container: %w", action, err)
	}
	return nil
}

func (a *Adapter) DeleteInstance(ctx context.Context, ref string) error {
	err := a.request(ctx, http.MethodDelete, "/1.0/instances/"+url.PathEscape(ref), url.Values{"project": {a.project}}, nil, nil)
	if isNotFound(err) {
		return runtimeapi.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("delete Incus container: %w", err)
	}
	return nil
}

func incusState(status string) (runtimeapi.InstanceState, error) {
	switch strings.ToLower(status) {
	case "running":
		return runtimeapi.StateRunning, nil
	case "stopped", "stopping", "starting", "frozen", "error":
		return runtimeapi.StateStopped, nil
	default:
		return "", fmt.Errorf("unsupported Incus instance status %q: %w", status, runtimeapi.ErrUnsupported)
	}
}
