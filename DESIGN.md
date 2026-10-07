---
name: Tether
description: A shared Capacity Survey system for Tether's diagnostic dashboard and operational Orchestrator.
colors:
  ground: "#eef3f5"
  surface: "#f9fbfb"
  surface-raised: "#ffffff"
  ink: "#0b1e38"
  ink-soft: "#50647b"
  line: "#cfdae0"
  mineral-navy: "#143a62"
  mineral-navy-deep: "#0a2948"
  glacial: "#7ab8e4"
  glacial-light: "#b9dcf3"
  lichen: "#14713f"
  lichen-soft: "#d5efe0"
  oxidized-coral: "#d94f34"
  oxidized-coral-control: "#c7442e"
  oxidized-coral-soft: "#fae2dc"
  focus: "#145fc2"
  seam-ink: "#ffffff"
  dark-ground: "#07131f"
  dark-surface: "#0d1c2b"
  dark-surface-raised: "#102338"
  dark-ink: "#edf6fa"
  dark-ink-soft: "#aebfcb"
  dark-line: "#294258"
  dark-mineral-navy: "#74b5e4"
  dark-mineral-navy-deep: "#091827"
  dark-glacial: "#559bd0"
  dark-glacial-light: "#2f6288"
  dark-lichen: "#63d28f"
  dark-lichen-soft: "#163d2b"
  dark-oxidized-coral: "#ff8069"
  dark-oxidized-coral-soft: "#4a211d"
  dark-focus: "#92c9ff"
  dark-seam-ink: "#07131f"
typography:
  display-dashboard:
    fontFamily: "Assistant, Segoe UI, sans-serif"
    fontSize: "clamp(2.55rem, 2.75vw, 3rem)"
    fontWeight: 720
    lineHeight: 0.98
    letterSpacing: "-0.035em"
  display-orchestrator:
    fontFamily: "Assistant, Segoe UI, sans-serif"
    fontSize: "clamp(2.8rem, 4.5vw, 5rem)"
    fontWeight: 720
    lineHeight: 0.95
    letterSpacing: "-0.035em"
  headline:
    fontFamily: "Assistant, Segoe UI, sans-serif"
    fontSize: "clamp(1.45rem, 2vw, 2rem)"
    fontWeight: 720
    lineHeight: 1.05
    letterSpacing: "-0.025em"
  title:
    fontFamily: "Assistant, Segoe UI, sans-serif"
    fontSize: "1.35rem"
    fontWeight: 720
    lineHeight: 1.05
  body:
    fontFamily: "Assistant, Segoe UI, sans-serif"
    fontSize: "1.05rem"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "Assistant, Segoe UI, sans-serif"
    fontSize: "0.78rem"
    fontWeight: 720
    lineHeight: 1.35
    letterSpacing: "0.02em"
  code:
    fontFamily: "Cascadia Mono, Consolas, monospace"
    fontSize: "0.75rem"
    fontWeight: 400
    lineHeight: 1.35
rounded:
  pill: "999px"
  compact: "8px"
  field: "10px"
  control: "11px"
  record: "12px"
  panel: "14px"
spacing:
  xxs: "0.35rem"
  xs: "0.55rem"
  sm: "0.75rem"
  md: "1rem"
  lg: "1.4rem"
  xl: "2rem"
  page: "clamp(1rem, 2.1vw, 2rem)"
components:
  button-primary:
    backgroundColor: "{colors.mineral-navy-deep}"
    textColor: "{colors.surface-raised}"
    typography: "{typography.label}"
    rounded: "{rounded.control}"
    padding: "0.65rem 0.95rem"
    height: "44px"
  button-secondary:
    backgroundColor: "{colors.surface-raised}"
    textColor: "{colors.ink}"
    typography: "{typography.label}"
    rounded: "{rounded.control}"
    padding: "0.65rem 0.95rem"
    height: "44px"
  field:
    backgroundColor: "{colors.ground}"
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    rounded: "{rounded.field}"
    padding: "0.65rem 0.75rem"
    height: "46px"
  code-value:
    backgroundColor: "{colors.ground}"
    textColor: "{colors.ink}"
    typography: "{typography.code}"
    rounded: "{rounded.field}"
    padding: "0.72rem 0.85rem"
  panel-raised:
    backgroundColor: "{colors.surface-raised}"
    textColor: "{colors.ink}"
    rounded: "{rounded.panel}"
    padding: "clamp(1rem, 2vw, 1.4rem)"
  freshness-live:
    backgroundColor: "{colors.ground}"
    textColor: "{colors.ink-soft}"
    typography: "{typography.label}"
    rounded: "{rounded.field}"
    padding: "0.72rem 0.85rem"
  freshness-snapshot:
    backgroundColor: "{colors.oxidized-coral-soft}"
    textColor: "{colors.oxidized-coral}"
    typography: "{typography.label}"
    rounded: "{rounded.field}"
    padding: "0.72rem 0.85rem"
---

# Design System: Tether

## Overview

**Creative North Star: "The Capacity Survey"**

Tether treats distributed compute like a geological survey: separate machines become one legible seam of pooled capacity. Pale stone fields and broad raised survey surfaces provide quiet structure; mineral navy and glacial blue reveal system relationships; lichen and oxidized coral state what is healthy, unavailable, recorded, or consequential. The atmosphere is technically precise and materially grounded, never terminal cosplay.

The browser dashboard and Wails Orchestrator are coordinated product surfaces within this one system. The dashboard reads the survey and remains explicitly diagnostic and read-only. The Orchestrator marks, connects, prepares, and activates the surveyed system. Both begin with state and system truth, then reveal contributors and records; the operational surface alone adds actions, review dialogs, endpoint credentials, and lifecycle controls.

Light and dark are equivalent environmental themes, not separate identities. Dashboard, Orchestrator, Overview, Topology, and Records are view or surface variants. Every variant shares Assistant typography, measured corners, broad regions, fine rules, contour language, state semantics, responsive re-composition, and an honest distinction between unknown data and zero.

**Key Characteristics:**

- Capacity and readiness first; records and actions follow system context.
- Restrained mineral materiality: stone, navy, glacial blue, lichen, and oxidized coral.
- Broad survey regions before small cards; fine rules organize dense operational detail.
- Explicit state truth: words, provenance, timestamps, and consequences accompany color.
- Responsive re-composition: grids stack, trust steps rotate, and tables become labeled records.
- Familiar controls with preserved Wails behavior; expression belongs to seams and contours.

## Colors

The shared palette is a cool mineral survey. The frontmatter is normative for light and dark values; dark mode remaps roles without changing hierarchy or meaning.

### Primary

- **Mineral Navy:** Allocated capacity, selected workspaces, control-plane seams, and restrained interactive emphasis.
- **Deep Mineral Navy:** Primary actions, the diagnostic footer, and high-contrast anchors.

### Secondary

- **Glacial Blue:** Free capacity and the connected, available portion of a system.
- **Glacial Light:** Contour strata and faint fine-pointer hover washes.

### Tertiary

- **Lichen Green:** Live, online, verified, healthy, loaded, ready, or running only.
- **Oxidized Coral:** Snapshot, offline, unreachable, loading, degraded, failed, or caution states. The Orchestrator uses its slightly deeper control variant for consequential action emphasis while preserving the same semantic family.

### Neutral

- **Ground:** Page field, form field, inset record, and fact-grid substrate.
- **Surface and Raised Surface:** Quiet content layers and major survey regions.
- **Ink and Soft Ink:** Primary measurements and supporting explanation respectively.
- **Line:** Dividers, field outlines, table rules, readiness connectors, and contributor guides.
- **Seam Ink:** Theme-aware measurement text over allocated capacity.
- **Focus:** Dedicated high-contrast keyboard focus, never a status color.

**The Semantic Mineral Rule.** Lichen and oxidized coral are state vocabulary, not decoration; pair either with a written state or consequence.

**The Theme Equivalence Rule.** Dark mode must not alter information hierarchy, data meaning, available actions, or surface identity.

**The One-Seam Rule.** Navy and glacial blue reach their greatest concentration in capacity or control-plane seams. Elsewhere they support navigation, interaction, and compact measurements.

## Typography

**Display Font:** Assistant variable (with Segoe UI and sans-serif fallbacks)  
**Body Font:** Assistant variable (with Segoe UI and sans-serif fallbacks)  
**Code Font:** Cascadia Mono (with Consolas and monospace fallbacks)

**Character:** Assistant gives both surfaces an open, contemporary technical voice without impersonating a terminal. Variable weights create hierarchy inside one family. Cascadia Mono is a narrow semantic exception for values that operators copy, compare, or enter exactly.

### Hierarchy

- **Dashboard Display** (720, dashboard display token, 0.98): compact diagnostic orientation, balanced around a short 15-character measure.
- **Orchestrator Display** (720, orchestrator display token, 0.95): stronger operational orientation, capped near 12 characters per line.
- **Headline** (720, headline token, 1.05): major data and workspace regions.
- **Title** (720, title token, 1.05): compact capacity and gateway panel titles.
- **Body** (400, body token, 1.5): system context, consequences, and explanatory copy; keep long lines near 38–60 characters by context.
- **Measurement** (720, fluid 1.6–2.5rem, 1): pooled counts, VRAM totals, and cluster facts; use tabular numerals.
- **Label** (720, label token, 1.35): table headers, state metadata, source notes, field labels, and compact actions.
- **Code Value** (400, code token, 1.35): genuine endpoints, API keys, paths, ports when represented as literal code, and code-like values only.
- **Wordmark** (800, 1.45rem desktop / 1.05rem compact, 0.2em tracking): TETHER only.

**The Measurement Voice Rule.** Large type belongs to capacity, readiness, and summary values. Paths, ports, provenance, and timestamps remain supporting information.

**The Semantic Code Rule.** Use Cascadia Mono only when character-level fidelity matters: endpoint, API-key, filesystem path, command, or code values. Never use monospace to make ordinary labels, headings, node names, or metadata look technical.

**The No-Kicker Rule.** Do not add decorative overlines, meaningless indices, pseudo-coordinate labels, or numbered ornaments. Numbers appear only when they encode real order, quantity, or readiness steps.

## Layout

Both surfaces use a compact sticky product bar, fluid page padding, and a broad centered content field. The dashboard caps at 94rem; the Orchestrator main workspace caps at 96rem. A 4.5rem utility bar preserves orientation and theme access, compressing to 4rem at small widths. Shared horizontal padding uses the `page` token and tightens to 0.85rem below 680px.

The first region is deliberately asymmetric: a narrow narrative column and a wide system panel use approximately 0.43fr / 1.1fr columns with a fluid 1.25–2rem gutter. On the dashboard the right side is the pooled-capacity seam. In the Orchestrator it is gateway readiness, endpoint credentials, local GPU role, and the control-plane seam. This shared composition coordinates the surfaces while keeping their jobs distinct.

At 980px first-view grids stack, supporting explanations widen, action groups move below their headings, and two-column record grids collapse. At 680px controls wrap into full-width groups, endpoint fields stack, horizontal readiness steps become a vertical path, node facts become single-column rows, dialog facts become one column, and dashboard tables become labeled records. Never solve density with illegibly small text or a horizontally scrolling mobile table.

The spacing rhythm is compact inside data structures and generous between regions: 0.35–0.75rem for related values, 1–1.4rem for component interiors, and about 1.15rem between major panels. Preserve the 320px minimum viewport and safe-area padding where a surface reaches the window edge.

Overview, Topology, and Records remain compositional variants within the same shell. Overview leads with pooled capacity or readiness, Topology may lead with relationships between contributors, and Records may lead with histories or inventories. None receives a separate palette, typography, or decorative language.

**The State-Before-Action Rule.** Establish identity, trust, readiness, availability, and consequences before presenting a control.

**The System-Before-Records Rule.** Orient the operator with cluster truth and provenance before dense rows or cards.

**The Reflow-Into-Records Rule.** On compact viewports, preserve semantic order while turning columns and fact grids into labeled vertical records.

## Elevation & Depth

The system combines tonal layering with one restrained ambient lift. Ground, raised panels, inset fields, fact grids, fine rules, and contour strata do most structural work. Major capacity, gateway, and workspace panels use survey lift (`0 16px 40px rgba(26, 55, 80, 0.09)`). Notices use a tighter operational shadow, and modal decisions use the strongest shadow plus a dark blurred backdrop. Ordinary controls and record cards remain flat.

### Shadow Vocabulary

- **Survey lift** (`0 16px 40px rgba(26, 55, 80, 0.09)`): major capacity, gateway, and workspace regions.
- **Operational notice** (`0 12px 30px rgba(10, 41, 72, 0.16)`): transient status and error notices only.
- **Decision lift** (`0 24px 70px rgba(4, 18, 32, 0.34)`): modal confirmation and placement-review dialogs only.
- **State halo** (`0 0 0 0.25–0.28rem` in the matching soft semantic color): compact live or readiness dots only.

**The Broad-Surface Elevation Rule.** Elevate meaningful regions and decisions, not every metric, row, field, or action.

**The Contours-Not-Glass Rule.** Seam strata may be translucent; content panels remain opaque and readable. The sticky bar alone may use a translucent ground wash with restrained blur.

## Shapes

The form language is softly surveyed rather than bubbly. Major panels and dialogs use 14px corners; seams and record cards use 12px; buttons use 11px; fields, notices, fact cells, code values, and callouts use 10px; compact lifecycle badges may use 8px. Status and readiness marks are circles. One-pixel rules organize most internal structure.

The capacity and control-plane seams are the signature silhouettes: clipped rounded regions filled with broad overlapping contours. The dashboard seam encodes allocated and free proportions. The Orchestrator seam communicates readiness through state-tinted material rather than a fabricated quantity.

**The Radius Hierarchy Rule.** Major region, record or seam, control, compact field, then status mark. Do not flatten everything to one radius or turn ordinary controls into capsules.

**The Honest Contour Rule.** Contours may express system continuity, state, or refresh. They must never imply a measurement the product does not possess.

## Components

### Utility Bar and Navigation

- **Style:** Sticky translucent ground, one bottom rule, tracked TETHER wordmark, explicit surface label, current status, and theme action.
- **Workspace tabs:** Text tabs sit on a fine baseline. The selected tab uses navy text and a 2px underline; unselected tabs remain soft ink. Arrow keys, Home, and End preserve the tab interaction contract.
- **Responsive:** Supporting surface labels may hide, but product identity, current state, and theme access remain.

### Buttons

- **Shape:** Restrained control corners (11px); primary controls meet a 44px minimum height, while dense action shelves may use the shipped 38px compact minimum.
- **Primary:** Deep mineral fill with white text. Use for the next explicit operational step or dashboard refresh, never for every available action.
- **Secondary:** Raised surface, ink text, and line border. Use for reversible or parallel actions.
- **Caution:** Oxidized coral may fill a confirmed consequential action; the dialog must first explain impact.
- **States:** Hover moves toward mineral navy, active shifts down 1px, disabled remains legible at reduced opacity, and focus uses the global 3px outline with 3px offset.

### Inputs and Selects

- **Style:** Ground substrate, 1px line border, 10px corners, full width, and a 46px minimum height.
- **Labels:** Always visible above the field; placeholder text is supporting help, not the field name.
- **Focus:** Global focus outline supplements the border rather than relying on a color-only shift.
- **Validation:** Keep errors explicit in words and route operational failures to the live notice region.

### Code Values

- **Style:** Ground substrate, 10px corners, compact padding, and the code typography token.
- **Use:** Local gateway endpoints, API keys, exact paths, or code-like credentials. Wrap long values safely without truncating essential characters.
- **Non-use:** Do not apply this treatment to ordinary technical prose, hostnames, model names, labels, or card metadata.

### Capacity and Control-Plane Seams

- **Capacity seam:** The dashboard’s dominant quantitative visualization. Both regions show a strong measurement and written label; the legend and accessible label repeat exact meaning. The visible split is clamped for legibility while accessible text carries exact values.
- **Control-plane seam:** The Orchestrator’s dominant state material. Lichen or coral may subtly mix into navy for running or unavailable states; it does not invent a numeric ratio.
- **Motion:** Refresh triggers one 720ms scan and saturation-settle pass. Reduced motion suppresses animation while state text and color still update.

### Status, Freshness, and Notices

- **Live and ready:** Lichen dot plus written state and timestamp or supporting detail.
- **Snapshot, unavailable, failed, or caution:** Oxidized coral plus literal state language, cause, and recovery context where available.
- **Unknown:** Use “Not reported,” “Checking,” or “Unavailable”; never render blank data as zero, healthy, or live.
- **Surface boundary:** The dashboard repeatedly states read-only diagnostic context. The Orchestrator states private control-plane context and places consequential changes behind review.

### Cards and Records

- **Major panels:** Raised surface, 14px corners, survey lift, and fluid 1–1.5rem padding.
- **Node and model cards:** Ground substrate, 12px corners, fine internal rules, identity and state first, facts second, actions last.
- **Dashboard table:** Full-width rows on desktop; below 680px each row becomes a labeled ground record without horizontal scrolling.
- **Hover:** Fine-pointer records may receive a faint glacial wash; hover must not resemble selection.
- **Empty states:** Directly name what is absent and, for operational surfaces, the next safe recovery step.

### Readiness Path

- **Desktop:** Three real ordered trust steps share one fine horizontal connector.
- **Mobile:** Rotate into a vertical path with the numbered mark spanning its title and explanation.
- **Rule:** Preserve the meaningful setup order. Do not reuse numbered circles as decorative section indices.

### Decision Dialogs

- **Structure:** Decision title, plain-language summary, bordered fact grid, consequence callout, then cancel and confirm actions.
- **Behavior:** Preserve native dialog semantics, focus handling, and every operational confirmation. Long dialogs scroll internally within the viewport.
- **Consequences:** Destructive, interruptive, or resource-heavy actions name affected models, placement, capacity sample, destination, or interruption before confirmation.

### Wails Operational Contract

- **Bindings:** Existing DOM IDs and Go binding calls are behavior contracts. Visual work must not rename, remove, or bypass them.
- **Refresh:** Live Snapshot and Models calls can update independently but must resolve into explicit visible state. Background preparation and telemetry polling remain quiet unless operator attention is required.
- **Actions:** Pairing, allowlisting, RPC lifecycle, local GPU role, gateway start, download, load, and unload remain real controls with disabled, progress, success, cancellation, and error states.
- **Security truth:** One-time pairing codes are not saved; pinned mTLS and Tailnet trust do not turn workers into a sandbox.

## Do's and Don'ts

### Do:

- **Do** keep capacity or readiness dominant before records and actions.
- **Do** coordinate dashboard and Orchestrator as two roles inside one Capacity Survey system.
- **Do** write “Live,” “Snapshot,” “read-only,” “Checking,” “Not reported,” and “Unavailable” wherever operator judgment depends on the distinction.
- **Do** pair every state color with text, provenance, consequences, or a timestamp.
- **Do** use Cascadia Mono only for genuine endpoint, API-key, path, command, or code values.
- **Do** preserve Wails bindings, element IDs, confirmations, progress, focus behavior, and visible errors.
- **Do** transform dense desktop structures into stacked, labeled records on compact viewports.
- **Do** keep light and dark structurally and semantically equivalent.

### Don't:

- **Don't** split dashboard, Orchestrator, Overview, Topology, or Records into unrelated palettes or themes.
- **Don't** turn the interface into equal metric tiles or reduce the seam to decorative chrome.
- **Don't** use monospace, terminal motifs, decorative kickers, or meaningless indices to signal technical credibility.
- **Don't** communicate health, trust, freshness, availability, or consequence through color alone.
- **Don't** imply browser-side control on the read-only dashboard or hide operational impact in the Orchestrator.
- **Don't** equate missing information with zero, healthy, ready, or live.
- **Don't** remove or rename Wails-bound IDs, skip decision dialogs, or replace real actions with visual stand-ins.
- **Don't** solve compact layouts with horizontal table scrolling or touch targets below the shipped minimums.
