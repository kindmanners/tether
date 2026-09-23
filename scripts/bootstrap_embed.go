// Package scripts exposes the platform node bootstrappers to the desktop Agent.
// Keeping the embedded bytes beside the executable script ensures the GUI and
// terminal flow always execute the same reviewed provisioning code.
package scripts

import _ "embed"

// WindowsNodeBootstrap is materialized into the user's local Tether install
// directory only while the Agent needs to run elevated setup.
//
//go:embed bootstrap-windows-node.ps1
var WindowsNodeBootstrap []byte

// LinuxNodeBootstrap is materialized into the user's local cache directory
// while the Agent runs the reviewed Linux setup. It deliberately never handles
// pairing or writes any state outside the local Tether directories it receives.
//
//go:embed bootstrap-linux-node.sh
var LinuxNodeBootstrap []byte

// LinuxOrchestratorBootstrap builds the local llama-server that a Tether
// Orchestrator uses to host models and connect to paired Agent RPC servers.
// It is separate from GPU-node setup: it never writes Agent configuration,
// opens ports, or participates in pairing.
//
//go:embed bootstrap-linux-orchestrator.sh
var LinuxOrchestratorBootstrap []byte

// WindowsOrchestratorBootstrap builds only the local llama-server used by the
// desktop Orchestrator. It does not create an Agent, open firewall ports, or
// alter Tailscale state.
//
//go:embed bootstrap-windows-orchestrator.ps1
var WindowsOrchestratorBootstrap []byte
