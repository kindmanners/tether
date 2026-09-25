# Product Design QA

source visual truth: pre-change desktop captures from the existing embedded UIs, rendered from [cmd/tether/frontend/dist/index.html](/home/star/code/go/tether/cmd/tether/frontend/dist/index.html) and [cmd/tether-agent/frontend/dist/index.html](/home/star/code/go/tether/cmd/tether-agent/frontend/dist/index.html) (Codex In-app Browser tabs 1 and 2; 888×890 browser capture)
implementation screenshot: post-change representative captures from the embedded UIs (Codex In-app Browser tabs 7 and 6; 920×620 and 760×560 CSS viewport checks)
viewport: Orchestrator 920×620 minimum supported and 1180×760 target; Agent 760×560 minimum supported and 970×700 target
state: representative live cluster state, setup-complete Agent, and active pairing-code state with mocked read-only preview bindings; production `window.go` method names and arguments were preserved
density normalization: browser screenshots were judged at the same CSS viewport through the Codex in-app browser; no raster assets were involved

## Comparison evidence

Full-view comparison shows the original dark mono UI had a flat hierarchy and equal visual weight across state, action, and supporting copy. The implementation establishes a white Swiss grid, an Yves Klein blue semantic accent, clear numbered process rhythm, stronger headline hierarchy, and state-driven motion. The Orchestrator capture shows the gateway and cluster states above the fold; the Agent capture shows readiness, pairing, progress, one-time code, and machine record as separate readable bands.

Focused comparison covered the Orchestrator node grid and Add Tailnet node dialog, plus the Agent checklist and one-time pairing code. The dialog retains the existing fields and actions while gaining focus treatment and a clear modal hierarchy. The checklist retains the existing live statuses while adding a progress bar and staggered state rows. No image assets or custom icons were present in the source, so no asset substitution was required.

## Findings

- [P2 resolved] Pairing code wrapped into two lines at the desktop viewport.
  Location: `cmd/tether-agent/frontend/dist/style.css`, `.code-panel strong`.
  Evidence: the first post-change pairing capture split `731 204` over two rows at the 1180-wide browser viewport.
  Fix: reduced the responsive display size, tightened tracking, and added `white-space: nowrap`; the revised capture shows `731 204` on one line at the minimum-window check.

No actionable P0, P1, or P2 findings remain.

## Required fidelity surfaces

- Fonts and typography: Helvetica Neue/Helvetica/Arial keeps the Swiss sans direction; display scale, tracking, line-height, and wrapping were checked at target and minimum windows.
- Spacing and layout rhythm: hairline rules, numbered rails, card grid, modal padding, and responsive single-column fallbacks were checked in the browser.
- Colors and visual tokens: neutral white surface, black ink, Yves Klein blue accent, and muted gray supporting text are used consistently; state emphasis remains blue and warning text remains amber-brown.
- Image quality and asset fidelity: there are no product images, illustrations, logos, or non-standard icons in these screens; no replacement assets were introduced.
- Copy and content: existing runtime labels and status detail remain intact; new copy names the product action or state and does not fabricate telemetry.

## Interaction checks

- Orchestrator: Add Tailnet node opens the existing dialog, candidate and RPC fields remain wired, and node actions continue to call `Pair`, `StartRPC`, `StopRPC`, `AddNode`, and `StartGateway` with the original arguments.
- Agent: setup, pairing, refresh, and finish controls remain wired to `StartProvisioning`, `OpenPairing`, and `State`; active pairing and progress states were visually exercised.
- Accessibility: semantic headings, dialog labels, live regions, keyboard-visible focus, and `prefers-reduced-motion` handling are present.
- Console errors: no browser console warnings or errors were reported for the representative mocked states.

## Comparison history

1. Initial implementation: P2 pairing code wrapping identified from the active pairing-state capture.
2. Fix applied: `.code-panel strong` now uses a smaller responsive scale, tighter tracking, and no wrapping.
3. Post-fix capture: active pairing state at the minimum supported Agent viewport shows the complete code on one line with the checklist and machine record still fitting cleanly.

final result: passed
