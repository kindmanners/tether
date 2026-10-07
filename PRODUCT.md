# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Primary users are technically proficient operators running local LLM inference across trusted machines on a Tailscale network. The Orchestrator is used during setup and active operation to pair nodes, prepare backends, manage the local gateway, and place models.

## Product Purpose

Tether combines GPU memory from allowlisted Tailnet machines so local models can be placed and served through one OpenAI-compatible endpoint. Success means an operator can understand readiness, establish trust, start the required services, and manage model lifecycle without confusing capacity pooling with faster inference.

## Positioning

Tether is a private, pinned-mTLS control plane for pooled local GPU capacity. It coordinates trusted machines; it does not claim to combine their compute into a single faster GPU.

## Operating Context

The desktop Orchestrator is the operational surface. It manages the local gateway, this machine's GPU contribution, Tailnet allowlisting, one-time-code pairing, remote RPC lifecycle, model downloads, placement review, loading, and unloading. The separate browser dashboard remains diagnostic and read-only.

## Capabilities and Constraints

- Wails desktop application with embedded static HTML, CSS, and JavaScript.
- Preserve the existing Go binding contract and all element IDs used by `cmd/tether/frontend/dist/app.js`.
- Operational or destructive actions retain their confirmation dialogs and explanatory copy.
- Live status, unavailable data, background preparation, download progress, empty states, and errors must remain explicit in words as well as color.
- Light and dark themes are supported.
- The interface must remain usable from a 320px-wide viewport upward.

## Brand Commitments

Use the approved Tether Capacity Survey language established by the web dashboard: Assistant typography, pale stone and deep mineral surfaces, navy/glacial capacity colors, lichen health, oxidized-coral attention states, broad survey panels, soft 10–14px corners, and restrained motion.

## Evidence on Hand

- Product and security behavior: `README.md`, `docs/README.md`, and Go implementation.
- Operational UI and interaction contract: `cmd/tether/frontend/dist/`.
- Approved visual reference: `web/dashboard/` and `.impeccable/design.json`.
- No external marketing claims, customer evidence, or performance benchmarks are supplied; do not fabricate them.

## Product Principles

- Make trust and operational state obvious before offering an action.
- Show pooled capacity as a system, then expose the machines and records beneath it.
- Preserve a strict distinction between diagnostic visibility and operational control.
- Prefer working, reversible controls and explicit consequences over decorative chrome.
- Treat missing information as unknown, never as zero or healthy.

## Accessibility & Inclusion

Keyboard navigation, visible focus, semantic status updates, reduced-motion support, non-color state labels, and touch-sized controls are required.
