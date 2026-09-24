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
	"fmt"
	"math"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"tether/internal/agent"
	"tether/internal/certs"
	"tether/internal/placement"
	"tether/internal/registry"
	"tether/internal/trust"
)

// planFor obtains a fresh Agent capability response immediately before a new
// model worker is launched. A running worker is intentionally reused; its
// reservation is already accounted for by the node it occupies.
func (g *gateway) planFor(modelPath string) (placement.Plan, error) {
	info, err := os.Stat(modelPath)
	if err != nil {
		return placement.Plan{}, fmt.Errorf("checking model file: %w", err)
	}
	modelBytes := int64(math.Ceil(float64(info.Size()) * g.cfg.modelOverhead))
	requirement := placement.Requirement{ModelBytes: modelBytes, KVCacheBytes: int64(g.cfg.ctxSize) * g.cfg.kvBytesPerToken}
	switch strings.ToLower(strings.TrimSpace(g.cfg.rpcMode)) {
	case "", "none":
		if !g.cfg.localGPU {
			return placement.Plan{}, fmt.Errorf("local-only placement is disabled because this Orchestrator is not contributing a GPU")
		}
		return placement.Plan{Mode: "local", Nodes: []placement.Node{{Hostname: "orchestrator", Local: true}}, Requirement: requirement}, nil
	case "auto":
		nodes, err := discoverPlacementNodes(g.cfg.allowlistPath, g.cfg.localGPU)
		if err != nil {
			return placement.Plan{}, err
		}
		return placement.Select(nodes, requirement)
	default:
		endpoints, err := resolveRPCEndpoints(g.cfg.rpcMode, g.cfg.allowlistPath)
		if err != nil {
			return placement.Plan{}, err
		}
		nodes := make([]placement.Node, 0, len(endpoints))
		for _, endpoint := range endpoints {
			nodes = append(nodes, placement.Node{Hostname: endpoint, Endpoint: endpoint, GPUFreeBytes: []int64{1}})
		}
		return placement.Plan{Mode: "manual", Nodes: nodes, Requirement: requirement}, nil
	}
}

func discoverPlacementNodes(allowlistPath string, localGPU bool) ([]placement.Node, error) {
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
	sort.Slice(regNodes, func(i, j int) bool { return regNodes[i].Hostname < regNodes[j].Hostname })
	result := make([]placement.Node, 0, len(regNodes))
	for _, node := range regNodes {
		if selfErr == nil && node.Hostname == self {
			if !localGPU {
				continue
			}
			gpus, err := localPlacementGPUs()
			if err != nil {
				continue
			}
			result = append(result, placement.Node{Hostname: node.Hostname, Local: true, GPUFreeBytes: gpuFreeBytes(gpus)})
			continue
		}
		tlsConfig, err := trust.PinnedTLSConfig(identity.TLSCertificate(), node.Hostname, false)
		if err != nil {
			continue
		}
		client := agent.NewClient(tlsConfig)
		addr := fmt.Sprintf("%s:%d", node.TailscaleIP, node.AgentPort)
		status, err := client.GetStatus(addr)
		if err != nil || status.Status != "Running" {
			continue
		}
		capabilities, err := client.GetCapabilities(addr)
		if err != nil {
			continue
		}
		result = append(result, placement.Node{Hostname: node.Hostname, Endpoint: fmt.Sprintf("%s:%d", node.TailscaleIP, node.RPCPort), GPUFreeBytes: gpuFreeBytes(capabilities.GPUs)})
	}
	return result, nil
}

func gpuFreeBytes(gpus []agent.GPUCapability) []int64 {
	values := make([]int64, 0, len(gpus))
	for _, gpu := range gpus {
		values = append(values, gpu.VRAMFreeBytes)
	}
	return values
}

func localPlacementGPUs() ([]agent.GPUCapability, error) {
	output, err := exec.Command("nvidia-smi", "--query-gpu=memory.free", "--format=csv,noheader,nounits").Output()
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
