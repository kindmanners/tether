# Tether

Tether is a desktop control plane for running distributed local LLM inference across multiple machines on a private network. It uses llama.cpp's built-in RPC mode to split model layers across GPUs on different physical devices, pooling VRAM without requiring a dedicated cluster or cloud infrastructure.

 ## Overview


 Running distributed llama.cpp RPC inference manually requires SSHing into multiple hosts, manually launching rpc-server binaries with specific flags, remembering IP assignments, and guessing whether nodes are healthy. Tether replaces this manual workflow with a lightweight, secure control plane:

 ### Node Registry: Automatically discovers hosts on your private Tailscale network and filters them through an app-level allowlist.

 ### Daemon Agent: A small remote agent on each GPU host manages and monitors local llama.cpp rpc-server processes.

 ### mTLS Security: Built-in, zero-trust mutual TLS with automated out-of-band pairing between nodes over Tailscale.

 ### Desktop Interface: A native cross-platform GUI built with Wails and Go for cluster orchestration and chat.

## System Architecture

Tether consists of three primary components:
```
┌─────────────────────────────────────────┐
│           Tether Orchestrator           │
│         (Desktop App: Go + Wails)       │
└────────────────────┬────────────────────┘
│
│  mTLS over Tailscale
▼
┌─────────────────────────────────────────┐
│              Tether Agent               │
│        (Lightweight Daemon per Node)    │
└────────────────────┬────────────────────┘
│
│  Spawns / Manages Subprocess
▼
┌─────────────────────────────────────────┐
│           llama.cpp rpc-server          │
│          (Local GPU Inference)          │
└─────────────────────────────────────────┘
```

### Components

1. **Orchestrator**
   - The central desktop application.
   - Monitors node health, manages cluster states, issues remote RPC commands, and routes user chat queries across the live inference cluster.

2. **Agent**
   - A lightweight background daemon running on each inference node.
   - Listens for mTLS commands from the Orchestrator to start, stop, or report on the local `llama.cpp rpc-server` process.

3. **Inference Backend (`llama.cpp rpc-server`)**
   - The native `llama.cpp` RPC server executable spawned locally by the Agent on GPU host machines.

---

## Build

### Tether executables

`tether` and `tether-agent` are Wails desktop applications. Their HTML, CSS,
and JavaScript assets are embedded in the Go binaries, so no Node installation
is required to build the checked-in interface. Install Go 1.22 or later and
the platform dependencies required by Wails, then build the desktop pair from
the repository root:

```bash
go build -tags production -o bin/tether ./cmd/tether
go build -tags production -o bin/tether-agent ./cmd/tether-agent
go build -o bin/tether-api ./cmd/tether-api
go build -o bin/tether-dashboard ./cmd/tether-dashboard
```

On Windows, use the release build script. It embeds Wails' Microsoft WebView2
bootstrapper in both desktop applications, so a machine without WebView2 is
prompted to install it on first launch:

```powershell
.\scripts\build-windows-release.ps1
```

The equivalent manual commands are:

```powershell
go build -tags 'production,wv2runtime.embed' -ldflags '-H windowsgui' -o .\bin\tether.exe .\cmd\tether
go build -tags 'production,wv2runtime.embed' -ldflags '-H windowsgui' -o .\bin\tether-agent.exe .\cmd\tether-agent
go build -o .\bin\tether-api.exe .\cmd\tether-api
```

Keep `tether-api` beside `tether` in a packaged installation. `tether` starts
that sibling automatically and makes the local OpenAI-compatible endpoint
available at `http://127.0.0.1:11435/v1`; it remains responsible for model
worker lifecycle and will report a missing model library or `llama-server`
clearly. `tether-dashboard` is still the independent, read-only browser
dashboard and is not part of the desktop UI.

### Orchestrator GPU role

The Orchestrator can either contribute its own NVIDIA GPU or run in
**control-only mode**. Use the **Use control-only mode** / **Contribute this
GPU** button in the desktop app to choose the role for that machine. The
choice is saved locally and restarts Tether's gateway so subsequent model
placement uses the new role.

The Orchestrator desktop app checks every paired Agent over pinned mTLS when
opened and then every five minutes while it is running. Each result is logged
with its UTC timestamp and round-trip latency in milliseconds; the latest
check appears on the node card. A running Agent RPC server disables **Start
RPC** until it is stopped. The UI also includes a persisted light/dark theme.

Every Orchestrator needs a local RPC-enabled `llama-server` to host model
workers and connect to remote Agents. On a new Linux Orchestrator, choose
**Prepare RPC backend** to build that server without CUDA. Choosing
**Contribute this GPU** instead exposes **Prepare CUDA backend**, which
explicitly installs missing build prerequisites when supported and builds
`llama-server` with both `GGML_CUDA=ON` and `GGML_RPC=ON`. Control-only mode
does not probe for, install, or compile CUDA. The setup is local to
`$XDG_DATA_HOME/tether/llama.cpp` and does not create an Agent, pair a node,
or change firewall/Tailscale settings.

On each GPU machine, `tether-agent` first performs a read-only local
preflight. It shows Tailscale, the Agent certificate, pinned Orchestrator
trust, the llama.cpp RPC server/configuration, and the GPU bootstrap report as
a clear checklist. Linux also shows NVIDIA driver/GPU, CUDA, configured local
Tailscale IPv4, Git/CMake/compiler, and the selected Agent/RPC ports. When
every check passes, no setup action is shown: the Agent starts its mTLS command
service and the next step is simply to use Tether on the paired Orchestrator.

A new Windows GPU node can start from just `tether-agent.exe`; its elevated
**Run audited local setup** flow visibly installs missing prerequisites,
handles Tailscale sign-in, builds the pinned llama.cpp CUDA RPC server, writes
local-only Agent configuration, and adds Tailnet-scoped firewall rules. Linux
offers the same explicit local action with live stage updates and a persistent
log while Git/CMake run. It builds the pinned CUDA/RPC server and writes only
the local Agent config and capability report; it gives narrow firewall guidance
instead of changing host firewall rules automatically. Neither platform
provisions pairing credentials or lets the Orchestrator choose a local binary
or bind address.

If Tailscale is absent, that same flow installs it with WinGet and deliberately
opens the normal Tailscale sign-in flow. The person running setup must log in
to the shared Tailnet (or accept its invitation); Tether never attempts to
provide credentials or automate that human authorization. Back on the
Orchestrator, use **Add Tailnet node**, select the online machine, and then
enter its displayed single-use pairing code. Adding it is an explicit local
allowlist decision; pairing pins both certificates before the Agent accepts
commands. The Orchestrator cannot set an Agent executable path or bind address
remotely.

### OpenAI-compatible API

`tether-api` is Tether's OpenAI-compatible inference gateway. It exposes
`GET /v1/models` and `POST /v1/chat/completions`, so Open WebUI and other
OpenAI-compatible clients can use Tether's model library and GPU nodes:

```bash
go run ./cmd/tether-api --listen 127.0.0.1:11435 --rpc auto
```

`--rpc auto` asks online, paired running Agents for current free VRAM at model
load time. It prefers a whole-model GPU placement and only falls back to RPC
splitting after capacity admission. To explicitly select endpoints, pass
`--rpc 100.x.y.z:50053,100.x.y.z:50054`. To run local-only, use `--rpc none`.
The API is loopback-only by default. When intentionally binding it to a
Tailnet/LAN address, set a strong API key with `--api-key`.

See [OpenAI API gateway](docs/openai-api.md) for Open WebUI configuration.
See [placement and model lifecycle](docs/placement.md) for the benchmark and
worker eviction policy.

### Orchestrator dashboard

The browser dashboard is a read-only local view of the cluster. It discovers
allowlisted nodes through Tailscale, obtains GPU/VRAM reports only from paired
Agents over pinned mTLS, reads its own NVIDIA VRAM directly when it is an
allowlisted GPU node, and scans the Orchestrator's local GGUF model directory:

```bash
go run ./cmd/tether-dashboard
```

Open `http://127.0.0.1:8080`. The default model directory is `~/models`; use
`-models-dir /path/to/models` when your GGUF library lives elsewhere. The UI
assets and dashboard API contract are in `web/dashboard/`.

### CUDA RPC server on a Windows GPU node

The GPU node also needs a CUDA-enabled `ggml-rpc-server` from llama.cpp. Install
the NVIDIA CUDA Toolkit, CMake, and Visual Studio 2022 Build Tools with the C++
workload. Then clone llama.cpp beside or outside this repository:

```powershell
git clone https://github.com/ggml-org/llama.cpp.git
cd llama.cpp

$cuda = 'C:\Program Files\NVIDIA GPU Computing Toolkit\CUDA\v13.4'
$env:CUDA_PATH = $cuda
$env:CUDA_PATH_V13_4 = $cuda
$env:CudaToolkitDir = "$cuda\"
$env:PATH = "$cuda\bin;$cuda\bin\x64;$env:PATH"

cmake -S . -B build-rpc-cuda -G 'Visual Studio 17 2022' -A x64 `
  -DGGML_CUDA=ON -DGGML_RPC=ON
cmake --build build-rpc-cuda --config Release --target ggml-rpc-server --parallel 4
```

The resulting executable is
`build-rpc-cuda\bin\Release\ggml-rpc-server.exe`. Keep the DLLs in that same
`Release` directory; the executable depends on them. The CUDA runtime DLLs are
normally installed in `CUDA\v13.4\bin\x64`, which must be on `PATH` when the
Agent starts.

Configure the Agent locally on the Windows GPU node at
`%AppData%\tether\agent_config.yaml`:

```yaml
rpc_server_path: 'C:\path\to\llama.cpp\build-rpc-cuda\bin\Release\ggml-rpc-server.exe'
rpc_listen_host: '100.x.y.z' # this node's Tailscale IPv4 address
```

Allow Tailnet access to TCP 7420 (Agent control) and the configured RPC port,
both in your Tailnet ACLs and Windows Firewall. Do not expose the RPC server to
a public network. On Mathesis, use TCP **50053**: TCP 50052 is occupied by
Incredibuild `LicenseService.exe` (see `HANDOFF.md`).

For an automated Windows GPU-node audit/provisioning flow, including CUDA
toolchain checks, port collision detection, firewall setup, and the pinned
llama.cpp CUDA RPC build, see
[Windows GPU node bootstrap](docs/windows-gpu-node-bootstrap.md).

For the Linux GPU-node flow, its supported package-manager behavior,
persistent log location, CUDA/RPC build, firewall guidance, and unchanged
pairing flow, see [Linux GPU node bootstrap](docs/linux-gpu-node-bootstrap.md).

### Linux Orchestrator llama.cpp build

For remote GPU nodes only, the Orchestrator needs RPC support:

```bash
git clone https://github.com/ggml-org/llama.cpp.git
cd llama.cpp
cmake -S . -B build-rpc -DGGML_RPC=ON -DCMAKE_BUILD_TYPE=Release
cmake --build build-rpc -j"$(nproc)"
```

If the Orchestrator also contributes its own NVIDIA GPU, build CUDA and RPC
together. `tether-api --rpc auto` checks that the selected server actually
lists a CUDA backend before it considers the local GPU for placement:

```bash
cmake -S . -B build-rpc-cuda -DGGML_CUDA=ON -DGGML_RPC=ON -DCMAKE_BUILD_TYPE=Release
cmake --build build-rpc-cuda --target llama-server -j"$(nproc)"
```

Use the resulting `build-rpc/bin/llama-cli` with the GPU node's Tailscale
endpoint, for example `--rpc 100.x.y.z:50053` when connecting to Mathesis.

---

## Discovery & Registry

Tether uses a **two-layer verification process** to safely identify and connect to valid inference nodes:

1. **Tailnet Presence**: Queries local Tailscale status to discover active peers, MagicDNS hostnames, and private IPs.
2. **App-Level Allowlist**: Compares discovered hosts against a local configuration file (`node_allowlist.yaml`).

A host is only registered as a Tether Node if it appears in both Tailscale's active peer list and the allowlist. This prevents unintended nodes on your network (like phones or non-GPU machines) from being queried.

---

## Security Model

Tether relies on strict mutual TLS (mTLS) for all Orchestrator-to-Agent communications, ensuring remote process execution commands cannot be forged.

### Pinned Self-Signed Certificates
- Each Agent generates its own ECDSA P-256 keypair locally upon first boot. Private keys never leave the local machine.
- Public certificates are exchanged during an initial pairing handshake and pinned by both parties.

### First-Contact Pairing Flow
Because first-time pairing occurs before mTLS trust is established, initial contact is secured using a **human-relayed pairing code + time-boxed window**:

1. **Initiation**: The Orchestrator displays a short-lived, human-readable pairing code (~2-minute TTL).
2. **Verification**: The user enters the code into the target Agent's local prompt.
3. **Exchange**: Once validated, public certificates are exchanged over Tailscale and pinned on both ends.
4. **Completion**: The single-use code is immediately discarded, and all subsequent communication uses mTLS.

### Running an Agent

`tether-agent` opens a pairing window only when it has no valid pinned
Orchestrator certificate. Once pairing succeeds, it loads its local Agent
configuration and remains running as the mTLS command daemon on port `7420`.
On later launches it skips pairing and starts that command daemon directly.
Use `tether-agent -pair` to deliberately re-pair and rotate the pinned
Orchestrator certificate.

In the Agent desktop app, **Re-pair Orchestrator** is the full recovery path:
after confirmation it stops the local command service, deletes the Agent's
previous private key/certificate and pinned Orchestrator certificate, then
opens a new one-time pairing window. A successful pairing records the
Orchestrator's Tailnet hostname and performs a Tailscale-layer heartbeat
immediately and every ten minutes thereafter. The heartbeat opens no extra
Tether port and its latest result is shown in the Agent UI.

The Agent configuration is local machine state at
`$XDG_CONFIG_HOME/tether/agent_config.yaml` on Linux (or the platform's user
config directory on other systems). It is never supplied by the
Orchestrator, because it controls the executable and bind address the Agent
is willing to run:

```yaml
rpc_server_path: /opt/llama.cpp/build/bin/ggml-rpc-server
rpc_listen_host: 100.64.246.74
```

### Controlling an Agent

Run `tether` from the directory containing `node_allowlist.yaml`, select an
online node, and enter its pairing code only if that node has not been paired
before. For a paired node, Tether builds its command client from the pinned
certificate and offers `status`, `start`, `stop`, and `quit`. `start` defaults
to that node's `rpc_port` from the allowlist. `ggml-rpc-server` exposes the
Agent's accelerator; it does not load a model. Load the GGUF model from an
Orchestrator-side `llama-cli` or `llama-server` process using `--rpc`.

---

## Configuration

Tether uses a clean YAML configuration for managing allowed nodes on the network.

### `node_allowlist.yaml` Example

nodes:
  - hostname: node-alpha
    role: rpc-node
    agent_port: 7420
    rpc_port: 50052

  - hostname: node-beta
    role: rpc-node
    agent_port: 7420
    rpc_port: 50052

Node Data Model
Go

type Node struct {
    Hostname     string
    TailscaleIP  string
    Role         string
    AgentPort    int
    RPCPort      int
    Status       NodeStatus
    LastSeen     time.Time
    Capabilities NodeCapabilities
}

type NodeCapabilities struct {
    GPUModel  string
    VRAMTotal int64 // bytes
}

Tech Stack

    Backend & Orchestration: Go

    Desktop UI Shell: Wails

    Networking & Mesh: Tailscale

    Inference Engine: llama.cpp (RPC Mode)

## License

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


## AI Coding Assistants

If you are using an LLM or AI-powered coding assistant, you MUST read and follow
the AI contribution requirements before contributing to Tether:

* CONTRIBUTING.md#ai-coding-assistants

Contributors remain responsible for the correctness, security, licensing, and
provenance of all submitted code, including AI-assisted contributions.

### (c) kindmanners
