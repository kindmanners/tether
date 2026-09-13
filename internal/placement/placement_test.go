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
