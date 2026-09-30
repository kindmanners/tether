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

The Agent control channel uses exact certificate-pinned mTLS. Tether now also
secures the inter-node RPC path used by `llama-server` with that pinned mTLS
connection: `llama-server` receives only Tether-owned `127.0.0.1` tunnel
addresses, and Tether carries each RPC connection through an HTTP/1.1 CONNECT
stream on the authenticated Agent port. The managed `ggml-rpc-server` also
binds only to `127.0.0.1`, so its raw RPC port is never exposed to the Tailnet,
LAN, or internet. See the
[network-policy guidance](docs/configuration.md#network-policy).

This containment reduces network exposure; it does not make llama.cpp's RPC
backend intrinsically safe. Tether assumes the paired Orchestrator, paired
Agents, and local processes on those machines are trusted peers.

## Quick start

1. Build the four Tether binaries by following the
   [contributor build instructions](CONTRIBUTING.md#build-and-test), then
   prepare each GPU node with the
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

See [Operating Agents](docs/agent-operations.md) for the complete GUI flow and
[OpenAI API gateway](docs/openai-api.md) for client and standalone gateway
configuration.

## Limits

Tether currently supports NVIDIA GPUs through CUDA. AMD/ROCm, Intel GPU, and
Apple Metal workers are not supported by the managed setup flows.

| Role | Windows | Linux | macOS |
| --- | --- | --- | --- |
| Orchestrator | Supported | Supported | Not supported |
| NVIDIA/CUDA GPU Agent | Supported | Supported | Not supported |

Follow the Windows or Linux bootstrap guide because their driver, compiler,
and CUDA prerequisites differ.

- Distributed RPC primarily pools GPU memory so a model that does not fit on
  one GPU can use several machines. It is not an automatic speedup: a model
  that already fits on one local GPU will usually pay network and coordination
  overhead. When the alternative spills layers to CPU, however, remote GPU
  offload can improve throughput substantially; one historical 14B benchmark
  measured roughly double the prompt and generation throughput. See the
  [performance expectation and benchmark](docs/placement.md#performance-expectations).
- Tether reduces RPC network exposure but is not a sandbox. Paired nodes and
  localhost processes are trusted, and a malicious authenticated peer can
  still send malicious llama.cpp RPC data.
- Model compatibility and inference behavior ultimately depend on the pinned
  upstream llama.cpp revision. Updating that revision requires human review.

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
