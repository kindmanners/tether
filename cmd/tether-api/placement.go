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
	"context"
	"fmt"
	"math"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tether/internal/agent"
	"tether/internal/certs"
	"tether/internal/gguf"
	"tether/internal/placement"
	"tether/internal/registry"
	"tether/internal/trust"
)

const (
	placementProbeTimeout     = 12 * time.Second
	placementProbeConcurrency = 4
)

type agentSelectionMode int

const (
	agentsNone agentSelectionMode = iota
	agentsAuto
	agentsExplicit
)

type agentSelection struct {
	mode      agentSelectionMode
	hostnames []string
}

func parseAgentSelection(value string) (agentSelection, error) {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "none":
		return agentSelection{mode: agentsNone}, nil
	case "auto":
		return agentSelection{mode: agentsAuto}, nil
	case "":
		return agentSelection{}, fmt.Errorf("Agent selection must be auto, none, or comma-separated paired Agent hostnames")
	}
	parts := strings.Split(value, ",")
	seen := make(map[string]bool, len(parts))
	for i, part := range parts {
		hostname := strings.TrimSpace(part)
		if hostname == "" {
			return agentSelection{}, fmt.Errorf("Agent selection contains an empty hostname")
		}
		if strings.EqualFold(hostname, "auto") || strings.EqualFold(hostname, "none") || strings.ContainsAny(hostname, ":[]/") {
			return agentSelection{}, fmt.Errorf("%q is not an Agent hostname; replace legacy host:port RPC endpoints with paired Agent hostnames", hostname)
		}
		if seen[hostname] {
			return agentSelection{}, fmt.Errorf("Agent hostname %q is duplicated", hostname)
		}
		seen[hostname] = true
		parts[i] = hostname
	}
	return agentSelection{mode: agentsExplicit, hostnames: parts}, nil
}

// planFor obtains a fresh Agent capability response immediately before a new
// model worker is launched. A running worker is intentionally reused; its
// reservation is already accounted for by the node it occupies.
func (g *gateway) planFor(modelPath string) (placement.Plan, error) {
	size, err := gguf.Size(modelPath)
	if err != nil {
		return placement.Plan{}, fmt.Errorf("checking model file: %w", err)
	}
	modelBytes := int64(math.Ceil(float64(size) * g.cfg.modelOverhead))
	requirement := placement.Requirement{ModelBytes: modelBytes, KVCacheBytes: int64(g.cfg.ctxSize) * g.cfg.kvBytesPerToken}
	switch g.cfg.agentSelection.mode {
	case agentsNone:
		if !g.cfg.localGPU {
			return placement.Plan{}, fmt.Errorf("local-only placement is disabled because this Orchestrator is not contributing a GPU")
		}
		return placement.Plan{Mode: "local", Nodes: []placement.Node{{Hostname: "orchestrator", Local: true}}, Requirement: requirement}, nil
	case agentsAuto, agentsExplicit:
		nodes, err := discoverPlacementNodes(g.cfg.allowlistPath, g.cfg.localGPU, g.cfg.agentSelection)
		if err != nil {
			return placement.Plan{}, err
		}
		return placement.Select(nodes, requirement)
	default:
		return placement.Plan{}, fmt.Errorf("invalid Agent selection")
	}
}

func discoverPlacementNodes(allowlistPath string, localGPU bool, selection agentSelection) ([]placement.Node, error) {
	allowlist, err := registry.LoadAllowlist(allowlistPath)
	if err != nil {
		return nil, err
	}
	peers, err := registry.QueryTailscalePeers()
	if err != nil {
		return nil, err
	}
	identity, err := certs.LoadOrCreate(orchestratorIdentityName)
	if err != nil {
		return nil, err
	}
	self, selfErr := registry.SelfHostname()
	regNodes := registry.Build(allowlist, peers).Online()
	filtered := regNodes[:0]
	for _, node := range regNodes {
		if node.Role == "rpc-node" {
			filtered = append(filtered, node)
		}
	}
	regNodes = filtered
	sort.Slice(regNodes, func(i, j int) bool { return regNodes[i].Hostname < regNodes[j].Hostname })
	if selection.mode == agentsExplicit {
		byName := make(map[string]*registry.Node, len(regNodes))
		for _, node := range regNodes {
			byName[node.Hostname] = node
		}
		selected := make([]*registry.Node, 0, len(selection.hostnames))
		for _, hostname := range selection.hostnames {
			node, ok := byName[hostname]
			if !ok {
				return nil, fmt.Errorf("selected Agent %q is unknown, offline, or not an rpc-node", hostname)
			}
			selected = append(selected, node)
		}
		regNodes = selected
	}
	ctx, cancel := context.WithTimeout(context.Background(), placementProbeTimeout)
	defer cancel()
	result := probePlacementNodes(ctx, regNodes, placementProbeConcurrency, func(ctx context.Context, node *registry.Node) (placement.Node, bool) {
		if selfErr == nil && node.Hostname == self {
			if !localGPU {
				return placement.Node{}, false
			}
			gpus, err := localPlacementGPUsContext(ctx)
			if err != nil {
				return placement.Node{}, false
			}
			return placement.Node{Hostname: node.Hostname, Local: true, GPUFreeBytes: gpuFreeBytes(gpus)}, true
		}
		tlsConfig, err := trust.PinnedTLSConfig(identity.TLSCertificate(), node.Hostname, false)
		if err != nil {
			return placement.Node{}, false
		}
		client := agent.NewClient(tlsConfig)
		addr := fmt.Sprintf("%s:%d", node.TailscaleIP, node.AgentPort)
		status, err := client.GetStatusContext(ctx, addr)
		if err != nil || status.Status != "Running" {
			return placement.Node{}, false
		}
		capabilities, err := client.GetCapabilitiesContext(ctx, addr)
		if err != nil {
			return placement.Node{}, false
		}
		return placement.Node{Hostname: node.Hostname, AgentAddress: fmt.Sprintf("%s:%d", node.TailscaleIP, node.AgentPort), GPUFreeBytes: gpuFreeBytes(capabilities.GPUs)}, true
	})
	if selection.mode == agentsExplicit && len(result) != len(regNodes) {
		return nil, fmt.Errorf("one or more selected Agents are unpaired, unreachable, or not running RPC")
	}
	return result, nil
}

type placementProbe func(context.Context, *registry.Node) (placement.Node, bool)

func probePlacementNodes(ctx context.Context, nodes []*registry.Node, concurrency int, probe placementProbe) []placement.Node {
	if concurrency < 1 {
		concurrency = 1
	}
	type probeResult struct {
		node placement.Node
		ok   bool
	}
	results := make([]probeResult, len(nodes))
	jobs := make(chan int, len(nodes))
	for i := range nodes {
		jobs <- i
	}
	close(jobs)

	workerCount := min(concurrency, len(nodes))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
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
					results[index].node, results[index].ok = probe(ctx, nodes[index])
				}
			}
		}()
	}
	workers.Wait()

	result := make([]placement.Node, 0, len(nodes))
	for _, probed := range results {
		if probed.ok {
			result = append(result, probed.node)
		}
	}
	return result
}

func gpuFreeBytes(gpus []agent.GPUCapability) []int64 {
	values := make([]int64, 0, len(gpus))
	for _, gpu := range gpus {
		values = append(values, gpu.VRAMFreeBytes)
	}
	return values
}

func localPlacementGPUsContext(ctx context.Context) ([]agent.GPUCapability, error) {
	output, err := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=memory.free", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil, err
	}
	lines := strings.FieldsFunc(string(output), func(r rune) bool { return r == '\n' || r == '\r' })
	gpus := make([]agent.GPUCapability, 0, len(lines))
	for _, line := range lines {
		mib, err := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing nvidia-smi free VRAM %q: %w", line, err)
		}
		gpus = append(gpus, agent.GPUCapability{VRAMFreeBytes: mib * 1024 * 1024})
	}
	return gpus, nil
}
