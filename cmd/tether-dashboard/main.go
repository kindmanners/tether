// Command tether-dashboard serves Tether's local web dashboard. It is a
// read-only Orchestrator view: registry data comes from Tailscale plus the
// allowlist, GPU data comes from each paired Agent's capability report, and
// GGUF files are scanned only on the Orchestrator's local disk.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"tether/internal/agent"
	"tether/internal/certs"
	"tether/internal/registry"
	"tether/internal/trust"
)

const orchestratorIdentityName = "orchestrator"

type dashboardNode struct {
	IsOrchestrator bool   `json:"isOrchestrator"`
	Hostname       string `json:"hostname"`
	Status         string `json:"status"`
	TailscaleIP    string `json:"tailscaleIP,omitempty"`
	AgentPort      int    `json:"agentPort"`
	RPCPort        int    `json:"rpcPort"`
	GPUModel       string `json:"gpuModel,omitempty"`
	VRAMTotalBytes int64  `json:"vramTotalBytes,omitempty"`
	VRAMFreeBytes  int64  `json:"vramFreeBytes,omitempty"`
	AgentStatus    string `json:"agentStatus,omitempty"`
	Note           string `json:"note,omitempty"`
}

type dashboardModel struct {
	Name   string                `json:"name"`
	Path   string                `json:"path"`
	Format string                `json:"format"`
	Note   string                `json:"note,omitempty"`
	States []dashboardModelState `json:"states,omitempty"`
}

// dashboardModelState is supplied by the Orchestrator-owned API gateway. The
// dashboard never guesses worker state from a GPU report: an unloaded model
// and an idle-loaded model are materially different for the next request.
type dashboardModelState struct {
	State     string   `json:"state"`
	Nodes     []string `json:"nodes,omitempty"`
	UpdatedAt string   `json:"updatedAt,omitempty"`
	IdleUntil string   `json:"idleUntil,omitempty"`
	Detail    string   `json:"detail,omitempty"`
}

type dashboardResponse struct {
	ObservedAt  string           `json:"observedAt"`
	Source      string           `json:"source"`
	ModelSource string           `json:"modelSource"`
	Nodes       []dashboardNode  `json:"nodes"`
	Models      []dashboardModel `json:"models"`
}

type dashboardServer struct {
	allowlistPath string
	modelsDir     string
	modelStateURL string
}

func main() {
	defaultModelsDir := "models"
	if home, err := os.UserHomeDir(); err == nil {
		defaultModelsDir = filepath.Join(home, "models")
	}

	listen := flag.String("listen", "127.0.0.1:8080", "HTTP address for the local dashboard")
	allowlistPath := flag.String("allowlist", "node_allowlist.yaml", "path to Tether node allowlist")
	modelsDir := flag.String("models-dir", defaultModelsDir, "directory containing orchestrator GGUF models")
	staticDir := flag.String("static-dir", "web/dashboard", "directory containing dashboard HTML assets")
	modelStateURL := flag.String("model-state-url", "http://127.0.0.1:11435/api/v1/model-states", "Tether API model-state endpoint; empty disables live model states")
	flag.Parse()

	if info, err := os.Stat(*staticDir); err != nil || !info.IsDir() {
		log.Fatalf("dashboard assets at %q are unavailable: %v", *staticDir, err)
	}

	server := &dashboardServer{allowlistPath: *allowlistPath, modelsDir: *modelsDir, modelStateURL: *modelStateURL}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/dashboard", server.handleDashboard)
	mux.Handle("/", http.FileServer(http.Dir(*staticDir)))

	log.Printf("Tether dashboard listening on http://%s", *listen)
	if err := http.ListenAndServe(*listen, mux); err != nil {
		log.Fatal(err)
	}
}

func (s *dashboardServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "only GET is supported", http.StatusMethodNotAllowed)
		return
	}

	data, err := s.collect()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (s *dashboardServer) collect() (*dashboardResponse, error) {
	allowlist, err := registry.LoadAllowlist(s.allowlistPath)
	if err != nil {
		return nil, fmt.Errorf("loading allowlist: %w", err)
	}
	peers, err := registry.QueryTailscalePeers()
	if err != nil {
		return nil, fmt.Errorf("querying Tailscale peers: %w", err)
	}

	identity, err := certs.LoadOrCreate(orchestratorIdentityName)
	if err != nil {
		return nil, fmt.Errorf("loading orchestrator identity: %w", err)
	}

	reg := registry.Build(allowlist, peers)
	nodes := reg.All()
	selfHostname, selfHostnameErr := registry.SelfHostname()
	localGPUs, localGPUErr := localNvidiaGPUs()
	result := make([]dashboardNode, 0, len(nodes))
	for _, node := range nodes {
		if selfHostnameErr == nil && node.Hostname == selfHostname {
			result = append(result, dashboardNodeFromLocalHost(node, localGPUs, localGPUErr))
			continue
		}
		result = append(result, dashboardNodeFromRegistry(identity, node))
	}
	sortDashboardNodes(result)

	models, err := scanGGUFModels(s.modelsDir)
	if err != nil {
		return nil, err
	}
	if states, err := fetchModelStates(s.modelStateURL); err == nil {
		for i := range models {
			if state, ok := states[models[i].Name]; ok {
				models[i].States = []dashboardModelState{state}
			}
		}
	}

	return &dashboardResponse{
		ObservedAt:  time.Now().UTC().Format(time.RFC3339),
		Source:      "Live Tailscale registry and paired Tether Agent data",
		ModelSource: "Scanned orchestrator model directory: " + s.modelsDir,
		Nodes:       result,
		Models:      models,
	}, nil
}

func sortDashboardNodes(nodes []dashboardNode) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].IsOrchestrator != nodes[j].IsOrchestrator {
			return nodes[i].IsOrchestrator
		}
		return nodes[i].Hostname < nodes[j].Hostname
	})
}

func fetchModelStates(url string) (map[string]dashboardModelState, error) {
	if strings.TrimSpace(url) == "" {
		return nil, nil
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model-state endpoint returned %d", response.StatusCode)
	}
	var payload struct {
		Models []struct {
			Model string `json:"model"`
			dashboardModelState
		} `json:"models"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, err
	}
	states := make(map[string]dashboardModelState, len(payload.Models))
	for _, model := range payload.Models {
		states[model.Model] = model.dashboardModelState
	}
	return states, nil
}

// dashboardNodeFromLocalHost lets the Orchestrator show its own NVIDIA GPUs
// without pretending it is a remote Agent. The remote path remains mTLS-only;
// this reads the local nvidia-smi executable and so never crosses a network.
func dashboardNodeFromLocalHost(node *registry.Node, gpus []agent.GPUCapability, gpuErr error) dashboardNode {
	result := dashboardNode{
		IsOrchestrator: true,
		Hostname:       node.Hostname,
		Status:         strings.ToLower(node.Status.String()),
		TailscaleIP:    node.TailscaleIP,
		AgentPort:      node.AgentPort,
		RPCPort:        node.RPCPort,
		AgentStatus:    "Local GPU scan",
	}
	if gpuErr != nil {
		result.Note = "Local GPU information is unavailable: " + gpuErr.Error()
		return result
	}
	for _, gpu := range gpus {
		result.VRAMTotalBytes += gpu.VRAMBytes
		result.VRAMFreeBytes += gpu.VRAMFreeBytes
	}
	if len(gpus) == 0 {
		result.Note = "No NVIDIA GPUs were reported by nvidia-smi"
		return result
	}
	result.GPUModel = joinGPUModelNames(gpus)
	result.Note = "Local NVIDIA GPU scan"
	return result
}

func dashboardNodeFromRegistry(identity *certs.Identity, node *registry.Node) dashboardNode {
	result := dashboardNode{
		Hostname:    node.Hostname,
		Status:      strings.ToLower(node.Status.String()),
		TailscaleIP: node.TailscaleIP,
		AgentPort:   node.AgentPort,
		RPCPort:     node.RPCPort,
	}
	if node.Status != registry.StatusOnline {
		result.Note = "Not currently reachable through Tailscale"
		return result
	}

	tlsConfig, err := trust.PinnedTLSConfig(identity.TLSCertificate(), node.Hostname, false)
	if err != nil {
		result.AgentStatus = "Not paired"
		result.Note = "Pair this node to retrieve its GPU capability report"
		return result
	}

	client := agent.NewClient(tlsConfig)
	addr := fmt.Sprintf("%s:%d", node.TailscaleIP, node.AgentPort)
	if status, err := client.GetStatus(addr); err != nil {
		result.AgentStatus = "Unreachable"
		result.Note = "Tether Agent did not respond"
	} else {
		result.AgentStatus = status.Status
		if status.LastError != "" {
			result.Note = "Last RPC error: " + status.LastError
		}
	}

	capabilities, err := client.GetCapabilities(addr)
	if err != nil {
		if result.Note == "" {
			result.Note = "GPU capability report is unavailable: " + err.Error()
		}
		return result
	}
	for _, gpu := range capabilities.GPUs {
		result.VRAMTotalBytes += gpu.VRAMBytes
		result.VRAMFreeBytes += gpu.VRAMFreeBytes
	}
	if len(capabilities.GPUs) > 0 {
		result.GPUModel = joinGPUModelNames(capabilities.GPUs)
	}
	return result
}

func joinGPUModelNames(gpus []agent.GPUCapability) string {
	names := make([]string, 0, len(gpus))
	for _, gpu := range gpus {
		if gpu.Name != "" {
			names = append(names, gpu.Name)
		}
	}
	return strings.Join(names, ", ")
}

// localNvidiaGPUs reads the NVIDIA driver's own management tool, which is the
// most direct source for both total and currently free VRAM on the
// Orchestrator. Values reported by nvidia-smi are MiB and are converted to
// bytes before the UI sees them.
func localNvidiaGPUs() ([]agent.GPUCapability, error) {
	output, err := exec.Command(
		"nvidia-smi",
		"--query-gpu=name,memory.total,memory.free,driver_version",
		"--format=csv,noheader,nounits",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("running nvidia-smi: %w", err)
	}
	return parseNvidiaSMI(string(output))
}

func parseNvidiaSMI(output string) ([]agent.GPUCapability, error) {
	lines := strings.FieldsFunc(output, func(r rune) bool { return r == '\n' || r == '\r' })
	gpus := make([]agent.GPUCapability, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, ",")
		if len(fields) != 4 {
			return nil, fmt.Errorf("unexpected nvidia-smi row %q", line)
		}
		totalMiB, err := strconv.ParseInt(strings.TrimSpace(fields[1]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing total VRAM in nvidia-smi row %q: %w", line, err)
		}
		freeMiB, err := strconv.ParseInt(strings.TrimSpace(fields[2]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing free VRAM in nvidia-smi row %q: %w", line, err)
		}
		gpus = append(gpus, agent.GPUCapability{
			Name:          strings.TrimSpace(fields[0]),
			DriverVersion: strings.TrimSpace(fields[3]),
			VRAMBytes:     totalMiB * 1024 * 1024,
			VRAMFreeBytes: freeMiB * 1024 * 1024,
		})
	}
	return gpus, nil
}

func scanGGUFModels(modelsDir string) ([]dashboardModel, error) {
	info, err := os.Stat(modelsDir)
	if os.IsNotExist(err) {
		return []dashboardModel{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checking model directory %q: %w", modelsDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("model path %q is not a directory", modelsDir)
	}

	models := make([]dashboardModel, 0)
	err = filepath.WalkDir(modelsDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".gguf") {
			return nil
		}
		relative, err := filepath.Rel(modelsDir, path)
		if err != nil {
			return fmt.Errorf("making model path relative: %w", err)
		}
		models = append(models, dashboardModel{
			Name:   strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())),
			Path:   filepath.ToSlash(filepath.Join("~", "models", relative)),
			Format: "GGUF",
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning GGUF models in %q: %w", modelsDir, err)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Path < models[j].Path })
	return models, nil
}
