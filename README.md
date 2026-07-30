# hyprlang2lua

Convert legacy [Hyprland](https://hypr.land) `hyprland.conf` (hyprlang) files
to the Lua configuration format introduced in Hyprland 0.55 (May 2026).
Mappings currently track Hyprland **0.56.1**.

**Try it online:** <https://eiontusk.github.io/hyprlang2lua/> — runs entirely
in your browser, no input ever leaves the page.

The converter is built around a hand-written lexer + recursive-descent parser
and a per-directive code generator. The output is idiomatic Lua that matches
the shape of the example config shipped at `/usr/share/hypr/hyprland.lua`,
mapping each hyprlang construct to the `hl.*` API exposed by the Lua stubs
at `/usr/share/hypr/stubs/hl.meta.lua`.

## Install

### Arch Linux (AUR)

```sh
paru -S hyprlang2lua          # or: yay -S hyprlang2lua
```

The PKGBUILD source lives at `packaging/aur/PKGBUILD`; release notes for
maintainers are in [`packaging/aur/MAINTAINING.md`](packaging/aur/MAINTAINING.md).

### Nix (flake)

```sh
nix run github:EIonTusk/hyprlang2lua -- input.conf > output.lua
nix profile install github:EIonTusk/hyprlang2lua    # persistent install
```

`nix develop` opens a dev shell with the Go toolchain, `gopls`, and `lua`
(used by the optional `luac -p` gate in the golden tests).

### Go (source)

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
| `    --no-merge`          | emit a separate `hl.X(...)` call per source line instead of merging mergeable APIs into one call. Merging is on by default and currently applies to `hl.config` — in practice it folds every per-section `hl.config({...})` into one call, with section-separating comments preserved inside the merged table. Other `hl.*` APIs (bind, window_rule, monitor, env, device, …) take one spec per call by design and pass through unchanged. |
| `    --no-polyfill`       | disable runtime Lua helper closures used to preserve hyprlang features without a direct Hyprland 0.55 typed-API equivalent (currently: percent-form `resizeactive`/`moveactive`, source globbing). Polyfill is on by default; passing this flag forces strict output and flags any such feature for manual review instead. |
| `    --hoist-vars`        | move every `$var = ...` rewrite into a single block at the top of the output instead of emitting each `local` in source position |
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
  `scrolling`, `quirks`, `input-capture`. Nested sections
  (`decoration { blur { } }`) emit nested Lua tables. Hyphenated routes
  (`input-capture`, `input:touchpad:tap-to-click`) are rewritten to the
  underscored spelling the Lua config registry uses — Hyprland's
  `luaConfigValueName` maps `-` to `_`, so the source spelling would be
  rejected as an unknown config key.
- `$var = value` → `local var = value`. References (`$var` on the right side
  of any directive) resolve to the local; mixed text builds a concat chain
  (`mainMod .. " + SHIFT + 1"`).
- The `bind` family — `bind`, `bindm`, `binde`, `bindr`, `bindl`, `bindn`,
  `bindo`, `bindt`, `bindi`, `bindp`, `bindc`, `bindd`, `bindu`, `bindx`, and
  any combined-flag form like `bindel` / `bindle`. Each flag suffix becomes the
  corresponding field on `HL.BindOptions`.
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
- `source = pattern/*.conf` (glob) → `hl_source_glob("pattern/*.lua")` when
  `--polyfill` is on. The runtime helper shell-expands the pattern via `ls`
  and `dofile`s each match. With `--no-polyfill` the directive is flagged.
- `submap = name` / `submap = reset` — bind directives between the two
  markers are buffered and emitted as a single `hl.define_submap("name",
  function() ... end)` block. Non-bind statements inside the block flush
  the buffer first, so source order survives even on weird input.
- `plugin { name { ... } }` — plugin sections are passed through as a Lua
  comment block with a TODO, since each plugin exposes its own keys under
  `hl.plugin.<name>` and we can't safely guess the API.
- `env = K, V` → `hl.env(K, V)` (no propagation). `envd = K, V` →
  `hl.env(K, V, true)` — `hl.env`'s third arg is the `dbus` boolean per
  Hyprland source (`src/config/lua/bindings/LuaBindingsConfigRules.cpp`):
  when true, hyprland calls `systemctl --user import-environment` and
  `dbus-update-activation-environment --systemd` for that variable.
- `execr-once = cmd` → `hl.dispatch(hl.dsp.exec_raw(cmd))` inside the
  `hyprland.start` hook. `exec_raw` is the native dispatcher per the
  [Dispatchers wiki](https://wiki.hypr.land/Configuring/Basics/Dispatchers/):
  "execute a raw command. While exec_cmd will do sh -c, this won't."
- Undeclared `$VAR` inside exec strings — preserved verbatim in the emitted
  Lua string so the downstream `/bin/sh -c` expansion still sees the sigil
  (the same fallback hyprlang uses for `$HOME` / `$XDG_*`). Undeclared `$X`
  in non-shell contexts (bind keys, config values, dispatcher table fields)
  still rewrites to a Lua local so the missing declaration surfaces as a
  clear load-time error instead of a silently non-matching bind.
- CSS-shorthand gap values (`gaps_in = 5 10 15 20`, etc.) — parsed into the
  typed `HL.CssGap` table `{ top, right, bottom, left }` per CSS box-shorthand
  rules. Applies to `general.gaps_in / gaps_out / float_gaps` and
  `monitorv2`'s `reserved` / `reserved_area`.
- Percent-form `resizeactive` / `moveactive` (e.g. `resizeactive 10% 5%`) and
  the `*windowpixel` / `exact` variants. The 0.55 typed dispatch API only
  accepts numeric pixels, so the converter emits a small runtime closure that
  resolves the percent at dispatch time against the active window or monitor
  and then calls `hl.dispatch(...)`. Disable with `--no-polyfill` to flag
  these instead.
- Dispatchers that moved to typed-table form in 0.55 — all mapped to
  their typed-table equivalents per the official wiki, with per-arg
  splitting where the 0.54 form packed multiple fields into one space-
  separated string: `signal`, `signalwindow`, `setprop` (`WIN PROP VAL [lock]`),
  `tagwindow` (`TAG [WIN]`), `alterzorder` (`MODE[,WIN]`),
  `fullscreenstate` (`INTERNAL CLIENT [ACTION]`), `fakefullscreen` /
  `togglefakefullscreen`, `lockactivegroup`, `lockgroups`,
  `denywindowfromgroup`, `changegroupactive`, `moveintogroup`,
  `moveoutofgroup` (WINDOW selector, not direction), `movewindoworgroup`,
  `movegroupwindow`, `cyclenext`, `swapnext`, `swapwindow`,
  `renameworkspace`, `moveworkspacetomonitor`, `swapactiveworkspaces`,
  `focusworkspaceoncurrentmonitor`. Legacy action vocabularies
  (`f`/`b`, `on`/`off`, `1`/`0`) translate to the new string forms
  (`next()`/`prev()`, `set`/`unset`, `lock`/`unlock`).
- `killactive` → `close()` (graceful, despite the name); `closewindow SEL`
  → `close(SEL)`; `killwindow SEL` → `kill(SEL)` (actually SIGKILL per
  the 0.54 wiki); `forcekillactive` → `kill()`.
- `movecurrentworkspacetomonitor MON` → inline closure resolving
  `hl.get_active_workspace().id` and dispatching `workspace.move({workspace, monitor=MON})`.
- `loadconfig` → inline `function() hl.exec_cmd("hyprctl reload") end`.
  No dispatcher equivalent in 0.55+; the one-liner makes a helper
  preamble unnecessary.
- `bind = ..., exec, hyprctl dispatch X args` — detected and rewritten to
  the direct `hl.dsp.*` call so the bind doesn't pay an `exec` per keypress.
  Other `hyprctl` subcommands (`reload`, `keyword`, `notify`, …) stay as
  literal `hl.dsp.exec_cmd` calls.
- `bindm = ..., resizewindow 1` / `resizewindow 2` (the mouse-bind
  force/block aspect-ratio modes — for mouse binds hyprlang passes the whole
  third CSV field as the dispatch arg) → `resize({ keep_aspect_ratio = true })`
  / `{ keep_aspect_ratio = false }`. These had no typed equivalent before
  Hyprland 0.56.
- `releaseinputcapture` → `hl.dsp.release_input_capture()` (both sides added
  in Hyprland 0.56).

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

### Deploy

The browser build is deployed to GitHub Pages by `.github/workflows/pages.yml`
on every push to `main`/`master` that touches the converter, `web/`, or the
module graph. The workflow builds `main.wasm` from source, stages the static
assets into `site/`, and hands them to `actions/deploy-pages@v4`.

One-time repo configuration: **Settings → Pages → Build and deployment →
Source** must be set to **"GitHub Actions"** (not "Deploy from a branch").
Without that, the deploy step fails with a 404. Trigger manually via the
**Actions** tab → *pages* → *Run workflow* if a redeploy is needed without
a code change.

## Tests

```sh
go test ./...
go test ./internal/converter -fuzz FuzzConvert -fuzztime 30s   # quick fuzz
go test ./internal/converter -run TestGolden -update           # refresh goldens
```

Golden fixtures live in `internal/converter/testdata/`; each `.conf` is
paired with the expected `.lua` output. `FuzzConvert` exercises the lexer
and parser against random byte sequences to catch panics.

### API surface gate

`TestAPISurface` executes every golden under a fake `hl` table and checks each
config key and bind option it touches against the API surface pinned in
`internal/converter/testdata/api/`. It exists to catch the one failure class
that neither the golden bytes nor `luac -p` can see: output that is valid Lua,
byte-identical to its golden, and still rejected at load because it names a
config key Hyprland doesn't have. The key walker deliberately mirrors
Hyprland's own `hlConfig` loop, so typed leaves (`HL.CssGap`, gradients) aren't
mistaken for nested sections. Skipped when no `lua` is on PATH.

The pinned lists are generated from Hyprland's own stub generator:

```sh
git clone --depth 1 --branch v0.56.1 https://github.com/hyprwm/Hyprland
python3 Hyprland/meta/generateLuaStubs.py --root Hyprland --output hl.meta.lua
# then extract the HL.ConfigKey alias and HL.BindOptions class into
# testdata/api/config_keys.txt and testdata/api/bind_options.txt
```

Regenerate them when retargeting a new Hyprland release — the resulting diff
*is* the list of config keys that moved, and bump
`testdata/api/HYPRLAND_VERSION` to match. The gate cannot check dispatcher
argument tables: the generated stubs type every dispatcher as
`fun(...): HL.Dispatcher`, so fields like `resize`'s `keep_aspect_ratio` carry
no type information there.

### Verifying against a real Hyprland

`TestHyprlandVerifyConfig` runs every golden through an actual Hyprland
binary's `--verify-config`, which loads the Lua config and reports errors
without starting a compositor. This is the authoritative gate — it checks
config keys, spec fields, dispatcher argument tables and rule effects against
the implementation itself:

```sh
HYPRLAND_BIN=/path/to/Hyprland go test ./internal/converter -run VerifyConfig
```

It is opt-in rather than PATH-discovered, and skips unless the binary's
`major.minor` matches `testdata/api/HYPRLAND_VERSION`, so an older local
install can't produce spurious failures (0.55.4 has no
`hl.dsp.release_input_capture` at all). It found the `hl.permission` bug where
the converter emitted `allow =` instead of the `mode =` that
`HL.PermissionSpec` declares — output that was valid Lua, matched its golden,
and passed the pinned-surface check, but was rejected at config load.

You don't need to install Hyprland to run this. On Arch, extracting the package
is enough:

```sh
curl -O https://archive.archlinux.org/packages/h/hyprland/hyprland-0.56.0-2-x86_64.pkg.tar.zst
tar -I zstd -xf hyprland-0.56.0-2-x86_64.pkg.tar.zst
# add any libs the binary reports as missing (ldd usr/bin/Hyprland) the same way,
# then point LD_LIBRARY_PATH at them
```

## Contributing

PRs are squash-merged, so the PR title becomes the master commit subject —
and the release workflow (`.github/workflows/release-on-merge.yml`) reads
that subject to decide whether to cut a release and how to bump the version.
Use the [Conventional Commits](https://www.conventionalcommits.org) format:

| PR title prefix                              | Release |
|----------------------------------------------|---------|
| `feat:` / `feat(scope):`                     | minor   |
| `fix:` / `fix(scope):`                       | patch   |
| `feat!:` / `fix!:` / `<type>(scope)!:`       | major   |
| commit body contains `BREAKING CHANGE:`      | major   |
| `chore:` / `docs:` / `refactor:` / `ci:` / `test:` / non-conventional | no release |

When a release-triggering commit lands on master, the workflow tags the
version, regenerates `packaging/aur/PKGBUILD` + `.SRCINFO`, bumps
`flake.nix`, fixes up a stale `vendorHash` if needed, opens a GitHub
Release, and pushes to the AUR. None of that requires anything from you —
just title the PR correctly.

If a release fails or you need an out-of-band cut, dispatch the workflow
manually from the **Actions** tab → *release-on-merge* → *Run workflow* and
pick `patch` / `minor` / `major`.

## Authoritative sources

Mappings were derived from, in priority order:

1. `/usr/share/hypr/stubs/hl.meta.lua` — the autogenerated Lua API stubs
   shipped with Hyprland (definitive list of `hl.*` functions, the
   `HL.ConfigKey` set, and every `*Spec` type).
2. `/usr/share/hypr/hyprland.lua` — the shipped example, used as a style
   reference for idiomatic table layout.
3. The Hyprland wiki and release notes for legacy hyprlang field names.

## License

MIT.
