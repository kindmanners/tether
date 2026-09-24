# Linux GPU-node bootstrap

Use this guide to prepare a Linux machine that will contribute an NVIDIA GPU
to Tether. The local `tether-agent` setup flow checks the driver, CUDA,
Tailscale, build tools, and Agent configuration before it starts its command
service.

## Prerequisites

- An NVIDIA GPU with a working driver and CUDA environment.
- Tailscale, authenticated to the same Tailnet as the Orchestrator.
- Git, CMake, and a C/C++ compiler if building llama.cpp manually.
- `tether-agent`.

The Linux Agent can guide an explicit local setup flow. It builds the pinned
CUDA/RPC server and writes local configuration; it does not create pairing
credentials or alter the Orchestrator's allowlist.

## Manual llama.cpp build

Build a CUDA-enabled RPC server from a llama.cpp checkout:

```bash
cmake -S . -B build-rpc-cuda -DGGML_CUDA=ON -DGGML_RPC=ON -DCMAKE_BUILD_TYPE=Release
cmake --build build-rpc-cuda --target ggml-rpc-server -j"$(nproc)"
```

Configure the Agent in `~/.config/tether/agent_config.yaml` (or under
`$XDG_CONFIG_HOME/tether`):

```yaml
rpc_server_path: /opt/llama.cpp/build-rpc-cuda/bin/ggml-rpc-server
rpc_listen_host: 100.x.y.z
```

Set `rpc_listen_host` to the node's Tailscale IPv4 address.

## Pair and operate

1. Add the node's hostname, Agent port, and RPC port to the Orchestrator
   `node_allowlist.yaml`.
2. Complete local Agent preflight or setup and start `tether-agent`.
3. Pair from the Orchestrator with the displayed one-time pairing code.
4. In the Tailnet policy, allow only the specific Orchestrator to reach TCP
   7420 and the configured RPC port. Match any host-firewall rule to that
   policy; a whole-Tailnet allow rule is only a broad backstop. See the
   [network-policy example](configuration.md#network-policy).

Do not expose either service to the public internet.
