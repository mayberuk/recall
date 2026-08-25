# Fonts

Three faces are vendored here rather than fetched from a font CDN, so the site
has no third-party request at all and renders identically offline.

| File | Family | Version | Licence |
|---|---|---|---|
| `bigshoulders.woff2` | Big Shoulders Display | 2.002 | SIL Open Font Licence 1.1 |
| `archivo.woff2` | Archivo | 2.001 | SIL Open Font Licence 1.1 |
| `jetbrainsmono.woff2` | JetBrains Mono | 2.211 | SIL Open Font Licence 1.1 |

The OFL permits bundling and redistribution, including inside a commercial
work, provided the fonts are not sold on their own and the licence travels with
them: <https://scripts.sil.org/OFL>. Each file carries its own licence URL in
its `name` table, which is where the versions above were read from.

These are the latin subsets only. The full families cover far more, and the
subset is what keeps each file near 35 KB.

Big Shoulders Display's outlines are also the source of the wordmark in
`docs/assets/`, which are glyph paths exported from this exact file at weight
700, so a shipped SVG needs no font at render time and cannot fall back to
something else.
