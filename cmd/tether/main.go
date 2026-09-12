// Command tether is the Tether Orchestrator. It discovers allowed Tailnet
// nodes, pairs with an Agent on first contact, then controls that Agent's
// local llama.cpp rpc-server through a pinned-mTLS command channel.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"tether/internal/agent"
	"tether/internal/certs"
	"tether/internal/pairing"
	"tether/internal/registry"
	"tether/internal/trust"
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
	fmt.Print("Select a node to pair with or control. Enter a number, or press Enter to exit: ")

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

	identity, err := certs.LoadOrCreate(orchestratorIdentityName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading orchestrator identity: %v\n", err)
		os.Exit(1)
	}

	addr := fmt.Sprintf("%s:%d", node.TailscaleIP, node.AgentPort)
	paired, err := isPaired(node.Hostname)
	if err != nil {
		fmt.Fprintf(os.Stderr, "checking trust for %q: %v\n", node.Hostname, err)
		fmt.Fprintln(os.Stderr, "Re-pair this Agent to replace its invalid or expired certificate pin.")
		os.Exit(1)
	}
	if !paired {
		if err := pair(stdin, identity, node, addr); err != nil {
			fmt.Fprintf(os.Stderr, "pairing failed: %v\n", err)
			os.Exit(1)
		}
	}

	tlsConfig, err := trust.PinnedTLSConfig(identity.TLSCertificate(), node.Hostname, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "building pinned mTLS configuration for %q: %v\n", node.Hostname, err)
		os.Exit(1)
	}
	if err := controlNode(stdin, os.Stdout, agent.NewClient(tlsConfig), node, addr); err != nil {
		fmt.Fprintf(os.Stderr, "controlling %q: %v\n", node.Hostname, err)
		os.Exit(1)
	}
}

// isPaired reports whether the Orchestrator has a currently usable pin for
// hostname. An expired pin is deliberately an error rather than permission to
// contact a peer whose identity can no longer be verified.
func isPaired(hostname string) (bool, error) {
	_, found, err := trust.Get(hostname)
	if err != nil {
		return false, err
	}
	return found, nil
}

func pair(stdin *bufio.Scanner, identity *certs.Identity, node *registry.Node, addr string) error {
	fmt.Printf("%s is not paired. Start tether-agent on that machine and enter its pairing code here.\n", node.Hostname)
	fmt.Printf("Pairing code for %s: ", node.Hostname)
	if !stdin.Scan() {
		if err := stdin.Err(); err != nil {
			return fmt.Errorf("reading pairing code: %w", err)
		}
		return fmt.Errorf("no pairing code entered")
	}
	code := strings.TrimSpace(stdin.Text())
	if code == "" {
		return fmt.Errorf("no pairing code entered")
	}

	fmt.Printf("Pairing with %s at %s...\n", node.Hostname, addr)
	result, err := pairing.NewClient(identity).Pair(addr, code)
	if err != nil {
		return err
	}
	if result.AgentHostname != node.Hostname {
		return fmt.Errorf("paired Agent identifies as %q, not selected node %q", result.AgentHostname, node.Hostname)
	}
	fmt.Printf("Paired successfully with %q.\n", result.AgentHostname)
	return nil
}

// commandClient is the subset of agent.Client used by the CLI. Keeping this
// small interface at the command-loop boundary lets the user-input behavior
// be tested without a network connection; agent.Client remains responsible
// for all HTTP and TLS behavior.
type commandClient interface {
	StartRPCServer(addr string, port int) (*agent.StatusResult, error)
	StopRPCServer(addr string) (*agent.StatusResult, error)
	GetStatus(addr string) (*agent.StatusResult, error)
}

// controlNode runs the selected node's interactive command loop. Requests are
// intentionally limited to the Agent's small command API. The Agent starts
// only its locally configured ggml-rpc-server; model loading happens later on
// the Orchestrator-side llama.cpp process.
func controlNode(stdin *bufio.Scanner, output io.Writer, client commandClient, node *registry.Node, addr string) error {
	fmt.Fprintf(output, "\nConnected to %s at %s.\n", node.Hostname, addr)
	for {
		fmt.Fprint(output, "Command [status, start, stop, quit]: ")
		if !stdin.Scan() {
			return stdin.Err()
		}

		switch strings.ToLower(strings.TrimSpace(stdin.Text())) {
		case "status":
			status, err := client.GetStatus(addr)
			printCommandResult(output, status, err)
		case "start":
			if err := startRPCServer(stdin, output, client, node, addr); err != nil {
				fmt.Fprintf(output, "Start failed: %v\n", err)
			}
		case "stop":
			status, err := client.StopRPCServer(addr)
			printCommandResult(output, status, err)
		case "quit", "exit", "":
			return nil
		default:
			fmt.Fprintln(output, "Unknown command. Choose status, start, stop, or quit.")
		}
	}
}

func startRPCServer(stdin *bufio.Scanner, output io.Writer, client commandClient, node *registry.Node, addr string) error {
	fmt.Fprintf(output, "RPC port [%d]: ", node.RPCPort)
	if !stdin.Scan() {
		if err := stdin.Err(); err != nil {
			return fmt.Errorf("reading RPC port: %w", err)
		}
		return fmt.Errorf("no RPC port entered")
	}
	port, err := parsePort(stdin.Text(), node.RPCPort)
	if err != nil {
		return err
	}

	status, err := client.StartRPCServer(addr, port)
	if err != nil {
		return fmt.Errorf("sending start command: %w", err)
	}
	printCommandResult(output, status, nil)
	return nil
}

func parsePort(input string, defaultPort int) (int, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultPort, nil
	}
	port, err := strconv.Atoi(input)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("RPC port must be a number between 1 and 65535")
	}
	return port, nil
}

func printCommandResult(output io.Writer, status *agent.StatusResult, err error) {
	if err != nil {
		fmt.Fprintf(output, "Command failed: %v\n", err)
		return
	}
	fmt.Fprintf(output, "Status: %s\n", status.Status)
	if status.LastError != "" {
		fmt.Fprintf(output, "Last error: %s\n", status.LastError)
	}
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
