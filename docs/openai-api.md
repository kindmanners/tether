# OpenAI-compatible API gateway

`tether-api` presents a local OpenAI-compatible API in front of the model
library and Tether-managed llama.cpp workers. It provides:

- `GET /v1/models`
- `POST /v1/chat/completions`

Start it with a llama.cpp `llama-server` that has RPC support:

```bash
TETHER_API_KEY="replace-with-a-strong-random-token" \
  go run ./cmd/tether-api --listen 127.0.0.1:11435 --agents auto
```

The default model directory is `~/models`. Choose another directory or server
binary explicitly when necessary:

```bash
tether-api \
  --api-key "replace-with-a-strong-random-token" \
  --models-dir /path/to/gguf-models \
  --llama-server /path/to/llama-server \
  --agents auto
```

## Agent selection

- `--agents auto` (the default) reads the allowlist and queries online, paired
  Agents for free VRAM when a model is loaded. It prefers one GPU when the
  model fits and uses RPC splitting only when required.
- `--agents node-alpha,node-beta` requires those exact allowlisted, online,
  paired Agent hostnames. Unknown, duplicate, offline, or unpaired names fail
  closed instead of silently falling back.
- `--agents none` runs without remote Agents.

The deprecated `--rpc` alias accepts the same hostname-only grammar for one
release. Raw `host:port` values fail with a migration error. With `--agents
auto`, select a different allowlist with `--allowlist path/to/node_allowlist.yaml`.
Use `--local-gpu=false` if the Orchestrator must not contribute a local CUDA GPU.

At worker launch Tether creates one ephemeral `127.0.0.1` listener per selected
Agent. `llama-server --rpc` receives only those loopback endpoints; every
accepted socket becomes a fresh pinned-mTLS CONNECT stream to that Agent.

## Client configuration

Set an OpenAI-compatible client base URL to:

```text
http://127.0.0.1:11435/v1
```

Every request needs the API key as a Bearer token, including requests over
loopback. The desktop app generates a fresh key when it starts, passes it to
the gateway through the environment, and displays it beside the endpoint for
local client configuration. Standalone launches must set `--api-key` or the
`TETHER_API_KEY` environment variable. Continue to restrict non-loopback
listeners to trusted Tailnet or LAN clients.

The gateway keeps one worker per loaded model. By default it unloads an idle
worker after five minutes. Use `--idle-unload 0` to disable that behaviour, or
adjust it with a Go duration such as `--idle-unload 15m`.

## API and model-library behavior

Streaming chat responses are flushed as SSE data arrives, so clients receive
tokens without proxy buffering. Before proxying a request, the gateway removes
client authorization and cookie headers and authenticates the private
llama.cpp worker with a separate credential. It also validates request hosts
and browser origins in addition to requiring the Bearer token.

The model library accepts ordinary `.gguf` files and split sets named like
`model-00001-of-00005.gguf`. A split set appears as one model and its complete
size is used for placement. `mmproj*.gguf` projector files are not registered
as standalone language models.

## Dashboard

The independent `tether-dashboard` uses the same API key to include live model
worker state. Prefer the environment variable so the credential does not
appear in process listings:

```bash
TETHER_API_KEY="the-key-shown-by-tether" tether-dashboard
```

The dashboard accepts only loopback listen addresses and rejects non-local
Host and Origin values because its inventory contains cluster topology and
hardware details. Use the default `127.0.0.1:8080`; provide remote access only
through a separately authenticated local proxy. Node inventory remains
available if the gateway is down, while model state reports the connection or
authentication error.
