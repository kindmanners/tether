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

import "testing"

func TestLlamaServerEnvironmentPreservesRPCTunnelContainment(t *testing.T) {
	env := llamaServerEnvironment([]string{"PATH=test"}, "worker-key", false)
	want := []string{
		"PATH=test",
		"LLAMA_API_KEY=worker-key",
		"GGML_RPC_NO_COMM=1",
		"CUDA_VISIBLE_DEVICES=",
		"GGML_CUDA_VISIBLE_DEVICES=",
	}
	if len(env) != len(want) {
		t.Fatalf("environment length = %d, want %d: %#v", len(env), len(want), env)
	}
	for i := range want {
		if env[i] != want[i] {
			t.Fatalf("environment[%d] = %q, want %q", i, env[i], want[i])
		}
	}
}
