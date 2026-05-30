package converter

import (
	"strings"
	"testing"
)

// TestEnvVarFallback locks in the fix for the silent-breakage bug where
// undeclared `$X` references inside exec strings were eagerly rewritten
// to nil Lua locals. The correct behaviour preserves the literal `$X`
// inside the emitted Lua string so /bin/sh -c expands it at exec time —
// matching hyprlang's own pass-through semantic.
//
// Bind keys deliberately use the opposite policy: undeclared $X is
// emitted as a Lua local reference so it fast-fails at config load
// with a clear nil-concat error, surfacing the typo instead of
// silently producing a non-matching bind. See TestUndeclaredVarInBindFastFails.
func TestEnvVarFallback(t *testing.T) {
	src := `$mainMod = SUPER
bind = $mainMod, X, exec, echo $HOME
exec-once = systemctl --user start $XDG_CURRENT_DESKTOP
bind = $mainMod SHIFT, Q, exec, $TERMINAL -- $mainMod
`
	out, _, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{
		`hl.bind(mainMod .. " + X",`,
		`hl.dsp.exec_cmd("echo $HOME")`,
		`hl.exec_cmd("systemctl --user start $XDG_CURRENT_DESKTOP")`,
		`hl.dsp.exec_cmd("$TERMINAL -- " .. mainMod)`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing fragment %q in:\n%s", want, out)
		}
	}
	for _, forbidden := range []string{
		`.. HOME`,
		`.. XDG_CURRENT_DESKTOP`,
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("output still contains broken bare ref %q:\n%s", forbidden, out)
		}
	}
}

// TestUndeclaredVarInBindFastFails confirms that an undeclared $var in a
// bind's mod position still emits a Lua local reference (not a literal
// string), so the missing declaration surfaces as a clear runtime error
// instead of a silently non-matching bind.
func TestUndeclaredVarInBindFastFails(t *testing.T) {
	// $UNDECLARED is never assigned — at config load this will throw
	// `attempt to concatenate a nil value (global 'UNDECLARED')`,
	// pointing the user straight at the typo.
	src := `bind = $UNDECLARED, K, exec, foo
`
	out, _, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if !strings.Contains(out, `hl.bind(UNDECLARED .. " + K",`) {
		t.Errorf("undeclared $X in bind mod should emit a Lua local ref (fast-fail), got:\n%s", out)
	}
	if strings.Contains(out, `"$UNDECLARED + K"`) {
		t.Errorf("undeclared $X in bind mod must not be preserved as literal text (would silently no-op):\n%s", out)
	}
}

// TestCssShorthandGaps verifies that gaps_in/gaps_out/float_gaps with
// 2-, 3- or 4-value CSS shorthand emit a typed HL.CssGap struct rather
// than a string (which the typed API rejects at config load).
func TestCssShorthandGaps(t *testing.T) {
	src := `general {
    gaps_in = 5
    gaps_out = 5 10
    float_gaps = 1 2 3
}
general:gaps_in = 5 10 15 20
`
	out, _, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{
		`gaps_out = { top = 5, right = 10, bottom = 5, left = 10 },`,
		`float_gaps = { top = 1, right = 2, bottom = 3, left = 2 },`,
		`gaps_in = { top = 5, right = 10, bottom = 15, left = 20 },`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// TestSourceGlobPolyfill verifies that a source pattern with glob chars
// emits hl_source_glob (with .conf→.lua) and that the path-component
// substitution doesn't mangle directory names like `.config`.
func TestSourceGlobPolyfill(t *testing.T) {
	src := `source = ~/.config/hypr/conf.d/*.conf
source = ./themes/*.conf
source = colors/single.conf
`
	out, _, err := ConvertWithOptions(src, Options{Polyfill: true})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{
		`local function hl_source_glob(pattern)`,
		`hl_source_glob("~/.config/hypr/conf.d/*.lua")`,
		`hl_source_glob("./themes/*.lua")`,
		`require("colors.single")`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, ".luaig") {
		t.Errorf("path-component swap leaked into directory name:\n%s", out)
	}

	out, _, err = ConvertWithOptions(src, Options{Polyfill: false})
	if err != nil {
		t.Fatalf("Convert (no polyfill): %v", err)
	}
	if !strings.Contains(out, "TODO: manual review — glob source") {
		t.Errorf("polyfill=false should flag glob sources:\n%s", out)
	}
	if strings.Contains(out, "local function hl_source_glob") {
		t.Errorf("polyfill=false should NOT emit hl_source_glob helper:\n%s", out)
	}
}

// TestEnvVsEnvd confirms the env/envd distinction migrates to the
// `dbus` boolean of hl.env's three-arg signature (per Hyprland source:
// src/config/lua/bindings/LuaBindingsConfigRules.cpp).
//
// `env = K, V`   → hl.env(K, V)         — no propagation
// `envd = K, V`  → hl.env(K, V, true)   — with propagation (systemctl
//                                          --user import-environment +
//                                          dbus-update-activation-environment)
func TestEnvVsEnvd(t *testing.T) {
	src := `env = XCURSOR_SIZE, 24
envd = XDG_CURRENT_DESKTOP, Hyprland
envd = WAYLAND_DISPLAY, wayland-0
`
	out, _, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{
		`hl.env("XCURSOR_SIZE", "24")`,                       // plain env, no dbus
		`hl.env("XDG_CURRENT_DESKTOP", "Hyprland", true)`,    // envd, dbus=true
		`hl.env("WAYLAND_DISPLAY", "wayland-0", true)`,       // envd, dbus=true
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// XCURSOR_SIZE was plain env — must NOT get the dbus=true arg.
	if strings.Contains(out, `hl.env("XCURSOR_SIZE", "24", true)`) {
		t.Errorf("plain env should not get dbus=true:\n%s", out)
	}
}

// TestNewDispatchersUseTableArgs verifies that signal / setprop /
// killwindow / closewindow / fakefullscreen / fullscreenstate /
// alter_zorder / tag emit the typed-table form the 0.55 API requires,
// not the legacy positional form. Mis-emitting positional args would
// fail at config load with a type-mismatch error.
//
// killwindow → kill (SIGKILL) per the 0.54 wiki: "killwindow kills a
// specified window". Despite the killactive / closewindow siblings
// being graceful, killwindow really did SIGKILL.
func TestNewDispatchersUseTableArgs(t *testing.T) {
	src := `bind = SUPER, J, signal, 9
bind = SUPER, P, setprop, ^(kitty)$ opacity 0.5
bind = SUPER, Q, killwindow, class:^firefox$
bind = SUPER, W, closewindow, class:^chromium$
bind = SUPER, F, fakefullscreen
bind = SUPER, G, togglefakefullscreen
bind = SUPER, S, fullscreenstate, 0 2
bind = SUPER ALT, S, fullscreenstate, 0 2 set
bind = SUPER, T, tagwindow, work
bind = SUPER ALT, T, tagwindow, +music ^(spotify)$
bind = SUPER, Z, alterzorder, top
bind = SUPER ALT, Z, alterzorder, bottom, ^(kitty)$
bind = SUPER, X, signalwindow, class:Alacritty, 9
`
	out, rpt, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{
		`hl.dsp.window.signal({ signal = 9 })`,
		`hl.dsp.window.signal({ window = "class:Alacritty", signal = 9 })`,
		// value is always quoted: hl.window.set_prop's source requires a
		// string (not a number) for both prop and value.
		`hl.dsp.window.set_prop({ prop = "opacity", value = "0.5", window = "^(kitty)$" })`,
		// 0.54 killwindow really is SIGKILL — different from killactive
		// (graceful) despite the matching naming.
		`hl.dsp.window.kill("class:^firefox$")`,
		`hl.dsp.window.close("class:^chromium$")`,
		// fakefullscreen / togglefakefullscreen → internal=0, client=2
		// (the example the Fullscreenstate wiki gives verbatim).
		`hl.dsp.window.fullscreen_state({ internal = 0, client = 2, action = "toggle" })`,
		// fullscreenstate 0 2 (no action) — 0.54's default was "toggle";
		// 0.55's hl.window.fullscreen_state defaults to "set" instead
		// (per Hyprland source). Always emit action="toggle" explicitly
		// to preserve 0.54 semantics.
		`hl.dsp.window.fullscreen_state({ internal = 0, client = 2, action = "toggle" })`,
		// fullscreenstate with explicit action keeps it.
		`hl.dsp.window.fullscreen_state({ internal = 0, client = 2, action = "set" })`,
		`hl.dsp.window.tag({ tag = "work" })`,
		`hl.dsp.window.tag({ tag = "+music", window = "^(spotify)$" })`,
		`hl.dsp.window.alter_zorder({ mode = "top" })`,
		`hl.dsp.window.alter_zorder({ mode = "bottom", window = "^(kitty)$" })`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if rpt.Flagged != 0 {
		t.Errorf("expected no flagged dispatchers, got %d: %+v", rpt.Flagged, rpt.Notes)
	}
}

// TestGroupDispatchersUseTableArgs covers the group-namespace migrations:
// lock_active / lock take {action=...}, deny_from_group remaps
// on/off/toggle → set/unset/toggle, changegroupactive splits into
// next()/prev()/active({index=N}), moveintogroup lives on the window
// namespace (not group). moveoutofgroup's arg is a WINDOW SELECTOR in
// 0.54, not a direction (per the 0.54 wiki: "left empty / active for
// current, or window for a specific window"). movewindoworgroup and
// movegroupwindow are also covered.
func TestGroupDispatchersUseTableArgs(t *testing.T) {
	src := `bind = SUPER, L, lockactivegroup, lock
bind = SUPER, U, lockactivegroup, unlock
bind = SUPER SHIFT, L, lockgroups, toggle
bind = SUPER, N, changegroupactive, f
bind = SUPER, P, changegroupactive, b
bind = SUPER, 2, changegroupactive, 2
bind = SUPER, D, denywindowfromgroup, on
bind = SUPER SHIFT, D, denywindowfromgroup, off
bind = SUPER CTRL, D, denywindowfromgroup, toggle
bind = SUPER, I, moveintogroup, l
bind = SUPER, O, moveoutofgroup
bind = SUPER SHIFT, O, moveoutofgroup, class:^firefox$
bind = SUPER, M, movewindoworgroup, l
bind = SUPER, F, movegroupwindow, f
bind = SUPER, B, movegroupwindow, b
`
	out, rpt, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{
		`hl.dsp.group.lock_active({ action = "lock" })`,
		`hl.dsp.group.lock_active({ action = "unlock" })`,
		`hl.dsp.group.lock({ action = "toggle" })`,
		`hl.dsp.group.next()`,
		`hl.dsp.group.prev()`,
		`hl.dsp.group.active({ index = 2 })`,
		`hl.dsp.window.deny_from_group({ action = "set" })`,
		`hl.dsp.window.deny_from_group({ action = "unset" })`,
		`hl.dsp.window.deny_from_group({ action = "toggle" })`,
		// moveintogroup lives on the WINDOW namespace per the wiki.
		`hl.dsp.window.move({ into_group = "l" })`,
		`hl.dsp.window.move({ out_of_group = true })`,
		// moveoutofgroup arg is a WINDOW SELECTOR, not a direction.
		`hl.dsp.window.move({ out_of_group = true, window = "class:^firefox$" })`,
		// movewindoworgroup → into_or_create_group per the wiki.
		`hl.dsp.window.move({ into_or_create_group = "l" })`,
		// movegroupwindow: b → forward=false, anything else → forward=true.
		`hl.dsp.group.move_window({ forward = true })`,
		`hl.dsp.group.move_window({ forward = false })`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if rpt.Flagged != 0 {
		t.Errorf("expected no flagged dispatchers, got %d: %+v", rpt.Flagged, rpt.Notes)
	}
}

// TestWorkspaceDispatchersUseTableArgs covers the workspace-namespace
// migrations: rename / move / swap_monitors all take tables now.
// renameworkspace with a single arg resolves the active workspace at
// dispatch time (the wiki spec requires an explicit workspace selector).
func TestWorkspaceDispatchersUseTableArgs(t *testing.T) {
	src := `bind = SUPER, R, renameworkspace, foo
bind = SUPER SHIFT, R, renameworkspace, 2 bar
bind = SUPER, M, moveworkspacetomonitor, 1 DP-2
bind = SUPER, S, swapactiveworkspaces, DP-1 DP-2
bind = SUPER, 5, focusworkspaceoncurrentmonitor, 5
`
	out, rpt, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{
		// Single-arg rename: closure that captures active workspace id.
		`hl.dispatch(hl.dsp.workspace.rename({ workspace = w.id, name = "foo" }))`,
		// Two-arg rename: explicit workspace.
		`hl.dsp.workspace.rename({ workspace = 2, name = "bar" })`,
		`hl.dsp.workspace.move({ workspace = 1, monitor = "DP-2" })`,
		`hl.dsp.workspace.swap_monitors({ monitor1 = "DP-1", monitor2 = "DP-2" })`,
		// focusworkspaceoncurrentmonitor is NATIVE in 0.55+ — no polyfill.
		`hl.dsp.focus({ workspace = 5, on_current_monitor = true })`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// No polyfill helper should be needed for focusworkspaceoncurrentmonitor.
	if strings.Contains(out, "hl_focus_ws_on_active_mon") {
		t.Errorf("focusworkspaceoncurrentmonitor must not emit a polyfill (native API exists):\n%s", out)
	}
	if rpt.Flagged != 0 {
		t.Errorf("expected no flagged dispatchers, got %d: %+v", rpt.Flagged, rpt.Notes)
	}
}

// TestExecrOnceNativeDispatch confirms that execr-once routes through
// the native hl.dsp.exec_raw dispatcher (wrapped in hl.dispatch so it
// can fire from a top-level startup hook) instead of a hand-rolled
// setsid wrapper. The wiki defines exec_raw as "execute a raw command.
// While exec_cmd will do sh -c, this won't."
func TestExecrOnceNativeDispatch(t *testing.T) {
	src := `execr-once = swayidle -w timeout 300 'swaylock -f'
exec-once = waybar
`
	out, _, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{
		`hl.dispatch(hl.dsp.exec_raw("swayidle -w timeout 300 'swaylock -f'"))`,
		`hl.exec_cmd("waybar")`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	for _, forbidden := range []string{
		"hl_exec_raw",
		"setsid -f /bin/sh -c",
		// The old TODO marker for the unhelpful warning should be gone too.
		"raw spawn / no signal-mask inherit",
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("output should not contain the obsolete exec_raw polyfill %q:\n%s", forbidden, out)
		}
	}
}

// TestMoveCurrentWorkspaceToMonitor confirms the 0.54 dispatcher
// `movecurrentworkspacetomonitor` (the actual name; my earlier code
// invented "moveworkspaceactivemon" which never existed) inlines a
// closure that resolves the active workspace at dispatch time and
// hl.dispatch'es workspace.move with a static monitor name.
func TestMoveCurrentWorkspaceToMonitor(t *testing.T) {
	src := `bind = SUPER SHIFT, M, movecurrentworkspacetomonitor, DP-2
`
	out, rpt, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{
		`function() local w = hl.get_active_workspace(); if not w then return end`,
		`hl.dispatch(hl.dsp.workspace.move({ workspace = w.id, monitor = "DP-2" })) end`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if rpt.Flagged != 0 {
		t.Errorf("expected no flag, got %d: %+v", rpt.Flagged, rpt.Notes)
	}
}

// TestHyprctlDispatchRewrite confirms that `exec, hyprctl dispatch X args`
// gets rewritten to the direct hl.dsp.* call instead of an exec_cmd.
// The inner dispatcher now uses the typed table form too.
func TestHyprctlDispatchRewrite(t *testing.T) {
	src := `bind = SUPER, K, exec, hyprctl dispatch togglefloating
bind = SUPER, M, exec, hyprctl dispatch movetoworkspace 3
bind = SUPER, R, exec, hyprctl reload
`
	out, _, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if !strings.Contains(out, `hl.bind("SUPER + K", hl.dsp.window.float({ action = "toggle" }))`) {
		t.Errorf("hyprctl dispatch togglefloating did not rewrite:\n%s", out)
	}
	if !strings.Contains(out, `hl.dsp.window.move({ workspace = 3 })`) {
		t.Errorf("hyprctl dispatch movetoworkspace did not rewrite:\n%s", out)
	}
	if !strings.Contains(out, `hl.dsp.exec_cmd("hyprctl reload")`) {
		t.Errorf("hyprctl reload should stay as exec_cmd (only `hyprctl dispatch` is rewritten):\n%s", out)
	}
}

// TestLoadconfigInlineDispatcher confirms that loadconfig emits an
// inline function literal calling hyprctl reload — no helper preamble
// needed for a one-liner that's only ever bound once.
func TestLoadconfigInlineDispatcher(t *testing.T) {
	src := `bind = SUPER, R, loadconfig
`
	out, rpt, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if !strings.Contains(out, `hl.bind("SUPER + R", function() hl.exec_cmd("hyprctl reload") end)`) {
		t.Errorf("loadconfig should emit inline reload closure, got:\n%s", out)
	}
	if strings.Contains(out, "local function hl_reload") {
		t.Errorf("loadconfig should NOT emit a helper preamble (it's a one-liner inline):\n%s", out)
	}
	if rpt.Flagged != 0 {
		t.Errorf("loadconfig should not be flagged anymore, got %d", rpt.Flagged)
	}
}

// TestSwapVsCycleNext locks in the corrected semantics:
//   - swapnext  → hl.dsp.window.swap (swaps the windows in place)
//   - cyclenext → hl.dsp.window.cycle_next (focuses the next window)
//
// Pre-fix both went through cycle_next, which silently broke swap binds.
func TestSwapVsCycleNext(t *testing.T) {
	src := `bind = SUPER, N, cyclenext
bind = SUPER, P, cyclenext, prev
bind = SUPER, T, cyclenext, tiled
bind = SUPER SHIFT, N, swapnext
bind = SUPER SHIFT, P, swapnext, prev
bind = SUPER, J, swapwindow, l
bind = SUPER, K, swapwindow, r
`
	out, rpt, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{
		`hl.dsp.window.cycle_next({ next = true })`,
		`hl.dsp.window.cycle_next({ prev = true })`,
		`hl.dsp.window.cycle_next({ next = true, tiled = true })`,
		`hl.dsp.window.swap({ next = true })`,
		`hl.dsp.window.swap({ prev = true })`,
		`hl.dsp.window.swap({ direction = "l" })`,
		`hl.dsp.window.swap({ direction = "r" })`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if rpt.Flagged != 0 {
		t.Errorf("expected no flags, got %d: %+v", rpt.Flagged, rpt.Notes)
	}
}

// TestSubmapTransformation locks in the buffering of bind* directives
// between `submap = NAME` and `submap = reset` into an
// hl.define_submap("name", function() ... end) block.
func TestSubmapTransformation(t *testing.T) {
	src := `bind = SUPER, R, submap, resize

submap = resize
bind = , H, resizeactive, -10 0
bind = , L, resizeactive, 10 0
bind = , escape, submap, reset
submap = reset

bind = SUPER, X, exec, kitty
`
	out, rpt, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{
		`hl.define_submap("resize", function()`,
		`    hl.bind("H", hl.dsp.window.resize({ x = -10, y = 0, relative = true }))`,
		`    hl.bind("L", hl.dsp.window.resize({ x = 10, y = 0, relative = true }))`,
		`    hl.bind("escape", hl.dsp.submap("reset"))`,
		`end)`,
		`hl.bind("SUPER + R", hl.dsp.submap("resize"))`,
		`hl.bind("SUPER + X", hl.dsp.exec_cmd("kitty"))`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "wrap the following binds in hl.define_submap") {
		t.Errorf("submap TODO leaked into output (should be auto-transformed now):\n%s", out)
	}
	if rpt.Flagged != 0 {
		t.Errorf("expected no flagged directives, got %d: %+v", rpt.Flagged, rpt.Notes)
	}
}
