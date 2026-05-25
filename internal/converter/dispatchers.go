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
func buildDispatcher(name string, args []string) (string, string) {
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
		expr, reason := pixelDispatchExpr("hl.dsp.window.resize", joinArgs(args), true)
		if reason != "" {
			return "", fmt.Sprintf("%s: %s", name, reason)
		}
		return expr, ""
	case "resizewindowpixel":
		expr, reason := pixelDispatchExpr("hl.dsp.window.resize", joinArgs(args), false)
		if reason != "" {
			return "", fmt.Sprintf("resizewindowpixel: %s", reason)
		}
		return expr, ""
	case "moveactive":
		expr, reason := pixelDispatchExpr("hl.dsp.window.move", joinArgs(args), true)
		if reason != "" {
			return "", fmt.Sprintf("moveactive: %s", reason)
		}
		return expr, ""
	case "movewindowpixel":
		expr, reason := pixelDispatchExpr("hl.dsp.window.move", joinArgs(args), false)
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

// pixelDispatchExpr emits a Lua table call for the
// resize/move-active/-window-pixel dispatcher family. The hyprlang form is
// either 'X Y', 'X% Y%' or 'exact X Y' and Hyprland's Lua API requires
// a table { x, y, relative?, window? } — passing a string fails at load
// time with 'expected no args, or a table'.
//
// 'relative' controls the default polarity (delta for *active/*window,
// absolute for *pixel). The 'exact' keyword in 'resizeactive exact X Y'
// overrides to absolute regardless. Comma-separated tail args ('X Y, WIN')
// are treated as a window selector and emitted as window = "WIN".
//
// Pixel/percent forms: '20' emits as a numeric literal; '20%' emits as the
// quoted string "20%" because the runtime accepts both numeric pixels and
// percent-string forms on the same x/y field. Anything we can't parse
// returns ("", reason) so the caller can flag the line for manual review.
func pixelDispatchExpr(call, raw string, relative bool) (string, string) {
	raw = strings.TrimSpace(raw)
	// 'exact X Y' inverts polarity: 'exact' means absolute pixels.
	if strings.HasPrefix(strings.ToLower(raw), "exact") && (len(raw) == 5 || raw[5] == ' ' || raw[5] == '\t') {
		raw = strings.TrimSpace(raw[5:])
		relative = false
	}
	// Optional trailing ', WINDOW' selector — applies to *windowpixel forms.
	var window string
	if i := strings.Index(raw, ","); i >= 0 {
		window = strings.TrimSpace(raw[i+1:])
		raw = strings.TrimSpace(raw[:i])
	}
	parts := strings.Fields(raw)
	if len(parts) != 2 {
		return "", fmt.Sprintf("expected 'X Y' or 'X%% Y%%', got %q", raw)
	}
	x, okX := pixelArg(parts[0])
	y, okY := pixelArg(parts[1])
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

// pixelArg parses one resize/move coordinate token. Plain integers (with
// optional sign) emit as numeric literals; the 'N%' percent form emits as
// a quoted string. Anything else fails so the caller flags the line.
func pixelArg(tok string) (string, bool) {
	if strings.HasSuffix(tok, "%") {
		n := strings.TrimSuffix(tok, "%")
		if _, err := strconv.Atoi(n); err != nil {
			return "", false
		}
		return quoteLuaString(tok), true
	}
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
