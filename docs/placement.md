# Placement and model lifecycle

Tether chooses placement when `tether-api` loads a model. The decision is
made per model load so it can use current Agent health and VRAM reports.

## Automatic placement

With `--rpc auto`, the gateway:

1. Finds nodes in `node_allowlist.yaml` that are online and paired.
2. Obtains their current free-VRAM reports over pinned mTLS.
3. Reserves memory for the GGUF file, runtime overhead, and KV cache.
4. Prefers a whole-model placement on one suitable GPU.
5. Falls back to a set of RPC nodes only when no single GPU has enough
   capacity.

Agent status and capability probes run concurrently with a single 12-second
placement deadline and a maximum of four in-flight probes. An unresponsive
node is omitted from that placement decision instead of delaying every later
candidate by its individual client timeout.

The default runtime reserve is the GGUF file size multiplied by `1.15`.
The default KV-cache reserve is 256 KiB per context token. Tune those
conservatively with `--model-overhead` and `--kv-cache-bytes-per-token` when
your workload has measured requirements.

## Worker lifecycle

Each loaded model owns a llama.cpp worker. The gateway starts a worker on its
selected placement, waits up to five minutes by default for it to become
healthy, and routes requests through the OpenAI-compatible endpoint.

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
