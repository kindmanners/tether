// Command tether is the Tether Orchestrator — the main desktop application
// that discovers nodes, will eventually control Agents on each node, and
// will host the chat UI once inference routing exists (design doc §3).
//
// Right now this only does step 1 of the build order in design doc §5:
// build the node registry and show what it found. Agent communication and
// the Wails GUI shell get added here incrementally as those pieces exist —
// see cmd/tether-agent for the separate Agent binary.
package main

import (
	"fmt"
	"os"
	"tether/internal/registry"
)

const allowlistPath = "node_allowlist.yaml"

func main() {
	allowlist, err := registry.LoadAllowlist(allowlistPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading allowlist: %v\n", err)
		os.Exit(1)
	}

	peers, err := registry.QueryTailscalePeers()
	if err != nil {
		fmt.Fprintf(os.Stderr, "querying tailscale: %v\n", err)
		os.Exit(1)
	}

	reg := registry.Build(allowlist, peers)

	fmt.Println("Tether nodes:")
	for _, node := range reg.All() {
		fmt.Printf("  %-12s %-16s %s\n", node.Hostname, node.Status, node.TailscaleIP)
	}
}