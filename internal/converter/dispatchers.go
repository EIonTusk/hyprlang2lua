package converter

import (
	"fmt"
	"strconv"
	"strings"
)

// buildDispatcher maps a hyprlang dispatcher name + comma-split args into a
// Lua expression that evaluates to a HL.Dispatcher. The full list of legacy
// names was derived from Hyprland's source (src/managers/KeybindManager.cpp)
// plus what's exposed in the Lua stubs under hl.dsp.* (see hl.meta.lua).
//
// Returns (expr, "") on success; (_, reason) on failure, where reason is a
// short human-readable explanation suitable for a TODO marker. We aim for
// *exact* parity with the dispatcher namespace shipped in 0.55 — anything
// outside that namespace must go through manual review, since hl.dispatch
// only accepts HL.Dispatcher values. A non-empty reason distinguishes
// "name we don't know" from "name we know but these arguments are wrong",
// so the caller can produce a useful flag note.
//
// Bound to the generator so dispatcher emissions share its declared-$var
// set, polyfill flag, and helper-needed bookkeeping.
func (g *generator) buildDispatcher(name string, args []string) (string, string) {
	return g.buildDispatcherRaw(name, args, joinArgs(args))
}

// buildDispatcherRaw is buildDispatcher for callers that still hold the
// argument text as the user wrote it. rawArgs is the unsplit remainder after
// the dispatcher's comma; dispatchers whose argument is verbatim text read it
// instead of re-joining the split fields, which would turn `echo a,b` into
// `echo a, b`. Everything else ignores it — reconstructing the string is only
// lossy for the comma joiner itself.
func (g *generator) buildDispatcherRaw(name string, args []string, rawArgs string) (string, string) {
	decls := g.declaredVars
	polyfill := g.polyfill
	switch name {
	// ---- Process / system ----
	case "exec":
		// 'exec, hyprctl dispatch X args' is the legacy idiom for invoking
		// another dispatcher through a child process. We can spot that case
		// and recurse so the bind becomes a direct hl.dsp.* call — one
		// fewer process spawn per keypress, and clearer generated Lua.
		if disp, sub := tryHyprctlDispatch(rawArgs); disp != "" {
			expr, reason := g.buildDispatcher(disp, sub)
			if reason == "" {
				return expr, ""
			}
			// If recursion fails, fall through to the literal exec emission
			// so the user still gets a working (if uglier) result.
		}
		// Shell-context formatting: declared $vars rewrite to locals,
		// undeclared $HOME / $XDG_* survive literally for /bin/sh to expand.
		return fmt.Sprintf("hl.dsp.exec_cmd(%s)", g.fmtShell(rawArgs)), ""
	case "execr":
		// exec_raw runs cmd directly (no `sh -c`), per the wiki: it
		// still benefits from declared-$var rewriting, but undeclared
		// refs survive (the eventual exec'd program may interpret `$X`).
		return fmt.Sprintf("hl.dsp.exec_raw(%s)", g.fmtShell(rawArgs)), ""
	case "exit":
		return "hl.dsp.exit()", ""
	case "forcerendererreload":
		return "hl.dsp.force_renderer_reload()", ""
	case "releaseinputcapture":
		// Added in Hyprland 0.56 on both front-ends at once — legacy
		// 'releaseinputcapture' (src/config/legacy/DispatcherTranslator.cpp)
		// and typed hl.dsp.release_input_capture(). No args either side.
		return "hl.dsp.release_input_capture()", ""
	case "noop", "no_op":
		// Wiki: `no_op()`. Useful for unbinding default keybinds in
		// conditional configs (rare in legacy hyprlang, but present).
		return "hl.dsp.no_op()", ""
	case "forceidle":
		// Wiki: `force_idle(seconds)` — positional numeric.
		return fmt.Sprintf("hl.dsp.force_idle(%s)", formatValue(joinArgs(args), nil)), ""
	case "dpms":
		// 0.54: "on, off, or toggle. For specific monitor add monitor
		// name after a space" → single space-separated arg. 0.55 wiki:
		// `dpms({ action?, monitor? })` — table form, action is a
		// string (not the boolean coercion formatValue would give to
		// "on"/"off").
		joined := strings.TrimSpace(joinArgs(args))
		action, monitor := splitFirstWord(joined)
		if action == "" {
			return "", "dpms needs on/off/toggle"
		}
		if monitor == "" {
			return fmt.Sprintf("hl.dsp.dpms({ action = %s })", quoteLuaString(action)), ""
		}
		return fmt.Sprintf(
			"hl.dsp.dpms({ action = %s, monitor = %s })",
			quoteLuaString(action), quoteLuaString(monitor),
		), ""
	case "event":
		// Wiki: `event(string)` — positional string. No table form.
		return fmt.Sprintf("hl.dsp.event(%s)", quoteLuaString(joinArgs(args))), ""
	case "global":
		// Wiki: `global(string)` — positional.
		return fmt.Sprintf("hl.dsp.global(%s)", quoteLuaString(joinArgs(args))), ""
	case "pass":
		// 0.54: "window". 0.55 source (LuaBindingsDispatchers.cpp:
		// hlPass uses requireTableFieldWindowSelector): the `window`
		// field is REQUIRED — `pass({})` would error at config load
		// with "window required". So flag bare invocations.
		joined := strings.TrimSpace(joinArgs(args))
		if joined == "" {
			return "", "pass needs a window selector"
		}
		return fmt.Sprintf(
			"hl.dsp.pass({ window = %s })",
			formatValue(joined, nil),
		), ""
	case "sendshortcut":
		// 0.54: "mod, key[, window]" — comma-separated. 0.55 wiki:
		// `send_shortcut({ mods, key, window? })`. splitCommas at the
		// bind layer already split the args into 2 or 3.
		if len(args) < 2 {
			return "", fmt.Sprintf("sendshortcut needs MOD,KEY[,WINDOW] (got %q)", joinArgs(args))
		}
		expr := fmt.Sprintf(
			"hl.dsp.send_shortcut({ mods = %s, key = %s",
			quoteLuaString(strings.TrimSpace(args[0])),
			quoteLuaString(strings.TrimSpace(args[1])),
		)
		if len(args) >= 3 {
			expr += ", window = " + formatValue(strings.TrimSpace(args[2]), nil)
		}
		return expr + " })", ""
	case "sendkeystate":
		// 0.54: "mod, key, state, window" — 3 or 4 comma-separated. 0.55
		// wiki: `send_key_state({ mods, key, state, window? })`.
		if len(args) < 3 {
			return "", fmt.Sprintf("sendkeystate needs MOD,KEY,STATE[,WINDOW] (got %q)", joinArgs(args))
		}
		expr := fmt.Sprintf(
			"hl.dsp.send_key_state({ mods = %s, key = %s, state = %s",
			quoteLuaString(strings.TrimSpace(args[0])),
			quoteLuaString(strings.TrimSpace(args[1])),
			quoteLuaString(strings.TrimSpace(args[2])),
		)
		if len(args) >= 4 {
			expr += ", window = " + formatValue(strings.TrimSpace(args[3]), nil)
		}
		return expr + " })", ""
	case "loadconfig":
		// 0.55+ removed loadconfig as a typed dispatcher (the Lua state
		// is re-executed automatically on save and on `hyprctl reload`).
		// Inline the equivalent so the bind keeps working — no helper
		// needed, the wrapping function is trivial.
		return `function() hl.exec_cmd("hyprctl reload") end`, ""

	// ---- Submap ----
	case "submap":
		return fmt.Sprintf("hl.dsp.submap(%s)", quoteLuaString(joinArgs(args))), ""

	// ---- Focus / cursor ----
	case "movefocus":
		return focusDirectionExpr(args)
	case "focuswindow":
		return fmt.Sprintf("hl.dsp.focus({ window = %s })", g.fmtVal(joinArgs(args))), ""
	case "focusmonitor":
		return fmt.Sprintf("hl.dsp.focus({ monitor = %s })", g.fmtVal(joinArgs(args))), ""
	case "focusurgentorlast":
		return "hl.dsp.focus({ urgent_or_last = true })", ""
	case "focuscurrentorlast":
		// 0.54: "Switch focus from current to previously focused window".
		// 0.55 wiki shows only `focus({ last })` — there is no
		// `current_or_last` field. The semantic ("focus the last") matches
		// `{ last = true }` exactly.
		return "hl.dsp.focus({ last = true })", ""
	case "movecursor":
		// 0.54: "x y" (space-separated). 0.55 wiki: `cursor.move({ x, y })`.
		fields := strings.Fields(strings.TrimSpace(joinArgs(args)))
		if len(fields) < 2 {
			return "", fmt.Sprintf("movecursor needs X Y (got %q)", joinArgs(args))
		}
		return fmt.Sprintf(
			"hl.dsp.cursor.move({ x = %s, y = %s })",
			formatValue(fields[0], nil), formatValue(fields[1], nil),
		), ""
	case "movecursortocorner":
		// 0.54: "direction, 0 - 3". 0.55 wiki:
		// `cursor.move_to_corner({ corner, window? })`.
		arg := strings.TrimSpace(joinArgs(args))
		if arg == "" {
			return "", "movecursortocorner needs a corner (0..3)"
		}
		return fmt.Sprintf(
			"hl.dsp.cursor.move_to_corner({ corner = %s })",
			formatValue(arg, nil),
		), ""

	// ---- Workspaces ----
	case "workspace":
		return fmt.Sprintf("hl.dsp.focus({ workspace = %s })", g.fmtVal(joinArgs(args))), ""
	case "movetoworkspace":
		return fmt.Sprintf("hl.dsp.window.move({ workspace = %s })", g.fmtVal(joinArgs(args))), ""
	case "movetoworkspacesilent":
		return fmt.Sprintf("hl.dsp.window.move({ workspace = %s, follow = false })", g.fmtVal(joinArgs(args))), ""
	case "togglespecialworkspace":
		// One of the few dispatchers that takes a positional string per
		// the wiki: `toggle_special(special_name)`.
		return fmt.Sprintf("hl.dsp.workspace.toggle_special(%s)", quoteLuaString(joinArgs(args))), ""
	case "renameworkspace":
		// Wiki: `rename({ workspace, name? })` — workspace is required.
		// The legacy single-arg form ('renameworkspace, foo') renames the
		// current workspace, so we resolve it at dispatch time. Two-arg
		// form ('2 foo') names workspace 2 directly.
		joined := strings.TrimSpace(joinArgs(args))
		first, rest := splitFirstWord(joined)
		if rest == "" {
			return fmt.Sprintf(
				`function() local w = hl.get_active_workspace(); if not w then return end; hl.dispatch(hl.dsp.workspace.rename({ workspace = w.id, name = %s })) end`,
				quoteLuaString(first),
			), ""
		}
		return fmt.Sprintf(
			"hl.dsp.workspace.rename({ workspace = %s, name = %s })",
			formatValue(first, nil), quoteLuaString(rest),
		), ""
	case "swapactiveworkspaces":
		// Wiki: `swap_monitors({ monitor1, monitor2 })`. Legacy hyprlang
		// took two space-separated monitor names in one arg.
		joined := strings.TrimSpace(joinArgs(args))
		a, b := splitFirstWord(joined)
		if b == "" {
			return "", fmt.Sprintf("swapactiveworkspaces needs two monitor names (got %q)", joined)
		}
		return fmt.Sprintf(
			"hl.dsp.workspace.swap_monitors({ monitor1 = %s, monitor2 = %s })",
			quoteLuaString(a), quoteLuaString(b),
		), ""
	case "moveworkspacetomonitor":
		// Wiki: `workspace.move({ workspace?, monitor })`. Legacy form is
		// 'WORKSPACE MONITOR' as a single space-separated arg.
		joined := strings.TrimSpace(joinArgs(args))
		ws, mon := splitFirstWord(joined)
		if mon == "" {
			return "", fmt.Sprintf("moveworkspacetomonitor needs a workspace and a monitor (got %q)", joined)
		}
		return fmt.Sprintf(
			"hl.dsp.workspace.move({ workspace = %s, monitor = %s })",
			formatValue(ws, nil), quoteLuaString(mon),
		), ""
	case "focusworkspaceoncurrentmonitor":
		// 0.54 wiki: arg is a workspace selector. Native in 0.55+:
		// `focus({ workspace, on_current_monitor? })`.
		return fmt.Sprintf(
			"hl.dsp.focus({ workspace = %s, on_current_monitor = true })",
			g.fmtVal(joinArgs(args)),
		), ""
	case "movecurrentworkspacetomonitor":
		// 0.54 wiki: "Moves the active workspace to a monitor. monitor"
		// — arg is the destination monitor; the workspace to move is
		// whichever is currently focused. 0.55's workspace.move takes
		// a fixed `workspace`, so we resolve the active one at dispatch
		// time via hl.get_active_workspace().
		return fmt.Sprintf(
			`function() local w = hl.get_active_workspace(); if not w then return end; hl.dispatch(hl.dsp.workspace.move({ workspace = w.id, monitor = %s })) end`,
			formatValue(joinArgs(args), nil),
		), ""

	// ---- Windows ----
	//
	// 0.54 wiki — note the killactive/killwindow naming is misleading:
	//   killactive       — closes (NOT kills) the active window
	//   forcekillactive  — kills the active window
	//   closewindow WIN  — closes (graceful)
	//   killwindow  WIN  — KILLS (SIGKILL)  ← actually kills despite "close" siblings
	// So killactive/closewindow map to close(); killwindow/forcekillactive
	// map to kill().
	case "killactive":
		return "hl.dsp.window.close()", ""
	case "forcekillactive":
		return "hl.dsp.window.kill()", ""
	case "closewindow":
		return fmt.Sprintf("hl.dsp.window.close(%s)", g.fmtVal(joinArgs(args))), ""
	case "killwindow":
		return fmt.Sprintf("hl.dsp.window.kill(%s)", g.fmtVal(joinArgs(args))), ""
	case "centerwindow":
		// 0.54: "none (for monitor center) or 1 (to respect monitor
		// reserved area)". 0.55 wiki: `center({ window? })` — no field
		// for "respect reserved", so the 1 arg has no typed equivalent.
		// Note in a Lua block comment so the surrounding hl.bind(...)
		// call stays syntactically valid (a line `--` would consume
		// the bind's closing paren).
		if joined := strings.TrimSpace(joinArgs(args)); joined != "" {
			return "hl.dsp.window.center() --[[ legacy `" + joined + "` arg (respect-reserved) has no typed equivalent in 0.55 ]]", ""
		}
		return "hl.dsp.window.center()", ""
	case "fullscreen":
		// 0.54: "mode action, where mode can be 0/fullscreen or 1/maximize,
		// and action is optional and can be toggle (default), set or unset".
		// Always emit action="toggle" explicitly to preserve the 0.54
		// default — 0.55's default isn't documented in the wiki and may
		// differ.
		if len(args) == 0 {
			return `hl.dsp.window.fullscreen({ mode = "fullscreen", action = "toggle" })`, ""
		}
		switch strings.TrimSpace(joinArgs(args)) {
		case "0":
			return `hl.dsp.window.fullscreen({ mode = "fullscreen", action = "toggle" })`, ""
		case "1":
			return `hl.dsp.window.fullscreen({ mode = "maximized", action = "toggle" })`, ""
		default:
			return fmt.Sprintf(`hl.dsp.window.fullscreen({ mode = %s, action = "toggle" })`, g.fmtVal(joinArgs(args))), ""
		}
	case "fakefullscreen", "togglefakefullscreen":
		// Wiki Fullscreenstate table: 0=None, 2=Fullscreen. fakefullscreen
		// keeps the window non-fullscreen in the layout (internal=0) but
		// reports fullscreen to the client (client=2) — the exact example
		// the wiki gives for "Keeps the window non-fullscreen, but the
		// client goes into fullscreen mode within the window".
		return `hl.dsp.window.fullscreen_state({ internal = 0, client = 2, action = "toggle" })`, ""
	case "fullscreenstate":
		// 0.54 wiki: `internal client action` (space-separated, action
		// optional, defaults to "toggle"). 0.55 wiki:
		// `fullscreen_state({ internal, client, action?, window? })`.
		//
		// IMPORTANT: 0.55's hlWindowFullscreenState defaults action to
		// "set" (per Hyprland src/config/lua/bindings/LuaBindingsDispatchers.cpp:
		// `int action = 1; // default to set semantics`), but 0.54 defaulted
		// to "toggle". To preserve 0.54 behaviour we emit action="toggle"
		// explicitly whenever the source didn't specify one.
		fields := strings.Fields(strings.TrimSpace(joinArgs(args)))
		if len(fields) < 2 {
			return "", fmt.Sprintf("fullscreenstate needs INTERNAL CLIENT [action] (got %q)", joinArgs(args))
		}
		action := "toggle"
		if len(fields) >= 3 {
			action = fields[2]
		}
		return fmt.Sprintf(
			"hl.dsp.window.fullscreen_state({ internal = %s, client = %s, action = %s })",
			formatValue(fields[0], nil), formatValue(fields[1], nil), quoteLuaString(action),
		), ""
	case "togglefloating":
		return "hl.dsp.window.float({ action = \"toggle\" })", ""
	case "setfloating":
		return "hl.dsp.window.float({ action = \"set\" })", ""
	case "settiled":
		return "hl.dsp.window.float({ action = \"unset\" })", ""
	case "pseudo":
		return "hl.dsp.window.pseudo()", ""
	case "movewindow":
		// 0.54 wiki: "direction or mon: and a monitor, optionally followed
		// by a space and silent". 0.55 wiki splits move into:
		//   move({ direction, group_aware?, window? })
		//   move({ monitor, follow?, window? })
		// Bare invocation (used by bindm for interactive drag) maps to
		// the dedicated drag() entry.
		if len(args) == 0 {
			return "hl.dsp.window.drag()", ""
		}
		arg := strings.TrimSpace(joinArgs(args))
		if strings.HasPrefix(arg, "mon:") {
			rest := strings.TrimSpace(strings.TrimPrefix(arg, "mon:"))
			mon, suffix := splitFirstWord(rest)
			if strings.EqualFold(suffix, "silent") {
				return fmt.Sprintf(
					"hl.dsp.window.move({ monitor = %s, follow = false })",
					quoteLuaString(mon),
				), ""
			}
			return fmt.Sprintf(
				"hl.dsp.window.move({ monitor = %s })",
				quoteLuaString(mon),
			), ""
		}
		return fmt.Sprintf("hl.dsp.window.move({ direction = %s })", g.fmtVal(arg)), ""
	case "resizeactive", "resizewindow":
		if len(args) == 0 {
			return "hl.dsp.window.resize()", ""
		}
		expr, reason := pixelDispatchExpr("hl.dsp.window.resize", joinArgs(args), true, polyfill, decls)
		if reason != "" {
			return "", fmt.Sprintf("%s: %s", name, reason)
		}
		return expr, ""
	case "resizewindowpixel":
		expr, reason := pixelDispatchExpr("hl.dsp.window.resize", joinArgs(args), false, polyfill, decls)
		if reason != "" {
			return "", fmt.Sprintf("resizewindowpixel: %s", reason)
		}
		return expr, ""
	case "moveactive":
		expr, reason := pixelDispatchExpr("hl.dsp.window.move", joinArgs(args), true, polyfill, decls)
		if reason != "" {
			return "", fmt.Sprintf("moveactive: %s", reason)
		}
		return expr, ""
	case "movewindowpixel":
		expr, reason := pixelDispatchExpr("hl.dsp.window.move", joinArgs(args), false, polyfill, decls)
		if reason != "" {
			return "", fmt.Sprintf("movewindowpixel: %s", reason)
		}
		return expr, ""
	case "pin", "togglepin":
		// Wiki: `pin({ window? })`. Bare invocation needs no window.
		return "hl.dsp.window.pin()", ""
	case "tagwindow":
		// 0.54 wiki: "apply tag to current or the first window matching
		// tag [window], e.g. code ^(foot)$" — tag is the first space-
		// separated token, optional second token is the window selector.
		// The tag may carry a +/-/~ prefix (add/remove/toggle) that the
		// typed API parses internally.
		joined := strings.TrimSpace(joinArgs(args))
		tag, win := splitFirstWord(joined)
		if win == "" {
			return fmt.Sprintf(
				"hl.dsp.window.tag({ tag = %s })",
				quoteLuaString(tag),
			), ""
		}
		return fmt.Sprintf(
			"hl.dsp.window.tag({ tag = %s, window = %s })",
			quoteLuaString(tag), formatValue(win, nil),
		), ""
	case "cleartagswindow":
		// Wiki: `clear_tags({ window? })` — the window field is optional,
		// the table itself can be omitted with no args.
		return "hl.dsp.window.clear_tags()", ""
	case "alterzorder":
		// 0.54 wiki: `zheight[,window]` — comma-separated mode and
		// optional window selector. splitCommas at the bind level
		// already split them into 1-or-2 args.
		if len(args) == 0 {
			return "", "alterzorder needs a mode (top|bottom)"
		}
		if len(args) == 1 {
			return fmt.Sprintf(
				"hl.dsp.window.alter_zorder({ mode = %s })",
				quoteLuaString(strings.TrimSpace(args[0])),
			), ""
		}
		return fmt.Sprintf(
			"hl.dsp.window.alter_zorder({ mode = %s, window = %s })",
			quoteLuaString(strings.TrimSpace(args[0])),
			formatValue(strings.TrimSpace(args[1]), nil),
		), ""
	case "bringactivetotop":
		return "hl.dsp.window.bring_to_top()", ""
	case "signal":
		// 0.54 wiki: "sends a signal to the active window. signal" —
		// ONE arg (the signal number), always targets active. The
		// per-window form is `signalwindow` (different dispatcher).
		if len(args) == 0 {
			return "", "signal needs a signal number"
		}
		return fmt.Sprintf(
			"hl.dsp.window.signal({ signal = %s })",
			formatValue(strings.TrimSpace(args[0]), nil),
		), ""
	case "signalwindow":
		// 0.54 wiki: "window,signal, e.g. class:Alacritty,9" — comma-
		// separated WINDOW then SIGNAL. After splitCommas the bind
		// emitter hands us args = [WINDOW, SIGNAL].
		if len(args) < 2 {
			return "", fmt.Sprintf("signalwindow needs WINDOW,SIGNAL (got %q)", joinArgs(args))
		}
		return fmt.Sprintf(
			"hl.dsp.window.signal({ window = %s, signal = %s })",
			formatValue(strings.TrimSpace(args[0]), nil),
			formatValue(strings.TrimSpace(args[1]), nil),
		), ""
	case "setprop":
		// 0.54 wiki: "Sets a window property. window property value"
		// — three space-separated fields. An optional trailing "lock"
		// is the lock flag on legacy hyprlang (no typed equivalent).
		// We also tolerate the (technically-invalid-in-0.54) 2-field
		// form PROP VALUE since 0.55's set_prop window field is
		// optional and many configs in the wild wrote it that way.
		joined := strings.TrimSpace(joinArgs(args))
		fields := strings.Fields(joined)
		hasLock := false
		if n := len(fields); n > 0 && strings.EqualFold(fields[n-1], "lock") {
			hasLock = true
			fields = fields[:n-1]
		}
		var window, prop, value string
		switch len(fields) {
		case 2:
			// PROP VALUE (active window assumed in 0.55).
			prop, value = fields[0], fields[1]
		case 3:
			window, prop, value = fields[0], fields[1], fields[2]
		default:
			if len(fields) < 2 {
				return "", fmt.Sprintf("setprop needs WINDOW PROP VALUE (or PROP VALUE) (got %q)", joined)
			}
			// 4+ fields: window prop, then value may contain spaces
			// (e.g. a hex colour spec).
			window, prop = fields[0], fields[1]
			value = strings.Join(fields[2:], " ")
		}
		// 0.55 source (LuaBindingsDispatchers.cpp: hlWindowSetProp uses
		// requireTableFieldStr for both prop AND value) — `value` must
		// be a STRING. formatValue() would emit a numeric literal for
		// "0.5", which then fails at config load with "value must be a
		// string". Always quote value as a Lua string.
		var expr string
		if window == "" {
			expr = fmt.Sprintf(
				"hl.dsp.window.set_prop({ prop = %s, value = %s })",
				quoteLuaString(prop), quoteLuaString(value),
			)
		} else {
			expr = fmt.Sprintf(
				"hl.dsp.window.set_prop({ prop = %s, value = %s, window = %s })",
				quoteLuaString(prop), quoteLuaString(value), formatValue(window, nil),
			)
		}
		if hasLock {
			// Block comment so the surrounding hl.bind(...) closing paren
			// isn't swallowed.
			expr += " --[[ legacy `lock` flag dropped (no typed equivalent) ]]"
		}
		return expr, ""
	case "togglegroup":
		return "hl.dsp.group.toggle()", ""
	case "toggleswallow":
		// Wiki: `toggle_swallow()` — no args.
		return "hl.dsp.window.toggle_swallow()", ""
	case "setignoregrouplock":
		// 0.54: "Temporarily enable or disable binds:ignore_group_lock.
		// on, off, or toggle". No typed equivalent in 0.55 — the
		// binds.ignore_group_lock config is set at load time via hl.config
		// only; runtime toggling has been removed.
		return "", "setignoregrouplock: no typed Lua API equivalent in 0.55 (binds.ignore_group_lock is load-time only via hl.config)"
	case "lockactivegroup":
		// Wiki: `lock_active({ action? })` — action is a string. Legacy
		// hyprlang took lock/unlock/toggle (and numeric 1/0).
		action, err := normalizeLockAction(joinArgs(args))
		if err != "" {
			return "", "lockactivegroup: " + err
		}
		return fmt.Sprintf(
			"hl.dsp.group.lock_active({ action = %s })",
			quoteLuaString(action),
		), ""
	case "lockgroups":
		// Wiki: `lock({ action?, window? })`. Same action set as lockactivegroup.
		action, err := normalizeLockAction(joinArgs(args))
		if err != "" {
			return "", "lockgroups: " + err
		}
		return fmt.Sprintf(
			"hl.dsp.group.lock({ action = %s })",
			quoteLuaString(action),
		), ""
	case "moveintogroup":
		// 0.54: takes a direction. 0.55 wiki:
		// `move({ into_group = direction })` (on WINDOW namespace).
		return fmt.Sprintf(
			"hl.dsp.window.move({ into_group = %s })",
			quoteLuaString(joinArgs(args)),
		), ""
	case "movewindoworgroup":
		// 0.54 wiki: "Behaves as moveintogroup if there is a group in
		// the given direction. Behaves as moveoutofgroup if there is
		// no group in the given direction relative to the active group.
		// Otherwise behaves like movewindow." 0.55 has a single typed
		// form: `move({ into_or_create_group = direction })`.
		return fmt.Sprintf(
			"hl.dsp.window.move({ into_or_create_group = %s })",
			quoteLuaString(joinArgs(args)),
		), ""
	case "movegroupwindow":
		// 0.54 wiki: "Swaps the active window with the next or previous
		// in a group. b for back, anything else for forward". 0.55:
		// `group.move_window({ forward?, window? })` — boolean.
		switch strings.ToLower(strings.TrimSpace(joinArgs(args))) {
		case "b":
			return "hl.dsp.group.move_window({ forward = false })", ""
		default:
			return "hl.dsp.group.move_window({ forward = true })", ""
		}
	case "moveoutofgroup":
		// 0.54 wiki: "left empty / active for current, or window for a
		// specific window" — the arg is a WINDOW SELECTOR, never a
		// direction (despite what the symmetric moveintogroup form
		// might suggest). 0.55's typed API supports both directionless
		// and directional forms, but the legacy hyprlang never wrote a
		// direction here, so always emit out_of_group=true and stash
		// the window selector in the window field.
		if joined := strings.TrimSpace(joinArgs(args)); joined != "" {
			return fmt.Sprintf(
				"hl.dsp.window.move({ out_of_group = true, window = %s })",
				formatValue(joined, nil),
			), ""
		}
		return "hl.dsp.window.move({ out_of_group = true })", ""
	case "denywindowfromgroup":
		// Wiki: `deny_from_group({ action? })`. Legacy on/off/toggle ⇒
		// set/unset/toggle in the typed action vocabulary.
		action, err := normalizeOnOffAction(joinArgs(args))
		if err != "" {
			return "", "denywindowfromgroup: " + err
		}
		return fmt.Sprintf(
			"hl.dsp.window.deny_from_group({ action = %s })",
			quoteLuaString(action),
		), ""
	case "changegroupactive":
		// Legacy 'f' / 'b' (forward / backward) → group.next() / .prev().
		// Numeric → group.active({ index = N }).
		arg := strings.TrimSpace(joinArgs(args))
		switch strings.ToLower(arg) {
		case "f", "forward":
			return "hl.dsp.group.next()", ""
		case "b", "back", "backward":
			return "hl.dsp.group.prev()", ""
		}
		if _, err := strconv.Atoi(arg); err != nil {
			return "", fmt.Sprintf("changegroupactive: expected 'f', 'b', or an index (got %q)", arg)
		}
		return fmt.Sprintf(
			"hl.dsp.group.active({ index = %s })",
			arg,
		), ""
	case "swapnext":
		// swap (not cycle_next) is the wiki name; the optional arg picks
		// next/prev. Default = next.
		switch strings.ToLower(strings.TrimSpace(joinArgs(args))) {
		case "", "next":
			return "hl.dsp.window.swap({ next = true })", ""
		case "prev", "previous":
			return "hl.dsp.window.swap({ prev = true })", ""
		default:
			return "", fmt.Sprintf("swapnext: expected next or prev (got %q)", joinArgs(args))
		}
	case "cyclenext":
		// Wiki: `cycle_next({ next?, tiled?, floating?, window? })`.
		// Legacy tokens 'prev', 'tiled', 'floating' map to the table.
		fields := parseCycleNextArgs(joinArgs(args))
		return fmt.Sprintf("hl.dsp.window.cycle_next(%s)", fields), ""
	case "swapwindow":
		// Wiki shows both `swap({ direction })` and `swap({ target })`.
		// Single-letter args (l/r/u/d) are directions; anything longer
		// (a window selector) becomes target.
		arg := strings.TrimSpace(joinArgs(args))
		if isShortDirection(arg) {
			return fmt.Sprintf(
				"hl.dsp.window.swap({ direction = %s })",
				quoteLuaString(arg),
			), ""
		}
		return fmt.Sprintf(
			"hl.dsp.window.swap({ target = %s })",
			formatValue(arg, nil),
		), ""
	case "togglesplit":
		return `hl.dsp.layout("togglesplit")`, ""
	case "layoutmsg":
		return fmt.Sprintf("hl.dsp.layout(%s)", quoteLuaString(joinArgs(args))), ""
	case "togglefullscreen":
		return `hl.dsp.window.fullscreen({ mode = "fullscreen", action = "toggle" })`, ""
	case "splitratio":
		return fmt.Sprintf("hl.dsp.layout(%s)", quoteLuaString("splitratio "+joinArgs(args))), ""
	}

	// Unknown dispatcher name — not present in our switch.
	return "", fmt.Sprintf("no mapping for dispatcher %q", name)
}

// buildMouseDispatcher translates the dispatcher field of a `bindm`. Mouse
// binds are parsed unlike every other bind: Hyprland forces the handler to
// "mouse" and passes the WHOLE third CSV field through as the argument
// (src/config/legacy/ConfigManager.cpp: `COMMAND = mouse ? HANDLER :
// ARGS[3 + ...]`), so the ratio suffix in 'resizewindow 1' arrives as part of
// the dispatcher name rather than as a separate arg — which is why this can't
// live in buildDispatcher's name table.
//
// Actions::mouse reads that trailing integer as MBIND_RESIZE_FORCE_RATIO (1)
// or MBIND_RESIZE_BLOCK_RATIO (2), inside a try/catch that falls through to an
// unconstrained MBIND_RESIZE for anything else. Hyprland 0.56 exposes both
// ratio modes to Lua via resize({ keep_aspect_ratio }) — which pushes 1 for
// true and 2 for false; before 0.56 neither had a typed equivalent.
//
// Returns ok=false for fields this doesn't own (including a bare
// 'resizewindow', which the normal dispatcher table already handles), so the
// caller falls back to buildDispatcher.
func (g *generator) buildMouseDispatcher(field string) (string, bool) {
	head, tail := splitFirstWord(field)
	if !strings.EqualFold(head, "resizewindow") || tail == "" {
		return "", false
	}
	switch tail {
	case "1":
		return "hl.dsp.window.resize({ keep_aspect_ratio = true })", true
	case "2":
		return "hl.dsp.window.resize({ keep_aspect_ratio = false })", true
	default:
		return "hl.dsp.window.resize()", true
	}
}

// splitFirstWord returns the first whitespace-delimited word in s and
// the remainder (trimmed). An s with no whitespace returns (s, "").
func splitFirstWord(s string) (string, string) {
	s = strings.TrimSpace(s)
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimSpace(s[i+1:])
}

// splitTail looks for a trailing space-separated word matching `suffix`
// (case-insensitive) and returns (everythingBefore, suffix, true) if it
// matched. Used by setprop to peel off the legacy `lock` flag.
func splitTail(s, suffix string) (string, string, bool) {
	s = strings.TrimRight(s, " \t")
	if !strings.HasSuffix(strings.ToLower(s), " "+strings.ToLower(suffix)) {
		return s, "", false
	}
	return s[:len(s)-len(suffix)-1], suffix, true
}

// normalizeLockAction maps the legacy hyprlang group-lock arg vocabulary
// (lock / unlock / toggle, plus the older numeric 1 / 0) to the typed
// action string the wiki shows. An empty input defaults to "toggle".
// Returns ("", reason) when the input doesn't match anything known.
func normalizeLockAction(raw string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "toggle":
		return "toggle", ""
	case "lock", "1", "true", "on":
		return "lock", ""
	case "unlock", "0", "false", "off":
		return "unlock", ""
	}
	return "", fmt.Sprintf("expected lock/unlock/toggle (got %q)", raw)
}

// normalizeOnOffAction maps the legacy on / off / toggle (and numeric
// 1 / 0) into the set / unset / toggle vocabulary the typed window
// dispatchers use. Empty defaults to "toggle".
func normalizeOnOffAction(raw string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "toggle":
		return "toggle", ""
	case "on", "1", "true", "set":
		return "set", ""
	case "off", "0", "false", "unset":
		return "unset", ""
	}
	return "", fmt.Sprintf("expected on/off/toggle (got %q)", raw)
}

// isShortDirection reports whether `s` is one of hyprland's single-letter
// or short-word direction tokens. Used to decide whether a swap dispatcher
// arg is a direction (l/r/u/d) or a window target (anything else).
func isShortDirection(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "l", "r", "u", "d", "left", "right", "up", "down":
		return true
	}
	return false
}

// parseCycleNextArgs turns the space-separated legacy cyclenext flags
// ("", "prev", "tiled", "floating", or combinations like "tiled prev")
// into a Lua table literal for hl.dsp.window.cycle_next({...}). Unknown
// tokens pass through as a leading TODO comment so the user sees them.
func parseCycleNextArgs(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return `{ next = true }`
	}
	fields := strings.Fields(raw)
	var (
		next     = true
		tiled    bool
		floating bool
		unknown  []string
	)
	for _, f := range fields {
		switch strings.ToLower(f) {
		case "prev", "previous":
			next = false
		case "next":
			next = true
		case "tiled":
			tiled = true
		case "floating":
			floating = true
		default:
			unknown = append(unknown, f)
		}
	}
	var parts []string
	if next {
		parts = append(parts, "next = true")
	} else {
		parts = append(parts, "prev = true")
	}
	if tiled {
		parts = append(parts, "tiled = true")
	}
	if floating {
		parts = append(parts, "floating = true")
	}
	tbl := "{ " + strings.Join(parts, ", ") + " }"
	if len(unknown) > 0 {
		// Block comment so the surrounding hl.bind(...) closing paren
		// isn't swallowed by `--`.
		tbl = tbl + " --[[ cyclenext: unrecognized arg(s) " + strconv.Quote(strings.Join(unknown, " ")) + " ]]"
	}
	return tbl
}

// tryHyprctlDispatch recognises the legacy idiom `hyprctl dispatch X arg1 arg2`
// (and the no-space variant `hyprctl dispatch X,arg1,arg2`) inside an exec
// arg. Returns the inner dispatcher name and its comma-split args when the
// pattern matches; otherwise ("", nil) so the caller falls back to the
// literal exec_cmd path. Only `hyprctl dispatch` is rewritable — other
// hyprctl subcommands (reload, keyword, notify, …) stay as exec calls.
func tryHyprctlDispatch(s string) (string, []string) {
	s = strings.TrimSpace(s)
	const prefix = "hyprctl dispatch "
	if !strings.HasPrefix(s, prefix) {
		return "", nil
	}
	rest := strings.TrimSpace(s[len(prefix):])
	if rest == "" {
		return "", nil
	}
	// Inner form 1: dispatcher followed by space-separated args.
	// Inner form 2: dispatcher followed by comma-separated args (rare, but
	// hyprctl accepts both for compat).
	var name string
	var argStr string
	if i := strings.IndexAny(rest, " ,"); i >= 0 {
		name = rest[:i]
		argStr = strings.TrimSpace(rest[i+1:])
	} else {
		name = rest
	}
	if argStr == "" {
		return name, nil
	}
	// If the args used commas, splitCommas keeps the same behaviour as a
	// real bind arg list. Single-space form ends up as a single string,
	// which downstream emitters that call joinArgs/joinFormatted will
	// pass through unchanged.
	return name, splitCommas(argStr)
}

// focusDirectionExpr handles 'movefocus, l|r|u|d|left|right|up|down'.
// On invalid input, returns a reason naming the offending argument so the
// flag note can guide the user toward the fix.
func focusDirectionExpr(args []string) (string, string) {
	if len(args) == 0 {
		return "", "movefocus needs a direction argument (l, r, u, or d)"
	}
	raw := strings.TrimSpace(args[0])
	dir := strings.ToLower(raw)
	switch dir {
	case "l", "left":
		dir = "left"
	case "r", "right":
		dir = "right"
	case "u", "up":
		dir = "up"
	case "d", "down":
		dir = "down"
	default:
		return "", fmt.Sprintf("movefocus: %q is not a valid direction (expected l, r, u, or d)", raw)
	}
	return fmt.Sprintf("hl.dsp.focus({ direction = %s })", quoteLuaString(dir)), ""
}

// joinArgs concatenates comma-split args back into a single string with
// the original comma joiners (hyprlang allows multi-comma args in some
// dispatchers like 'exec'). Whitespace around each arg is preserved as
// emitted by the splitter.
func joinArgs(args []string) string {
	return strings.Join(args, ", ")
}

// pixelDispatchExpr emits a Lua call for the resize/move-active/-window-pixel
// dispatcher family. The hyprlang form is 'X Y', 'X% Y%' or 'exact X Y', with
// an optional trailing ', WINDOW' selector. Hyprland 0.55's typed Lua API
// requires a table { x, y, relative?, window? } where x/y are NUMERIC pixels
// only — strings (including "10%") are rejected by tableOptNum and fail at
// config-load with "'x' and 'y' are required".
//
// Pure-numeric inputs ('20 0', 'exact 1024 768') emit a single typed call:
//
//	hl.dsp.window.resize({ x = 20, y = 0, relative = true })
//
// Percent inputs ('10% 5%') have no direct API equivalent. With polyfill=false
// they're rejected via reason. With polyfill=true they're emitted as a closure
// that resolves the percent against the appropriate runtime reference at
// dispatch time and hl.dispatch()es the resulting typed call. References per
// legacy hyprlang (src/Compositor.cpp parseWindowVectorArgsRelative):
//
//	resizeactive / moveactive       → active window  (w.size for resize, w.at for move)
//	resizewindowpixel / movewindowpixel + selector → selected window via hl.get_window(sel)
//	'exact' prefix                  → active monitor's size, absolute
//
// 'relative' controls the default polarity (delta for *active/*window,
// absolute for *pixel); the 'exact' keyword overrides to absolute regardless.
// `declared` is the active declared-$var set so an optional `, $win` trailing
// selector resolves through the same rules as elsewhere in the codegen.
func pixelDispatchExpr(call, raw string, relative, polyfill bool, declared map[string]bool) (string, string) {
	raw = strings.TrimSpace(raw)
	exact := false
	if strings.HasPrefix(strings.ToLower(raw), "exact") && (len(raw) == 5 || raw[5] == ' ' || raw[5] == '\t') {
		raw = strings.TrimSpace(raw[5:])
		relative = false
		exact = true
	}
	var window string
	if i := strings.Index(raw, ","); i >= 0 {
		window = strings.TrimSpace(raw[i+1:])
		raw = strings.TrimSpace(raw[:i])
	}
	parts := strings.Fields(raw)
	if len(parts) != 2 {
		return "", fmt.Sprintf("expected 'X Y' or 'X%% Y%%', got %q", raw)
	}

	hasPercent := strings.HasSuffix(parts[0], "%") || strings.HasSuffix(parts[1], "%")
	if hasPercent {
		if !polyfill {
			return "", fmt.Sprintf("percent coords %q have no direct Lua API equivalent (Hyprland 0.55's x/y are numeric-only); enable polyfill to emit a runtime helper closure", raw)
		}
		return percentPolyfillExpr(call, parts[0], parts[1], relative, exact, window, declared)
	}

	x, okX := pixelInt(parts[0])
	y, okY := pixelInt(parts[1])
	if !okX || !okY {
		return "", fmt.Sprintf("could not parse pixel coords %q", raw)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s({ x = %s, y = %s", call, x, y)
	if relative {
		b.WriteString(", relative = true")
	}
	if window != "" {
		fmt.Fprintf(&b, ", window = %s", formatValue(window, declared))
	}
	b.WriteString(" })")
	return b.String(), ""
}

// percentPolyfillExpr emits a closure that resolves a percent X/Y pair at
// dispatch time, then hl.dispatch()es the typed resize/move call. Returned
// as a single-line Lua expression so it slots into hl.bind(key, EXPR, opts)
// exactly like the typed form. 'exact' inputs reference the active monitor's
// size; window-selector inputs reference that window's runtime size/position;
// otherwise the active window is used. A nil-guard skips the dispatch when
// the reference object isn't available, matching the legacy dispatcher's
// silent no-op when there is no active window.
func percentPolyfillExpr(call, xTok, yTok string, relative, exact bool, window string, declared map[string]bool) (string, string) {
	isMove := strings.Contains(call, ".move")
	var setup, refX, refY string
	switch {
	case exact:
		setup = "local m = hl.get_active_monitor(); if not m then return end"
		refX, refY = "m.width", "m.height"
	case window != "":
		setup = fmt.Sprintf("local w = hl.get_window(%s); if not w then return end", formatValue(window, declared))
		if isMove {
			refX, refY = "w.at.x", "w.at.y"
		} else {
			refX, refY = "w.size.x", "w.size.y"
		}
	default:
		setup = "local w = hl.get_active_window(); if not w then return end"
		if isMove {
			refX, refY = "w.at.x", "w.at.y"
		} else {
			refX, refY = "w.size.x", "w.size.y"
		}
	}

	xExpr, okX := polyfillCoord(xTok, refX)
	yExpr, okY := polyfillCoord(yTok, refY)
	if !okX || !okY {
		return "", fmt.Sprintf("could not parse polyfill coords %q %q", xTok, yTok)
	}

	var tbl strings.Builder
	fmt.Fprintf(&tbl, "{ x = %s, y = %s", xExpr, yExpr)
	if relative {
		tbl.WriteString(", relative = true")
	}
	if window != "" {
		fmt.Fprintf(&tbl, ", window = %s", formatValue(window, declared))
	}
	tbl.WriteString(" }")

	return fmt.Sprintf("function() %s; hl.dispatch(%s(%s)) end", setup, call, tbl.String()), ""
}

// polyfillCoord turns one X or Y token into a Lua expression suitable for the
// polyfill closure body. Plain integers pass through as literals; 'N%' becomes
// `math.floor(ref * N / 100)` so the runtime resize/move sees a clean integer.
func polyfillCoord(tok, ref string) (string, bool) {
	if strings.HasSuffix(tok, "%") {
		n := strings.TrimSuffix(tok, "%")
		if _, err := strconv.Atoi(n); err != nil {
			return "", false
		}
		return fmt.Sprintf("math.floor(%s * %s / 100)", ref, n), true
	}
	if _, err := strconv.Atoi(tok); err != nil {
		return "", false
	}
	return tok, true
}

// pixelInt parses a plain integer pixel token (no percent — the caller has
// already routed percent inputs to the polyfill path).
func pixelInt(tok string) (string, bool) {
	if _, err := strconv.Atoi(tok); err != nil {
		return "", false
	}
	return tok, true
}

// joinFormatted formats each arg via formatValue() and joins with ', '.
// Use when the dispatcher signature expects positional Lua values.
func joinFormatted(args []string, declared map[string]bool) string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = formatValue(a, declared)
	}
	return strings.Join(out, ", ")
}
