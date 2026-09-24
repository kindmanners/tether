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

package placement

import "testing"

func TestSelectPrefersOneNodeThatFits(t *testing.T) {
	plan, err := Select([]Node{
		{Hostname: "small", GPUFreeBytes: []int64{4}},
		{Hostname: "large", GPUFreeBytes: []int64{12}},
	}, Requirement{ModelBytes: 7, KVCacheBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != "whole" || len(plan.Nodes) != 1 || plan.Nodes[0].Hostname != "large" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestSelectPinsWholeModelToChosenLocalGPU(t *testing.T) {
	plan, err := Select([]Node{{Hostname: "orchestrator", Local: true, GPUFreeBytes: []int64{4, 12}}}, Requirement{ModelBytes: 7, KVCacheBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Nodes[0].Device; got != "CUDA1" {
		t.Fatalf("selected device = %q, want CUDA1", got)
	}
}

func TestSelectFallsBackToMesh(t *testing.T) {
	plan, err := Select([]Node{
		{Hostname: "one", GPUFreeBytes: []int64{4}},
		{Hostname: "two", GPUFreeBytes: []int64{5}},
	}, Requirement{ModelBytes: 7, KVCacheBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != "split" || len(plan.Nodes) != 2 {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestSelectRejectsInsufficientClusterCapacity(t *testing.T) {
	_, err := Select([]Node{{Hostname: "one", GPUFreeBytes: []int64{4}}, {Hostname: "two", GPUFreeBytes: []int64{3}}}, Requirement{ModelBytes: 7, KVCacheBytes: 1})
	if err == nil {
		t.Fatal("Select succeeded despite insufficient capacity")
	}
}
