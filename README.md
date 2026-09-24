# Tether

Tether is a desktop control plane for distributed local LLM inference across
machines on a private Tailscale network. It uses llama.cpp's RPC mode to pool
GPU memory across physical devices without requiring cloud infrastructure.

## What it includes

Tether builds four binaries:

| Binary | Purpose |
| --- | --- |
| `tether` | The Wails desktop Orchestrator for pairing nodes, managing their RPC servers, and using the model library. |
| `tether-agent` | The Wails desktop app that runs on each GPU node and manages its local `ggml-rpc-server`. |
| `tether-api` | A local OpenAI-compatible gateway for Tether-managed model workers. |
| `tether-dashboard` | An independent, read-only browser dashboard for the cluster. |

The Agent control channel uses pinned mTLS. The separate llama.cpp RPC data
channel is protected by your Tailscale policy; see the
[network-policy guidance](docs/configuration.md#network-policy).

## Build

Install Go 1.27 or later and the platform dependencies required by Wails. The
desktop applications embed their checked-in web assets, so Node.js is not
required to build them.

```bash
go build -tags production -o bin/tether ./cmd/tether
go build -tags production -o bin/tether-agent ./cmd/tether-agent
go build -o bin/tether-api ./cmd/tether-api
go build -o bin/tether-dashboard ./cmd/tether-dashboard
```

On Windows, use the release script to build `tether`, `tether-agent`, and
`tether-api` with the WebView2 bootstrapper:

```powershell
.\scripts\build-windows-release.ps1
```

Keep `tether-api` beside `tether` in a packaged installation. The desktop
Orchestrator starts it as a sibling process and exposes the local endpoint at
`http://127.0.0.1:11435/v1`. The desktop generates a fresh API key for each
run and displays it beside the endpoint; configure clients to send that key as
an OpenAI Bearer token.

## Quick start

1. Build the Orchestrator and Agent, then prepare each GPU node with the
   appropriate [Windows](docs/windows-gpu-node-bootstrap.md) or
   [Linux](docs/linux-gpu-node-bootstrap.md) guide.
2. Add the node's Tailnet hostname and ports to
   [`node_allowlist.yaml`](docs/configuration.md#node-allowlist).
3. Start `tether-agent` on the GPU node and complete its local preflight or
   setup.
4. Open `tether`, choose **Add Tailnet node**, select the online allowlisted
   node, and enter the one-time code shown by the Agent.
5. Use the node card in the Orchestrator to view Agent health and start or
   stop its RPC server. Load models through the desktop app or `tether-api`,
   then copy the displayed endpoint and API key into your OpenAI-compatible
   client.

See [Operating Agents](docs/agent-operations.md) for the complete GUI flow.

## OpenAI-compatible gateway

Every `tether-api` request requires an API key, including requests from the
local machine. The desktop supplies its generated key to the gateway through
the process environment. For a standalone launch, set `TETHER_API_KEY` or pass
`--api-key` explicitly:

```bash
TETHER_API_KEY="replace-with-a-strong-random-token" \
  tether-api --listen 127.0.0.1:11435 --rpc auto
```

The gateway currently provides `GET /v1/models` and
`POST /v1/chat/completions`. Streaming chat responses are flushed as SSE data
arrives, so clients such as Open WebUI receive tokens without proxy buffering.
The gateway removes client authorization and cookie headers before proxying a
request and authenticates each private llama.cpp worker separately.

Model loading is serialized across the gateway so concurrent requests cannot
be admitted against the same stale VRAM snapshot. Requests for workers that
are already loaded continue normally during another model load. Tether watches
worker processes during startup and while serving; an unexpected exit removes
the worker from the active set and records it as crashed so the next request
can start a replacement.

The model library accepts ordinary `.gguf` files and split sets named like
`model-00001-of-00005.gguf`. Split files appear as one model and their complete
size is used for placement. `mmproj*.gguf` projector files are not registered
as standalone language models. Automatic placement pins whole-model local
loads to the selected CUDA device and hides local CUDA devices when the plan
uses remote GPUs only.

The gateway validates request hosts and browser origins in addition to its
Bearer token. On Linux, worker processes use a parent-death signal; on Windows,
they run in a kill-on-close Job Object. These safeguards prevent model workers
from being left behind after a gateway crash or forced shutdown.

## Documentation

The [documentation index](docs/README.md) contains the operational and
integration guides:

- [Configuration](docs/configuration.md) — allowlists, local Agent settings,
  and Tailnet policy.
- [Operating Agents](docs/agent-operations.md) — pairing, health, RPC server
  controls, and re-pairing in the desktop apps.
- [OpenAI API gateway](docs/openai-api.md) — OpenAI-compatible client setup.
- [Placement and model lifecycle](docs/placement.md) — automatic RPC
  placement, capacity admission, and idle unloading.
- [Windows GPU-node bootstrap](docs/windows-gpu-node-bootstrap.md) and
  [Linux GPU-node bootstrap](docs/linux-gpu-node-bootstrap.md) — GPU-node
  setup.

## License

Tether is licensed under the [GNU Affero General Public License v3.0](LICENSE).

## AI Coding Assistants

If you use an LLM or AI-powered coding assistant, read the
[AI contribution requirements](CONTRIBUTING.md#ai-coding-assistants) before
contributing. Contributors remain responsible for the correctness, security,
licensing, and provenance of AI-assisted changes.

### (c) kindmanners
