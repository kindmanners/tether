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

// Package placement contains the deterministic, VRAM-only placement policy
// used by the Orchestrator.  It deliberately knows nothing about HTTP or
// llama.cpp processes, which keeps the admission decision testable.
package placement

import (
	"fmt"
	"sort"
)

// Node is a GPU contributor observed by the Orchestrator. Endpoint is empty
// for the Orchestrator's own CUDA backend; a remote node has an RPC endpoint.
type Node struct {
	Hostname     string
	Endpoint     string
	Local        bool
	Device       string
	GPUFreeBytes []int64
}

// Requirement is the conservative memory reservation for one model worker.
// ModelBytes includes the GGUF file and runtime overhead; KVCacheBytes is a
// configured context reservation. They stay separate for explainable logs/UI.
type Requirement struct {
	ModelBytes   int64
	KVCacheBytes int64
}

func (r Requirement) TotalBytes() int64 { return r.ModelBytes + r.KVCacheBytes }

// Plan is either a whole-model placement on one GPU node or a fallback split
// over all usable contributors. llama.cpp owns layer distribution in split
// mode; Tether only admits a plan that has enough reported capacity.
type Plan struct {
	Mode        string
	Nodes       []Node
	Requirement Requirement
}

// Select first attempts whole-model placement.  This is the low-latency path
// for models that fit one GPU.  If none can fit, all usable nodes become the
// existing RPC mesh fallback, but only after a cluster-wide capacity check.
func Select(nodes []Node, requirement Requirement) (Plan, error) {
	if requirement.ModelBytes <= 0 || requirement.KVCacheBytes < 0 {
		return Plan{}, fmt.Errorf("invalid model memory requirement")
	}
	need := requirement.TotalBytes()
	usable := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		if largest(node.GPUFreeBytes) > 0 {
			usable = append(usable, node)
		}
	}
	if len(usable) == 0 {
		return Plan{}, fmt.Errorf("no running GPU nodes reported free VRAM")
	}
	sort.Slice(usable, func(i, j int) bool {
		left, right := largest(usable[i].GPUFreeBytes), largest(usable[j].GPUFreeBytes)
		if left == right {
			return usable[i].Hostname < usable[j].Hostname
		}
		return left > right
	})
	if largest(usable[0].GPUFreeBytes) >= need {
		if usable[0].Local {
			usable[0].Device = fmt.Sprintf("CUDA%d", largestIndex(usable[0].GPUFreeBytes))
		}
		return Plan{Mode: "whole", Nodes: []Node{usable[0]}, Requirement: requirement}, nil
	}

	var total int64
	for _, node := range usable {
		for _, free := range node.GPUFreeBytes {
			if free > 0 {
				total += free
			}
		}
	}
	if total < need {
		return Plan{}, fmt.Errorf("insufficient reported free VRAM: need %d bytes (model %d + KV cache %d), cluster has %d bytes", need, requirement.ModelBytes, requirement.KVCacheBytes, total)
	}
	return Plan{Mode: "split", Nodes: usable, Requirement: requirement}, nil
}

func largest(values []int64) int64 {
	var result int64
	for _, value := range values {
		if value > result {
			result = value
		}
	}
	return result
}

func largestIndex(values []int64) int {
	index := 0
	for i := 1; i < len(values); i++ {
		if values[i] > values[index] {
			index = i
		}
	}
	return index
}
