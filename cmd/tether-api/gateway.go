package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"tether/internal/executil"
	"tether/internal/placement"
)

type gatewayConfig struct {
	modelsDir          string
	llamaServer        string
	rpcMode            string
	allowlistPath      string
	apiKey             string
	ctxSize            int
	parallel           int
	idleTimeout        time.Duration
	workerStartTimeout time.Duration
	localGPU           bool
	modelOverhead      float64
	kvBytesPerToken    int64
	requestShutdown    func()
}

type modelWorker struct {
	key     string
	modelID string
	model   string
	plan    placement.Plan
	address string
	cmd     *exec.Cmd
	active  int
	state   string
	lastUse time.Time
	timer   *time.Timer
	pinned  bool
}

type gateway struct {
	cfg     gatewayConfig
	models  map[string]string
	mu      sync.Mutex
	workers map[string]*modelWorker
	states  map[string]modelState
	client  *http.Client
}

type modelState struct {
	Model     string   `json:"model"`
	State     string   `json:"state"`
	Nodes     []string `json:"nodes,omitempty"`
	UpdatedAt string   `json:"updatedAt"`
	IdleUntil string   `json:"idleUntil,omitempty"`
	Detail    string   `json:"detail,omitempty"`
}

func newGateway(cfg gatewayConfig) (*gateway, error) {
	models, err := scanGatewayModels(cfg.modelsDir)
	if err != nil {
		return nil, err
	}
	return &gateway{
		cfg: cfg, models: models, workers: make(map[string]*modelWorker),
		states: make(map[string]modelState), client: &http.Client{Timeout: 0},
	}, nil
}

func scanGatewayModels(dir string) (map[string]string, error) {
	models := make(map[string]string)
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".gguf") {
			return nil
		}
		id := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if _, exists := models[id]; exists {
			return fmt.Errorf("duplicate model id %q; GGUF file names must be unique", id)
		}
		models[id] = path
		return nil
	})
	if os.IsNotExist(err) {
		return models, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning GGUF models in %q: %w", dir, err)
	}
	return models, nil
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !g.authorized(r) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.URL.Path == "/api/v1/model-states" {
		if r.Method != http.MethodGet {
			http.Error(w, "only GET is supported", http.StatusMethodNotAllowed)
			return
		}
		g.writeStates(w)
		return
	}
	if r.URL.Path == "/api/v1/models/refresh" {
		if r.Method != http.MethodPost {
			http.Error(w, "only POST is supported", http.StatusMethodNotAllowed)
			return
		}
		if err := g.refreshModels(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		g.writeStates(w)
		return
	}
	if r.URL.Path == "/api/v1/internal/shutdown" {
		if r.Method != http.MethodPost {
			http.Error(w, "only POST is supported", http.StatusMethodNotAllowed)
			return
		}
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err != nil || !isLoopbackHost(host) {
			http.Error(w, "shutdown is available only from loopback", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		if g.cfg.requestShutdown != nil {
			go g.cfg.requestShutdown()
		}
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/models/") {
		g.handleModelAction(w, r)
		return
	}
	switch {
	case r.URL.Path == "/v1/models" && r.Method == http.MethodGet:
		g.writeModels(w)
	case r.URL.Path == "/v1/chat/completions" && r.Method == http.MethodPost:
		g.handleChat(w, r)
	default:
		http.Error(w, "Tether API currently supports GET /v1/models and POST /v1/chat/completions", http.StatusNotFound)
	}
}

func (g *gateway) handleModelAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/models/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	modelID, err := url.PathUnescape(parts[0])
	if err != nil {
		http.Error(w, "invalid model id", http.StatusBadRequest)
		return
	}
	switch parts[1] {
	case "load":
		if r.Method != http.MethodPost {
			http.Error(w, "only POST is supported", http.StatusMethodNotAllowed)
			return
		}
		err = g.load(modelID)
	case "unload":
		if r.Method != http.MethodPost {
			http.Error(w, "only POST is supported", http.StatusMethodNotAllowed)
			return
		}
		err = g.unloadModel(modelID)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	g.writeStates(w)
}

func (g *gateway) authorized(r *http.Request) bool {
	if g.cfg.apiKey == "" {
		return true
	}
	return r.Header.Get("Authorization") == "Bearer "+g.cfg.apiKey
}

func (g *gateway) writeModels(w http.ResponseWriter) {
	g.mu.Lock()
	ids := make([]string, 0, len(g.models))
	for id := range g.models {
		ids = append(ids, id)
	}
	g.mu.Unlock()
	sort.Strings(ids)
	data := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		data = append(data, map[string]any{"id": id, "object": "model", "owned_by": "tether"})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

func (g *gateway) handleChat(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<20))
	if err != nil {
		http.Error(w, "request body is too large", http.StatusRequestEntityTooLarge)
		return
	}
	var request struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &request); err != nil || request.Model == "" {
		http.Error(w, "a JSON request with a model is required", http.StatusBadRequest)
		return
	}
	worker, err := g.acquire(request.Model)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer g.release(worker)

	upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://"+worker.address+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	upstream.Header = r.Header.Clone()
	upstream.Header.Del("Authorization")
	upstream.Host = ""
	resp, err := g.client.Do(upstream)
	if err != nil {
		http.Error(w, "model worker request failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for key, values := range resp.Header {
		if strings.EqualFold(key, "Content-Length") || strings.EqualFold(key, "Connection") {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (g *gateway) acquire(modelID string) (*modelWorker, error) {
	g.mu.Lock()
	if worker := g.workerForModelLocked(modelID); worker != nil {
		if worker.timer != nil {
			worker.timer.Stop()
			worker.timer = nil
		}
		worker.active++
		worker.state = "loaded"
		g.recordLocked(worker, "")
		g.mu.Unlock()
		return worker, nil
	}
	path, ok := g.models[modelID]
	if !ok {
		g.mu.Unlock()
		return nil, fmt.Errorf("model %q is not in Tether's GGUF library", modelID)
	}
	g.states[modelID] = modelState{Model: modelID, State: "loading", UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	g.mu.Unlock()

	plan, err := g.planFor(path)
	if err != nil {
		g.setState(modelID, "unloaded", nil, err.Error())
		return nil, err
	}
	worker, err := g.launch(modelID, path, plan)
	if err != nil {
		g.setState(modelID, "unloaded", planNodeNames(plan), err.Error())
		return nil, err
	}

	g.mu.Lock()
	// Another same-model request may have completed the expensive launch first.
	if existing := g.workerForModelLocked(modelID); existing != nil {
		g.mu.Unlock()
		_ = executil.KillProcessTree(worker.cmd)
		_, _ = worker.cmd.Process.Wait()
		return g.acquire(modelID)
	}
	worker.key = workerKey(modelID, plan)
	worker.active = 1
	worker.state = "loaded"
	g.workers[worker.key] = worker
	g.recordLocked(worker, "")
	g.mu.Unlock()
	return worker, nil
}

func (g *gateway) release(worker *modelWorker) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.workers[worker.key] != worker {
		return
	}
	worker.active--
	if worker.active > 0 {
		return
	}
	worker.lastUse = time.Now()
	if worker.pinned {
		worker.state = "loaded"
		g.recordLocked(worker, "")
		return
	}
	if g.cfg.idleTimeout > 0 {
		worker.state = "idle-countdown"
		worker.timer = time.AfterFunc(g.cfg.idleTimeout, func() { g.unload(worker) })
	} else {
		worker.state = "loaded"
	}
	g.recordLocked(worker, "")
}

// load starts a model worker without a chat request and keeps it resident
// until the Models control explicitly unloads it. This makes model placement
// observable and controllable from the Orchestrator rather than treating the
// first client prompt as an implicit load command.
func (g *gateway) load(modelID string) error {
	g.mu.Lock()
	if worker := g.workerForModelLocked(modelID); worker != nil {
		if worker.timer != nil {
			worker.timer.Stop()
			worker.timer = nil
		}
		worker.pinned = true
		worker.state = "loaded"
		g.recordLocked(worker, "")
		g.mu.Unlock()
		return nil
	}
	path, ok := g.models[modelID]
	if !ok {
		g.mu.Unlock()
		return fmt.Errorf("model %q is not in Tether's GGUF library", modelID)
	}
	g.states[modelID] = modelState{Model: modelID, State: "loading", UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	g.mu.Unlock()

	plan, err := g.planFor(path)
	if err != nil {
		g.setState(modelID, "unloaded", nil, err.Error())
		return err
	}
	worker, err := g.launch(modelID, path, plan)
	if err != nil {
		g.setState(modelID, "unloaded", planNodeNames(plan), err.Error())
		return err
	}

	g.mu.Lock()
	if existing := g.workerForModelLocked(modelID); existing != nil {
		g.mu.Unlock()
		_ = executil.KillProcessTree(worker.cmd)
		_, _ = worker.cmd.Process.Wait()
		return g.load(modelID)
	}
	worker.key = workerKey(modelID, plan)
	worker.pinned = true
	worker.state = "loaded"
	g.workers[worker.key] = worker
	g.recordLocked(worker, "")
	g.mu.Unlock()
	return nil
}

func (g *gateway) unloadModel(modelID string) error {
	g.mu.Lock()
	worker := g.workerForModelLocked(modelID)
	if worker == nil {
		if _, ok := g.models[modelID]; !ok {
			g.mu.Unlock()
			return fmt.Errorf("model %q is not in Tether's GGUF library", modelID)
		}
		g.states[modelID] = modelState{Model: modelID, State: "unloaded", UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
		g.mu.Unlock()
		return nil
	}
	if worker.active > 0 {
		g.mu.Unlock()
		return fmt.Errorf("model %q has an active request", modelID)
	}
	if worker.timer != nil {
		worker.timer.Stop()
		worker.timer = nil
	}
	worker.pinned = false
	worker.state = "unloading"
	g.recordLocked(worker, "")
	g.mu.Unlock()
	g.unload(worker)
	return nil
}

func (g *gateway) refreshModels() error {
	models, err := scanGatewayModels(g.cfg.modelsDir)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.models = models
	for modelID := range g.states {
		if _, installed := models[modelID]; !installed && g.workerForModelLocked(modelID) == nil {
			delete(g.states, modelID)
		}
	}
	return nil
}

func (g *gateway) unload(worker *modelWorker) {
	g.mu.Lock()
	if g.workers[worker.key] != worker || worker.active != 0 {
		g.mu.Unlock()
		return
	}
	worker.state = "unloading"
	g.recordLocked(worker, "")
	g.mu.Unlock()
	_ = executil.KillProcessTree(worker.cmd)
	_, _ = worker.cmd.Process.Wait()
	g.mu.Lock()
	if g.workers[worker.key] == worker {
		delete(g.workers, worker.key)
		g.states[worker.modelID] = modelState{Model: worker.modelID, State: "unloaded", Nodes: planNodeNames(worker.plan), UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	}
	g.mu.Unlock()
}

func (g *gateway) launch(modelID, modelPath string, plan placement.Plan) (*modelWorker, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("reserving worker port: %w", err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	args := []string{"--model", modelPath, "--host", "127.0.0.1", "--port", strings.TrimPrefix(address, "127.0.0.1:"), "--ctx-size", fmt.Sprint(g.cfg.ctxSize), "--parallel", fmt.Sprint(g.cfg.parallel), "--n-gpu-layers", "99"}
	endpoints := rpcEndpoints(plan)
	if len(endpoints) > 0 {
		args = append(args, "--rpc", strings.Join(endpoints, ","))
	}
	cmd := exec.Command(g.cfg.llamaServer, args...)
	executil.IsolateProcessTree(cmd)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting model worker: %w", err)
	}
	if err := waitForWorker(address, cmd, g.cfg.workerStartTimeout); err != nil {
		_ = executil.KillProcessTree(cmd)
		_, _ = cmd.Process.Wait()
		return nil, err
	}
	log.Printf("loaded %s using %s placement on %s", modelID, plan.Mode, strings.Join(planNodeNames(plan), ", "))
	return &modelWorker{modelID: modelID, model: modelPath, plan: plan, address: address, cmd: cmd}, nil
}

func waitForWorker(address string, cmd *exec.Cmd, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return fmt.Errorf("model worker exited during startup")
		}
		resp, err := http.Get("http://" + address + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("model worker did not become ready within %s", timeout)
}

func (g *gateway) setState(model, state string, nodes []string, detail string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.states[model] = modelState{Model: model, State: state, Nodes: nodes, Detail: detail, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
}

func (g *gateway) recordLocked(worker *modelWorker, detail string) {
	state := modelState{Model: worker.modelID, State: worker.state, Nodes: planNodeNames(worker.plan), Detail: detail, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	if worker.state == "idle-countdown" {
		state.IdleUntil = worker.lastUse.Add(g.cfg.idleTimeout).UTC().Format(time.RFC3339)
	}
	g.states[worker.modelID] = state
}

func (g *gateway) writeStates(w http.ResponseWriter) {
	g.mu.Lock()
	states := make([]modelState, 0, len(g.models))
	for model := range g.models {
		state, ok := g.states[model]
		if !ok {
			state = modelState{Model: model, State: "unloaded", UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
		}
		states = append(states, state)
	}
	g.mu.Unlock()
	sort.Slice(states, func(i, j int) bool { return states[i].Model < states[j].Model })
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"models": states})
}

func (g *gateway) shutdown(ctx context.Context) {
	g.mu.Lock()
	workers := make([]*modelWorker, 0, len(g.workers))
	for _, worker := range g.workers {
		if worker.timer != nil {
			worker.timer.Stop()
		}
		workers = append(workers, worker)
	}
	g.mu.Unlock()
	for _, worker := range workers {
		if worker.cmd.Process != nil {
			_ = executil.KillProcessTree(worker.cmd)
		}
	}
	for _, worker := range workers {
		done := make(chan struct{})
		go func(c *exec.Cmd) { _, _ = c.Process.Wait(); close(done) }(worker.cmd)
		select {
		case <-done:
		case <-ctx.Done():
			return
		}
	}
}

func planNodeNames(plan placement.Plan) []string {
	names := make([]string, 0, len(plan.Nodes))
	for _, n := range plan.Nodes {
		names = append(names, n.Hostname)
	}
	return names
}

func workerKey(modelID string, plan placement.Plan) string {
	return modelID + "\x00" + strings.Join(planNodeNames(plan), ",")
}

func (g *gateway) workerForModelLocked(modelID string) *modelWorker {
	for _, worker := range g.workers {
		if worker.modelID == modelID {
			return worker
		}
	}
	return nil
}
func rpcEndpoints(plan placement.Plan) []string {
	endpoints := make([]string, 0, len(plan.Nodes))
	for _, n := range plan.Nodes {
		if !n.Local && n.Endpoint != "" {
			endpoints = append(endpoints, n.Endpoint)
		}
	}
	return endpoints
}
