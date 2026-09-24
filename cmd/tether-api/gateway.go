// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"tether/internal/executil"
	"tether/internal/gguf"
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
	planner            func(string) (placement.Plan, error)
	launcher           func(context.Context, string, string, placement.Plan) (*modelWorker, error)
}

type modelWorker struct {
	key     string
	modelID string
	model   string
	plan    placement.Plan
	address string
	cmd     *exec.Cmd
	done    chan struct{}
	exitErr error
	apiKey  string
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
	loadMu  sync.Mutex
	workers map[string]*modelWorker
	states  map[string]modelState
	ctx     context.Context
	cancel  context.CancelFunc
}

type modelState struct {
	Model     string   `json:"model"`
	State     string   `json:"state"`
	Nodes     []string `json:"nodes,omitempty"`
	UpdatedAt string   `json:"updatedAt"`
	IdleUntil string   `json:"idleUntil,omitempty"`
	Detail    string   `json:"detail,omitempty"`
}

// modelPlan is an advisory placement preview. Admission is intentionally
// repeated by load because available VRAM can change between review and start.
type modelPlan struct {
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

func newGateway(cfg gatewayConfig) (*gateway, error) {
	models, err := scanGatewayModels(cfg.modelsDir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &gateway{
		cfg: cfg, models: models, workers: make(map[string]*modelWorker),
		states: make(map[string]modelState), ctx: ctx, cancel: cancel,
	}, nil
}

func scanGatewayModels(dir string) (map[string]string, error) {
	models := make(map[string]string)
	entries, err := gguf.Scan(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		models[entry.ID] = entry.Path
	}
	return models, nil
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !g.authorized(r) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !validRequestHost(r.Host) || !validRequestOrigin(r) {
		http.Error(w, "forbidden request origin", http.StatusForbidden)
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
			g.internalError(w, "refreshing model library", err, http.StatusInternalServerError)
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
	if strings.HasPrefix(r.URL.Path, "/api/v1/models/") && strings.HasSuffix(r.URL.Path, "/plan") {
		if r.Method != http.MethodGet {
			http.Error(w, "only GET is supported", http.StatusMethodNotAllowed)
			return
		}
		g.handleModelPlan(w, r)
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

func (g *gateway) handleModelPlan(w http.ResponseWriter, r *http.Request) {
	modelID, err := url.PathUnescape(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/models/"), "/plan"))
	if err != nil || modelID == "" || strings.Contains(modelID, "/") {
		http.Error(w, "invalid model id", http.StatusBadRequest)
		return
	}
	plan, err := g.modelPlan(modelID)
	if err != nil {
		g.internalError(w, "planning model placement", err, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(plan)
}

func (g *gateway) modelPlan(modelID string) (modelPlan, error) {
	g.mu.Lock()
	path, ok := g.models[modelID]
	g.mu.Unlock()
	if !ok {
		return modelPlan{}, fmt.Errorf("model %q is not in Tether's GGUF library", modelID)
	}
	size, err := gguf.Size(path)
	if err != nil {
		return modelPlan{}, fmt.Errorf("reading model %q: %w", modelID, err)
	}
	planner := g.cfg.planner
	if planner == nil {
		planner = g.planFor
	}
	placementPlan, err := planner(path)
	if err != nil {
		return modelPlan{}, err
	}
	return modelPlan{
		Model: modelID, SizeBytes: size, ModelReserveBytes: placementPlan.Requirement.ModelBytes,
		KVCacheBytes: placementPlan.Requirement.KVCacheBytes, ReserveBytes: placementPlan.Requirement.TotalBytes(),
		Mode: string(placementPlan.Mode), Nodes: planNodeNames(placementPlan), ObservedAt: time.Now().UTC().Format(time.RFC3339),
		Detail: "Preview only; capacity is checked again when loading starts.",
	}, nil
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
		err = g.load(r.Context(), modelID)
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
		g.internalError(w, "performing model action", err, http.StatusServiceUnavailable)
		return
	}
	g.writeStates(w)
}

func (g *gateway) authorized(r *http.Request) bool {
	want := []byte("Bearer " + g.cfg.apiKey)
	got := []byte(r.Header.Get("Authorization"))
	return g.cfg.apiKey != "" && len(got) == len(want) && subtle.ConstantTimeCompare(got, want) == 1
}

func validRequestHost(hostport string) bool {
	host := hostport
	if parsed, _, err := net.SplitHostPort(hostport); err == nil {
		host = parsed
	}
	host = strings.Trim(host, "[]")
	return strings.EqualFold(host, "localhost") || net.ParseIP(host) != nil
}

func validRequestOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Host != "" && strings.EqualFold(parsed.Host, r.Host) && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func (g *gateway) internalError(w http.ResponseWriter, operation string, err error, status int) {
	log.Printf("%s: %v", operation, err)
	http.Error(w, operation+" failed", status)
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
	worker, err := g.acquire(r.Context(), request.Model)
	if err != nil {
		g.internalError(w, "loading model worker", err, http.StatusServiceUnavailable)
		return
	}
	defer g.release(worker)

	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(&url.URL{Scheme: "http", Host: worker.address})
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Set("Authorization", "Bearer "+worker.apiKey)
		},
		FlushInterval: -1,
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, proxyErr error) {
			log.Printf("model worker proxy failed for %s: %v", worker.modelID, proxyErr)
			http.Error(rw, "model worker request failed", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

func (g *gateway) acquire(ctx context.Context, modelID string) (*modelWorker, error) {
	g.mu.Lock()
	if worker := g.workerForModelLocked(modelID); worker != nil {
		select {
		case <-worker.done:
			g.markCrashedLocked(worker)
		default:
			result, err := g.activateWorkerLocked(worker)
			g.mu.Unlock()
			return result, err
		}
	}
	g.mu.Unlock()

	g.loadMu.Lock()
	defer g.loadMu.Unlock()
	worker, err := g.ensureWorkerLocked(ctx, modelID, false)
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.activateWorkerLocked(worker)
}

func (g *gateway) activateWorkerLocked(worker *modelWorker) (*modelWorker, error) {
	select {
	case <-worker.done:
		return nil, fmt.Errorf("model worker exited")
	default:
	}
	if g.workers[worker.key] != worker || worker.state == "unloading" || worker.state == "crashed" {
		return nil, fmt.Errorf("model worker is not available")
	}
	if worker.timer != nil {
		worker.timer.Stop()
		worker.timer = nil
	}
	worker.active++
	worker.state = "loaded"
	g.recordLocked(worker, "")
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
func (g *gateway) load(ctx context.Context, modelID string) error {
	g.loadMu.Lock()
	defer g.loadMu.Unlock()
	_, err := g.ensureWorkerLocked(ctx, modelID, true)
	return err
}

func (g *gateway) ensureWorkerLocked(ctx context.Context, modelID string, pin bool) (*modelWorker, error) {
	g.mu.Lock()
	if worker := g.workerForModelLocked(modelID); worker != nil {
		exited := false
		select {
		case <-worker.done:
			g.markCrashedLocked(worker)
			exited = true
		default:
		}
		if !exited {
			if worker.state == "unloading" {
				g.mu.Unlock()
				return nil, fmt.Errorf("model %q is unloading", modelID)
			}
			if worker.timer != nil {
				worker.timer.Stop()
				worker.timer = nil
			}
			worker.pinned = worker.pinned || pin
			worker.state = "loaded"
			g.recordLocked(worker, "")
			g.mu.Unlock()
			return worker, nil
		}
	}
	path, ok := g.models[modelID]
	if !ok {
		g.mu.Unlock()
		return nil, fmt.Errorf("model %q is not in Tether's GGUF library", modelID)
	}
	g.states[modelID] = modelState{Model: modelID, State: "loading", UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	g.mu.Unlock()

	planner := g.cfg.planner
	if planner == nil {
		planner = g.planFor
	}
	plan, err := planner(path)
	if err != nil {
		g.setState(modelID, "unloaded", nil, err.Error())
		return nil, err
	}
	launcher := g.cfg.launcher
	if launcher == nil {
		launcher = g.launch
	}
	worker, err := launcher(ctx, modelID, path, plan)
	if err != nil {
		g.setState(modelID, "unloaded", planNodeNames(plan), err.Error())
		return nil, err
	}
	worker.key = workerKey(modelID, plan)
	worker.pinned = pin
	worker.state = "loaded"
	g.mu.Lock()
	g.workers[worker.key] = worker
	g.recordLocked(worker, "")
	g.mu.Unlock()
	go g.watchWorker(worker)
	return worker, nil
}

func (g *gateway) unloadModel(modelID string) error {
	g.loadMu.Lock()
	defer g.loadMu.Unlock()
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
	if !g.unloadLocked(worker) {
		return fmt.Errorf("model %q could not be unloaded because it became active", modelID)
	}
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
	for modelID, state := range g.states {
		if _, installed := models[modelID]; installed {
			if state.State == "removed" {
				if worker := g.workerForModelLocked(modelID); worker != nil {
					g.recordLocked(worker, "")
				}
			}
			continue
		}
		if g.workerForModelLocked(modelID) == nil {
			delete(g.states, modelID)
			continue
		}
		state.State = "removed"
		state.Detail = "Removed from the library; unload required."
		state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		g.states[modelID] = state
	}
	return nil
}

func (g *gateway) unload(worker *modelWorker) {
	g.loadMu.Lock()
	defer g.loadMu.Unlock()
	g.unloadLocked(worker)
}

func (g *gateway) unloadLocked(worker *modelWorker) bool {
	g.mu.Lock()
	if g.workers[worker.key] != worker || worker.active != 0 {
		g.mu.Unlock()
		return false
	}
	worker.state = "unloading"
	g.recordLocked(worker, "")
	g.mu.Unlock()
	_ = executil.KillProcessTree(worker.cmd)
	<-worker.done
	g.mu.Lock()
	if g.workers[worker.key] == worker {
		delete(g.workers, worker.key)
		if _, installed := g.models[worker.modelID]; installed {
			g.states[worker.modelID] = modelState{Model: worker.modelID, State: "unloaded", Nodes: planNodeNames(worker.plan), UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
		} else {
			delete(g.states, worker.modelID)
		}
	}
	g.mu.Unlock()
	return true
}

func (g *gateway) launch(ctx context.Context, modelID, modelPath string, plan placement.Plan) (*modelWorker, error) {
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
	apiKey, err := randomToken(32)
	if err != nil {
		return nil, fmt.Errorf("creating model worker credential: %w", err)
	}
	for _, node := range plan.Nodes {
		if node.Local && node.Device != "" && plan.Mode == "whole" {
			args = append(args, "--device", node.Device)
		}
	}
	cmd := exec.CommandContext(g.ctx, g.cfg.llamaServer, args...)
	executil.IsolateProcessTree(cmd)
	cmd.Cancel = func() error { return executil.KillProcessTree(cmd) }
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(os.Environ(), "LLAMA_API_KEY="+apiKey)
	if !planUsesLocalGPU(plan) {
		cmd.Env = append(cmd.Env, "CUDA_VISIBLE_DEVICES=", "GGML_CUDA_VISIBLE_DEVICES=")
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	releaseLifetime, err := executil.StartWithParentLifetime(cmd)
	if err != nil {
		return nil, fmt.Errorf("starting model worker: %w", err)
	}
	worker := &modelWorker{modelID: modelID, model: modelPath, plan: plan, address: address, cmd: cmd, done: make(chan struct{}), apiKey: apiKey}
	go func() {
		worker.exitErr = cmd.Wait()
		releaseLifetime()
		close(worker.done)
	}()
	if err := waitForWorker(ctx, address, worker, g.cfg.workerStartTimeout); err != nil {
		_ = executil.KillProcessTree(cmd)
		<-worker.done
		return nil, err
	}
	log.Printf("loaded %s using %s placement on %s", modelID, plan.Mode, strings.Join(planNodeNames(plan), ", "))
	return worker, nil
}

func waitForWorker(ctx context.Context, address string, worker *modelWorker, timeout time.Duration) error {
	startupCtx, cancelStartup := context.WithTimeout(ctx, timeout)
	defer cancelStartup()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	client := &http.Client{Timeout: time.Second}
	probe := func() (bool, error) {
		probeCtx, cancel := context.WithTimeout(startupCtx, time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, "http://"+address+"/health", nil)
		if err != nil {
			return false, fmt.Errorf("creating worker readiness probe: %w", err)
		}
		resp, err := client.Do(request)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true, nil
			}
		}
		return false, nil
	}
	for {
		ready, err := probe()
		if err != nil || ready {
			return err
		}
		select {
		case <-startupCtx.Done():
			if ctx.Err() != nil {
				return fmt.Errorf("model worker startup canceled: %w", ctx.Err())
			}
			return fmt.Errorf("model worker did not become ready within %s", timeout)
		case <-worker.done:
			return fmt.Errorf("model worker exited during startup: %v", worker.exitErr)
		case <-ticker.C:
		}
	}
}

func randomToken(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func planUsesLocalGPU(plan placement.Plan) bool {
	for _, node := range plan.Nodes {
		if node.Local {
			return true
		}
	}
	return false
}

func (g *gateway) watchWorker(worker *modelWorker) {
	<-worker.done
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.workers[worker.key] != worker {
		return
	}
	if worker.state == "unloading" {
		if worker.timer != nil {
			worker.timer.Stop()
			worker.timer = nil
		}
		delete(g.workers, worker.key)
		if _, installed := g.models[worker.modelID]; installed {
			g.states[worker.modelID] = modelState{Model: worker.modelID, State: "unloaded", Nodes: planNodeNames(worker.plan), UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
		} else {
			delete(g.states, worker.modelID)
		}
		return
	}
	g.markCrashedLocked(worker)
	log.Printf("model worker %s crashed: %v", worker.modelID, worker.exitErr)
}

func (g *gateway) markCrashedLocked(worker *modelWorker) {
	if worker.timer != nil {
		worker.timer.Stop()
		worker.timer = nil
	}
	delete(g.workers, worker.key)
	detail := "model worker exited unexpectedly"
	if worker.exitErr != nil {
		detail = worker.exitErr.Error()
	}
	g.states[worker.modelID] = modelState{Model: worker.modelID, State: "crashed", Nodes: planNodeNames(worker.plan), Detail: detail, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
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
	states := make([]modelState, 0, len(g.models)+len(g.states))
	emitted := make(map[string]bool, len(g.models)+len(g.states))
	for model := range g.models {
		state, ok := g.states[model]
		if !ok {
			state = modelState{Model: model, State: "unloaded", UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
		}
		states = append(states, state)
		emitted[model] = true
	}
	for model, state := range g.states {
		if emitted[model] || g.workerForModelLocked(model) == nil {
			continue
		}
		state.State = "removed"
		state.Detail = "Removed from the library; unload required."
		states = append(states, state)
	}
	g.mu.Unlock()
	sort.Slice(states, func(i, j int) bool { return states[i].Model < states[j].Model })
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"models": states})
}

func (g *gateway) shutdown(ctx context.Context) {
	g.cancel()
	g.loadMu.Lock()
	defer g.loadMu.Unlock()
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
		select {
		case <-worker.done:
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
