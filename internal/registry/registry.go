package registry

// Registry holds the current merged view of all Tether nodes: the result
// of combining the hand-maintained allowlist (§4.2 of the design doc) with
// whatever Tailscale currently reports as live peers (§4.1, layers 1+2).
//
// This is intentionally in-memory only no persistence, no database. See
// the design doc's reasoning: the allowlist is config a human edits
// directly in YAML, and until there's a genuine need to persist runtime
// history (health-check history, job logs), rebuilding this from the
// allowlist + a fresh Tailscale query on every Orchestrator start is
// simpler and has nothing to get out of sync.
type Registry struct {
	nodes map[string]*Node // keyed by hostname
}

// Build constructs a Registry by merging an Allowlist with a slice of
// LivePeer results (typically from QueryTailscalePeers).
//
// A host becomes a Node in the returned Registry if and only if it appears
// in BOTH inputs this is the two-layer verification from the design doc
// (§4.1): allowlist-only entries are hosts you've configured but that
// aren't currently reachable on the tailnet (could be offline, could be a
// typo in the YAML); peer-only entries are tailnet devices Tailscale knows
// about that were never declared as Tether nodes (e.g. Citadel, your
// phone) and must never be treated as one just because they happen to be
// online.
//
// Build takes its inputs as parameters rather than calling
// QueryTailscalePeers/LoadAllowlist itself. This keeps Build pure and
// trivially testable with hand-constructed slices — no real tailnet or
// YAML file needed to test the merge logic in isolation. The actual
// "go fetch both of these and call Build" orchestration belongs in
// cmd/tether/main.go, not here.
func Build(allowlist *Allowlist, peers []LivePeer) *Registry {
	peersByHostname := make(map[string]LivePeer, len(peers))
	for _, p := range peers {
		peersByHostname[p.Hostname] = p
	}

	nodes := make(map[string]*Node, len(allowlist.Nodes))
	for _, entry := range allowlist.Nodes {
		node := &Node{
			Hostname:  entry.Hostname,
			Role:      entry.Role,
			AgentPort: entry.AgentPort,
			RPCPort:   entry.RPCPort,
			Status:    StatusOffline, // default: not seen as a live peer
		}

		if peer, ok := peersByHostname[entry.Hostname]; ok {
			node.TailscaleIP = peer.IPv4Address
			node.LastSeen = peer.SeenAt
			if peer.Online {
				node.Status = StatusOnline
			}
			// If the peer exists but Online is false (e.g. Mathesis in our
			// test run), the node stays StatusOffline even though it did
			// technically match "matched the allowlist" and "currently
			// reachable" are different questions, and Status should
			// reflect the latter.
		}

		nodes[entry.Hostname] = node
	}

	return &Registry{nodes: nodes}
}

// Get returns the Node for a given hostname, and whether it was found.
// Returns nil, false for any hostname not in the allowlist — including
// tailnet peers that exist but were never declared as Tether nodes.
func (r *Registry) Get(hostname string) (*Node, bool) {
	node, ok := r.nodes[hostname]
	return node, ok
}

// All returns every Node currently in the registry, in no particular
// order. Callers that need a stable order (e.g. CLI output) should sort
// the result themselves rather than this function taking on that concern.
func (r *Registry) All() []*Node {
	all := make([]*Node, 0, len(r.nodes))
	for _, node := range r.nodes {
		all = append(all, node)
	}
	return all
}

// Online returns only the Nodes currently considered reachable
// (StatusOnline). This is the set most callers actually want when deciding
// which nodes to route inference work to — the difference from All()
// matters once the registry has more than a couple of nodes and some are
// routinely offline.
func (r *Registry) Online() []*Node {
	online := make([]*Node, 0, len(r.nodes))
	for _, node := range r.nodes {
		if node.Status == StatusOnline {
			online = append(online, node)
		}
	}
	return online
}