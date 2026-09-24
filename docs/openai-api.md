# OpenAI-compatible API gateway

`tether-api` presents a local OpenAI-compatible API in front of the model
library and Tether-managed llama.cpp workers. It provides:

- `GET /v1/models`
- `POST /v1/chat/completions`

Start it with a llama.cpp `llama-server` that has RPC support:

```bash
go run ./cmd/tether-api --listen 127.0.0.1:11435 --rpc auto
```

The default model directory is `~/models`. Choose another directory or server
binary explicitly when necessary:

```bash
tether-api \
  --models-dir /path/to/gguf-models \
  --llama-server /path/to/llama-server \
  --rpc auto
```

## RPC modes

- `--rpc auto` (the default) reads the allowlist and queries online, paired
  Agents for free VRAM when a model is loaded. It prefers one GPU when the
  model fits and uses RPC splitting only when required.
- `--rpc host:port,host:port` uses the specified RPC endpoints.
- `--rpc none` runs without remote RPC nodes.

With `--rpc auto`, select a different allowlist with `--allowlist path/to/node_allowlist.yaml`.
Use `--local-gpu=false` if the Orchestrator must not contribute a local CUDA GPU.

## Client configuration

Set an OpenAI-compatible client base URL to:

```text
http://127.0.0.1:11435/v1
```

The loopback default needs no API key. If you intentionally bind beyond the
local machine, `--api-key` is required; use a strong secret and restrict
network access to trusted Tailnet or LAN clients.

The gateway keeps one worker per loaded model. By default it unloads an idle
worker after five minutes. Use `--idle-unload 0` to disable that behaviour, or
adjust it with a Go duration such as `--idle-unload 15m`.
