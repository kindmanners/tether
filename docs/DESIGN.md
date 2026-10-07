---
name: Tether
description: A restrained geological survey system for a read-only distributed-compute dashboard.
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
  display:
    fontFamily: "Assistant, Segoe UI, sans-serif"
    fontSize: "clamp(2.55rem, 2.75vw, 3rem)"
    fontWeight: 720
    lineHeight: 0.98
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
    lineHeight: 1.55
  label:
    fontFamily: "Assistant, Segoe UI, sans-serif"
    fontSize: "0.78rem"
    fontWeight: 700
    lineHeight: 1.35
    letterSpacing: "0.02em"
rounded:
  pill: "999px"
  control: "11px"
  inset: "12px"
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
  button-quiet:
    backgroundColor: "{colors.surface-raised}"
    textColor: "{colors.ink}"
    typography: "{typography.label}"
    rounded: "{rounded.control}"
    padding: "0.65rem 0.95rem"
    height: "44px"
  panel-raised:
    backgroundColor: "{colors.surface-raised}"
    textColor: "{colors.ink}"
    rounded: "{rounded.panel}"
    padding: "clamp(1rem, 2vw, 1.4rem)"
  freshness-live:
    backgroundColor: "{colors.ground}"
    textColor: "{colors.ink-soft}"
    typography: "{typography.label}"
    rounded: "10px"
    padding: "0.72rem 0.85rem"
  freshness-snapshot:
    backgroundColor: "{colors.oxidized-coral-soft}"
    textColor: "{colors.oxidized-coral}"
    typography: "{typography.label}"
    rounded: "10px"
    padding: "0.72rem 0.85rem"
---

# Design System: Tether

## Overview

**Creative North Star: "The Capacity Survey"**

Tether treats distributed compute like a geological survey: individual machines become one legible seam of pooled capacity. The system is calm, technical, and materially grounded. Pale stone fields and broad white survey panels establish quiet structure, while mineral navy and glacial blue make the capacity relationship immediately visible without resorting to a wall of interchangeable metric cards or terminal cosplay.

The interface earns trust through explicit language. “Live,” “Snapshot,” “read-only,” freshness, degraded state, and unavailable data are always written, never communicated by color alone. The system is operational rather than theatrical: familiar controls, dense but readable records, and one deliberately expressive signature—the layered capacity seam.

Light and dark are equivalent environmental themes. Overview, Topology, and Records are information-architecture variants within the same system, not excuses for separate color identities. Every view keeps the mineral palette, Assistant typography, measured corner language, state semantics, and read-only boundary.

**Key Characteristics:**

- Capacity first: the pooled seam remains the dominant visual and narrative artifact.
- Restrained mineral materiality: stone, navy, glacial blue, lichen, and oxidized coral.
- Broad regions before small cards; technical records follow the system-level overview.
- Explicit operational truth: status words and provenance accompany every meaningful color cue.
- Responsive re-composition: desktop tables become labeled records rather than squeezed columns.

## Colors

The palette is a cool mineral survey: quiet pale substrates, deep ink and navy measurements, glacial capacity, lichen health, and oxidized coral for snapshots or degraded states. The frontmatter is normative for both light and dark values.

### Primary

- **Mineral Navy:** Owns allocated capacity, the primary refresh control, footer field, and restrained emphasis.
- **Deep Mineral Navy:** Anchors high-contrast controls and the diagnostic footer; it is not a generic decorative fill.

### Secondary

- **Glacial Blue:** Represents free capacity and the connected, available portion of the system.
- **Glacial Light:** Supplies subtle contour layers and hover washes without creating another semantic status.

### Tertiary

- **Lichen Green:** Means live, online, verified, healthy, or loaded only.
- **Oxidized Coral:** Means recorded snapshot, offline, loading, idle countdown, degraded, or otherwise attention-worthy only.

### Neutral

- **Ground:** The page field and inset-record substrate.
- **Surface and Raised Surface:** Quiet content layers; raised surface contains the major survey regions.
- **Ink and Soft Ink:** Primary measurements and supporting metadata respectively.
- **Line:** Structural dividers, table rules, control outlines, and contributor guides.
- **Seam Ink:** Theme-aware text over the allocated seam.
- **Focus:** A dedicated, high-contrast keyboard focus color.

**The Semantic Mineral Rule.** Lichen and oxidized coral are status vocabulary, not decoration; pair either with a written state.

**The Theme Equivalence Rule.** Dark mode remaps the same semantic roles. It must not alter hierarchy, data meaning, or view identity.

**The One-Seam Rule.** Mineral navy and glacial blue reach their highest visual concentration in the capacity seam. Elsewhere they support navigation, interaction, or small state cues.

## Typography

**Display Font:** Assistant variable (with Segoe UI and sans-serif fallbacks)  
**Body Font:** Assistant variable (with Segoe UI and sans-serif fallbacks)  
**Label Font:** Assistant variable with tabular numerals where measurements update

**Character:** Assistant gives the dashboard an open, contemporary technical voice without mimicking a terminal. Variable weights support a tight hierarchy inside a single family; measurements remain human-readable and operational.

### Hierarchy

- **Display** (720, responsive display token, 0.98): page-level orientation only; balance the line breaks and cap readable width around 15 characters.
- **Headline** (720, responsive headline token, 1.05): major data regions such as Compute contributors and Available models.
- **Title** (720, title token, 1.05): compact panel titles such as Cluster capacity.
- **Body** (400, body token, 1.55): context and explanatory copy; keep long explanatory lines near 37–58 characters depending on viewport.
- **Measurement** (720, fluid 1.6–2.5rem, 1): pooled counts and VRAM totals; use tabular numerals.
- **Label** (700, label token, 1.35): table headers, state metadata, source notes, and compact operational labels.
- **Wordmark** (800, 1.45rem desktop / 1.05rem mobile, 0.2em tracking): TETHER only; never reuse this spacing for labels.

**The Measurement Voice Rule.** Large type is reserved for system capacity and summary values. File paths, ports, provenance, and timestamps remain compact supporting text.

**The No-Terminal Rule.** Do not introduce monospace as atmosphere. Use it only if a future view contains code that genuinely requires fixed-width alignment.

## Layout

The page is a sequence of broad survey regions inside a maximum width of 94rem. Horizontal page padding is fluid through the `page` spacing token and tightens to 0.85rem below 680px. The 4.5rem sticky top bar preserves orientation and keeps theme and refresh actions accessible; at small widths it compresses to 4rem and hides nonessential explanatory labels while retaining accessible control names.

The desktop overview is intentionally asymmetric: a narrow narrative column and a wider capacity panel use approximately 0.43fr / 1.1fr columns with a 1.25–2rem fluid gutter. This imbalance makes the capacity seam dominant. At 980px the overview stacks, and the model row reduces from four columns to three. At 680px the layout fully re-composes: summaries become vertical rows, model records become a one-column sequence, and the node table becomes labeled record cards. Never solve mobile density with horizontal table scrolling or illegibly small text.

The spacing rhythm is compact inside data structures and generous between regions. Use 0.35–0.75rem for related labels and values, 1–1.4rem for component interiors, and about 1.15rem between major panels. Preserve safe-area padding in the footer and a 320px minimum viewport.

Overview, Topology, and Records must share the same shell, 94rem content boundary, panel treatment, spacing scale, and status vocabulary. Overview leads with the pooled seam; Topology may lead with relationships between contributors; Records may lead with sortable history or inventories. Those are view-specific compositions, not theme changes. Each still begins with system truth and moves toward technical detail.

**The System-Before-Records Rule.** Orient the operator with cluster state and provenance before presenting dense rows.

**The Reflow-Into-Records Rule.** Below the mobile breakpoint, table headers become per-field labels and each row becomes a readable record with the source order intact.

## Elevation & Depth

The system uses a restrained hybrid of tonal layering and one ambient shadow. The ground, raised panels, inset status strips, rules, and seam strata do most of the structural work. Major capacity and data panels receive a single diffuse elevation (`0 16px 40px rgba(26, 55, 80, 0.09)`); controls and mobile record cards remain flat. The sticky top bar uses a translucent ground wash and 14px backdrop blur so it separates through material rather than a heavy shadow.

### Shadow Vocabulary

- **Survey lift** (`0 16px 40px rgba(26, 55, 80, 0.09)`): major capacity and data panels only.
- **State halo** (`0 0 0 0.28rem` in the matching soft semantic color): the small live or snapshot dot only.

**The Broad-Surface Elevation Rule.** Elevate meaningful regions, not every metric, row, or control.

**The Contours-Not-Glass Rule.** The layered seam may use translucent strata; content panels remain opaque, quiet, and readable.

## Shapes

The form language is softly surveyed rather than bubbly. Major panels use the 14px panel radius, the capacity seam and mobile record cards use 12px, controls use 11px, and compact freshness strips use 10px. Status and legend marks are true circles; scrollbar thumbs are pill-shaped. One-pixel rules provide most internal structure.

The capacity seam is the signature silhouette: a clipped rounded rectangle split into allocated and free regions, with broad overlapping contour strata. The boundary is quantitative, while the internal curves suggest geological layers without obscuring the numbers.

**The Radius Hierarchy Rule.** Broad region, inset region, control, then status mark: do not flatten every element to one radius or inflate ordinary panels into capsules.

## Components

### Top Bar and Navigation

- **Style:** Sticky, translucent ground material with a single bottom rule. The tracked wordmark anchors the left; read-only context immediately follows on desktop.
- **Actions:** Theme and refresh remain on the right, each meeting the 44px minimum target. Supporting timestamp may disappear on narrow widths but remains visible in overview status.
- **States:** Hover shifts toward mineral navy; focus uses a 3px focus outline with 3px offset. The active theme is communicated through label and `aria-pressed`, not a new visual identity.

### Buttons

- **Shape:** Gently squared control corners (11px) and a 44px minimum height.
- **Primary:** Deep mineral fill, white text, compact horizontal padding, and a refresh glyph. Hover brightens toward mineral navy.
- **Quiet:** Raised surface, ink text, and a structural line border. Hover changes border and text to mineral navy.
- **Disabled / Refreshing:** Retain the control shape and label the action “Refreshing”; use 0.7 opacity and a wait cursor.
- **Motion:** The refresh glyph spins while the capacity seam receives one 720ms scan-and-settle pass.

### Capacity Seam

- **Role:** The system’s dominant data visualization and signature component. It communicates allocated versus free pooled VRAM and attributes total capacity to contributors.
- **Shape:** A 12px clipped region with a minimum height of 7.4rem desktop and 8.2rem mobile.
- **Allocation:** The visible split is clamped between 18% and 82% so labels remain legible; the accessible label carries the exact values.
- **Content:** Both sides contain a strong measurement and a written label. A legend repeats allocated, free, and total values above; contributor annotations sit below.
- **Motion:** A bright wave travels once across the seam on refresh and strata briefly resaturate. Reduced motion suppresses animation while state text and colors still update.

### Status and Freshness

- **Live:** Lichen text, dot, and soft halo paired with the word “Live” plus timestamp.
- **Snapshot / Offline:** Oxidized coral text, dot, and soft field paired with explicit recorded-snapshot wording.
- **Read-only:** State the boundary in the top bar, freshness strip, explanatory copy, and diagnostic footer where context requires it. Do not imply start, stop, pairing, or other controls.
- **Unavailable / Loading:** Use literal phrases such as “Not reported,” “Loading,” or “Worker state unavailable.” Never show a blank as if it were zero.

### Cards / Containers

- **Corner Style:** Broad 14px survey panels; 12px mobile record cards.
- **Background:** Raised surface for major regions, ground for inset records and live freshness, coral-soft for snapshot freshness.
- **Border:** Internal one-pixel rules and contributor guides; panels rely on tonal contrast and survey lift rather than boxed borders.
- **Internal Padding:** Fluid 1–1.5rem for major panels and 0.85rem for compact mobile records.

### Data Tables and Records

- **Desktop:** Full-width tables with quiet headers, 0.8–0.95rem cell padding, line dividers, and bold identity in the first column.
- **Pointer Hover:** A faint glacial wash may identify the scanned row; it must not look selected.
- **Mobile:** Hide the visual header but preserve semantics; expose each cell’s label inside a ground-colored 12px record card. Keep status, details, ports, and provenance readable without horizontal scrolling.
- **Empty States:** Replace the data structure with a direct sentence that names what was not reported.

### Model Records

- **Desktop:** Four-column rows balance model identity, path, format/note, and lifecycle placement. At medium width, the nonessential note may hide.
- **Mobile:** Collapse to one column, restore the note, move the long path last, and preserve lifecycle words and node placement.

## Do's and Don'ts

### Do:

- **Do** keep the pooled capacity seam visually dominant in Overview and use it as the system’s signature, not a generic progress bar.
- **Do** write “Live,” “Snapshot,” “read-only,” “Not reported,” and “unavailable” wherever those distinctions affect operator judgment.
- **Do** pair state colors with text, provenance, and timestamps.
- **Do** preserve theme parity and persistent user theme preference.
- **Do** transform desktop tables into labeled mobile records while preserving content order and semantics.
- **Do** use broad survey regions, quiet rules, and one ambient panel elevation.
- **Do** treat Overview, Topology, and Records as compositional variants of one system.

### Don't:

- **Don't** turn Overview into a grid of equal metric cards or reduce the capacity seam to a decorative banner.
- **Don't** use terminal cosplay, gratuitous monospace, neon accents, or a different palette per view.
- **Don't** communicate health, freshness, or availability through color alone.
- **Don't** imply browser-side operational control; start, stop, pair, and lifecycle actions remain in the desktop Orchestrator.
- **Don't** equate missing data with zero, healthy, or live.
- **Don't** compress desktop columns into a horizontally scrolling mobile table.
- **Don't** add shadows, pills, or accent color to every row and metric; rarity is what preserves hierarchy.
