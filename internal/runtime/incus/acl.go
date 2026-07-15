// SPDX-License-Identifier: AGPL-3.0-only

package incus

const (
	// ManagedBridgeGateway is the stable gateway address for the OpenBox bridge.
	ManagedBridgeGateway = "10.42.0.1/24"

	// ManagedBridgeGatewayHost is the host address within ManagedBridgeGateway.
	ManagedBridgeGatewayHost = "10.42.0.1"

	// DefaultDenyACLName is attached to every managed instance NIC.
	DefaultDenyACLName = "openbox-default-deny"

	// LLMGatewayPort is a bridge-gateway placeholder until the management
	// gateway is assigned its final location in a later slice.
	LLMGatewayPort = "18789"
)

type networkACLRule struct {
	Action          string `json:"action"`
	Source          string `json:"source,omitempty"`
	Destination     string `json:"destination,omitempty"`
	Protocol        string `json:"protocol,omitempty"`
	DestinationPort string `json:"destination_port,omitempty"`
	Description     string `json:"description,omitempty"`
}

func networkACLResource() resource {
	return resource{
		Name:        DefaultDenyACLName,
		Description: "OpenBox default-deny instance network policy",
		Config:      managedConfig("network-acl", nil),
		Egress: []networkACLRule{
			{
				Action: "allow", Destination: ManagedBridgeGatewayHost, Protocol: "udp", DestinationPort: "53",
				Description: "allow DNS UDP to managed bridge gateway",
			},
			{
				Action: "allow", Destination: ManagedBridgeGatewayHost, Protocol: "tcp", DestinationPort: "53",
				Description: "allow DNS TCP to managed bridge gateway",
			},
			{
				Action: "allow", Destination: ManagedBridgeGatewayHost, Protocol: "tcp", DestinationPort: LLMGatewayPort,
				Description: "allow LLM Gateway management placeholder",
			},
		},
		Ingress: []networkACLRule{
			{
				Action: "allow", Source: ManagedBridgeGatewayHost, Protocol: "tcp", DestinationPort: "22",
				Description: "allow OpenBox SSH gateway access",
			},
		},
	}
}
