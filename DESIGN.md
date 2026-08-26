---
name: recall
description: Warm ledger stock, a deck log that records what it did not find
colors:
  stock: "#f0ebdd"
  stock-2: "#f7f3e8"
  stock-3: "#e2dac6"
  ink: "#1c1a15"
  ink-2: "#57503f"
  ink-ghost: "rgba(28,26,21,.36)"
  rule: "#1d4a6d"
  rule-faint: "rgba(29,74,109,.3)"
  rule-hair: "rgba(29,74,109,.16)"
  stamp: "#b41d51"
  stamp-wash: "rgba(180,29,81,.11)"
  ledger: "#2c6549"
  ledger-band: "rgba(44,101,73,.13)"
  ledger-wash: "rgba(44,101,73,.11)"
  carbon: "#8a5510"
  carbon-wash: "rgba(138,85,16,.11)"
  field-bg: "#1d4a6d"
  field-ink: "#f4f0e5"
  hole: "#d5cbb2"
typography:
  display:
    fontFamily: "Big Shoulders Display, ui-sans-serif, system-ui, sans-serif"
    fontSize: "clamp(3.4rem, 12.4vw, 8.4rem)"
    fontWeight: 700
    lineHeight: 0.84
    letterSpacing: "-0.04em"
  section:
    fontFamily: "Big Shoulders Display, ui-sans-serif, system-ui, sans-serif"
    fontSize: "clamp(2.2rem, 5.6vw, 4rem)"
    fontWeight: 700
    lineHeight: 0.94
    letterSpacing: "-0.035em"
  lede:
    fontFamily: "Archivo, ui-sans-serif, system-ui, sans-serif"
    fontSize: "clamp(1.06rem, 0.6vw + 0.95rem, 1.3rem)"
    fontWeight: 400
    lineHeight: 1.48
    letterSpacing: "normal"
  body:
    fontFamily: "Archivo, ui-sans-serif, system-ui, sans-serif"
    fontSize: "clamp(0.97rem, 0.3vw + 0.9rem, 1.05rem)"
    fontWeight: 400
    lineHeight: 1.58
    letterSpacing: "normal"
  field-label:
    fontFamily: "Archivo, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.645rem"
    fontWeight: 600
    lineHeight: 1.4
    letterSpacing: "0.19em"
  data:
    fontFamily: "JetBrains Mono, ui-monospace, SFMono-Regular, Menlo, monospace"
    fontSize: "0.78rem"
    fontWeight: 400
    lineHeight: 1.58
    letterSpacing: "-0.01em"
rounded:
  none: "0"
spacing:
  gut: "clamp(1.1rem, 3vw, 2.4rem)"
  entry: "clamp(2.4rem, 5vw, 3.8rem)"
  feed: "clamp(0px, 3vw, 34px)"
components:
  install-entry:
    backgroundColor: "{colors.stock-2}"
    textColor: "{colors.ink}"
    typography: "{typography.data}"
    rounded: "{rounded.none}"
    padding: "0.78rem 0.9rem"
  install-entry-button:
    backgroundColor: "transparent"
    textColor: "{colors.ink-2}"
    typography: "{typography.field-label}"
    rounded: "{rounded.none}"
    padding: "0 1rem"
  install-entry-button-hover:
    backgroundColor: "{colors.stamp-wash}"
    textColor: "{colors.ink}"
  sheet-header:
    backgroundColor: "{colors.rule}"
    textColor: "{colors.stock-2}"
    typography: "{typography.field-label}"
    rounded: "{rounded.none}"
    padding: "0.5rem 0.8rem"
  sheet-row-marked:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.stock}"
    typography: "{typography.data}"
    rounded: "{rounded.none}"
    padding: "0.4rem 0.8rem"
  author-chip-you:
    backgroundColor: "{colors.stamp}"
    textColor: "{colors.stock-2}"
    typography: "{typography.field-label}"
    rounded: "{rounded.none}"
    padding: "0.1rem 0.35rem"
  pressed-stamp:
    backgroundColor: "transparent"
    textColor: "{colors.stamp}"
    typography: "{typography.field-label}"
    rounded: "{rounded.none}"
    padding: "0.16rem 0.5rem"
---

## Overview

**North star: continuous-feed ledger stock.** The page is a length of green-bar printout on warm
bookkeeping paper: sprocket strips down both edges, banded rows, perforated tears between entries.
The stock is an oatmeal cream with a red bias, never the cool blue-white a screen defaults to.
Dark is the same run read under a chart lamp, and a three-state control in the masthead lets a
reader pin either or leave it to their system. It comes from the
naval deck log, the one document form whose governing discipline is that *nothing to report* is a
required entry. That is recall's coverage footer, and its `exit 1`, four centuries early.

The category default this refuses, explicitly: the near-black developer-tool hero with an
oversized mono headline, a faked terminal window, a copy-install button and a star count. The site
is a **form, never a terminal emulator.** Rendered CLI output appears as printed entries inside a
ruled grid, never inside window chrome.

Two rules do most of the work. **Rank is inversion, not tint.** The entry that matters flips to
dark ground, which is the same logic as the terminal's reverse-video match, so the two surfaces
share reasoning rather than a swatch. And **colour carries meaning, never decoration**, so four inks,
each with one job, so the palette argues the product's case on its own.

Dark mode is not an inversion of the light theme. It is the same run of paper read under a chart
lamp on night watch.

## Colors

Four inks. Each has exactly one job, and an element takes its ink from what it *means*, never from
what would look good there.

### Primary
`stamp` `#bd1e61`: **what you act on.** The mark, the install stamp, key figures, one emphasis word
per heading. Rubber-stamp ink, historically violet or red. Used sparingly and always slightly
off-square. Maps to ANSI magenta in the terminal system.

### Secondary
`ledger` `#2f6b4f`: **what it found.** The near-miss panel, author labels, timeline marks, and the
green-bar row banding. `rule` `#1f4e79`: **structure.** Table header bars, the MCP field, rules.

### Tertiary
`carbon` `#8a5a12`: **what it skipped.** Owns the coverage block entirely, and nothing else. Named
for duplicate-sheet carbon ink.

### Neutral
`stock` `#e3e7e1` cool grey-green ledger paper, deliberately **not cream**; `stock-2` `#edefe9`
printed blocks; `ink` `#14181c` with a blue cast; `ink-2` `#4b5760` tinted from the ground's hue,
never neutral grey.

### Named Rules

- **One ink, one job.** If a new element needs colour, decide what it *means* first (acted on,
  found, skipped, or structural) and take that ink. Never introduce a fifth.
- **Rose and ochre never sit on a green band.** Both drop to ~3.9:1 there. Banding is confined to
  printout blocks, where only `ink` and `ink-2` appear. The "you" label is an inverted rose chip
  (5.15:1) precisely because rose text on a banded row would fail.
- **Every pair is measured, both themes.** 14/14 verified at the last pass. Dark mode has failed
  twice while light passed, both times on a token that inverts lightness, so check it separately.
- **The blue field carries its own tokens.** `rule` flips light in dark mode, so a field reusing it
  collapses to 2.83:1. `field-bg` / `field-ink` exist for that reason.

## Typography

Three faces, all self-hosted Latin-subset woff2, ~34KB each. Every one was chosen against
impeccable's training-data-default list.

- **Big Shoulders Display**: display voice. Industrial wayfinding lettering; reads as a printed
  manifest header, not a book. Used at genuine size: 8.4rem headline, 4rem section heads.
- **Archivo**: form furniture and body. Drawn for print forms.
- **JetBrains Mono**: every datum, and only data.

### Hierarchy
`display` 8.4rem/0.84 → `section` 4rem/0.94 → `lede` 1.3rem/1.48 → `body` 1.05rem/1.58 →
`field-label` 0.645rem uppercase 0.19em → `data` 0.78rem.

### Named Rules

- **Display type earns its size.** Big Shoulders is condensed and built to be large. Setting it
  politely at 2–3rem is the single fastest way to make this world look uninspired; that is a
  diagnosed failure of this build, not a hypothetical.
- **Mono is for data, never for atmosphere.** Prose is Archivo. Monospace as a costume for
  "technical" is banned.
- **One emphasis word per heading**, in `stamp`, so the page can be scanned in five seconds.
- Body measure stays 54–62ch. Tracking never tighter than `-0.04em`.

## Layout

- **Sprocket strips frame the page.** Fixed full-height feeds left and right, `clamp(0,3vw,34px)`,
  4.5px holes on a 30px pitch, dashed inner rule. Dropped below 44rem.
- **Entries flow, sections do not box.** Each section is separated by a `2px dashed` perforation.
  There are no cards. Nested cards are always wrong.
- **Proof occupies the first viewport.** Real product output sits immediately below the headline,
  with its explanation as a caption underneath. Show, then tell.
- Content max-width 72rem, gutter `clamp(1.1rem,3vw,2.4rem)`, entry rhythm `clamp(2.4rem,5vw,3.8rem)`.
- Single breakpoint at 56rem collapses the hero and stat grids; 44rem drops the feeds.

## Elevation & Depth

**There are no shadows in this system. Zero `box-shadow` declarations.** Paper has no drop
shadow; depth comes from ink density and inversion. `ink-ghost` recedes, `ink` advances, and the
one thing that must dominate flips to dark ground.

### Named Rules
- **Never add a shadow to make something stand out.** Invert it, or raise its ink.
- **Signal blocks carry a printed rule above, never a side tab.** A thick coloured `border-left`
  is both the craft floor's ban and the detector's top AI tell. This build introduced it twice and
  had it caught twice. `border-top: 3px solid <ink>` is the sanctioned form.

## Shapes

**Radius is zero everywhere.** Not a single `border-radius` in the stylesheet. Printed forms have
square corners; a rounded corner reads as software and breaks the object immediately.

Rules come in three printed weights: `2px double` (masthead), `2px solid` (register and column
heads), `1px solid` hairline (row separators), plus `2px dashed` for perforations and feed edges.

## Components

### Install entry
A bordered cell with the command and a flush copy control. On copy it presses a rose
**ENTERED IN LOG** stamp, rotated −7°, overscaled on impact and settling.

### Sheet
The printout. Blue filled header bar, green-bar banding on alternating row pairs at 14%. The
marked session inverts to dark ground with its match in rose. Author labels are `ledger` green,
except "you", which is an inverted rose chip.

### Coverage block
Ochre, and only ochre. Four real footer lines at 1:1, ghosted, one openable at a time, and the open
line inverts to ochre ground. Teaches by isolating real output rather than redrawing it.

### Pressed stamp
Reusable assertion mark: 2px border, uppercase display type, rotated −3.5°, three inks
(`stamp` / `good` / `warn`).

### Motion
**Two families, one machine.** Every moving thing on this page is either the print head laying ink
down or a rubber stamp landing on it. Nothing fades, nothing slides in from an edge, and no two
sections share an entrance.

*The head prints.* A `.carriage`, an absolutely-positioned cover the colour of the block beneath
it, carrying a 2px `rule` top border that reads as the print line, starts at `height:100%` and
retracts to `0`, anchored bottom. The ease is `steps()`, not a curve: a carriage advances in
discrete rows, and a smooth glide would be a different machine. Used twice, on the proof sheet
(`steps(9)`, 780ms) and the miss report (`steps(6)`, 560ms), and re-run on every query change
(`steps(8)`, 620ms) so the gesture is a *response*, not a page-load flourish.

*The ink lands.* `press` is `cubic-bezier(.16,.84,.3,1)`, overshoot then settle. Three instances:
FOUND on the proof sheet (fired by the carriage's completion, not by its own observer, so the
stamp always lands on a finished sheet), EVERY TIME in the coverage heading, and ENTERED IN LOG on
copy. All three carry the broken-ink mask.

*One exception, and it is a different machine on purpose.* The timeline bars plot upward
(`scaleY`, `outExpo`, 60ms stagger) because a plotter is not a print head. Its scroll observer
watches the **section**, never the bars, because a bar starts at zero height and a zero-height target
never crosses a threshold.

**Motion is a separate chunk nobody who declined it downloads.** `prefers-reduced-motion: reduce`
short-circuits before the dynamic `import()`, so a reader who asked for less fetches 1.3 KB and no
animation library at all. Every animated element starts from a state the script itself arms
(`.armed`), so with the chunk absent, blocked, or failed the page is simply already printed.
Interaction (copy, the coverage accordion, re-running the query) ships inline and never waits on
a bundle.

### Texture
**Texture is the stock, never decoration.** Four devices, all from the same physical object, none
of them an image file:

- **Paper tooth**: greyscale `feTurbulence` at `.07` alpha (`.10` in dark), as one absolutely-
  positioned layer over the whole document at `z-index:5`. It sits *over* the ink, because ink is
  printed onto textured paper rather than floating above it, and it scrolls with the sheet.
- **Perforation and fold**: every section boundary is a row of 0.95px dots at a 9px pitch, with a
  26px crease band across it: `crease-lo` above, `crease-hi` below. Continuous-feed paper's
  perforation *is* its fold, so the two are one device.
- **Line screen in the green bars**: banded rows carry a 1px-on / 4px-off `ledger-screen` rule
  over the flat fill. A printed band has a screen; a CSS fill does not.
- **Broken ink**: every rubber stamp is masked with a second turbulence field thresholded to knock
  out roughly a tenth of its area, border included. A stamp that lays down a solid field is a
  rectangle, not a stamp.

The sprocket strips are `position:absolute`, not fixed: the paper travels past the reader, the
printer does not travel with the window.

## Do's and Don'ts

**Do**
- Decide what an element *means* before choosing its ink.
- Let real product output carry the argument; keep prose to ≤40 words a paragraph.
- Set display type at full strength.
- Theme browser surfaces: selection, caret, scrollbars, focus ring, underline offset, tabular
  numerals. It is the cheapest signal a page was built rather than assembled.
- Re-measure contrast in **both** themes after any colour change.

**Don't**
- Add a fifth ink, a shadow, or a border radius.
- Reach for a texture *image*. Every texture here is generated, tokenised, and theme-aware.
- Animate anything that is not the print head, the plotter, or a stamp, or give two
  sections the same entrance.
- Point a scroll observer at an element the animation collapses to zero size.
- Put a thick coloured border on the side of anything.
- Render CLI output inside faked terminal chrome.
- Use cream, sepia, paper texture images, torn edges, or a serif display face. This world is a
  cool grey-green *printout*, not a warm document, and cream is the AI-default trap for it.
- Write an eyebrow or kicker above a heading.
- Set body prose in monospace.

---

## Terminal system (built)

Structure is carried by typography first and colour only ever second: `«»` around matches, `──`
prefixing footer lines, `·` between facts, `>` marking the focused turn. Every one of those
survives a pipe, and colour is added on top of them rather than in place of them.

- **Matches render as reverse video**, not colour. It swaps whatever the terminal already uses, so
  it cannot clash with a theme and cannot produce an unreadable pair. The guillemets are inverted
  *with* the match, so the marker reads as one solid block.
- **The CLI inherits the user's theme.** Counting the roles settled it: after subtracting reverse
  video, plain body, dim, bold, and status green/amber/red, exactly **one** discretionary colour
  remains. Hardcoding a hex for one role buys nothing and fights every themed terminal.
- **Six roles.** Match: reverse video, SGR 7 · Handle (session ids, `run:` suggestions, the
  `recall show` recovery line): magenta · Key (author and tier labels, dead-end terms): bold, no
  colour · Quiet (the `──` footer lines, edge elisions, `×N` counts): dim · Status (`ok`/`warn`/
  `fail`): green/yellow/red, never rebranded · Body: unstyled.
- **Author labels collapse to human-vs-rest**, carried by weight not hue. Four hues would fail
  deuteranopia and reopen the palette; `--mine` is already an alias for `--author human`.
- **Colour never carries meaning alone.** Output is piped, redirected and machine-parsed, and the
  coverage footer is a correctness contract.

### The two rules the implementation is built on

**Colour may add to an answer, never subtract from it.** `style.Strip(coloured)` reproduces the
plain bytes exactly, verified end to end, not just in unit tests. This is not tidiness: every size
recall reports, the `--max-bytes` cap, the large-answer warning, and the budget-fitting search all
measure by stripping the attributes back off. Let colour remove so much as a guillemet and a
terminal and a pipe get told two different numbers for the same answer, which is the one thing a
tool that prices its own output may not do. It is why the guillemets and the backticks stay inside
their attribute rather than being replaced by it.

**Colour is structurally unable to reach a machine format.** The palette lives in an *unexported*
field on each view type, so `encoding/json` cannot serialise it and no future caller can wire it
into `--json`, `--format jsonl`, `--ids`, or the fzf record stream, and those last two are parsed as
session ids by every binding recall ships. `Globals.Palette` returns the zero palette for any
format but text, and the zero `style.Palette` writes nothing, so the default everywhere is plain.

### No degradation ladder, on purpose

The earlier note here called for truecolor → 256 → 16 → none. There is nothing to degrade: a ladder
exists to approximate a *specific* colour on a terminal that cannot show it, and not one of the six
roles names a specific colour. They are named ANSI attributes the terminal maps through its own
theme, so the only two states are on and off.

`--color auto|always|never`, defaulting to auto: colour when a character device is reading, and
nothing else. `NO_COLOR` (set and non-empty) and `TERM=dumb` both disable auto; `always` overrides
them, because it is the user overriding detection. TTY detection reads the file mode rather than
taking a dependency: the two modules recall allows earned their place on measurement, and a third
for one syscall would not.
