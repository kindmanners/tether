package registry
// Package registry maintains Tether's view of which machines on the tailnet
// are usable inference nodes, merging live Tailscale peer data with a
// hand-maintained allowlist (see node_allowlist.yaml).

import "time"

// NodeStatus describes what the registry currently believes about a node's
// reachability. This is distinct from whether the node's llama.cpp RPC
// server is actually running — that's tracked separately once the Agent
// exists (see design doc §4.1, layer 3).
type NodeStatus int

const (
	// StatusUnknown means the registry hasn't checked this node yet, or the
	// last check result is stale enough not to trust.
	StatusUnknown NodeStatus = iota

	// StatusOnline means the node appeared in the last Tailscale peer query
	// and passed a basic reachability check.
	StatusOnline

	// StatusOffline means the node is in the allowlist but did not appear
	// as an active peer in the last Tailscale query (e.g. machine is off).
	StatusOffline

	// StatusAgentUnreachable means the node is online at the Tailscale
	// level, but the Tether Agent daemon on it did not respond. Not used
	// yet — reserved for when the Agent exists (design doc §5, step 2).
	StatusAgentUnreachable
)

// String gives a human-readable label, used for logging and any future CLI
// output. Kept here rather than relying on the int value directly so
// printing a Node never accidentally shows a bare "2" instead of "Offline".
func (s NodeStatus) String() string {
	switch s {
	case StatusOnline:
		return "Online"
	case StatusOffline:
		return "Offline"
	case StatusAgentUnreachable:
		return "AgentUnreachable"
	default:
		return "Unknown"
	}
}

// NodeCapabilities describes what a node can actually contribute to
// distributed inference. Nothing populates this yet — it exists now so the
// Node struct doesn't need a breaking change later when scheduling logic
// needs it (design doc §4.3).
type NodeCapabilities struct {
	CPUModel string
	GPUModel  string
	VRAMTotal int64
	RAMTotal int64
}

// Node represents a single machine the registry knows about. A Node only
// exists in the registry if it satisfies both discovery layers described in
// the design doc (§4.1): it must appear as a live Tailscale peer AND be
// present in the YAML allowlist. Appearing in only one or the other means
// it does not become a Node — see registry.go for the merge logic.
type Node struct {
	// Hostname is the Tailscale MagicDNS name, e.g. "ataraxia". Used as the
	// primary key for matching allowlist entries to live Tailscale peers.
	Hostname string

	// TailscaleIP is the peer's 100.x.x.x tailnet address, taken from the
	// live `tailscale status` query. Empty if the node has never been seen
	// as an active peer.
	TailscaleIP string

	// Role is an app-level tag from the allowlist config — Tailscale itself
	// has no concept of this. Currently only "rpc-node" is meaningful.
	Role string

	// AgentPort is the port the Tether Agent daemon will listen on for this
	// node, once the Agent exists. Comes from the allowlist config.
	AgentPort int

	// RPCPort is the port llama.cpp's rpc-server will listen on for this
	// node, once started. Comes from the allowlist config.
	RPCPort int

	// Status is the registry's current best understanding of reachability.
	// See NodeStatus for what each value means.
	Status NodeStatus

	// LastSeen is the timestamp of the last successful check that saw this
	// node as an active Tailscale peer. Zero value means never seen.
	LastSeen time.Time

	// Capabilities is unpopulated in v1 — reserved for scheduling logic
	// once nodes can report their own GPU/VRAM info.
	Capabilities NodeCapabilities
}