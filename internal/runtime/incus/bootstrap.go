// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const (
	ManagedLabel  = "user.openbox.managed"
	ResourceLabel = "user.openbox.resource"
)

type BootstrapConfig struct {
	Project, Network, StoragePool, ContainerProfile, VMProfile string
}

func (c BootstrapConfig) defaults() BootstrapConfig {
	if c.Project == "" {
		c.Project = "openbox"
	}
	if c.Network == "" {
		c.Network = "openbox0"
	}
	if c.ContainerProfile == "" {
		c.ContainerProfile = "openbox-container"
	}
	if c.VMProfile == "" {
		c.VMProfile = "openbox-vm"
	}
	return c
}

type ConflictError struct {
	Kind, Name string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("Incus %s %q exists but is not managed by OpenBox; rename it or choose a different OpenBox resource name", e.Kind, e.Name)
}

type DriftError struct {
	Kind, Name string
	Fields     []string
}

func (e *DriftError) Error() string {
	return fmt.Sprintf("managed Incus %s %q differs in required fields (%s); restore those fields to the documented OpenBox values or choose a different resource name", e.Kind, e.Name, strings.Join(e.Fields, ", "))
}

type resource struct {
	Name        string                       `json:"name"`
	Description string                       `json:"description,omitempty"`
	Type        string                       `json:"type,omitempty"`
	Config      map[string]string            `json:"config,omitempty"`
	Devices     map[string]map[string]string `json:"devices,omitempty"`
}

func (a *Adapter) Bootstrap(ctx context.Context, config BootstrapConfig) error {
	config = config.defaults()
	if config.StoragePool == "" {
		return errors.New("Incus storage pool is required")
	}
	if err := a.requireStoragePool(ctx, config.StoragePool); err != nil {
		return err
	}
	project := resource{
		Name: config.Project, Description: "OpenBox managed project",
		Config: managedConfig("project", map[string]string{"features.images": "false", "features.networks": "true", "features.profiles": "true"}),
	}
	projectExists, err := a.checkExisting(ctx, "project", "/1.0/projects/"+url.PathEscape(config.Project), nil, project)
	if err != nil {
		return err
	}
	projectQuery := url.Values{"project": {config.Project}}
	if projectExists {
		checks := []struct {
			kind, path string
			value      resource
		}{
			{kind: "network", path: "/1.0/networks/" + url.PathEscape(config.Network), value: networkResource(config)},
			{kind: "profile", path: "/1.0/profiles/" + url.PathEscape(config.ContainerProfile), value: profileResource(config.ContainerProfile, "container-profile", config)},
			{kind: "profile", path: "/1.0/profiles/" + url.PathEscape(config.VMProfile), value: profileResource(config.VMProfile, "vm-profile", config)},
		}
		for _, check := range checks {
			if _, err := a.checkExisting(ctx, check.kind, check.path, projectQuery, check.value); err != nil {
				return err
			}
		}
	}
	if err := a.ensure(ctx, "project", "/1.0/projects/"+url.PathEscape(config.Project), "/1.0/projects", nil, project); err != nil {
		return err
	}
	network := networkResource(config)
	if err := a.ensure(ctx, "network", "/1.0/networks/"+url.PathEscape(config.Network), "/1.0/networks", projectQuery, network); err != nil {
		return err
	}
	for _, profile := range []struct {
		name, kind string
	}{
		{name: config.ContainerProfile, kind: "container-profile"},
		{name: config.VMProfile, kind: "vm-profile"},
	} {
		value := profileResource(profile.name, profile.kind, config)
		if err := a.ensure(ctx, "profile", "/1.0/profiles/"+url.PathEscape(profile.name), "/1.0/profiles", projectQuery, value); err != nil {
			return err
		}
	}
	return nil
}

func (a *Adapter) requireStoragePool(ctx context.Context, name string) error {
	var value resource
	if err := a.request(ctx, http.MethodGet, "/1.0/storage-pools/"+url.PathEscape(name), nil, nil, &value); err != nil {
		if isNotFound(err) {
			return fmt.Errorf("Incus storage pool %q does not exist; create it before bootstrapping OpenBox", name)
		}
		return err
	}
	return nil
}

func (a *Adapter) ensure(ctx context.Context, kind, itemPath, collectionPath string, query url.Values, desired resource) error {
	exists, err := a.checkExisting(ctx, kind, itemPath, query, desired)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if err := a.request(ctx, http.MethodPost, collectionPath, query, desired, nil); err != nil {
		return fmt.Errorf("create managed Incus %s %q: %w", kind, desired.Name, err)
	}
	return nil
}

func (a *Adapter) checkExisting(ctx context.Context, kind, itemPath string, query url.Values, desired resource) (bool, error) {
	var existing resource
	err := a.request(ctx, http.MethodGet, itemPath, query, nil, &existing)
	if err == nil {
		if existing.Config[ManagedLabel] != "true" || existing.Config[ResourceLabel] != desired.Config[ResourceLabel] {
			return true, &ConflictError{Kind: kind, Name: desired.Name}
		}
		if fields := requiredDrift(existing, desired); len(fields) > 0 {
			return true, &DriftError{Kind: kind, Name: desired.Name, Fields: fields}
		}
		return true, nil
	}
	if !isNotFound(err) {
		return false, err
	}
	return false, nil
}

func requiredDrift(existing, desired resource) []string {
	var fields []string
	if desired.Type != "" && existing.Type != desired.Type {
		fields = append(fields, "type")
	}
	for key, wanted := range desired.Config {
		if existing.Config[key] != wanted {
			fields = append(fields, "config."+key)
		}
	}
	for deviceName, desiredDevice := range desired.Devices {
		existingDevice, exists := existing.Devices[deviceName]
		if !exists {
			fields = append(fields, "devices."+deviceName)
			continue
		}
		for key, wanted := range desiredDevice {
			if existingDevice[key] != wanted {
				fields = append(fields, "devices."+deviceName+"."+key)
			}
		}
	}
	sort.Strings(fields)
	return fields
}

func networkResource(config BootstrapConfig) resource {
	return resource{
		Name: config.Network, Description: "OpenBox managed bridge", Type: "bridge",
		Config: managedConfig("network", map[string]string{"ipv4.address": "auto", "ipv4.nat": "true", "ipv6.address": "none"}),
	}
}

func profileResource(name, kind string, config BootstrapConfig) resource {
	return resource{
		Name: name, Description: "OpenBox managed profile",
		Config: managedConfig(kind, nil),
		Devices: map[string]map[string]string{
			"eth0": {"type": "nic", "network": config.Network, "name": "eth0"},
			"root": {"type": "disk", "path": "/", "pool": config.StoragePool},
		},
	}
}

func managedConfig(kind string, extra map[string]string) map[string]string {
	config := map[string]string{ManagedLabel: "true", ResourceLabel: kind}
	for key, value := range extra {
		config[key] = value
	}
	return config
}

// MarshalBootstrapConfig exists only to make bootstrap inputs easy to record in
// future durable operations without exposing Incus response types.
func MarshalBootstrapConfig(config BootstrapConfig) ([]byte, error) {
	return json.Marshal(config.defaults())
}
