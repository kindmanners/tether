// Command tether is the Tether Orchestrator — the main desktop application
// that discovers nodes, will eventually control Agents on each node, and
// will host the chat UI once inference routing exists (design doc §3).
//
// CURRENT SCOPE: node discovery (design doc §5, step 1) plus an
// interactive pairing flow (§6.2) for adding a discovered node as a
// trusted Agent. Agent command/control and the chat UI come later — see
// cmd/tether-agent for the separate Agent binary.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"tether/internal/certs"
	"tether/internal/pairing"
	"tether/internal/registry"
)

const allowlistPath = "node_allowlist.yaml"

// orchestratorIdentityName is a fixed identity name for the Orchestrator
// itself, unlike Agents (which are keyed by their own Tailscale
// hostname). The Orchestrator is a single, known role in this system —
// design doc §3 doesn't anticipate more than one Orchestrator per
// install — so a fixed name is simpler than deriving one, and it means
// the Orchestrator's identity persists correctly across restarts
// regardless of which machine or hostname it happens to run on.
const orchestratorIdentityName = "orchestrator"

// pairingPort must match cmd/tether-agent's pairingPort — both sides
// need to agree on which port the Agent's pairing server listens on.
// Currently hardcoded identically in both binaries rather than shared
// from one place; worth revisiting (e.g. moving into node_allowlist.yaml
// per-node, which already has an agent_port field per design doc §4.4)
// once an Agent's pairing port might ever differ from another's.
const pairingPort = 7420

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
	nodes := reg.All()
	for i, node := range nodes {
		fmt.Printf("  [%d] %-12s %-16s %s\n", i+1, node.Hostname, node.Status, node.TailscaleIP)
	}

	if len(nodes) == 0 {
		fmt.Println("  (none found — check node_allowlist.yaml and that tailscale is running)")
		return
	}

	fmt.Println()
	fmt.Print("Pair with which node? Enter a number, or press Enter to skip: ")

	stdin := bufio.NewScanner(os.Stdin)
	if !stdin.Scan() {
		return
	}
	choice := strings.TrimSpace(stdin.Text())
	if choice == "" {
		return
	}

	node, err := selectNode(nodes, choice)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if node.Status != registry.StatusOnline {
		fmt.Fprintf(os.Stderr, "%s is not currently online — start tether-agent on that machine first.\n", node.Hostname)
		os.Exit(1)
	}

	fmt.Printf("Enter the pairing code shown on %s's screen: ", node.Hostname)
	if !stdin.Scan() {
		return
	}
	code := strings.TrimSpace(stdin.Text())
	if code == "" {
		fmt.Fprintln(os.Stderr, "no code entered")
		os.Exit(1)
	}

	identity, err := certs.LoadOrCreate(orchestratorIdentityName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading orchestrator identity: %v\n", err)
		os.Exit(1)
	}

	client := pairing.NewClient(identity)
	addr := fmt.Sprintf("%s:%d", node.TailscaleIP, pairingPort)

	fmt.Printf("Pairing with %s at %s...\n", node.Hostname, addr)
	result, err := client.Pair(addr, code)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pairing failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "If the code was mistyped, you can try again while the agent's pairing window is still open.")
		os.Exit(1)
	}

	fmt.Printf("Paired successfully with %q.\n", result.AgentHostname)
}

// selectNode parses a 1-based index string (as displayed to the user) and
// returns the corresponding node. Kept separate from main's own flow so
// the index-parsing and bounds-checking logic isn't buried inline in a
// much longer function.
func selectNode(nodes []*registry.Node, choice string) (*registry.Node, error) {
	var index int
	if _, err := fmt.Sscanf(choice, "%d", &index); err != nil {
		return nil, fmt.Errorf("%q is not a valid number", choice)
	}
	if index < 1 || index > len(nodes) {
		return nil, fmt.Errorf("%d is out of range (expected 1-%d)", index, len(nodes))
	}
	return nodes[index-1], nil
}