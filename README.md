# hyprlang2lua

Convert legacy [Hyprland](https://hypr.land) `hyprland.conf` (hyprlang) files
to the Lua configuration format introduced in Hyprland 0.55 (May 2026).

The converter is built around a hand-written lexer + recursive-descent parser
and a per-directive code generator. The output is idiomatic Lua that matches
the shape of the example config shipped at `/usr/share/hypr/hyprland.lua`,
mapping each hyprlang construct to the `hl.*` API exposed by the Lua stubs
at `/usr/share/hypr/stubs/hl.meta.lua`.

## Install

```sh
go install github.com/EIonTusk/hyprlang2lua/cmd/hyprlang2lua@latest
```

…or build locally:

```sh
go build -o hyprlang2lua ./cmd/hyprlang2lua
```

Go 1.26+. The library (`internal/converter`) is standard-library only; the
CLI adds a single dependency, `github.com/spf13/pflag`, for POSIX-style flag
parsing.

A WebAssembly build is included under `web/` for an in-browser converter — see
[Browser build](#browser-build) below.

## Usage

```sh
# single file → stdout
hyprlang2lua ~/.config/hypr/hyprland.conf > ~/.config/hypr/hyprland.lua

# from stdin
cat hyprland.conf | hyprlang2lua > hyprland.lua

# write next to each *.conf in a tree
hyprlang2lua --dir ~/.config/hypr

# show coverage stats on stderr
hyprlang2lua --report hyprland.conf > hyprland.lua

# CI mode: exit non-zero if anything was flagged for manual review
hyprlang2lua --check hyprland.conf > /dev/null
```

Flags:

| flag                      | effect                                                              |
|---------------------------|---------------------------------------------------------------------|
| `-d, --dir DIR`           | walk a directory, writing `*.lua` next to every `*.conf`            |
| `    --in-place`          | with `--dir`, overwrite existing `*.lua` siblings (off by default)  |
| `-o, --out FILE`          | write to FILE (single-file mode; default stdout)                    |
| `-r, --report`            | print `translated / passthrough / flagged / coverage%` to stderr    |
| `-c, --check`             | exit code `3` if any directive was flagged for manual review        |
| `-m, --merge`             | merge every `hl.X(...)` call into a single one if the API supports it (default **on**; pass `--merge=false` to disable). In practice this folds every per-section `hl.config({...})` into one call — section-separating comments are preserved inside the merged table. Other `hl.*` APIs (bind, window_rule, monitor, env, device, …) take one spec per call by design and pass through unchanged. `--merge-config` is kept as a deprecated alias. |
| `-s, --strip-comments`    | drop comments from the output (`-- TODO: manual review` markers from flagged directives are kept) |

With no positional argument, the CLI reads from stdin — unless stdin is a
TTY, in which case it prints usage instead of hanging on a read.

Exit codes: `0` success, `1` I/O or conversion error, `2` usage/flag error,
`3` `--check` failed (at least one flagged directive).

## Supported directives

### Phase 1 (translated automatically)

- `key = value` at the top level and inside any of the recognized sections:
  `general`, `decoration`, `input`, `animations`, `gestures`, `misc`,
  `binds`, `cursor`, `debug`, `dwindle`, `master`, `group`, `render`,
  `xwayland`, `opengl`, `ecosystem`, `experimental`, `layout`,
  `scrolling`, `quirks`. Nested sections (`decoration { blur { } }`) emit
  nested Lua tables.
- `$var = value` → `local var = value`. References (`$var` on the right side
  of any directive) resolve to the local; mixed text builds a concat chain
  (`mainMod .. " + SHIFT + 1"`).
- The `bind` family — `bind`, `bindm`, `binde`, `bindr`, `bindl`, `bindn`,
  `bindo`, `bindt`, `bindi`, `bindp`, `bindc`, `bindd`, and any combined-flag
  form like `bindel` / `bindle`. Each flag suffix becomes the corresponding
  field on `HL.BindOptions`.
- `exec`, `exec-once`, `execr-once`, `exec-shutdown`. Bundled into one
  `hl.on("hyprland.start", function() ... end)` (or `config.reloaded` /
  `hyprland.shutdown`) block per kind.
- `monitor`, `windowrule`, `windowrulev2`, `workspace`, `layerrule`,
  `env`, `envd`, `animation`, `bezier`, `gesture`, `permission`.
- `device:<name> { ... }`, mapped to `hl.device({ name = "<name>", ... })`.
- `# comment` → `-- comment`, in roughly the same source position.

### Phase 2 (translated where possible, flagged where ambiguous)

- `source = path` → `require("path")` plus a comment reminding the user that
  the sourced `.conf` must itself be converted. *Reason:* `require()` integrates
  with Lua's `package.path` and preserves the user's modular structure;
  `dofile()` would force relative paths, and inline-expansion would bloat
  output and discard organization.
- `submap = name` / `submap = reset` — these define stateful blocks of binds
  in hyprlang. The Lua API expects a callback (`hl.define_submap("name",
  function() hl.bind(...) end)`), which requires reordering the source. The
  converter emits a TODO at the `submap =` line so users can wrap the
  following block by hand.
- `plugin { name { ... } }` — plugin sections are passed through as a Lua
  comment block with a TODO, since each plugin exposes its own keys under
  `hl.plugin.<name>` and we can't safely guess the API.
- `envd` is converted to `hl.env(...)`; the systemd/D-Bus propagation that
  `envd` provided needs to be replicated outside Lua.

Anything not in either list is preserved with a `-- TODO: manual review`
comment, contributes to `flagged` in the report, and trips `--check`.

## Architecture

```
internal/converter/   pure Go — lexer, parser, AST, Lua codegen.
                      no os, no net, no filesystem. wasm-compatible.
                      single entry point: Convert(src) -> (lua, Report, err)
cmd/hyprlang2lua/     thin CLI wrapper over the converter package.
```

The core is deliberately I/O-free so the same code backs both the CLI and
the WebAssembly build at `web/wasm/main.go`.

## Browser build

`web/` contains a self-contained converter UI that runs entirely client-side
— the conversion happens in WebAssembly compiled from the same
`internal/converter` package, so no input ever leaves the page.

Build the wasm artifact and serve the directory:

```sh
cd web/wasm
GOOS=js GOARCH=wasm go build -o ../main.wasm .
cd ..
python3 -m http.server 8080     # or any static server
```

Then open `http://localhost:8080/`. The wasm module exposes a single global,
`window.hyprlang2lua.convert(src)`, returning `{ lua, translated, passthrough,
flagged, coverage, notes, error }`.

## Tests

```sh
go test ./...
go test ./internal/converter -fuzz FuzzConvert -fuzztime 30s   # quick fuzz
go test ./internal/converter -run TestGolden -update           # refresh goldens
```

Golden fixtures live in `internal/converter/testdata/`; each `.conf` is
paired with the expected `.lua` output. `FuzzConvert` exercises the lexer
and parser against random byte sequences to catch panics.

## Authoritative sources

Mappings were derived from, in priority order:

1. `/usr/share/hypr/stubs/hl.meta.lua` — the autogenerated Lua API stubs
   shipped with Hyprland 0.55 (definitive list of `hl.*` functions, the
   `HL.ConfigKey` set, and every `*Spec` type).
2. `/usr/share/hypr/hyprland.lua` — the shipped example, used as a style
   reference for idiomatic table layout.
3. The Hyprland wiki and release notes for legacy hyprlang field names.

## License

MIT.
