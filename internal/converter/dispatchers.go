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
func buildDispatcher(name string, args []string, polyfill bool) (string, string) {
	switch name {
	// ---- Process / system ----
	case "exec":
		// Run the joined args through formatValue so '$terminal' becomes a
		// local variable reference rather than a literal '$' string.
		return fmt.Sprintf("hl.dsp.exec_cmd(%s)", formatValue(joinArgs(args))), ""
	case "execr":
		return fmt.Sprintf("hl.dsp.exec_raw(%s)", formatValue(joinArgs(args))), ""
	case "exit":
		return "hl.dsp.exit()", ""
	case "forcerendererreload":
		return "hl.dsp.force_renderer_reload()", ""
	case "forceidle":
		return "hl.dsp.force_idle()", ""
	case "dpms":
		return fmt.Sprintf("hl.dsp.dpms(%s)", joinFormatted(args)), ""
	case "event":
		return fmt.Sprintf("hl.dsp.event(%s)", joinFormatted(args)), ""
	case "global":
		return fmt.Sprintf("hl.dsp.global(%s)", quoteLuaString(joinArgs(args))), ""
	case "pass":
		return fmt.Sprintf("hl.dsp.pass(%s)", joinFormatted(args)), ""
	case "sendshortcut":
		return fmt.Sprintf("hl.dsp.send_shortcut(%s)", quoteLuaString(joinArgs(args))), ""
	case "sendkeystate":
		return fmt.Sprintf("hl.dsp.send_key_state(%s)", quoteLuaString(joinArgs(args))), ""

	// ---- Submap ----
	case "submap":
		return fmt.Sprintf("hl.dsp.submap(%s)", quoteLuaString(joinArgs(args))), ""

	// ---- Focus / cursor ----
	case "movefocus":
		return focusDirectionExpr(args)
	case "focuswindow":
		return fmt.Sprintf("hl.dsp.focus({ window = %s })", formatValue(joinArgs(args))), ""
	case "focusmonitor":
		return fmt.Sprintf("hl.dsp.focus({ monitor = %s })", formatValue(joinArgs(args))), ""
	case "focusurgentorlast":
		return "hl.dsp.focus({ urgent_or_last = true })", ""
	case "focuscurrentorlast":
		return "hl.dsp.focus({ current_or_last = true })", ""
	case "movecursor":
		return fmt.Sprintf("hl.dsp.cursor.move(%s)", joinFormatted(args)), ""
	case "movecursortocorner":
		return fmt.Sprintf("hl.dsp.cursor.move_to_corner(%s)", joinFormatted(args)), ""

	// ---- Workspaces ----
	case "workspace":
		return fmt.Sprintf("hl.dsp.focus({ workspace = %s })", formatValue(joinArgs(args))), ""
	case "movetoworkspace":
		return fmt.Sprintf("hl.dsp.window.move({ workspace = %s })", formatValue(joinArgs(args))), ""
	case "movetoworkspacesilent":
		return fmt.Sprintf("hl.dsp.window.move({ workspace = %s, follow = false })", formatValue(joinArgs(args))), ""
	case "togglespecialworkspace":
		return fmt.Sprintf("hl.dsp.workspace.toggle_special(%s)", quoteLuaString(joinArgs(args))), ""
	case "renameworkspace":
		return fmt.Sprintf("hl.dsp.workspace.rename(%s)", quoteLuaString(joinArgs(args))), ""
	case "swapactiveworkspaces":
		return fmt.Sprintf("hl.dsp.workspace.swap_monitors(%s)", quoteLuaString(joinArgs(args))), ""
	case "moveworkspacetomonitor":
		return fmt.Sprintf("hl.dsp.workspace.move(%s)", quoteLuaString(joinArgs(args))), ""

	// ---- Windows ----
	case "killactive", "closewindow":
		return "hl.dsp.window.close()", ""
	case "centerwindow":
		return "hl.dsp.window.center()", ""
	case "fullscreen":
		if len(args) == 0 {
			return `hl.dsp.window.fullscreen({ mode = "fullscreen" })`, ""
		}
		switch strings.TrimSpace(joinArgs(args)) {
		case "0":
			return `hl.dsp.window.fullscreen({ mode = "fullscreen" })`, ""
		case "1":
			return `hl.dsp.window.fullscreen({ mode = "maximized" })`, ""
		default:
			return fmt.Sprintf(`hl.dsp.window.fullscreen({ mode = %s })`, formatValue(joinArgs(args))), ""
		}
	case "fullscreenstate":
		return fmt.Sprintf("hl.dsp.window.fullscreen_state(%s)", joinFormatted(args)), ""
	case "togglefloating":
		return "hl.dsp.window.float({ action = \"toggle\" })", ""
	case "setfloating":
		return "hl.dsp.window.float({ action = \"set\" })", ""
	case "settiled":
		return "hl.dsp.window.float({ action = \"unset\" })", ""
	case "pseudo":
		return "hl.dsp.window.pseudo()", ""
	case "movewindow":
		if len(args) == 0 {
			return "hl.dsp.window.drag()", ""
		}
		return fmt.Sprintf("hl.dsp.window.move({ direction = %s })", formatValue(joinArgs(args))), ""
	case "resizeactive", "resizewindow":
		if len(args) == 0 {
			return "hl.dsp.window.resize()", ""
		}
		expr, reason := pixelDispatchExpr("hl.dsp.window.resize", joinArgs(args), true, polyfill)
		if reason != "" {
			return "", fmt.Sprintf("%s: %s", name, reason)
		}
		return expr, ""
	case "resizewindowpixel":
		expr, reason := pixelDispatchExpr("hl.dsp.window.resize", joinArgs(args), false, polyfill)
		if reason != "" {
			return "", fmt.Sprintf("resizewindowpixel: %s", reason)
		}
		return expr, ""
	case "moveactive":
		expr, reason := pixelDispatchExpr("hl.dsp.window.move", joinArgs(args), true, polyfill)
		if reason != "" {
			return "", fmt.Sprintf("moveactive: %s", reason)
		}
		return expr, ""
	case "movewindowpixel":
		expr, reason := pixelDispatchExpr("hl.dsp.window.move", joinArgs(args), false, polyfill)
		if reason != "" {
			return "", fmt.Sprintf("movewindowpixel: %s", reason)
		}
		return expr, ""
	case "pin":
		return "hl.dsp.window.pin()", ""
	case "tagwindow":
		return fmt.Sprintf("hl.dsp.window.tag(%s)", quoteLuaString(joinArgs(args))), ""
	case "cleartagswindow":
		return "hl.dsp.window.clear_tags()", ""
	case "alterzorder":
		return fmt.Sprintf("hl.dsp.window.alter_zorder(%s)", quoteLuaString(joinArgs(args))), ""
	case "bringactivetotop":
		return "hl.dsp.window.bring_to_top()", ""
	case "togglegroup":
		return "hl.dsp.group.toggle()", ""
	case "lockactivegroup":
		return fmt.Sprintf("hl.dsp.group.lock_active(%s)", formatValue(joinArgs(args))), ""
	case "lockgroups":
		return fmt.Sprintf("hl.dsp.group.lock(%s)", formatValue(joinArgs(args))), ""
	case "moveintogroup":
		return fmt.Sprintf("hl.dsp.group.move_window(%s)", quoteLuaString(joinArgs(args))), ""
	case "moveoutofgroup":
		return "hl.dsp.group.move_window(\"out\")", ""
	case "denywindowfromgroup":
		return fmt.Sprintf("hl.dsp.window.deny_from_group(%s)", formatValue(joinArgs(args))), ""
	case "changegroupactive":
		return fmt.Sprintf("hl.dsp.group.active(%s)", quoteLuaString(joinArgs(args))), ""
	case "swapnext", "cyclenext":
		return fmt.Sprintf("hl.dsp.window.cycle_next(%s)", quoteLuaString(joinArgs(args))), ""
	case "swapwindow":
		return fmt.Sprintf("hl.dsp.window.swap(%s)", quoteLuaString(joinArgs(args))), ""
	case "togglesplit":
		return "hl.dsp.layout(\"togglesplit\")", ""
	case "layoutmsg":
		return fmt.Sprintf("hl.dsp.layout(%s)", quoteLuaString(joinArgs(args))), ""
	case "togglepin":
		return "hl.dsp.window.pin()", ""
	case "togglefullscreen":
		return `hl.dsp.window.fullscreen({ mode = "fullscreen" })`, ""
	case "splitratio":
		return fmt.Sprintf("hl.dsp.layout(%s)", quoteLuaString("splitratio "+joinArgs(args))), ""
	}

	// Unknown dispatcher name — not present in our switch.
	return "", fmt.Sprintf("no mapping for dispatcher %q", name)
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
func pixelDispatchExpr(call, raw string, relative, polyfill bool) (string, string) {
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
		return percentPolyfillExpr(call, parts[0], parts[1], relative, exact, window)
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
		fmt.Fprintf(&b, ", window = %s", formatValue(window))
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
func percentPolyfillExpr(call, xTok, yTok string, relative, exact bool, window string) (string, string) {
	isMove := strings.Contains(call, ".move")
	var setup, refX, refY string
	switch {
	case exact:
		setup = "local m = hl.get_active_monitor(); if not m then return end"
		refX, refY = "m.width", "m.height"
	case window != "":
		setup = fmt.Sprintf("local w = hl.get_window(%s); if not w then return end", formatValue(window))
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
		fmt.Fprintf(&tbl, ", window = %s", formatValue(window))
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
func joinFormatted(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = formatValue(a)
	}
	return strings.Join(out, ", ")
}
