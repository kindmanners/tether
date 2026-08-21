package main
// Command tether-test is a throwaway harness, not the real Tether
// entrypoint. It exists to verify tailscale.go and allowlist.go work
// against real data before registry.go's merge logic is built on top of
// them. Delete this file (and cmd/tether-test/) once registry.go exists and
// cmd/tether/main.go takes over as the real entrypoint.


import (
	"fmt"
	"os"

	"tether/internal/registry"
)

func main() {
	fmt.Println("=== Testing QueryTailscalePeers() ===")
	peers, err := registry.QueryTailscalePeers()
	if err != nil {
		fmt.Fprintf(os.Stderr, "QueryTailscalePeers failed: %v\n", err)
		os.Exit(1)
	}

	if len(peers) == 0 {
		fmt.Println("No peers found. Either nothing is on the tailnet besides Self, or something's wrong.")
	}

	for _, p := range peers {
		fmt.Printf("  hostname=%-12s ipv4=%-15s online=%-5v seenAt=%s\n",
			p.Hostname, p.IPv4Address, p.Online, p.SeenAt.Format("15:04:05"))
	}

	fmt.Println()
	fmt.Println("=== Testing LoadAllowlist() ===")
	allowlist, err := registry.LoadAllowlist("node_allowlist.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "LoadAllowlist failed: %v\n", err)
		os.Exit(1)
	}

	for _, entry := range allowlist.Nodes {
		fmt.Printf("  hostname=%-12s role=%-10s agentPort=%-6d rpcPort=%d\n",
			entry.Hostname, entry.Role, entry.AgentPort, entry.RPCPort)
	}

	fmt.Println()
	fmt.Println("=== Manual cross-check (registry.go doesn't exist yet, so eyeball this) ===")
	for _, entry := range allowlist.Nodes {
		found := false
		for _, p := range peers {
			if p.Hostname == entry.Hostname {
				found = true
				fmt.Printf("  MATCH: %s -> ip=%s online=%v\n", entry.Hostname, p.IPv4Address, p.Online)
				break
			}
		}
		if !found {
			fmt.Printf("  NO MATCH: %s is in allowlist but was not seen as a live Tailscale peer\n", entry.Hostname)
		}
	}
}