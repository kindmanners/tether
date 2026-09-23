package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

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
	allowlistPath   string
	configPath      string
	mu              sync.Mutex
	gateway         *exec.Cmd
	gatewayStarting bool
	gatewayError    string
	gatewayLog      string
	preparing       bool
	prepareDetail   string
	prepareLog      string
	progressPath    string
	monitorCancel   context.CancelFunc
	usageHistory    map[string][]NodeUsageSample
	modelDownload   ModelDownload
	downloadCancel  context.CancelFunc
	gatewayDone     chan struct{}
	modelGateway    modelGatewayClient
}

const (
	defaultAgentPort  = 7420
	modelGatewayURL   = "http://127.0.0.1:11435"
	gptOSS20BFilename = "gpt-oss-20b-MXFP4.gguf"
	// Pin an immutable Hugging Face revision. The size and SHA-256 are the
	// publisher's LFS metadata for this exact artifact, not a mutable `main`.
	gptOSS20BDownloadURL          = "https://huggingface.co/ggml-org/gpt-oss-20b-GGUF/resolve/b97cbb20d1995efd41dce8c4dd1ddf86e8db375b/gpt-oss-20b-MXFP4.gguf"
	gptOSS20BExpectedSize   int64 = 12_109_566_624
	gptOSS20BSHA256               = "27cd6c432c7672cb812a92f611cf3ba7bbc35928262bb1e1253ff4ee6ae35901"
	nodeRefreshBudget             = 12 * time.Second
	maxConcurrentNodeProbes       = 4
)

type OrchestratorSnapshot struct {
	AllowlistPath      string        `json:"allowlistPath"`
	ContributeLocalGPU bool          `json:"contributeLocalGPU"`
	Backend            BackendState  `json:"backend"`
	Nodes              []DesktopNode `json:"nodes"`
	Gateway            GatewayState  `json:"gateway"`
}

type DesktopNode struct {
	Hostname    string            `json:"hostname"`
	Address     string            `json:"address"`
	Tailnet     string            `json:"tailnet"`
	Paired      bool              `json:"paired"`
	AgentStatus string            `json:"agentStatus"`
	Detail      string            `json:"detail"`
	RPCPort     int               `json:"rpcPort"`
	PingMS      int64             `json:"pingMs"`
	PingAt      string            `json:"pingAt"`
	GPUHistory  []NodeUsageSample `json:"gpuHistory,omitempty"`
}

// NodeUsageSample is the aggregate of the GPUs reported by one Agent at one
// point in time. The desktop UI uses the recent samples for its compact
// per-node activity chart; it is intentionally process-local, not a durable
// monitoring database.
type NodeUsageSample struct {
	ObservedAt  string `json:"observedAt"`
	GPUPercent  int    `json:"gpuPercent"`
	VRAMPercent int    `json:"vramPercent"`
}

type GatewayState struct {
	Running   bool   `json:"running"`
	Available bool   `json:"available"`
	Detail    string `json:"detail"`
	Endpoint  string `json:"endpoint"`
}

// ModelLibrary is the Orchestrator's local GGUF inventory plus the gateway's
// live worker state. Model files remain local to the control host; only their
// llama.cpp layers are placed across paired GPU nodes.
type ModelLibrary struct {
	Directory string         `json:"directory"`
	Models    []DesktopModel `json:"models"`
	Download  ModelDownload  `json:"download"`
}

type DesktopModel struct {
	ID        string   `json:"id"`
	Filename  string   `json:"filename"`
	Path      string   `json:"path"`
	SizeBytes int64    `json:"sizeBytes"`
	State     string   `json:"state"`
	Nodes     []string `json:"nodes,omitempty"`
	UpdatedAt string   `json:"updatedAt,omitempty"`
	Detail    string   `json:"detail,omitempty"`
}

type ModelDownload struct {
	ID         string `json:"id"`
	Filename   string `json:"filename"`
	State      string `json:"state"`
	Bytes      int64  `json:"bytes"`
	TotalBytes int64  `json:"totalBytes"`
	Detail     string `json:"detail,omitempty"`
	Source     string `json:"source,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
	CanCancel  bool   `json:"canCancel,omitempty"`
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
	return &OrchestratorApp{
		allowlistPath: allowlistPath,
		configPath:    configPath,
		usageHistory:  make(map[string][]NodeUsageSample),
		modelGateway:  newLocalModelGatewayClient(),
	}
}

func (a *OrchestratorApp) startup(context.Context) {
	// A packaged installation includes tether-api beside this executable. Start
	// it opportunistically so double-clicking Tether makes the standard local
	// OpenAI endpoint available without a separate terminal command. Any
	// missing inference prerequisite remains visible in the UI.
	go func() {
		config, err := a.orchestratorConfig()
		if err == nil && a.localBackendState(config).Ready {
			if err := a.StartGateway(); err != nil {
				a.mu.Lock()
				a.gatewayError = err.Error()
				a.mu.Unlock()
				log.Printf("orchestrator: local gateway did not start: %v", err)
			}
		}
	}()

	monitorCtx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.monitorCancel = cancel
	a.mu.Unlock()
	go a.monitorPairedAgents(monitorCtx)
}

func (a *OrchestratorApp) shutdown(context.Context) {
	a.mu.Lock()
	if a.monitorCancel != nil {
		a.monitorCancel()
		a.monitorCancel = nil
	}
	a.mu.Unlock()
	if err := a.stopGateway(); err != nil {
		log.Printf("orchestrator: stopping gateway: %v", err)
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

	selfHostname, _ := registry.SelfHostname()
	nodes := registry.Build(allowlist, peers).All()
	if selfHostname != "" {
		filtered := nodes[:0]
		for _, node := range nodes {
			if node.Hostname != selfHostname {
				filtered = append(filtered, node)
			}
		}
		nodes = filtered
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Hostname < nodes[j].Hostname })
	view := make([]DesktopNode, len(nodes))
	probeIndexes := make([]int, 0, len(nodes))
	for index, node := range nodes {
		item := DesktopNode{
			Hostname: node.Hostname,
			Tailnet:  node.Status.String(),
			Address:  node.TailscaleIP,
			RPCPort:  node.RPCPort,
		}
		_, item.Paired, err = trust.Get(node.Hostname)
		if err != nil {
			item.Detail = "Pairing needs attention: " + err.Error()
			view[index] = item
			continue
		}
		if node.Status != registry.StatusOnline {
			item.Detail = "This machine is not currently reachable through Tailscale."
			view[index] = item
			continue
		}
		if !item.Paired {
			item.Detail = "Open Tether Agent on this machine, then enter its one-time code here."
			view[index] = item
			continue
		}
		if _, tlsErr := trust.PinnedTLSConfig(identity.TLSCertificate(), node.Hostname, false); tlsErr != nil {
			item.Detail = "Pairing needs attention: " + tlsErr.Error()
			view[index] = item
			continue
		}
		item.AgentStatus = "Checking"
		item.Detail = "Checking pinned Agent status…"
		item.GPUHistory = a.nodeUsageHistory(node.Hostname)
		view[index] = item
		probeIndexes = append(probeIndexes, index)
	}
	a.probeNodes(view, nodes, probeIndexes, identity)

	return &OrchestratorSnapshot{
		AllowlistPath:      a.allowlistPath,
		ContributeLocalGPU: config.ContributeLocalGPU,
		Backend:            a.localBackendState(config),
		Nodes:              view,
		Gateway:            a.gatewayState(),
	}, nil
}

// probeNodes checks paired Agents in a bounded pool. One refresh has a fixed
// budget, so unavailable nodes cannot add ten seconds each to the dashboard.
func (a *OrchestratorApp) probeNodes(view []DesktopNode, nodes []*registry.Node, indexes []int, identity *certs.Identity) {
	ctx, cancel := context.WithTimeout(context.Background(), nodeRefreshBudget)
	defer cancel()
	jobs := make(chan int)
	var workers sync.WaitGroup
	count := min(maxConcurrentNodeProbes, len(indexes))
	for range count {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case index, ok := <-jobs:
					if !ok {
						return
					}
					node := nodes[index]
					item := view[index]
					tlsConfig, err := trust.PinnedTLSConfig(identity.TLSCertificate(), node.Hostname, false)
					if err != nil {
						item.AgentStatus = "Unreachable"
						item.Detail = "Pairing needs attention: " + err.Error()
						view[index] = item
						continue
					}
					checkedAt, startedAt := time.Now().UTC(), time.Now()
					client := agent.NewClient(tlsConfig)
					status, statusErr := client.GetStatusContext(ctx, agentAddress(node))
					item.PingMS = time.Since(startedAt).Milliseconds()
					item.PingAt = checkedAt.Format(time.RFC3339)
					if statusErr != nil {
						item.AgentStatus = "Unreachable"
						if ctx.Err() != nil {
							item.Detail = "Refresh budget expired; try again."
						} else {
							item.Detail = statusErr.Error()
						}
						item.GPUHistory = a.nodeUsageHistory(node.Hostname)
						log.Printf("orchestrator: agent heartbeat host=%q timestamp=%s latency_ms=%d error=%q", node.Hostname, item.PingAt, item.PingMS, statusErr)
					} else {
						item.AgentStatus, item.Detail = status.Status, status.LastError
						if capabilities, capabilitiesErr := client.GetCapabilitiesContext(ctx, agentAddress(node)); capabilitiesErr == nil {
							item.GPUHistory = a.recordNodeUsage(node.Hostname, checkedAt, capabilities.GPUs)
						} else {
							item.GPUHistory = a.nodeUsageHistory(node.Hostname)
						}
						log.Printf("orchestrator: agent heartbeat host=%q timestamp=%s latency_ms=%d status=%q", node.Hostname, item.PingAt, item.PingMS, status.Status)
					}
					view[index] = item
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, index := range indexes {
			select {
			case jobs <- index:
			case <-ctx.Done():
				return
			}
		}
	}()
	workers.Wait()
	if ctx.Err() != nil {
		for _, index := range indexes {
			if view[index].AgentStatus == "Checking" {
				view[index].AgentStatus = "Stale"
				view[index].Detail = "Refresh budget expired; last successful sample is retained."
			}
		}
	}
}

const usageHistoryWindow = time.Minute

func (a *OrchestratorApp) recordNodeUsage(hostname string, observedAt time.Time, gpus []agent.GPUCapability) []NodeUsageSample {
	sample, ok := aggregateNodeUsage(observedAt, gpus)
	if !ok {
		return a.nodeUsageHistory(hostname)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.usageHistory == nil {
		a.usageHistory = make(map[string][]NodeUsageSample)
	}
	history := append(a.usageHistory[hostname], sample)
	cutoff := observedAt.Add(-usageHistoryWindow)
	first := 0
	for first < len(history) {
		at, err := time.Parse(time.RFC3339, history[first].ObservedAt)
		if err == nil && !at.Before(cutoff) {
			break
		}
		first++
	}
	history = append([]NodeUsageSample(nil), history[first:]...)
	a.usageHistory[hostname] = history
	return append([]NodeUsageSample(nil), history...)
}

func (a *OrchestratorApp) nodeUsageHistory(hostname string) []NodeUsageSample {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]NodeUsageSample(nil), a.usageHistory[hostname]...)
}

func aggregateNodeUsage(observedAt time.Time, gpus []agent.GPUCapability) (NodeUsageSample, bool) {
	if len(gpus) == 0 {
		return NodeUsageSample{}, false
	}

	var totalVRAM, usedVRAM, utilization int64
	validGPUs := 0
	for _, gpu := range gpus {
		if gpu.VRAMBytes <= 0 {
			continue
		}
		validGPUs++
		totalVRAM += gpu.VRAMBytes
		freeVRAM := min(max(gpu.VRAMFreeBytes, 0), gpu.VRAMBytes)
		usedVRAM += gpu.VRAMBytes - freeVRAM
		utilization += int64(min(max(gpu.UtilizationPercent, 0), 100))
	}
	if totalVRAM == 0 {
		return NodeUsageSample{}, false
	}

	return NodeUsageSample{
		ObservedAt:  observedAt.UTC().Format(time.RFC3339),
		GPUPercent:  int(utilization / int64(validGPUs)),
		VRAMPercent: int(usedVRAM * 100 / totalVRAM),
	}, true
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
	a.mu.Unlock()
	if wasRunning {
		if err := a.stopGateway(); err != nil {
			return fmt.Errorf("stopping gateway workers before changing the GPU setting: %w", err)
		}
		if err := a.StartGateway(); err != nil {
			return fmt.Errorf("the GPU setting was saved, but restarting the gateway failed; use Start gateway to retry: %w", err)
		}
	}
	return nil
}

// PrepareLocalBackend runs the reviewed, platform-local build. It creates only
// the user's llama.cpp checkout and Orchestrator preference; it never turns
// this control host into an Agent or changes Tailscale/firewall state.
func (a *OrchestratorApp) PrepareLocalBackend() error {
	if runtime.GOOS == "windows" {
		return a.prepareWindowsLocalBackend()
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("automatic Orchestrator backend setup is currently available on Linux and Windows")
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
	// Give the operator an immediate, clear answer before attempting a start.
	// The Agent independently enforces this too, so a concurrent request cannot
	// launch a second RPC server between this check and the start command.
	current, err := a.nodeStatus(hostname)
	if err != nil {
		return nil, err
	}
	if current.AgentStatus == "Running" {
		return nil, fmt.Errorf("%s already has an RPC server running", hostname)
	}
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

func (a *OrchestratorApp) nodeStatus(hostname string) (*DesktopNode, error) {
	return a.command(hostname, func(client *agent.Client, address string, _ int) (*agent.StatusResult, error) {
		return client.GetStatus(address)
	})
}

// monitorPairedAgents keeps the control plane aware of paired Agents even
// while the dashboard is not open. Each check travels over pinned mTLS and is
// intentionally limited to status; it cannot start or stop a remote process.
func (a *OrchestratorApp) monitorPairedAgents(ctx context.Context) {
	check := func() {
		if _, err := a.Snapshot(); err != nil {
			log.Printf("orchestrator: five-minute agent heartbeat failed: %v", err)
		}
	}
	check()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}

// StartGateway starts the sibling tether-api executable. Keeping the gateway
// as its own process retains its isolated model-worker lifecycle while making
// the standard local OpenAI endpoint available from the desktop app.
func (a *OrchestratorApp) StartGateway() error {
	a.mu.Lock()
	if a.gatewayStarting || (a.gateway != nil && a.gateway.Process != nil) {
		a.mu.Unlock()
		return fmt.Errorf("the local gateway is already running")
	}
	a.gatewayStarting = true
	a.gatewayError = ""
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.gatewayStarting = false
		a.mu.Unlock()
	}()
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
	allowlistPath, err := filepath.Abs(a.allowlistPath)
	if err != nil {
		return fmt.Errorf("resolving node allowlist path: %w", err)
	}
	modelsDirectory, err := defaultModelsDirectory()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(modelsDirectory, 0700); err != nil {
		return fmt.Errorf("creating model library: %w", err)
	}
	arguments := []string{"--rpc", "auto", "--llama-server", llamaServer, "--allowlist", allowlistPath, "--models-dir", modelsDirectory}
	if !config.ContributeLocalGPU {
		arguments = append(arguments, "--local-gpu=false")
	}
	command := exec.Command(path, arguments...)
	command.Env = localBackendEnvironment(config)
	executil.IsolateProcessTree(command)
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return fmt.Errorf("locating gateway log directory: %w", err)
	}
	logDirectory := filepath.Join(cacheRoot, "tether")
	if err := os.MkdirAll(logDirectory, 0700); err != nil {
		return fmt.Errorf("creating gateway log directory: %w", err)
	}
	logPath := filepath.Join(logDirectory, "gateway.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("opening gateway log: %w", err)
	}
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("starting local gateway: %w", err)
	}
	_ = logFile.Close()
	a.mu.Lock()
	a.gateway = command
	a.gatewayLog = logPath
	done := make(chan struct{})
	a.gatewayDone = done
	a.mu.Unlock()
	go func() {
		waitErr := command.Wait()
		a.mu.Lock()
		if a.gateway == command {
			a.gateway = nil
			a.gatewayDone = nil
			if waitErr != nil {
				a.gatewayError = fmt.Sprintf("The local gateway exited: %v. See %s", waitErr, logPath)
			}
		}
		a.mu.Unlock()
		close(done)
	}()

	deadline := time.Now().Add(20 * time.Second)
	client := &http.Client{Timeout: time.Second}
	for time.Now().Before(deadline) {
		select {
		case <-done:
			a.mu.Lock()
			detail := a.gatewayError
			a.mu.Unlock()
			if detail == "" {
				detail = "The local gateway exited before it became ready. See " + logPath
			}
			return fmt.Errorf("%s", detail)
		default:
		}
		response, requestErr := client.Get(modelGatewayURL + "/v1/models")
		if requestErr == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				a.mu.Lock()
				a.gatewayError = ""
				a.mu.Unlock()
				return nil
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	detail := fmt.Sprintf("the local gateway is still starting after 20 seconds; see %s", logPath)
	a.mu.Lock()
	a.gatewayError = detail
	a.mu.Unlock()
	return fmt.Errorf("%s", detail)
}

// stopGateway asks tether-api to drain its model workers, then waits for the
// gateway to exit. A forced process-tree termination is a bounded fallback so
// a wedged gateway cannot strand llama-server workers or their GPU memory.
func (a *OrchestratorApp) stopGateway() error {
	a.mu.Lock()
	command, done := a.gateway, a.gatewayDone
	a.mu.Unlock()
	if command == nil || command.Process == nil || done == nil {
		return nil
	}

	request, err := http.NewRequest(http.MethodPost, modelGatewayURL+"/api/v1/internal/shutdown", nil)
	if err == nil {
		response, requestErr := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		if requestErr == nil {
			response.Body.Close()
			err = nil
		} else {
			err = requestErr
		}
	}
	select {
	case <-done:
		return nil
	case <-time.After(12 * time.Second):
		if killErr := executil.KillProcessTree(command); killErr != nil {
			return fmt.Errorf("graceful shutdown timed out (%v); killing gateway process tree: %w", err, killErr)
		}
	}
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("gateway process tree did not exit after forced termination")
	}
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
	if a.gatewayError != "" {
		state.Detail = a.gatewayError
	} else if state.Running {
		if a.gatewayStarting {
			state.Detail = "The local OpenAI-compatible gateway is starting."
		} else {
			state.Detail = "The local OpenAI-compatible gateway is running."
		}
	} else {
		state.Detail = "Ready to start " + filepath.Base(path) + "."
	}
	return state
}

// Models returns every GGUF installed in the Orchestrator's local library and
// merges the gateway's worker state when the local endpoint is available.
func (a *OrchestratorApp) Models() (*ModelLibrary, error) {
	directory, err := defaultModelsDirectory()
	if err != nil {
		return nil, err
	}
	models, err := scanDesktopModels(directory)
	if err != nil {
		return nil, err
	}
	if states, err := a.gatewayClient().States(); err == nil {
		seen := make(map[string]bool, len(models))
		for i := range models {
			seen[models[i].ID] = true
			if state, ok := states[models[i].ID]; ok {
				models[i].State = state.State
				models[i].Nodes = state.Nodes
				models[i].UpdatedAt = state.UpdatedAt
				models[i].Detail = state.Detail
			}
		}
		// A worker whose source GGUF was removed remains controllable until it
		// is explicitly unloaded. Do not hide it merely because it is no longer
		// part of the scanned library.
		for id, state := range states {
			if seen[id] || state.State != "removed" {
				continue
			}
			models = append(models, DesktopModel{ID: id, Filename: "Removed from library", State: state.State, Nodes: state.Nodes, UpdatedAt: state.UpdatedAt, Detail: state.Detail})
		}
		sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	}
	a.mu.Lock()
	download := a.modelDownload
	a.mu.Unlock()
	return &ModelLibrary{Directory: directory, Models: models, Download: download}, nil
}

// LoadModel explicitly keeps a selected installed GGUF resident in the local
// gateway. Its layers are placed by tether-api across the current GPU mesh.
func (a *OrchestratorApp) LoadModel(modelID string) error {
	if strings.TrimSpace(modelID) == "" {
		return fmt.Errorf("choose an installed model to load")
	}
	if err := a.ensureGateway(); err != nil {
		return err
	}
	return a.gatewayClient().Action(modelID, "load")
}

// ModelPlan returns the gateway's current, non-binding placement preview so
// the desktop can make an expensive model load explicit before it begins.
func (a *OrchestratorApp) ModelPlan(modelID string) (*ModelPlacementPlan, error) {
	if strings.TrimSpace(modelID) == "" {
		return nil, fmt.Errorf("choose an installed model to review")
	}
	if err := a.ensureGateway(); err != nil {
		return nil, err
	}
	return a.gatewayClient().Plan(modelID)
}

func (a *OrchestratorApp) UnloadModel(modelID string) error {
	if strings.TrimSpace(modelID) == "" {
		return fmt.Errorf("choose a loaded model to unload")
	}
	return a.gatewayClient().Action(modelID, "unload")
}

// RefreshModels rescans a running gateway before returning the desktop
// library, so a deleted but loaded GGUF remains visible for explicit unload.
func (a *OrchestratorApp) RefreshModels() (*ModelLibrary, error) {
	if a.gatewayState().Running {
		if err := a.gatewayClient().Refresh(); err != nil {
			return nil, err
		}
	}
	return a.Models()
}

// DownloadGPTOSS20B downloads the official ggml-org MXFP4 GGUF into the same
// local model directory the gateway scans. The partial file is never exposed
// as installed until the complete download has been atomically renamed.
func (a *OrchestratorApp) DownloadGPTOSS20B() error {
	directory, err := defaultModelsDirectory()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	destination := filepath.Join(directory, gptOSS20BFilename)
	if _, err := os.Stat(destination); err == nil {
		valid, verifyErr := verifyModelDownload(context.Background(), destination)
		if verifyErr != nil {
			return fmt.Errorf("verifying existing %s: %w", gptOSS20BFilename, verifyErr)
		}
		if valid {
			return fmt.Errorf("%s is already installed and verified", gptOSS20BFilename)
		}
		return fmt.Errorf("existing %s is not the expected artifact; move it aside before retrying", gptOSS20BFilename)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking existing %s: %w", gptOSS20BFilename, err)
	}
	partial := destination + ".partial"
	var offset int64
	if info, err := os.Stat(partial); err == nil {
		offset = info.Size()
		if offset > gptOSS20BExpectedSize {
			return fmt.Errorf("partial %s is larger than the expected download; remove it before retrying", gptOSS20BFilename)
		}
		if offset == gptOSS20BExpectedSize {
			valid, verifyErr := verifyModelDownload(context.Background(), partial)
			if verifyErr != nil {
				return fmt.Errorf("verifying completed partial %s: %w", gptOSS20BFilename, verifyErr)
			}
			if valid {
				if err := os.Rename(partial, destination); err != nil {
					return fmt.Errorf("recovering verified %s: %w", gptOSS20BFilename, err)
				}
				a.markModelDownloadComplete(gptOSS20BExpectedSize)
				if a.gatewayState().Running {
					_ = a.refreshGatewayModels()
				}
				return nil
			}
			// The operator explicitly chose Retry. A full-size partial with a
			// bad digest cannot be resumed safely, so restart it from zero.
			if err := os.Truncate(partial, 0); err != nil {
				return fmt.Errorf("resetting corrupt partial %s: %w", gptOSS20BFilename, err)
			}
			offset = 0
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking partial %s: %w", gptOSS20BFilename, err)
	}
	available, err := availableDiskBytes(directory)
	if err != nil {
		return err
	}
	required := gptOSS20BExpectedSize - offset + 1<<30 // retain 1 GiB working headroom.
	if available < uint64(required) {
		return fmt.Errorf("insufficient disk space for %s: need %s free (including headroom), have %s", gptOSS20BFilename, formatBytes(required), formatBytes(int64(available)))
	}
	a.mu.Lock()
	if a.modelDownload.State == "downloading" {
		a.mu.Unlock()
		return fmt.Errorf("a model download is already in progress")
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.downloadCancel = cancel
	a.modelDownload = ModelDownload{ID: strings.TrimSuffix(gptOSS20BFilename, filepath.Ext(gptOSS20BFilename)), Filename: gptOSS20BFilename, State: "downloading", Bytes: offset, TotalBytes: gptOSS20BExpectedSize, Source: gptOSS20BDownloadURL, SHA256: gptOSS20BSHA256, CanCancel: true, Detail: "Connecting to the verified Hugging Face artifact."}
	a.mu.Unlock()
	go a.downloadGPTOSS20B(ctx, directory, destination)
	return nil
}

// CancelModelDownload stops an active transfer and retains its .partial file
// for an explicit retry/resume. It never promotes unverified content.
func (a *OrchestratorApp) CancelModelDownload() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.modelDownload.State != "downloading" || a.downloadCancel == nil {
		return fmt.Errorf("no model download is in progress")
	}
	a.modelDownload.Detail = "Cancellation requested; preserving the verified partial progress."
	a.downloadCancel()
	return nil
}

func (a *OrchestratorApp) downloadGPTOSS20B(ctx context.Context, directory, destination string) {
	partial := destination + ".partial"
	var offset int64
	if info, err := os.Stat(partial); err == nil {
		offset = info.Size()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gptOSS20BDownloadURL, nil)
	if err != nil {
		a.setModelDownloadFailed(err)
		return
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := (&http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 30 * time.Second}}).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			a.setModelDownloadCancelled()
			return
		}
		a.setModelDownloadFailed(fmt.Errorf("downloading %s: %w", gptOSS20BFilename, err))
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		a.setModelDownloadFailed(fmt.Errorf("downloading %s: server returned %d", gptOSS20BFilename, response.StatusCode))
		return
	}
	if response.StatusCode == http.StatusOK {
		offset = 0
	} else if !validContentRange(response.Header.Get("Content-Range"), offset) {
		a.setModelDownloadFailed(fmt.Errorf("downloading %s: server returned an invalid resume range", gptOSS20BFilename))
		return
	}
	file, err := os.OpenFile(partial, os.O_CREATE|os.O_WRONLY|map[bool]int{true: os.O_APPEND, false: os.O_TRUNC}[offset > 0], 0600)
	if err != nil {
		a.setModelDownloadFailed(err)
		return
	}
	defer file.Close()
	if response.ContentLength >= 0 && response.ContentLength+offset != gptOSS20BExpectedSize {
		a.setModelDownloadFailed(fmt.Errorf("downloading %s: server advertised %s, want %s", gptOSS20BFilename, formatBytes(response.ContentLength+offset), formatBytes(gptOSS20BExpectedSize)))
		return
	}
	a.setModelDownloadProgress(offset, gptOSS20BExpectedSize, "Downloading the official GGUF from Hugging Face.")
	buffer := make([]byte, 1024*1024)
	written := offset
	for {
		count, readErr := response.Body.Read(buffer)
		if count > 0 {
			if written+int64(count) > gptOSS20BExpectedSize {
				a.setModelDownloadFailed(fmt.Errorf("downloading %s: response exceeds expected size", gptOSS20BFilename))
				return
			}
			if _, err := file.Write(buffer[:count]); err != nil {
				a.setModelDownloadFailed(err)
				return
			}
			written += int64(count)
			a.setModelDownloadProgress(written, gptOSS20BExpectedSize, "Downloading the official GGUF from Hugging Face.")
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			if ctx.Err() != nil {
				a.setModelDownloadCancelled()
				return
			}
			a.setModelDownloadFailed(readErr)
			return
		}
	}
	if written != gptOSS20BExpectedSize {
		a.setModelDownloadFailed(fmt.Errorf("downloading %s: truncated response (%s, want %s)", gptOSS20BFilename, formatBytes(written), formatBytes(gptOSS20BExpectedSize)))
		return
	}
	if err := file.Close(); err != nil {
		a.setModelDownloadFailed(err)
		return
	}
	valid, err := verifyModelDownload(ctx, partial)
	if err != nil {
		if ctx.Err() != nil {
			a.setModelDownloadCancelled()
		} else {
			a.setModelDownloadFailed(fmt.Errorf("verifying %s: %w", gptOSS20BFilename, err))
		}
		return
	}
	if !valid {
		a.setModelDownloadFailed(fmt.Errorf("verifying %s: SHA-256 did not match the publisher's digest", gptOSS20BFilename))
		return
	}
	if err := os.Rename(partial, destination); err != nil {
		a.setModelDownloadFailed(err)
		return
	}
	a.markModelDownloadComplete(written)
	if a.gatewayState().Running {
		_ = a.refreshGatewayModels()
	}
}

func (a *OrchestratorApp) markModelDownloadComplete(bytes int64) {
	a.mu.Lock()
	a.downloadCancel = nil
	a.modelDownload = ModelDownload{ID: strings.TrimSuffix(gptOSS20BFilename, filepath.Ext(gptOSS20BFilename)), Filename: gptOSS20BFilename, State: "complete", Bytes: bytes, TotalBytes: gptOSS20BExpectedSize, Source: gptOSS20BDownloadURL, SHA256: gptOSS20BSHA256, Detail: "Verified SHA-256 and installed in the local model library."}
	a.mu.Unlock()
}

func (a *OrchestratorApp) setModelDownloadProgress(written, total int64, detail string) {
	a.mu.Lock()
	a.modelDownload.Bytes = written
	a.modelDownload.TotalBytes = total
	a.modelDownload.Detail = detail
	a.mu.Unlock()
}

func (a *OrchestratorApp) setModelDownloadFailed(err error) {
	a.mu.Lock()
	a.downloadCancel = nil
	a.modelDownload.State = "failed"
	a.modelDownload.CanCancel = false
	a.modelDownload.Detail = err.Error()
	a.mu.Unlock()
}

func (a *OrchestratorApp) setModelDownloadCancelled() {
	a.mu.Lock()
	a.downloadCancel = nil
	a.modelDownload.State = "cancelled"
	a.modelDownload.CanCancel = false
	a.modelDownload.Detail = "Cancelled. Partial download retained; choose Download again to resume."
	a.mu.Unlock()
}

func validContentRange(value string, offset int64) bool {
	parts := strings.Fields(value)
	if len(parts) != 2 || parts[0] != "bytes" {
		return false
	}
	start, _, found := strings.Cut(parts[1], "-")
	if !found {
		return false
	}
	return start == fmt.Sprint(offset) && strings.HasSuffix(parts[1], "/"+fmt.Sprint(gptOSS20BExpectedSize))
}

func verifyModelDownload(ctx context.Context, path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 1024*1024)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			size += int64(count)
			if _, err := hash.Write(buffer[:count]); err != nil {
				return false, err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return false, readErr
		}
	}
	return size == gptOSS20BExpectedSize && fmt.Sprintf("%x", hash.Sum(nil)) == gptOSS20BSHA256, nil
}

func formatBytes(value int64) string {
	const gib = int64(1024 * 1024 * 1024)
	return fmt.Sprintf("%.1f GiB", float64(value)/float64(gib))
}

func (a *OrchestratorApp) ensureGateway() error {
	if !a.gatewayState().Running {
		if err := a.StartGateway(); err != nil {
			return err
		}
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := a.gatewayClient().States(); err == nil {
			return a.refreshGatewayModels()
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("the local model gateway did not become ready")
}

func (a *OrchestratorApp) refreshGatewayModels() error {
	return a.gatewayClient().Refresh()
}

func (a *OrchestratorApp) gatewayClient() modelGatewayClient {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.modelGateway == nil {
		a.modelGateway = newLocalModelGatewayClient()
	}
	return a.modelGateway
}

type gatewayModelState struct {
	Model     string   `json:"model"`
	State     string   `json:"state"`
	Nodes     []string `json:"nodes"`
	UpdatedAt string   `json:"updatedAt"`
	Detail    string   `json:"detail"`
}

type ModelPlacementPlan struct {
	Model             string   `json:"model"`
	SizeBytes         int64    `json:"sizeBytes"`
	ReserveBytes      int64    `json:"reserveBytes"`
	ModelReserveBytes int64    `json:"modelReserveBytes"`
	KVCacheBytes      int64    `json:"kvCacheBytes"`
	Mode              string   `json:"mode"`
	Nodes             []string `json:"nodes"`
	Detail            string   `json:"detail"`
	ObservedAt        string   `json:"observedAt"`
}

func defaultModelsDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating the local model directory: %w", err)
	}
	return filepath.Join(home, "models"), nil
}

func scanDesktopModels(directory string) ([]DesktopModel, error) {
	models := make([]DesktopModel, 0)
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".gguf") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		filename := entry.Name()
		models = append(models, DesktopModel{ID: strings.TrimSuffix(filename, filepath.Ext(filename)), Filename: filename, Path: path, SizeBytes: info.Size(), State: "unloaded"})
		return nil
	})
	if os.IsNotExist(err) {
		return models, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning model library %q: %w", directory, err)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
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
	candidates := make([]string, 0, 4)
	if config.LlamaServerPath != "" && config.LlamaServerLocalGPU == config.ContributeLocalGPU {
		candidates = append(candidates, config.LlamaServerPath)
	}
	if config.ContributeLocalGPU {
		candidates = append(candidates, platformLlamaServerPath(filepath.Join("llama.cpp", "build-rpc-cuda")))
	} else {
		candidates = append(candidates, platformLlamaServerPath(filepath.Join("llama.cpp", "build-rpc")))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			absolute, absErr := filepath.Abs(candidate)
			if absErr != nil {
				return "", fmt.Errorf("resolving llama-server path %q: %w", candidate, absErr)
			}
			return absolute, nil
		}
	}
	mode := "RPC control-only"
	if config.ContributeLocalGPU {
		mode = "CUDA and RPC"
	}
	return "", fmt.Errorf("local %s inference backend is not prepared; choose Prepare local backend", mode)
}

func platformLlamaServerPath(buildRoot string) string {
	parts := []string{buildRoot, "bin"}
	if runtime.GOOS == "windows" {
		parts = append(parts, "Release")
	}
	parts = append(parts, executableName("llama-server"))
	return filepath.Join(parts...)
}

func localBackendEnvironment(config orchestratorconfig.Config) []string {
	environment := os.Environ()
	if runtime.GOOS != "windows" || !config.ContributeLocalGPU {
		return environment
	}
	programFiles := os.Getenv("ProgramFiles")
	if programFiles == "" {
		return environment
	}
	candidates, _ := filepath.Glob(filepath.Join(programFiles, "NVIDIA GPU Computing Toolkit", "CUDA", "v*", "bin"))
	if len(candidates) == 0 {
		return environment
	}
	sort.Sort(sort.Reverse(sort.StringSlice(candidates)))
	pathValue := os.Getenv("PATH")
	for _, candidate := range candidates {
		pathValue = candidate + string(os.PathListSeparator) + pathValue
	}
	for i, item := range environment {
		if strings.HasPrefix(strings.ToUpper(item), "PATH=") {
			environment[i] = "PATH=" + pathValue
			return environment
		}
	}
	return append(environment, "PATH="+pathValue)
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

func prepareWindowsOrchestratorProvisioner() (string, string, error) {
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return "", "", fmt.Errorf("locating local setup directory: %w", err)
	}
	root := filepath.Join(cacheRoot, "tether")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", "", fmt.Errorf("creating local setup directory: %w", err)
	}
	script := filepath.Join(root, "bootstrap-windows-orchestrator.ps1")
	if err := os.WriteFile(script, bootstrapassets.WindowsOrchestratorBootstrap, 0600); err != nil {
		return "", "", fmt.Errorf("writing local Orchestrator setup script: %w", err)
	}
	return script, root, nil
}

func (a *OrchestratorApp) prepareWindowsLocalBackend() error {
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
	script, root, err := prepareWindowsOrchestratorProvisioner()
	if err != nil {
		return err
	}
	progressPath := filepath.Join(root, "orchestrator-setup-progress.json")
	logPath := filepath.Join(root, "orchestrator-setup.log")
	if err := os.WriteFile(logPath, []byte("Tether Windows Orchestrator backend setup started. Full output will remain in this file.\r\n"), 0600); err != nil {
		return fmt.Errorf("creating Orchestrator setup log: %w", err)
	}
	if err := writeBackendProgress(progressPath, backendProgress{Step: "requirements", Detail: "Preparing the Windows local backend setup."}); err != nil {
		return err
	}
	a.mu.Lock()
	a.preparing = true
	a.prepareDetail = "Preparing the Windows inference backend. Progress is saved in the setup log."
	a.prepareLog = logPath
	a.progressPath = progressPath
	a.mu.Unlock()
	go a.runWindowsOrchestratorProvisioner(script, progressPath, logPath, config.ContributeLocalGPU)
	return nil
}

func (a *OrchestratorApp) runWindowsOrchestratorProvisioner(script, progressPath, logPath string, contributeGPU bool) {
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0600)
	if err == nil {
		defer logFile.Close()
		arguments := []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script, "-InstallMissing", "-ProgressPath", progressPath}
		if contributeGPU {
			arguments = append(arguments, "-LocalGPU")
		}
		command := exec.Command("powershell.exe", arguments...)
		executil.HideWindow(command)
		command.Stdout, command.Stderr = logFile, logFile
		err = command.Run()
	}
	a.mu.Lock()
	a.preparing = false
	if err != nil {
		progress := readBackendProgress(progressPath)
		if progress.Detail != "" {
			a.prepareDetail = "Windows backend setup stopped: " + progress.Detail
		} else {
			a.prepareDetail = "Windows backend setup did not complete: " + err.Error()
		}
		a.mu.Unlock()
		return
	}
	a.prepareDetail = "Local Windows inference backend is ready."
	a.mu.Unlock()

	config, configErr := a.orchestratorConfig()
	if configErr != nil {
		return
	}
	config.LlamaServerPath = defaultWindowsLlamaServerPath(contributeGPU)
	config.LlamaServerLocalGPU = contributeGPU
	if configErr = orchestratorconfig.Save(a.configPath, config); configErr == nil {
		_ = a.StartGateway()
	}
}

func defaultWindowsLlamaServerPath(contributeGPU bool) string {
	dataRoot := os.Getenv("LOCALAPPDATA")
	if dataRoot == "" {
		dataRoot, _ = os.UserConfigDir()
	}
	build := "build-rpc"
	if contributeGPU {
		build = "build-rpc-cuda"
	}
	return platformLlamaServerPath(filepath.Join(dataRoot, "tether", "llama.cpp", build))
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

// resolveAllowlistPath makes packaged desktop startup independent of the
// process working directory and keeps the mutable allowlist out of Program
// Files. An existing source-tree or beside-executable file is copied once as
// migration input; all subsequent edits use the private user copy.
func resolveAllowlistPath() (string, error) {
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating user configuration for the node allowlist: %w", err)
	}
	directory := filepath.Join(configRoot, "tether")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", fmt.Errorf("creating node allowlist directory: %w", err)
	}
	path := filepath.Join(directory, "node_allowlist.yaml")
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path, nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("checking node allowlist: %w", err)
	}

	data := []byte("nodes: []\n")
	candidates := make([]string, 0, 2)
	if executable, executableErr := os.Executable(); executableErr == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), "node_allowlist.yaml"))
	}
	if workingCopy, absErr := filepath.Abs("node_allowlist.yaml"); absErr == nil {
		candidates = append(candidates, workingCopy)
	}
	for _, candidate := range candidates {
		if candidateData, readErr := os.ReadFile(candidate); readErr == nil {
			data = candidateData
			break
		}
	}
	if err := writeFileAtomically(path, data, 0600); err != nil {
		return "", fmt.Errorf("creating node allowlist: %w", err)
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
