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

// llamaServerEnvironment applies the containment settings required for a
// Tether-managed llama-server process. ggml-rpc's optional collective path
// creates direct server-to-server connections, which bypass Tether's
// certificate-pinned CONNECT tunnels and loopback-only RPC policy.
func llamaServerEnvironment(parent []string, apiKey string, localGPU bool) []string {
	env := append([]string{}, parent...)
	env = append(env,
		"LLAMA_API_KEY="+apiKey,
		"GGML_RPC_NO_COMM=1",
	)
	if !localGPU {
		env = append(env, "CUDA_VISIBLE_DEVICES=", "GGML_CUDA_VISIBLE_DEVICES=")
	}
	return env
}
