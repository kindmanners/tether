package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"

	"tether/internal/agent"
	"tether/internal/certs"
	"tether/internal/pairing"
	"tether/internal/registry"
	"tether/internal/trust"

	"gopkg.in/yaml.v3"
)

// OrchestratorApp is the desktop-facing adapter for Tether's existing
// discovery, pairing and mTLS control path. It deliberately exposes a small
// set of actions rather than a general remote-command facility.
type OrchestratorApp struct {
	allowlistPath string
	mu            sync.Mutex
	gateway       *exec.Cmd
}

const defaultAgentPort = 7420

type OrchestratorSnapshot struct {
	AllowlistPath string        `json:"allowlistPath"`
	Nodes         []DesktopNode `json:"nodes"`
	Gateway       GatewayState  `json:"gateway"`
}

type DesktopNode struct {
	Hostname    string `json:"hostname"`
	Address     string `json:"address"`
	Tailnet     string `json:"tailnet"`
	Paired      bool   `json:"paired"`
	AgentStatus string `json:"agentStatus"`
	Detail      string `json:"detail"`
	RPCPort     int    `json:"rpcPort"`
}

type GatewayState struct {
	Running   bool   `json:"running"`
	Available bool   `json:"available"`
	Detail    string `json:"detail"`
	Endpoint  string `json:"endpoint"`
}

type TailnetCandidate struct {
	Hostname string `json:"hostname"`
	Address  string `json:"address"`
}

func NewOrchestratorApp(allowlistPath string) *OrchestratorApp {
	return &OrchestratorApp{allowlistPath: allowlistPath}
}

func (a *OrchestratorApp) startup(context.Context) {
	// A packaged installation includes tether-api beside this executable. Start
	// it opportunistically so double-clicking Tether makes the standard local
	// OpenAI endpoint available without a separate terminal command. Any
	// missing inference prerequisite remains visible in the UI.
	go func() { _ = a.StartGateway() }()
}

func (a *OrchestratorApp) shutdown(context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.gateway != nil && a.gateway.Process != nil {
		_ = a.gateway.Process.Kill()
	}
}

// Snapshot refreshes the live Tailscale registry and, for paired online
// nodes, reads the Agent's current RPC process state over pinned mTLS.
func (a *OrchestratorApp) Snapshot() (*OrchestratorSnapshot, error) {
	allowlist, err := registry.LoadAllowlist(a.allowlistPath)
	if err != nil {
		return nil, fmt.Errorf("loading node allowlist: %w", err)
	}
	peers, err := registry.QueryTailscalePeers()
	if err != nil {
		return nil, fmt.Errorf("checking Tailscale: %w", err)
	}
	identity, err := certs.LoadOrCreate(orchestratorIdentityName)
	if err != nil {
		return nil, fmt.Errorf("loading local identity: %w", err)
	}

	nodes := registry.Build(allowlist, peers).All()
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Hostname < nodes[j].Hostname })
	view := make([]DesktopNode, 0, len(nodes))
	for _, node := range nodes {
		item := DesktopNode{
			Hostname: node.Hostname,
			Tailnet:  node.Status.String(),
			Address:  node.TailscaleIP,
			RPCPort:  node.RPCPort,
		}
		_, item.Paired, err = trust.Get(node.Hostname)
		if err != nil {
			item.Detail = "Pairing needs attention: " + err.Error()
			view = append(view, item)
			continue
		}
		if node.Status != registry.StatusOnline {
			item.Detail = "This machine is not currently reachable through Tailscale."
			view = append(view, item)
			continue
		}
		if !item.Paired {
			item.Detail = "Open Tether Agent on this machine, then enter its one-time code here."
			view = append(view, item)
			continue
		}
		tlsConfig, tlsErr := trust.PinnedTLSConfig(identity.TLSCertificate(), node.Hostname, false)
		if tlsErr != nil {
			item.Detail = "Pairing needs attention: " + tlsErr.Error()
			view = append(view, item)
			continue
		}
		status, statusErr := agent.NewClient(tlsConfig).GetStatus(agentAddress(node))
		if statusErr != nil {
			item.AgentStatus = "Unreachable"
			item.Detail = statusErr.Error()
		} else {
			item.AgentStatus = status.Status
			item.Detail = status.LastError
		}
		view = append(view, item)
	}

	return &OrchestratorSnapshot{
		AllowlistPath: a.allowlistPath,
		Nodes:         view,
		Gateway:       a.gatewayState(),
	}, nil
}

// Pair validates the selected live node before making the one-time pairing
// request. The Agent certificate identity must agree with the selected host.
func (a *OrchestratorApp) Pair(hostname, code string) error {
	node, identity, err := a.onlineNode(hostname)
	if err != nil {
		return err
	}
	if code == "" {
		return fmt.Errorf("enter the one-time pairing code displayed by Tether Agent")
	}
	result, err := pairing.NewClient(identity).Pair(agentAddress(node), code)
	if err != nil {
		return fmt.Errorf("pairing %s: %w", hostname, err)
	}
	if result.AgentHostname != node.Hostname {
		return fmt.Errorf("the Agent identified as %q, not selected node %q", result.AgentHostname, node.Hostname)
	}
	return nil
}

// TailnetCandidates lists online peers that have not yet been granted an
// allowlist entry. They are candidates, not automatically trusted nodes: the
// user must deliberately select one before it can expose an Agent endpoint.
func (a *OrchestratorApp) TailnetCandidates() ([]TailnetCandidate, error) {
	allowlist, err := registry.LoadAllowlist(a.allowlistPath)
	if err != nil {
		return nil, fmt.Errorf("loading node allowlist: %w", err)
	}
	known := make(map[string]bool, len(allowlist.Nodes))
	for _, node := range allowlist.Nodes {
		known[node.Hostname] = true
	}
	peers, err := registry.QueryTailscalePeers()
	if err != nil {
		return nil, fmt.Errorf("checking Tailscale: %w", err)
	}
	selfHostname, _ := registry.SelfHostname()
	candidates := make([]TailnetCandidate, 0)
	for _, peer := range peers {
		if peer.Online && peer.Hostname != "" && peer.Hostname != selfHostname && peer.IPv4Address != "" && !known[peer.Hostname] {
			candidates = append(candidates, TailnetCandidate{Hostname: peer.Hostname, Address: peer.IPv4Address})
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Hostname < candidates[j].Hostname })
	return candidates, nil
}

// AddNode records a selected live Tailnet peer in the Orchestrator's local
// allowlist. This preserves the two-layer model: merely joining a Tailnet
// never grants Tether control, but no YAML editing is needed for onboarding.
func (a *OrchestratorApp) AddNode(hostname string, rpcPort int) error {
	if hostname == "" {
		return fmt.Errorf("select a Tailnet machine")
	}
	if rpcPort == 0 {
		rpcPort = 50053
	}
	if rpcPort < 1 || rpcPort > 65535 || rpcPort == defaultAgentPort {
		return fmt.Errorf("RPC port must be between 1 and 65535 and differ from the Agent port")
	}
	allowlist, err := registry.LoadAllowlist(a.allowlistPath)
	if err != nil {
		return fmt.Errorf("loading node allowlist: %w", err)
	}
	for _, node := range allowlist.Nodes {
		if node.Hostname == hostname {
			return fmt.Errorf("%s is already in the node allowlist", hostname)
		}
	}
	candidates, err := a.TailnetCandidates()
	if err != nil {
		return err
	}
	found := false
	for _, candidate := range candidates {
		if candidate.Hostname == hostname {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("%s is not an online unallowlisted Tailnet peer", hostname)
	}
	allowlist.Nodes = append(allowlist.Nodes, registry.AllowlistEntry{Hostname: hostname, Role: "rpc-node", AgentPort: defaultAgentPort, RPCPort: rpcPort})
	data, err := yaml.Marshal(allowlist)
	if err != nil {
		return fmt.Errorf("encoding node allowlist: %w", err)
	}
	if err := writeFileAtomically(a.allowlistPath, data, 0600); err != nil {
		return fmt.Errorf("writing node allowlist: %w", err)
	}
	return nil
}

func (a *OrchestratorApp) StartRPC(hostname string) (*DesktopNode, error) {
	return a.command(hostname, func(client *agent.Client, address string, port int) (*agent.StatusResult, error) {
		return client.StartRPCServer(address, port)
	})
}

func (a *OrchestratorApp) StopRPC(hostname string) (*DesktopNode, error) {
	return a.command(hostname, func(client *agent.Client, address string, _ int) (*agent.StatusResult, error) {
		return client.StopRPCServer(address)
	})
}

func (a *OrchestratorApp) command(hostname string, command func(*agent.Client, string, int) (*agent.StatusResult, error)) (*DesktopNode, error) {
	node, identity, err := a.onlineNode(hostname)
	if err != nil {
		return nil, err
	}
	tlsConfig, err := trust.PinnedTLSConfig(identity.TLSCertificate(), node.Hostname, false)
	if err != nil {
		return nil, fmt.Errorf("%s is not paired: %w", hostname, err)
	}
	status, err := command(agent.NewClient(tlsConfig), agentAddress(node), node.RPCPort)
	if err != nil {
		return nil, fmt.Errorf("sending command to %s: %w", hostname, err)
	}
	return &DesktopNode{Hostname: node.Hostname, Address: node.TailscaleIP, Tailnet: node.Status.String(), Paired: true, AgentStatus: status.Status, Detail: status.LastError, RPCPort: node.RPCPort}, nil
}

// StartGateway starts the sibling tether-api executable. Keeping the gateway
// as its own process retains its isolated model-worker lifecycle while making
// the standard local OpenAI endpoint available from the desktop app.
func (a *OrchestratorApp) StartGateway() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.gateway != nil && a.gateway.Process != nil {
		return fmt.Errorf("the local gateway is already running")
	}
	path, err := siblingExecutable("tether-api")
	if err != nil {
		return err
	}
	command := exec.Command(path, "--rpc", "auto")
	if err := command.Start(); err != nil {
		return fmt.Errorf("starting local gateway: %w", err)
	}
	a.gateway = command
	go func() {
		_ = command.Wait()
		a.mu.Lock()
		if a.gateway == command {
			a.gateway = nil
		}
		a.mu.Unlock()
	}()
	return nil
}

func (a *OrchestratorApp) gatewayState() GatewayState {
	a.mu.Lock()
	defer a.mu.Unlock()
	path, err := siblingExecutable("tether-api")
	state := GatewayState{Endpoint: "http://127.0.0.1:11435/v1"}
	if err != nil {
		state.Detail = err.Error()
		return state
	}
	state.Available = true
	state.Running = a.gateway != nil && a.gateway.Process != nil
	if state.Running {
		state.Detail = "The local OpenAI-compatible gateway is running."
	} else {
		state.Detail = "Ready to start " + filepath.Base(path) + "."
	}
	return state
}

func (a *OrchestratorApp) onlineNode(hostname string) (*registry.Node, *certs.Identity, error) {
	allowlist, err := registry.LoadAllowlist(a.allowlistPath)
	if err != nil {
		return nil, nil, fmt.Errorf("loading node allowlist: %w", err)
	}
	peers, err := registry.QueryTailscalePeers()
	if err != nil {
		return nil, nil, fmt.Errorf("checking Tailscale: %w", err)
	}
	node, found := registry.Build(allowlist, peers).Get(hostname)
	if !found {
		return nil, nil, fmt.Errorf("%q is not in the node allowlist", hostname)
	}
	if node.Status != registry.StatusOnline {
		return nil, nil, fmt.Errorf("%s is not online through Tailscale", hostname)
	}
	identity, err := certs.LoadOrCreate(orchestratorIdentityName)
	if err != nil {
		return nil, nil, fmt.Errorf("loading local identity: %w", err)
	}
	return node, identity, nil
}

func agentAddress(node *registry.Node) string {
	return fmt.Sprintf("%s:%d", node.TailscaleIP, node.AgentPort)
}

func siblingExecutable(name string) (string, error) {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	current, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating this executable: %w", err)
	}
	path := filepath.Join(filepath.Dir(current), name)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("%s must be installed beside Tether to start the local gateway", name)
	}
	return path, nil
}

func writeFileAtomically(path string, data []byte, permissions os.FileMode) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".tether-allowlist-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(permissions); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
