# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

- **Inferred for this session:** A technically proficient homelab owner or infrastructure operator checking a private multi-machine LLM cluster from a desktop or mobile browser.
- **Open decision:** Whether the primary audience is an individual operator or a shared operations team.

## Product Purpose

Tether is a desktop control plane for distributed local LLM inference across trusted machines on a private Tailscale network. The browser dashboard is a read-only diagnostic surface for quickly understanding cluster availability, pooled VRAM, node health, and model lifecycle state; operational control remains in the desktop Orchestrator.

Success means an operator can distinguish live data from a recorded fallback snapshot, identify unavailable compute, and understand the cluster's usable model capacity without mistaking the dashboard for a control surface.

## Positioning

Tether pools GPU memory across trusted physical devices through llama.cpp RPC so operators can run models or contexts that do not fit on one GPU without using cloud inference. It promises additional capacity, not automatic speed.

## Operating Context

- Private Tailnet with a desktop Orchestrator and paired GPU Agents.
- Read-only browser dashboard used for at-a-glance diagnostics on desktop, tablet, or phone.
- Live dashboard data may be unavailable, in which case the surface shows an explicitly labeled recorded snapshot.
- Operators use node identity, GPU model, VRAM, local RPC and Agent ports, Agent state, and model placement/lifecycle data to diagnose the cluster.

## Capabilities and Constraints

- Preserve the existing dashboard data model, refresh action, theme preference, live/snapshot distinction, nodes table, model inventory, and empty states.
- The dashboard must not imply that it can start, stop, pair, or otherwise control nodes.
- Tether currently supports NVIDIA/CUDA GPU Agents on Windows and Linux; macOS, AMD/ROCm, Intel GPU, and Apple Metal workers are not supported by managed setup flows.
- Security language must preserve that paired nodes and local processes are trusted and that pinned mTLS reduces exposure without turning llama.cpp RPC into a sandbox.
- **Inferred for this session:** Existing factual content and behavior are fixed; labels and hierarchy may be clarified where they do not alter meaning.

## Brand Commitments

- Product name: Tether.
- Voice: technically precise, direct, restrained, and honest about limitations.
- Avoid cloud-service framing, invented performance claims, or language that suggests automatic speedups.

## Evidence on Hand

- Product and security facts: `README.md`.
- Dashboard implementation and real field vocabulary: `web/dashboard/index.html`, `web/dashboard/app.js`, and `web/dashboard/styles.css`.
- A single recorded fallback node and model exist in `web/dashboard/app.js`; these are diagnostic sample data, not a customer claim.
- No logo artwork, photography, testimonials, customer list, or commercial proof assets are present and none should be fabricated.

## Product Principles

1. Capacity over hype: show what the cluster can hold without implying guaranteed acceleration.
2. Trust through legibility: make live, stale, unavailable, and transitional states unmistakable.
3. Read-only means read-only: keep diagnostics rich while reserving control for the desktop Orchestrator.
4. Local-first privacy: reflect the private, operator-owned network context without claiming sandbox-level isolation.
5. Useful at a glance, complete on demand: prioritize cluster health while keeping the underlying technical detail accessible.

## Accessibility & Inclusion

- **Inferred for this session:** Meet modern web accessibility expectations, including keyboard operability, visible focus, meaningful status text beyond color, 44px touch targets, responsive reflow, and an intentional reduced-motion experience.
