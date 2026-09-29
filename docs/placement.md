# Placement and model lifecycle

Tether chooses placement when `tether-api` loads a model. The decision is
made per model load so it can use current Agent health and VRAM reports.

## Automatic placement

With `--agents auto`, the gateway:

1. Finds nodes in `node_allowlist.yaml` that are online and paired.
2. Obtains their current free-VRAM reports over pinned mTLS.
3. Reserves memory for the GGUF file, runtime overhead, and KV cache.
4. Prefers a whole-model placement on one suitable GPU.
5. Falls back to a set of RPC nodes only when no single GPU has enough
   capacity.

Placement previews perform only authenticated health and capability probes.
They create no listeners or tunnels. Ephemeral loopback tunnels are opened
only when a worker launches and close on launch failure, crash, unload, or
gateway shutdown.

Agent status and capability probes run concurrently with a single 12-second
placement deadline and a maximum of four in-flight probes. An unresponsive
node is omitted from that placement decision instead of delaying every later
candidate by its individual client timeout.

The default runtime reserve is the GGUF file size multiplied by `1.15`.
The default KV-cache reserve is 256 KiB per context token. Tune those
conservatively with `--model-overhead` and `--kv-cache-bytes-per-token` when
your workload has measured requirements.

Model loading is serialized across the gateway so concurrent launches cannot
be admitted against the same stale VRAM snapshot. Requests for workers that
are already loaded continue while another model is loading. Whole-model local
placements are pinned to the selected CUDA device; local CUDA devices are
hidden when a plan uses only remote GPUs.

## Performance expectations

Tether's primary performance benefit is capacity: several GPUs can hold a
model that would otherwise spill to CPU or fail to load. RPC is not inherently
faster than one local GPU. If the model already fits locally, network transfer
and coordination will usually add latency and may reduce throughput. If the
local baseline spills layers to CPU, replacing that spill with remote GPU
offload can instead produce a substantial improvement.

Model loading is also distinct from steady-state inference. A remote worker
may spend minutes receiving several GiB of weights the first time a model is
loaded. Keep a worker resident when that startup cost matters, and benchmark
the actual hosts and network used for deployment.

### Qwen2.5-Coder-14B Q6_K placement benchmark (2026-09-16)

The larger `Qwen2.5-Coder-14B-Instruct-Q6_K` model was downloaded from the
official Qwen GGUF repository, merged from its two published segments, and
verified as a 12,124,683,712-byte GGUF v3 file. Both benchmark paths used the
CUDA+RPC llama.cpp build at `b10931-3057bb66c` (commit
`3057bb66c86c46d5781e50e85462a760ba7d1feb`) with this identical inference
configuration:

```yaml
prompt:     Hello, what is the job of an garbage collector?
context:    -c 8192
prediction: -n 128
batch:      -b 2048 -ub 512
attention:  -fa on
KV cache:   -ctk f16 -ctv f16
sampler:    -s 42 --temp 0 --top-k 1 --top-p 1
```

| Path | GPU layer placement | Prompt throughput | Generation throughput |
| --- | --- | ---: | ---: |
| A: Test computer only | RTX 3060: 36 of 48 layers; remaining layers spilled to CPU | 60.9 t/s | 6.6 t/s |
| B: Test computer + Tether RPC | RTX 3060 plus Computer B RTX 3050; all layers offloaded | 119.7 t/s | 13.4 t/s |

The model produced the same deterministic answer prefix on both paths: it
explained the programming-language memory-management meaning first, then the
waste-management meaning. For this run, the multi-GPU RPC path improved prompt
throughput by about 97% and generation throughput by about 103%.

Path B had a multi-minute, one-time model-load phase while several GiB of
weights were transferred to Computer B. The hosts were on different networks,
so this reflects an inter-network Tailnet path. Model-load time is deliberately
excluded from the steady-state tokens-per-second figures. The model worker was
exited after each benchmark, releasing its GPU allocations.

This is a historical, directional result rather than a performance guarantee.
It used an older llama.cpp revision and predates Tether's current pinned-mTLS
CONNECT tunnel, so current results will vary with the pinned revision, model,
GPU balance, network path, and tunnel overhead.

## Worker lifecycle

Each loaded model owns a llama.cpp worker. The gateway starts a worker on its
selected placement, waits up to five minutes by default for it to become
healthy, and routes requests through the OpenAI-compatible endpoint.

Tether watches each worker during startup and while it is serving. An
unexpected exit removes the worker from the active set and records it as
crashed so a later request can start a replacement. Linux workers receive a
parent-death signal, while Windows workers run in a kill-on-close Job Object,
reducing the chance that a worker survives a gateway crash or forced shutdown.

An idle worker is unloaded after five minutes by default, freeing its GPU
allocation for later placement decisions. Configure this with `--idle-unload`:

```bash
# Keep workers resident.
tether-api --idle-unload 0

# Release workers after fifteen minutes without requests.
tether-api --idle-unload 15m
```

Use `--ctx-size` and `--parallel` to control the context size and concurrent
requests per worker. Increasing either increases the memory required for a
safe placement.
