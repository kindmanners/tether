// Command tether-agent is the Tether Agent — a lightweight daemon that
// runs on every machine contributing GPU to the cluster (design doc §3).
// It listens for commands from the Orchestrator over mTLS and starts/stops
// the local llama.cpp rpc-server process. It does not make scheduling
// decisions on its own.
//
// This binary is deliberately separate from cmd/tether (the Orchestrator):
// they run on different machines and have different responsibilities. See
// design doc §3 for the full architecture split.
//
// CURRENT SCOPE: pairing only. Running this binary starts a single
// pairing session (design doc §6.2) and exits once it completes or its
// window expires. There is no long-running daemon mode yet, and no
// process-control (starting/stopping llama.cpp) yet — those come in a
// later build step (design doc §5, step 3), once there's something for a
// persistent Agent to actually manage. Running this repeatedly re-pairs
// (or re-confirms pairing with) the Orchestrator each time; per design
// doc §6.1 step 4, re-pairing is the intended way to rotate a node's
// identity if it's ever needed, not a special separate flow.
package main

import (
	"fmt"
	"os"

	"tether/internal/certs"
	"tether/internal/pairing"
	"tether/internal/registry"
)

// pairingPort is the port the Agent's pairing server listens on. Matches
// the agent_port convention already used in node_allowlist.yaml entries
// (design doc §4.4) — the same port is reused for pairing now and for
// the Agent's general command API later, rather than introducing a
// second port just for the pairing phase.
const pairingPort = 7420

func main() {
	hostname, err := registry.SelfHostname()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tether-agent: could not determine this machine's Tailscale hostname: %v\n", err)
		fmt.Fprintln(os.Stderr, "tether-agent: is Tailscale installed and running on this machine?")
		os.Exit(1)
	}

	// Identity is keyed by the Tailscale hostname (not os.Hostname()) so
	// this Agent's identity name always matches what the Orchestrator's
	// registry calls the same machine — see registry.SelfHostname's doc
	// comment for why that consistency matters (a real bug we found:
	// Windows machine names can differ from Tailscale-derived names).
	identity, err := certs.LoadOrCreate(hostname)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tether-agent: could not load or create identity for %q: %v\n", hostname, err)
		os.Exit(1)
	}

	server, err := pairing.NewServer(identity, pairing.Window)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tether-agent: could not start pairing session: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("=========================================")
	fmt.Printf("  Tether Agent — %s\n", hostname)
	fmt.Println("=========================================")
	fmt.Println()
	fmt.Println("  Ready to pair with the Orchestrator.")
	fmt.Println()
	fmt.Printf("  Pairing code:  %s\n", server.Code())
	fmt.Println()
	fmt.Printf("  Enter this code in the Orchestrator within %s.\n", pairing.Window)
	fmt.Println("  This code is single-use and shown only here — it is never logged.")
	fmt.Println()

	addr := fmt.Sprintf(":%d", pairingPort)
	if err := server.Start(addr); err != nil {
		fmt.Println()
		fmt.Printf("  Pairing did not complete: %v\n", err)
		fmt.Println("  Run tether-agent again to try once more.")
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("  Pairing succeeded. The Orchestrator can now reach this node.")
}