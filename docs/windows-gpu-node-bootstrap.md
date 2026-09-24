# Windows GPU-node bootstrap

Use this guide to prepare a Windows machine that will contribute an NVIDIA GPU
to Tether. The Agent runs locally on that machine; the Orchestrator never
chooses its executable path or network bind address.

## Prerequisites

- An NVIDIA GPU with a working driver.
- Tailscale, signed in to the Tailnet that contains the Orchestrator.
- NVIDIA CUDA Toolkit, CMake, and Visual Studio 2022 Build Tools with the C++
  workload, if building llama.cpp manually.
- `tether-agent.exe`.

The Agent desktop app provides an audited local setup flow that checks these
prerequisites, can guide Tailscale sign-in, builds the pinned CUDA/RPC backend,
and writes the local Agent configuration. It does not pair automatically.

## Manual llama.cpp build

If you need to build the RPC server yourself, clone llama.cpp and run:

```powershell
cmake -S . -B build-rpc-cuda -G 'Visual Studio 17 2022' -A x64 `
  -DGGML_CUDA=ON -DGGML_RPC=ON
cmake --build build-rpc-cuda --config Release --target ggml-rpc-server --parallel 4
```

The result is normally
`build-rpc-cuda\bin\Release\ggml-rpc-server.exe`. Keep its adjacent DLLs in
place. Configure the Agent locally at `%AppData%\tether\agent_config.yaml`:

```yaml
rpc_server_path: C:\path\to\ggml-rpc-server.exe
rpc_listen_host: 100.x.y.z
```

Use the node's Tailscale IPv4 address for `rpc_listen_host`.

## Pair and operate

1. Add the node's Tailscale hostname and ports to the Orchestrator's
   `node_allowlist.yaml`.
2. Start `tether-agent.exe` and complete its local preflight/setup.
3. In the Orchestrator, select the online allowlisted node and enter the
   Agent's displayed, single-use pairing code.
4. In the Tailnet policy, allow only the specific Orchestrator to reach TCP
   7420 and the configured RPC port. The bootstrap's Windows Firewall rules
   permit the Tailnet range and are only a broad host-level backstop; they do
   not authorize all Tailnet peers to use the Agent or RPC server. See the
   [network-policy example](configuration.md#network-policy).

Never expose the Agent or RPC port to a public network.
