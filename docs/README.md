# Tether documentation

The root [README](../README.md) is the project overview and quickest route to
building Tether. These guides cover tasks that benefit from more operational
detail.

| Guide | Use it when you need to... |
| --- | --- |
| [Configuration](configuration.md) | declare the GPU nodes Tether may use and configure an Agent locally |
| [Operating Agents](agent-operations.md) | pair a GPU node and manage its RPC server from the desktop apps |
| [OpenAI API gateway](openai-api.md) | connect an OpenAI-compatible client to `tether-api` |
| [Placement and model lifecycle](placement.md) | understand RPC selection, capacity admission, and idle unloading |
| [Two-node acceptance test](acceptance-test.md) | qualify a release candidate on real GPU nodes and capture sanitized evidence |
| [Windows GPU-node bootstrap](windows-gpu-node-bootstrap.md) | prepare a Windows node for CUDA RPC inference |
| [Linux GPU-node bootstrap](linux-gpu-node-bootstrap.md) | prepare a Linux node for CUDA RPC inference |
| [Security policy](../SECURITY.md) | understand RPC trust boundaries or report a vulnerability privately |

## Run records

- [Distributed acceptance run — 2026-10-07](distributed-acceptance-2026-10-07.md)
  records the Wayfarer, Ataraxia, and Mathesis test results, pause state, and
  remaining recovery checks.

## Documentation conventions

Keep this directory focused on operator and integration guidance. Put a short
overview and common build commands in the root README, and keep implementation
details close to the code as Go documentation and tests. Do not include private
Tailnet addresses, pairing codes, API keys, or machine-specific executable
paths in committed examples.
