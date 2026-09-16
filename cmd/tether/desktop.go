package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"

	"tether/internal/agent"
	"tether/internal/certs"
	"tether/internal/executil"
	"tether/internal/orchestratorconfig"
	"tether/internal/pairing"
	"tether/internal/registry"
	"tether/internal/trust"
	bootstrapassets "tether/scripts"

	"gopkg.in/yaml.v3"
)

// OrchestratorApp is the desktop-facing adapter for Tether's existing
// discovery, pairing and mTLS control path. It deliberately exposes a small
// set of actions rather than a general remote-command facility.
type OrchestratorApp struct {
	allowlistPath string
	configPath    string
	mu            sync.Mutex
	gateway       *exec.Cmd
	preparing     bool
	prepareDetail string
	prepareLog    string
	progressPath  string
}

const defaultAgentPort = 7420

type OrchestratorSnapshot struct {
	AllowlistPath      string        `json:"allowlistPath"`
	ContributeLocalGPU bool          `json:"contributeLocalGPU"`
	Backend            BackendState  `json:"backend"`
	Nodes              []DesktopNode `json:"nodes"`
	Gateway            GatewayState  `json:"gateway"`
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

// BackendState is the local llama.cpp prerequisite for serving models. It is
// separate from Agent state: an Orchestrator always needs an RPC-enabled
// llama-server, while CUDA is only built when this machine contributes GPU.
type BackendState struct {
	Ready     bool   `json:"ready"`
	Preparing bool   `json:"preparing"`
	Detail    string `json:"detail"`
	LogPath   string `json:"logPath"`
}

type TailnetCandidate struct {
	Hostname string `json:"hostname"`
	Address  string `json:"address"`
}

func NewOrchestratorApp(allowlistPath string) *OrchestratorApp {
	configPath, err := orchestratorconfig.DefaultPath()
	if err != nil {
		// Snapshot and StartGateway return a readable error if this unusual
		// platform failure persists; retaining an empty path avoids panicking
		// while Wails is starting.
		configPath = ""
	}
	return &OrchestratorApp{allowlistPath: allowlistPath, configPath: configPath}
}

func (a *OrchestratorApp) startup(context.Context) {
	// A packaged installation includes tether-api beside this executable. Start
	// it opportunistically so double-clicking Tether makes the standard local
	// OpenAI endpoint available without a separate terminal command. Any
	// missing inference prerequisite remains visible in the UI.
	go func() {
		config, err := a.orchestratorConfig()
		if err == nil && a.localBackendState(config).Ready {
			_ = a.StartGateway()
		}
	}()
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
	config, err := a.orchestratorConfig()
	if err != nil {
		return nil, err
	}
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
		AllowlistPath:      a.allowlistPath,
		ContributeLocalGPU: config.ContributeLocalGPU,
		Backend:            a.localBackendState(config),
		Nodes:              view,
		Gateway:            a.gatewayState(),
	}, nil
}

// SetLocalGPUContribution records whether this Orchestrator should use its
// own CUDA backend for placement. The gateway is restarted when Tether owns
// it, because a running llama-server worker cannot safely change placement.
func (a *OrchestratorApp) SetLocalGPUContribution(enabled bool) error {
	if a.configPath == "" {
		return fmt.Errorf("locating the local Orchestrator config is unavailable on this machine")
	}
	config, err := a.orchestratorConfig()
	if err != nil {
		return err
	}
	config.ContributeLocalGPU = enabled
	if err := orchestratorconfig.Save(a.configPath, config); err != nil {
		return err
	}

	a.mu.Lock()
	wasRunning := a.gateway != nil && a.gateway.Process != nil
	if wasRunning {
		_ = a.gateway.Process.Kill()
		a.gateway = nil
	}
	a.mu.Unlock()
	if wasRunning {
		if err := a.StartGateway(); err != nil {
			return fmt.Errorf("restarting the gateway with the new GPU setting: %w", err)
		}
	}
	return nil
}

// PrepareLocalBackend runs the reviewed, local Linux build. It creates only
// the user's llama.cpp checkout and Orchestrator preference; it never turns
// this control host into an Agent or changes Tailscale/firewall state.
func (a *OrchestratorApp) PrepareLocalBackend() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("automatic Orchestrator backend setup is currently available on Linux")
	}
	config, err := a.orchestratorConfig()
	if err != nil {
		return err
	}
	a.mu.Lock()
	if a.preparing {
		a.mu.Unlock()
		return fmt.Errorf("local backend setup is already running")
	}
	a.mu.Unlock()

	script, root, err := prepareLinuxOrchestratorProvisioner()
	if err != nil {
		return err
	}
	progressPath := filepath.Join(root, "orchestrator-setup-progress.json")
	logPath := filepath.Join(root, "orchestrator-setup.log")
	if err := os.WriteFile(logPath, []byte("Tether Orchestrator backend setup started. Full output will remain in this file.\n"), 0600); err != nil {
		return fmt.Errorf("creating Orchestrator setup log: %w", err)
	}
	if err := writeBackendProgress(progressPath, backendProgress{Step: "requirements", Detail: "Preparing the audited local backend setup."}); err != nil {
		return err
	}
	a.mu.Lock()
	a.preparing = true
	a.prepareDetail = "Preparing the local inference backend. Progress is saved in the setup log."
	a.prepareLog = logPath
	a.progressPath = progressPath
	a.mu.Unlock()
	go a.runLinuxOrchestratorProvisioner(script, progressPath, logPath, config.ContributeLocalGPU)
	return nil
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
	config, err := a.orchestratorConfig()
	if err != nil {
		return err
	}
	llamaServer, err := a.localLlamaServer(config)
	if err != nil {
		return err
	}
	arguments := []string{"--rpc", "auto", "--llama-server", llamaServer}
	if !config.ContributeLocalGPU {
		arguments = append(arguments, "--local-gpu=false")
	}
	command := exec.Command(path, arguments...)
	executil.HideWindow(command)
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

func (a *OrchestratorApp) orchestratorConfig() (orchestratorconfig.Config, error) {
	if a.configPath == "" {
		return orchestratorconfig.Config{}, fmt.Errorf("locating the local Orchestrator config is unavailable on this machine")
	}
	return orchestratorconfig.Load(a.configPath)
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

func (a *OrchestratorApp) localBackendState(config orchestratorconfig.Config) BackendState {
	a.mu.Lock()
	preparing, detail, logPath, progressPath := a.preparing, a.prepareDetail, a.prepareLog, a.progressPath
	a.mu.Unlock()
	if preparing {
		if progress := readBackendProgress(progressPath); progress.Detail != "" {
			detail = progress.Detail
		}
		return BackendState{Preparing: true, Detail: detail, LogPath: logPath}
	}
	server, err := a.localLlamaServer(config)
	if err != nil {
		if detail != "" {
			return BackendState{Detail: detail, LogPath: logPath}
		}
		return BackendState{Detail: err.Error(), LogPath: logPath}
	}
	if config.ContributeLocalGPU {
		return BackendState{Ready: true, Detail: "CUDA and RPC local inference backend is ready: " + server, LogPath: logPath}
	}
	return BackendState{Ready: true, Detail: "RPC control-only inference backend is ready: " + server, LogPath: logPath}
}

func (a *OrchestratorApp) localLlamaServer(config orchestratorconfig.Config) (string, error) {
	candidates := make([]string, 0, 2)
	if config.LlamaServerPath != "" && config.LlamaServerLocalGPU == config.ContributeLocalGPU {
		candidates = append(candidates, config.LlamaServerPath)
	}
	if config.ContributeLocalGPU {
		candidates = append(candidates, filepath.Join("llama.cpp", "build-rpc-cuda", "bin", executableName("llama-server")))
	} else {
		candidates = append(candidates, filepath.Join("llama.cpp", "build-rpc", "bin", executableName("llama-server")))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	mode := "RPC control-only"
	if config.ContributeLocalGPU {
		mode = "CUDA and RPC"
	}
	return "", fmt.Errorf("local %s inference backend is not prepared; choose Prepare local backend", mode)
}

func executableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

type backendProgress struct {
	Step   string `json:"step"`
	Detail string `json:"detail"`
}

func writeBackendProgress(path string, progress backendProgress) error {
	data, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func readBackendProgress(path string) backendProgress {
	data, err := os.ReadFile(path)
	if err != nil {
		return backendProgress{}
	}
	var progress backendProgress
	if json.Unmarshal(data, &progress) != nil {
		return backendProgress{}
	}
	return progress
}

func prepareLinuxOrchestratorProvisioner() (string, string, error) {
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return "", "", fmt.Errorf("locating local setup directory: %w", err)
	}
	root := filepath.Join(cacheRoot, "tether")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", "", fmt.Errorf("creating local setup directory: %w", err)
	}
	script := filepath.Join(root, "bootstrap-linux-orchestrator.sh")
	if err := os.WriteFile(script, bootstrapassets.LinuxOrchestratorBootstrap, 0700); err != nil {
		return "", "", fmt.Errorf("writing local Orchestrator setup script: %w", err)
	}
	return script, root, nil
}

func (a *OrchestratorApp) runLinuxOrchestratorProvisioner(script, progressPath, logPath string, contributeGPU bool) {
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0600)
	if err == nil {
		defer logFile.Close()
		arguments := []string{script, "--install-missing", "--progress-path", progressPath}
		if contributeGPU {
			arguments = append(arguments, "--local-gpu")
		}
		command := exec.Command("bash", arguments...)
		command.Stdout, command.Stderr = logFile, logFile
		err = command.Run()
	}
	a.mu.Lock()
	a.preparing = false
	if err != nil {
		progress := readBackendProgress(progressPath)
		if progress.Detail != "" {
			a.prepareDetail = "Local backend setup stopped: " + progress.Detail
		} else {
			a.prepareDetail = "Local backend setup did not complete: " + err.Error()
		}
		a.mu.Unlock()
		return
	}
	a.prepareDetail = "Local inference backend is ready."
	a.mu.Unlock()

	config, configErr := a.orchestratorConfig()
	if configErr != nil {
		return
	}
	config.LlamaServerPath = defaultLinuxLlamaServerPath(contributeGPU)
	config.LlamaServerLocalGPU = contributeGPU
	if configErr = orchestratorconfig.Save(a.configPath, config); configErr == nil {
		_ = a.StartGateway()
	}
}

func defaultLinuxLlamaServerPath(contributeGPU bool) string {
	dataRoot := os.Getenv("XDG_DATA_HOME")
	if dataRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dataRoot = filepath.Join(home, ".local", "share")
	}
	build := "build-rpc"
	if contributeGPU {
		build = "build-rpc-cuda"
	}
	return filepath.Join(dataRoot, "tether", "llama.cpp", build, "bin", "llama-server")
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
