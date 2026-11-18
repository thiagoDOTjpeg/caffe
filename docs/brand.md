# Brand

**Caffè** — *Brew better software.*

A local-first orchestrator for autonomous coding agents.

## Names

| Context | Name |
|---|---|
| Product, prose, documentation | Caffè |
| CLI executable | `caffe` |
| Go module and import path | `caffe` |
| Project directory | `.caffe/` |
| Default database | `.caffe/caffe.db` |
| Environment variables | `CAFFE_*` |
| Default branch prefix | `caffe/<run>/<task>` |

The accented `è` is for people. It never appears in executable names, Go
identifiers, environment variables, paths, configuration keys or shell
commands.

Herdr is the name of an external agent runtime that Caffè can drive through an
adapter (`internal/runtime/herdr`). It is not part of Caffè's identity; keep
its name exact wherever the adapter is discussed.

## Palette

| Name | Hex | Use |
|---|---|---|
| Espresso | `#241A16` | primary background |
| Secondary background | `#30231D` | panels, code blocks on dark |
| Cream | `#F3E7D5` | primary text on dark, the wordmark |
| Caramel | `#C5A27B` | the single accent |
| Muted text | `#A99583` | secondary text |

Cream on Espresso is the default pairing. Caramel is an accent, not a text
colour for body copy.

## Wordmark

[`assets/caffe.svg`](assets/caffe.svg): a cup seen from above (cream rim,
caramel crema arc) beside the name in a serif face and the tagline in
monospace. No gradients, no steam, no mascots.

## Voice

- A mature developer tool: say what it does and what it does not.
- Coffee is an accent, not a theme. One touch is enough; never rename
  technical concepts after coffee (a run is a run, not a brew).
- No AI imagery: no robots, brains or sparkles.

## In the terminal

The CLI is plain text: no colours, no ASCII art, no decoration that a script
would have to strip. The identity shows in the name, the tagline in
`caffe --help`, and consistent wording.
