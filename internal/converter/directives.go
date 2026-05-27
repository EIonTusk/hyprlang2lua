package converter

import (
	"fmt"
	"strings"
)

// matchKV is one (key, value) pair from a windowrule's match clause — e.g.
// {"class", ".*"} or {"title", "^foo$"}. Promoted to package scope so the
// generator's pending window_rule buffer can hold a list of them.
type matchKV struct{ k, v string }

// blockField is a normalized "key[:sub] = value" entry seen inside a
// rule's curly-brace block body. The parser produces two distinct node
// kinds for these lines — KeyValue for unrecognized names and Directive
// for names that also exist at top level (`monitor`, `animation`, `bind`,
// …) — so block emitters can't just match on KeyValue or they'll silently
// drop fields whose name happens to be in the directive set.
type blockField struct {
	key   string
	sub   string
	value string
	line  int
}

// asBlockField normalizes one body node into a (key, sub, value, line)
// tuple. Returns ok=false for nodes that have no key=value shape (blanks,
// comments handled separately, nested sections, etc.).
func asBlockField(n node) (blockField, bool) {
	switch c := n.(type) {
	case KeyValue:
		return blockField{c.Key, c.Sub, c.Value, c.line}, true
	case Directive:
		return blockField{c.Name, "", c.Value, c.line}, true
	}
	return blockField{}, false
}

func (g *generator) emitDirective(d Directive) {
	// Anything that isn't itself a coalescable rule directive closes the
	// pending-rule stream. emit() already flushes for nodes that come
	// through it, but emitDirective is also reachable directly from
	// emitConfigSection (stray directives inside a section body), so the
	// guard is repeated here as a backstop.
	switch d.Name {
	case "windowrule", "windowrulev2", "layerrule", "workspace":
		// extend the pending stream, don't break it
	default:
		g.flushPendingRule()
	}
	switch {
	case strings.HasPrefix(d.Name, "bind"):
		g.emitBind(d)
	case d.Name == "exec", d.Name == "exec-once", d.Name == "execr-once":
		g.emitExec(d)
	case d.Name == "exec-shutdown":
		// Closest event is hyprland.shutdown. Route through formatExecCall
		// so '$var' references resolve to locals and '[RULES] cmd' prefixes
		// get lifted into the hl.exec_cmd rules table, just like exec/exec-once.
		g.writeln("hl.on(\"hyprland.shutdown\", function()")
		g.writeln("    " + formatExecCall(d.Value))
		g.writeln("end)")
		g.writeln("")
		g.translated(1)
	case d.Name == "monitor":
		g.emitMonitor(d)
	case d.Name == "workspace":
		g.emitWorkspace(d)
	case d.Name == "windowrule":
		g.emitWindowRule(d, false)
	case d.Name == "windowrulev2":
		g.emitWindowRule(d, true)
	case d.Name == "layerrule":
		g.emitLayerRule(d)
	case d.Name == "env":
		g.emitEnv(d, false)
	case d.Name == "envd":
		g.emitEnv(d, true)
	case d.Name == "source":
		g.emitSource(d)
	case d.Name == "submap":
		g.emitSubmap(d)
	case d.Name == "animation":
		g.emitAnimation(d)
	case d.Name == "bezier":
		g.emitBezier(d)
	case d.Name == "gesture":
		g.emitGesture(d)
	case d.Name == "permission":
		g.emitPermission(d)
	case d.Name == "unbind":
		g.flag(d.line, "unbind requires capturing the original bind handle; rewrite using returned hl.Keybind:remove()")
		g.writef("-- TODO: manual review — 'unbind = %s'. In Lua, capture the result of hl.bind(...) and call :remove(). See hl.meta.lua.", d.Value)
	default:
		g.flag(d.line, "unhandled directive: "+d.Name)
		g.writef("-- TODO: manual review — unhandled directive '%s = %s'", d.Name, d.Value)
	}
}

// splitCommas splits on top-level commas, honoring '\,' as an escape.
// Trims spaces around each field.
func splitCommas(s string) []string {
	var out []string
	var cur strings.Builder
	i := 0
	for i < len(s) {
		c := s[i]
		if c == '\\' && i+1 < len(s) && s[i+1] == ',' {
			cur.WriteByte(',')
			i += 2
			continue
		}
		if c == ',' {
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
			i++
			continue
		}
		cur.WriteByte(c)
		i++
	}
	out = append(out, strings.TrimSpace(cur.String()))
	return out
}

// emitBind translates bind[mlertnopcd] = MOD, KEY, DISPATCHER, ARGS...
//
// Flag-derived options:
//   bindm — mouse-button bind. The Lua API has no 'mouse' bind option (see
//           HL.BindOptions in hl.meta.lua); the key string itself encodes
//           the mouse button (e.g. "SUPER + mouse:272") and the dispatcher
//           (movewindow → hl.dsp.window.drag) carries the semantic. No
//           option field is emitted.
//   binde — { repeating = true }
//   bindl — { locked = true }
//   bindr — { release = true }
//   bindn — { non_consuming = true }
//   bindo — only-when-no-modifier (no direct opt; flag as TODO)
//   bindt — { transparent = true }
//   bindi — { ignore_mods = true }
//   bindp — { long_press = true }   (per stub HL.BindOptions field name)
//   bindc — { click = true }
//   bindd — { drag = true }
//
// Multi-flag forms like 'bindle' combine; we walk the suffix one char at a time.
func (g *generator) emitBind(d Directive) {
	suffix := strings.TrimPrefix(d.Name, "bind")
	opts := map[string]string{}
	unknownFlags := []byte{}
	for i := 0; i < len(suffix); i++ {
		switch suffix[i] {
		case 'm':
			// No-op: 'mouse' is not a HL.BindOptions field; the key string
			// and dispatcher already convey mouse-button semantics.
		case 'e':
			opts["repeating"] = "true"
		case 'l':
			opts["locked"] = "true"
		case 'r':
			opts["release"] = "true"
		case 'n':
			opts["non_consuming"] = "true"
		case 't':
			opts["transparent"] = "true"
		case 'i':
			opts["ignore_mods"] = "true"
		case 'p':
			opts["long_press"] = "true"
		case 'c':
			opts["click"] = "true"
		case 'd':
			opts["drag"] = "true"
		default:
			unknownFlags = append(unknownFlags, suffix[i])
		}
	}
	if len(unknownFlags) > 0 {
		g.writef("-- TODO: manual review — unknown bind flag(s) %q on line %d", string(unknownFlags), d.line)
		g.flag(d.line, "unknown bind flags: "+string(unknownFlags))
	}

	parts := splitCommas(d.Value)
	if len(parts) < 3 {
		g.writef("-- TODO: manual review — malformed bind on line %d: %s", d.line, d.Value)
		g.flag(d.line, "malformed bind: "+d.Value)
		return
	}
	mod := parts[0]
	key := parts[1]
	dispatcher := parts[2]
	args := []string{}
	if len(parts) > 3 {
		args = parts[3:]
	}

	keyExpr := combineModKey(mod, key)
	dispatchExpr, reason := buildDispatcher(dispatcher, args, g.polyfill)
	if reason != "" {
		// Emit a best-effort guess as a comment so the user can see exactly
		// what was attempted, plus the specific reason it couldn't be safely
		// translated — distinguishing "unknown dispatcher name" from "known
		// name but bad arguments" matters for the user when iterating live.
		g.writef("-- TODO: manual review on line %d — %s", d.line, reason)
		g.flag(d.line, reason)
		g.writef("-- hl.bind(%s, hl.dsp.%s(%s)%s)", keyExpr, sanitizeDispatcher(dispatcher), joinQuoted(args), formatBindOpts(opts))
		return
	}

	g.writef("hl.bind(%s, %s%s)", keyExpr, dispatchExpr, formatBindOpts(opts))
	g.translated(1)
}

func formatBindOpts(opts map[string]string) string {
	if len(opts) == 0 {
		return ""
	}
	// Stable order so output is deterministic.
	order := []string{"locked", "release", "repeating", "non_consuming", "transparent", "ignore_mods", "long_press", "click", "drag"}
	parts := []string{}
	for _, k := range order {
		if v, ok := opts[k]; ok {
			parts = append(parts, k+" = "+v)
		}
	}
	return ", { " + strings.Join(parts, ", ") + " }"
}

// knownModifiers lists every modifier name Hyprland's bind parser accepts,
// per src/managers/KeybindManager.cpp. Hyprland's hyprlang front-end matched
// these case-insensitively; the new Lua hl.bind() requires uppercase, so we
// normalize literal modifier tokens at conversion time.
var knownModifiers = map[string]bool{
	"SHIFT": true,
	"CAPS": true, "CAPSLOCK": true,
	"CTRL": true, "CONTROL": true,
	"ALT": true, "MOD1": true,
	"MOD2": true, "MOD3": true,
	"SUPER": true, "WIN": true, "LOGO": true, "MOD4": true,
	"MOD5": true,
}

// normalizeModToken uppercases a single mod-position token if it matches a
// known Hyprland modifier name (case-insensitively). Unknown tokens pass
// through unchanged — they're either typos or user-defined.
func normalizeModToken(s string) string {
	up := strings.ToUpper(s)
	if knownModifiers[up] {
		return up
	}
	return s
}

// combineModKey turns hyprlang's '(mod, key)' bind pair into a single Lua
// expression matching the shipped /usr/share/hypr/hyprland.lua idiom:
//
//   ('SUPER',          'Q')        -> "\"SUPER + Q\""
//   ('$mainMod',       'Q')        -> "mainMod .. \" + Q\""
//   ('$mainMod SHIFT', '1')        -> "mainMod .. \" + SHIFT + 1\""
//   ('SUPER SHIFT',    '1')        -> "\"SUPER + SHIFT + 1\""
//   ('shift',          'PRINT')    -> "\"SHIFT + PRINT\""   (case-normalized)
//   ('',               'XF86Mute') -> "\"XF86Mute\""
//
// Hyprland's bind key parser treats spaces between modifier names as the
// same as '+'. We normalize to '+' for consistency with the example, and
// uppercase any known modifier name since hl.bind is case-sensitive.
func combineModKey(mod, key string) string {
	mod = strings.TrimSpace(mod)
	key = strings.TrimSpace(key)
	// A multi-digit integer in the key slot is a raw evdev keycode (e.g.
	// multimedia keys not in Hyprland's key-name table). Hyprland's bind
	// parser accepts these only via the 'code:N' prefix; without it, lookup
	// fails silently. Single digits ("1".."9", "0") are valid keysym names
	// for the number-row keys and are left alone.
	if len(key) > 1 && isAllDigits(key) {
		key = "code:" + key
	}
	if mod == "" {
		return formatValue(key)
	}
	modParts := strings.Fields(mod)
	if len(modParts) == 0 {
		return formatValue(key)
	}
	// Bare $var as the entire mod (common case: $mainMod).
	if len(modParts) == 1 && isDollarRef(modParts[0]) {
		return fmt.Sprintf("%s .. %s", luaIdent(modParts[0]), quoteLuaString(" + "+key))
	}
	// Normalize literal mod tokens (uppercase known modifier names).
	for i, p := range modParts {
		if !isDollarRef(p) {
			modParts[i] = normalizeModToken(p)
		}
	}
	hasRef := false
	for _, p := range modParts {
		if isDollarRef(p) {
			hasRef = true
			break
		}
	}
	if !hasRef {
		return quoteLuaString(strings.Join(modParts, " + ") + " + " + key)
	}
	// Mix of $var and literals — emit a concat chain.
	var pieces []string
	var litRun []string
	flushLit := func() {
		if len(litRun) > 0 {
			pieces = append(pieces, quoteLuaString(strings.Join(litRun, " + ")))
			litRun = nil
		}
	}
	for _, p := range modParts {
		if isDollarRef(p) {
			flushLit()
			pieces = append(pieces, luaIdent(p))
			continue
		}
		litRun = append(litRun, p)
	}
	// Final tail with the key glued on.
	tail := " + " + key
	if len(litRun) > 0 {
		pieces = append(pieces, quoteLuaString(" + "+strings.Join(litRun, " + ")+tail))
	} else {
		pieces = append(pieces, quoteLuaString(tail))
	}
	return strings.Join(pieces, " .. ")
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// sanitizeDispatcher turns a hyprlang dispatcher name like 'togglefloating'
// into something at least syntactically valid in a Lua expression — used only
// in commented-out fallback output.
func sanitizeDispatcher(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '_', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "no_op"
	}
	return b.String()
}

func joinQuoted(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = formatValue(a)
	}
	return strings.Join(parts, ", ")
}

func (g *generator) emitExec(d Directive) {
	cmd := strings.TrimSpace(d.Value)
	if cmd == "" {
		return
	}
	switch d.Name {
	case "exec":
		g.exec = append(g.exec, cmd)
	case "execr-once":
		g.execrOnce = append(g.execrOnce, cmd)
		g.flag(d.line, "execr-once: raw-spawn semantic not preserved (hl.exec_cmd has no analog)")
	default: // exec-once
		g.execOnce = append(g.execOnce, cmd)
	}
	g.translated(1)
}

// emitMonitor: 'monitor = NAME,MODE,POSITION,SCALE[,extra...]'
// or 'monitor = NAME,disable' or 'monitor = NAME,addreserved,...'
func (g *generator) emitMonitor(d Directive) {
	parts := splitCommas(d.Value)
	if len(parts) == 0 || (len(parts) == 1 && parts[0] == "") {
		g.flag(d.line, "empty monitor directive")
		return
	}
	name := parts[0]

	if len(parts) >= 2 && strings.EqualFold(parts[1], "disable") {
		g.writeln("hl.monitor({")
		g.writef("    output = %s,", quoteLuaString(name))
		g.writeln("    disabled = true,")
		g.writeln("})")
		g.writeln("")
		g.translated(1)
		return
	}

	// addreserved / transform / bitdepth / cm etc. are out-of-line keywords
	// — handle the common case (4-field positional) and emit a TODO for
	// extension forms.
	fields := []string{}
	fields = append(fields, fmt.Sprintf("    output = %s,", quoteLuaString(name)))
	if len(parts) > 1 && parts[1] != "" {
		fields = append(fields, fmt.Sprintf("    mode = %s,", quoteLuaString(parts[1])))
	}
	if len(parts) > 2 && parts[2] != "" {
		fields = append(fields, fmt.Sprintf("    position = %s,", quoteLuaString(parts[2])))
	}
	if len(parts) > 3 && parts[3] != "" {
		fields = append(fields, fmt.Sprintf("    scale = %s,", quoteLuaString(parts[3])))
	}
	extras := []string(nil)
	if len(parts) > 4 {
		extras = parts[4:]
	}

	g.writeln("hl.monitor({")
	for _, f := range fields {
		g.writeln(f)
	}
	if len(extras) > 0 {
		// Try to recognize a few keyword extensions: 'transform,N', 'bitdepth,N',
		// 'vrr,N', 'mirror,name', 'cm,...'.
		i := 0
		for i < len(extras) {
			kw := strings.ToLower(extras[i])
			switch kw {
			case "transform", "bitdepth", "vrr":
				if i+1 < len(extras) {
					g.writef("    %s = %s,", kw, formatValue(extras[i+1]))
					i += 2
					continue
				}
			case "mirror":
				if i+1 < len(extras) {
					g.writef("    mirror = %s,", quoteLuaString(extras[i+1]))
					i += 2
					continue
				}
			}
			g.writef("    -- TODO: manual review — extra field %q", extras[i])
			g.flag(d.line, "monitor extra field: "+extras[i])
			i++
		}
	}
	g.writeln("})")
	g.writeln("")
	g.translated(1)
}

// emitMonitorV2Block handles the curly-brace form for monitor config
// added in hyprland 0.54 as the `monitorv2` special category. Fields
// (output, mode, position, scale, transform, vrr, mirror, bitdepth, cm,
// sdr_*, supports_*, *_luminance, …) map onto hl.monitor({...}) — the
// Lua API has only one entry point regardless of which form the user
// wrote in hyprlang. Numeric/boolean fields go through formatValue so a
// hyprlang `disabled = 1` ends up as the appropriate Lua literal.
func (g *generator) emitMonitorV2Block(s Section) {
	g.flushPendingRule()
	var fieldLines []string
	add := func(format string, args ...any) {
		fieldLines = append(fieldLines, fmt.Sprintf(format, args...))
	}

	for _, child := range s.Body {
		if cmt, ok := child.(Comment); ok {
			if !g.stripComments {
				fieldLines = append(fieldLines, "    "+formatLuaComment(stripCommentIndent(cmt.Text)))
			}
			continue
		}
		f, ok := asBlockField(child)
		if !ok {
			continue
		}
		if f.sub != "" {
			add("    -- TODO: manual review — unrecognized monitorv2 block field %q", f.key+":"+f.sub)
			g.flag(f.line, "monitorv2 block field: "+f.key+":"+f.sub)
			continue
		}
		// Always emit monitor fields as strings, matching the legacy
		// `monitor =` directive emitter. Hyprlang values like `0x0`
		// (monitor position) and `1920x1080@144` look like hex/numerics
		// to formatValue's heuristic — quoting unconditionally keeps the
		// source's exact spelling intact and matches /usr/share/hypr's
		// shipped example. A few keys are known boolean-ish (disabled,
		// vrr, supports_*) and pass through the boolean/number coercer
		// instead so `disabled = 1` becomes the right Lua type.
		switch strings.ToLower(f.key) {
		case "disabled", "vrr", "supports_wide_color", "supports_hdr":
			add("    %s = %s,", luaTableKey(f.key), formatValue(f.value))
		default:
			add("    %s = %s,", luaTableKey(f.key), quoteLuaString(f.value))
		}
	}

	g.writeln("hl.monitor({")
	for _, l := range fieldLines {
		g.writeln(l)
	}
	g.writeln("})")
	g.writeln("")
	g.translated(1)
}

// workspaceFieldMap rewrites hyprlang's compact field spellings to the
// HL.WorkspaceRuleSpec field names listed in /usr/share/hypr/stubs/hl.meta.lua.
// Anything not in this map passes through unchanged (and may surface as a
// TODO if it doesn't exist on the spec).
var workspaceFieldMap = map[string]string{
	"gapsin":          "gaps_in",
	"gapsout":         "gaps_out",
	"floatgaps":       "float_gaps",
	"bordersize":      "border_size",
	"border":          "no_border", // negated! handled below
	"noborder":        "no_border",
	"norounding":      "no_rounding",
	"noshadow":        "no_shadow",
	"oncreatedempty":  "on_created_empty",
	"defaultname":     "default_name",
}

// emitWorkspace: 'workspace = NAME,key:val,key:val,...'
//
// Adjacent workspace directives for the same workspace name coalesce
// into a single hl.workspace_rule({...}) call. This matches the common
// hyprlang pattern where a user splits per-workspace properties across
// several lines for readability.
func (g *generator) emitWorkspace(d Directive) {
	parts := splitCommas(d.Value)
	if len(parts) == 0 || parts[0] == "" {
		g.flushPendingRule()
		g.flag(d.line, "empty workspace directive")
		return
	}
	name := parts[0]
	preamble := []string{
		fmt.Sprintf("    workspace = %s,", quoteLuaString(name)),
	}

	var actions []string
	add := func(format string, args ...any) {
		actions = append(actions, fmt.Sprintf(format, args...))
	}

	for _, kv := range parts[1:] {
		k, v, ok := splitColon(kv)
		if !ok {
			add("    -- TODO: manual review — workspace field %q", kv)
			g.flag(d.line, "workspace field: "+kv)
			continue
		}
		mapped, hadMap := workspaceFieldMap[strings.ToLower(k)]
		if !hadMap {
			mapped = k
		}
		// Negation: hyprlang's 'border:false' means no_border = true.
		if strings.ToLower(k) == "border" && (v == "false" || v == "0" || v == "no" || v == "off") {
			add("    no_border = true,")
			continue
		}
		add("    %s = %s,", luaTableKey(mapped), formatValue(v))
	}

	g.coalesceRule("workspace_rule", "workspace="+name, preamble, actions)
	g.translated(1)
}

// emitWindowRuleBlock handles the block form introduced alongside the
// directive form in hyprland 0.54:
//
//	windowrule {
//	    name = move-kitty
//	    match:class = kitty
//	    move = 100 100
//	    animation = popin
//	}
//
// Each child KeyValue is classified:
//   - `name = X` carries the rule's identifier (used by unbind/handle).
//   - `match:KEY = VALUE` contributes one entry to the match table.
//   - any other bare `key = value` is an action/property field.
//
// Block rules always emit standalone — their `name` makes each one a
// distinct entity, so coalescing adjacent blocks would silently fuse
// different rules. Pending coalesce streams are flushed first to keep
// source order.
func (g *generator) emitWindowRuleBlock(s Section) {
	g.flushPendingRule()
	var name string
	var matches []matchKV
	var actionLines []string

	add := func(format string, args ...any) {
		actionLines = append(actionLines, fmt.Sprintf(format, args...))
	}

	for _, child := range s.Body {
		if cmt, ok := child.(Comment); ok {
			if !g.stripComments {
				actionLines = append(actionLines, "    "+formatLuaComment(stripCommentIndent(cmt.Text)))
			}
			continue
		}
		f, ok := asBlockField(child)
		if !ok {
			continue
		}
		switch {
		case f.key == "match" && f.sub != "":
			matches = append(matches, matchKV{f.sub, f.value})
		case f.sub == "" && f.key == "name":
			name = f.value
		case f.sub == "":
			// Re-route through the directive-form action formatter so
			// 'noborder', 'move 100 100', 'opacity 0.9 …' etc. all map
			// to the same Lua field names regardless of how the user
			// wrote them. The block form gives us key/value separately,
			// so reconstruct "key value" before feeding the formatter.
			value := strings.TrimSpace(f.value)
			if value == "" {
				emitWindowAction(g, f.key, f.line, &actionLines)
			} else {
				emitWindowAction(g, f.key+" "+value, f.line, &actionLines)
			}
		default:
			add("    -- TODO: manual review — unrecognized window_rule block field %q", f.key+":"+f.sub)
			g.flag(f.line, "window_rule block field: "+f.key+":"+f.sub)
		}
	}

	g.writeln("hl.window_rule({")
	if name != "" {
		g.writef("    name = %s,", quoteLuaString(name))
	}
	if len(matches) > 0 {
		g.writeln("    match = {")
		for _, m := range matches {
			g.writef("        %s = %s,", luaTableKey(m.k), formatValue(m.v))
		}
		g.writeln("    },")
	}
	for _, a := range actionLines {
		g.writeln(a)
	}
	g.writeln("})")
	g.writeln("")
	g.translated(1)
}

// emitWorkspaceBlock handles the curly-brace form for workspace rules.
// The directive-form's leading positional (the workspace selector) is
// expressed via the block's `name = ...` field; everything else is a
// property and runs through the same field-name normalization as the
// directive form.
func (g *generator) emitWorkspaceBlock(s Section) {
	g.flushPendingRule()
	var name string
	var fieldLines []string

	add := func(format string, args ...any) {
		fieldLines = append(fieldLines, fmt.Sprintf(format, args...))
	}

	for _, child := range s.Body {
		if cmt, ok := child.(Comment); ok {
			if !g.stripComments {
				fieldLines = append(fieldLines, "    "+formatLuaComment(stripCommentIndent(cmt.Text)))
			}
			continue
		}
		f, ok := asBlockField(child)
		if !ok {
			continue
		}
		if f.sub != "" {
			add("    -- TODO: manual review — unrecognized workspace_rule block field %q", f.key+":"+f.sub)
			g.flag(f.line, "workspace_rule block field: "+f.key+":"+f.sub)
			continue
		}
		if f.key == "name" {
			name = f.value
			continue
		}
		mapped, hadMap := workspaceFieldMap[strings.ToLower(f.key)]
		if !hadMap {
			mapped = f.key
		}
		if strings.ToLower(f.key) == "border" {
			v := strings.ToLower(strings.TrimSpace(f.value))
			if v == "false" || v == "0" || v == "no" || v == "off" {
				add("    no_border = true,")
				continue
			}
		}
		add("    %s = %s,", luaTableKey(mapped), formatValue(f.value))
	}

	g.writeln("hl.workspace_rule({")
	if name != "" {
		g.writef("    workspace = %s,", quoteLuaString(name))
	}
	for _, l := range fieldLines {
		g.writeln(l)
	}
	g.writeln("})")
	g.writeln("")
	g.translated(1)
}

// emitLayerRuleBlock handles the curly-brace form for layer rules.
// `name = ...` is the rule identifier; `match:namespace = ...` becomes
// the inline match table; remaining bare key=value entries are
// properties (blur, no_anim, ignore_alpha, etc.) and route through the
// directive-form action map for field-name consistency.
func (g *generator) emitLayerRuleBlock(s Section) {
	g.flushPendingRule()
	var name string
	var matches []matchKV
	var fieldLines []string

	add := func(format string, args ...any) {
		fieldLines = append(fieldLines, fmt.Sprintf(format, args...))
	}

	for _, child := range s.Body {
		if cmt, ok := child.(Comment); ok {
			if !g.stripComments {
				fieldLines = append(fieldLines, "    "+formatLuaComment(stripCommentIndent(cmt.Text)))
			}
			continue
		}
		f, ok := asBlockField(child)
		if !ok {
			continue
		}
		switch {
		case f.key == "match" && f.sub != "":
			matches = append(matches, matchKV{f.sub, f.value})
		case f.sub == "" && f.key == "name":
			name = f.value
		case f.sub == "":
			// Same one-rule-per-line forms the directive emitter handles:
			// `blur`, `ignorealpha 0.5`, `animation slide`, etc. The block
			// writes them as `blur = true` or `ignorealpha = 0.5`; rebuild
			// the "rule arg…" string before delegating.
			ruleForm := strings.TrimSpace(f.key)
			v := strings.TrimSpace(f.value)
			switch strings.ToLower(v) {
			case "", "true", "yes", "on", "1":
				// Bare flag (e.g. `blur = true`) collapses to the rule name.
			case "false", "no", "off", "0":
				// Negated bare flag — emit a TODO since the directive form
				// doesn't have a negate; user must remove the rule.
				add("    -- TODO: manual review — disable %q has no layer_rule directive analog", ruleForm)
				g.flag(f.line, "layer_rule block negation: "+ruleForm)
				continue
			default:
				ruleForm = ruleForm + " " + v
			}
			emitLayerRuleField(g, ruleForm, f.line, &fieldLines)
		default:
			add("    -- TODO: manual review — unrecognized layer_rule block field %q", f.key+":"+f.sub)
			g.flag(f.line, "layer_rule block field: "+f.key+":"+f.sub)
		}
	}

	g.writeln("hl.layer_rule({")
	if name != "" {
		g.writef("    name = %s,", quoteLuaString(name))
	}
	if len(matches) > 0 {
		g.writeln("    match = {")
		for _, m := range matches {
			g.writef("        %s = %s,", luaTableKey(m.k), formatValue(m.v))
		}
		g.writeln("    },")
	}
	for _, l := range fieldLines {
		g.writeln(l)
	}
	g.writeln("})")
	g.writeln("")
	g.translated(1)
}

// emitLayerRuleField mirrors the rule-mapping switch in emitLayerRule but
// appends to a string slice rather than writing directly. Kept separate
// from the directive emitter so future changes to one form don't
// inadvertently change the other.
func emitLayerRuleField(g *generator, rule string, line int, out *[]string) {
	add := func(format string, args ...any) {
		*out = append(*out, fmt.Sprintf(format, args...))
	}
	r := strings.TrimSpace(rule)
	switch {
	case r == "noanim":
		add("    no_anim = true,")
	case r == "blur":
		add("    blur = true,")
	case r == "blurpopups":
		add("    blur_popups = true,")
	case r == "dimaround":
		add("    dim_around = true,")
	case r == "noscreenshare":
		add("    no_screen_share = true,")
	case r == "xray":
		add("    xray = true,")
	case strings.HasPrefix(r, "ignorealpha "):
		add("    ignore_alpha = %s,", formatValue(strings.TrimSpace(r[12:])))
	case strings.HasPrefix(r, "animation "):
		add("    animation = %s,", quoteLuaString(strings.TrimSpace(r[10:])))
	case strings.HasPrefix(r, "order "):
		add("    order = %s,", formatValue(strings.TrimSpace(r[6:])))
	default:
		add("    -- TODO: manual review — unmapped layer rule: %q", r)
		g.flag(line, "layer rule: "+r)
	}
}

// emitWindowRule handles 'windowrule' (legacy) and 'windowrulev2'.
//
// Hyprland 0.41+ unified the two: any field of the form 'match:KEY VALUE' or
// 'KEY:VALUE' is a matcher, every other field is a property/action. Fields
// can appear in any order. We classify each field, accumulate matches in the
// 'match' subtable, and emit the rest as window-rule properties.
//
// For legacy v1 (no 'match:' anywhere, no 'KEY:' fields), the trailing field
// is treated as a class regex per the pre-0.41 grammar.
func (g *generator) emitWindowRule(d Directive, v2 bool) {
	parts := splitCommas(d.Value)
	if len(parts) < 2 {
		// A malformed rule shouldn't be folded into the previous one — the
		// TODO line is its own emission, so close out any pending coalesce
		// stream first.
		g.flushPendingRule()
		g.flag(d.line, "malformed window rule: "+d.Value)
		g.writef("-- TODO: manual review — malformed windowrule on line %d: %s", d.line, d.Value)
		return
	}

	var matches []matchKV
	var actions []string

	for i, f := range parts {
		k, v, isMatch := classifyWindowRuleField(f, v2, i == 0)
		if isMatch {
			matches = append(matches, matchKV{k, v})
			continue
		}
		actions = append(actions, f)
	}

	// v1 fallback: if we found no matches at all and there are exactly two
	// fields, treat the second as the legacy trailing class regex.
	if len(matches) == 0 && !v2 && len(parts) == 2 {
		matches = append(matches, matchKV{"class", parts[1]})
		actions = []string{parts[0]}
	}

	var actionLines []string
	for _, a := range actions {
		emitWindowAction(g, a, d.line, &actionLines)
	}

	// Build the preamble: a nested `match = {...},` block at one indent
	// level. groupKey serializes the match list so equality is a string
	// compare; order-preserving on purpose so a user who wrote matchers
	// in a different order on a second rule won't see surprise merges.
	preamble := buildMatchPreamble(matches)
	groupKey := groupKeyForMatches(matches)

	g.coalesceRule("window_rule", groupKey, preamble, actionLines)
	g.translated(1)
}

// buildMatchPreamble renders a `match = {...},` block (or omits it
// entirely when matches is empty) at the same indent the legacy emitter
// used. Returned strings carry no trailing newline.
func buildMatchPreamble(matches []matchKV) []string {
	if len(matches) == 0 {
		return nil
	}
	out := []string{"    match = {"}
	for _, m := range matches {
		out = append(out, fmt.Sprintf("        %s = %s,", luaTableKey(m.k), formatValue(m.v)))
	}
	out = append(out, "    },")
	return out
}

// groupKeyForMatches serializes a match list into a canonical string for
// equality comparison. Pipes and equals signs are fine as separators
// because match keys are Lua identifiers and values come from the source
// verbatim — they can't introduce ambiguity even if a value contained '|'.
func groupKeyForMatches(matches []matchKV) string {
	if len(matches) == 0 {
		return ""
	}
	var b strings.Builder
	for i, m := range matches {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(m.k)
		b.WriteByte('=')
		b.WriteString(m.v)
	}
	return b.String()
}

// classifyWindowRuleField decides whether a single comma-separated field is a
// matcher (returns key, value, true) or a property/action (returns _,_, false).
//
// Recognized matcher forms:
//   match:KEY VALUE     — modern explicit matcher, e.g. 'match:class .*'
//   KEY:VALUE           — v2-style compact matcher, e.g. 'class:^kitty$'
//                         (only when v2 is true OR no leading word like 'float')
//
// firstField hints whether this is the leading field; we don't want 'float'
// alone to be classified as a matcher just because it has no colon.
func classifyWindowRuleField(field string, v2, firstField bool) (string, string, bool) {
	field = strings.TrimSpace(field)
	if strings.HasPrefix(field, "match:") {
		rest := strings.TrimPrefix(field, "match:")
		sp := strings.IndexByte(rest, ' ')
		if sp < 0 {
			// 'match:foo' with no value is bogus; fall through.
			return "", "", false
		}
		return strings.TrimSpace(rest[:sp]), strings.TrimSpace(rest[sp+1:]), true
	}
	if !v2 {
		return "", "", false
	}
	// v2: 'KEY:VALUE' where KEY is a known match key. We require this list
	// rather than treating every colon-separated field as a match, because
	// actions like 'workspace 2' or 'monitor DP-1' use spaces, not colons,
	// in the v2 grammar.
	knownMatchKeys := map[string]bool{
		"class": true, "title": true, "initialclass": true, "initialtitle": true,
		"tag": true, "xwayland": true, "floating": true, "fullscreen": true,
		"pinned": true, "focus": true, "workspace": true, "onworkspace": true,
		"fullscreenstate": true, "pid": true,
		// per HL.WindowQueryFilter in hl.meta.lua
		"monitor": true, "mapped": true,
	}
	if i := strings.IndexByte(field, ':'); i > 0 {
		k := strings.ToLower(strings.TrimSpace(field[:i]))
		v := strings.TrimSpace(field[i+1:])
		if knownMatchKeys[k] && !firstField {
			return k, v, true
		}
		// The leading field is allowed to be a matcher only for backward
		// compat with configs that write 'class:foo, float' — but that's
		// non-idiomatic; we err on the side of treating leading colon-fields
		// as actions, which is the common case.
	}
	return "", "", false
}

// truthyAction handles bare-flag actions that may carry an explicit '1'/'0'
// or 'on'/'off' / 'true'/'false' suffix, e.g. 'float', 'float 1', 'float 0'.
func truthyAction(a, head string) (bool, bool) {
	if a == head {
		return true, true
	}
	if !strings.HasPrefix(a, head+" ") {
		return false, false
	}
	arg := strings.TrimSpace(a[len(head):])
	switch strings.ToLower(arg) {
	case "1", "true", "on", "yes":
		return true, true
	case "0", "false", "off", "no":
		return false, true
	}
	return false, false
}

// windowRuleFlag describes one boolean window-rule effect: the
// hyprlang spellings the converter recognizes and the Lua field name
// it emits. invert=true flips the value before emission — only used
// for 'tile', whose semantics are the inverse of 'float'.
//
// Hyprland's parser does exact string matching against EFFECT_STRINGS
// (src/desktop/rule/windowRule/WindowRuleEffectContainer.cpp) and
// accepts only the underscored canonical spelling, which is listed
// first in aliases. The no-underscore folk variants ('nofocus',
// 'noshadow', …) are kept as legacy aliases for older configs and the
// wiki that still document them.
type windowRuleFlag struct {
	aliases []string
	field   string
	invert  bool
}

// windowRuleFlags lists every boolean effect (entry in EFFECT_STRINGS
// whose value resolves through truthy(raw) in WindowRule.cpp). Order
// is irrelevant — none of the spellings prefix-overlap with each
// other under truthyAction's "head" or "head SPACE arg" match.
var windowRuleFlags = []windowRuleFlag{
	{[]string{"float"}, "float", false},
	{[]string{"tile"}, "float", true},
	{[]string{"pin"}, "pin", false},
	{[]string{"fullscreen"}, "fullscreen", false},
	{[]string{"maximize"}, "maximize", false},
	{[]string{"center"}, "center", false},
	{[]string{"immediate"}, "immediate", false},
	{[]string{"pseudo"}, "pseudo", false},
	{[]string{"persistent_size"}, "persistent_size", false},
	{[]string{"allows_input"}, "allows_input", false},
	{[]string{"dim_around"}, "dim_around", false},
	{[]string{"decorate"}, "decorate", false},
	{[]string{"focus_on_activate"}, "focus_on_activate", false},
	{[]string{"keep_aspect_ratio"}, "keep_aspect_ratio", false},
	{[]string{"nearest_neighbor"}, "nearest_neighbor", false},
	{[]string{"opaque"}, "opaque", false},
	{[]string{"force_rgbx"}, "force_rgbx", false},
	{[]string{"xray"}, "xray", false},
	{[]string{"render_unfocused"}, "render_unfocused", false},
	{[]string{"confine_pointer"}, "confine_pointer", false},
	{[]string{"no_focus", "nofocus"}, "no_focus", false},
	{[]string{"no_initial_focus", "noinitialfocus"}, "no_initial_focus", false},
	{[]string{"no_anim", "noanim"}, "no_anim", false},
	{[]string{"no_blur", "noblur"}, "no_blur", false},
	{[]string{"no_shadow", "noshadow"}, "no_shadow", false},
	{[]string{"no_screen_share", "noscreenshare"}, "no_screen_share", false},
	{[]string{"no_dim"}, "no_dim", false},
	{[]string{"no_follow_mouse"}, "no_follow_mouse", false},
	{[]string{"no_max_size"}, "no_max_size", false},
	{[]string{"no_shortcuts_inhibit"}, "no_shortcuts_inhibit", false},
	{[]string{"no_vrr"}, "no_vrr", false},
	{[]string{"no_auto_hdr"}, "no_auto_hdr", false},
	{[]string{"stay_focused", "stayfocused"}, "stay_focused", false},
	{[]string{"sync_fullscreen", "syncfullscreen"}, "sync_fullscreen", false},
}

// windowRuleArg describes one argument-taking window-rule effect. When
// numeric=true the raw arg is fed through formatValue (so '2' stays
// numeric); otherwise it's wrapped as a Lua string via quoteLuaString.
// Vec2 and struct args ('move 100 200', 'fullscreen_state 0 0') are
// quoted as-is and parsed on the Lua side.
type windowRuleArg struct {
	aliases []string
	field   string
	numeric bool
}

// windowRuleArgs lists every argument-taking effect. Aliases include
// the no-underscore folk spellings for the keywords that historically
// had one ('bordersize', 'bordercolor', 'suppressevent').
var windowRuleArgs = []windowRuleArg{
	{[]string{"move"}, "move", false},
	{[]string{"size"}, "size", false},
	{[]string{"min_size"}, "min_size", false},
	{[]string{"max_size"}, "max_size", false},
	{[]string{"workspace"}, "workspace", false},
	{[]string{"opacity"}, "opacity", true},
	{[]string{"rounding"}, "rounding", true},
	{[]string{"rounding_power"}, "rounding_power", true},
	{[]string{"border_size", "bordersize"}, "border_size", true},
	{[]string{"border_color", "bordercolor"}, "border_color", false},
	{[]string{"monitor"}, "monitor", false},
	{[]string{"tag"}, "tag", false},
	{[]string{"animation"}, "animation", false},
	{[]string{"suppress_event", "suppressevent"}, "suppress_event", false},
	{[]string{"group"}, "group", false},
	{[]string{"content"}, "content", false},
	{[]string{"fullscreen_state"}, "fullscreen_state", false},
	{[]string{"no_close_for"}, "no_close_for", true},
	{[]string{"scrolling_width"}, "scrolling_width", true},
	{[]string{"scroll_mouse"}, "scroll_mouse", true},
	{[]string{"scroll_touchpad"}, "scroll_touchpad", true},
	{[]string{"idle_inhibit"}, "idle_inhibit", false},
}

// emitWindowAction formats one rule action and appends it (no trailing
// newline) to *out so the pending-window-rule buffer can hold it for
// later coalescing. The flushed line is written back through g.writeln,
// which adds the newline.
func emitWindowAction(g *generator, action string, line int, out *[]string) {
	a := strings.TrimSpace(action)
	add := func(format string, args ...any) {
		*out = append(*out, fmt.Sprintf(format, args...))
	}

	// Boolean flags: 'no_focus', 'float 1', 'pin 0', etc.
	for _, f := range windowRuleFlags {
		for _, alias := range f.aliases {
			val, ok := truthyAction(a, alias)
			if !ok {
				continue
			}
			if f.invert {
				val = !val
			}
			add("    %s = %t,", f.field, val)
			return
		}
	}

	// Argument-taking effects: 'move 100 100', 'opacity 0.9', etc.
	for _, ar := range windowRuleArgs {
		for _, alias := range ar.aliases {
			prefix := alias + " "
			if !strings.HasPrefix(a, prefix) {
				continue
			}
			raw := strings.TrimSpace(a[len(prefix):])
			if ar.numeric {
				add("    %s = %s,", ar.field, formatValue(raw))
			} else {
				add("    %s = %s,", ar.field, quoteLuaString(raw))
			}
			return
		}
	}

	add("    -- TODO: manual review — unmapped window rule action: %q", a)
	g.flag(line, "window rule action: "+a)
}

// emitLayerRule: 'layerrule = RULE[:ARG], NAMESPACE_REGEX'
//
// Adjacent layerrules that target the same namespace coalesce into a
// single hl.layer_rule({...}) call — the body fields union onto one rule,
// matching the way users write a hyprlang block of related rules per
// namespace.
func (g *generator) emitLayerRule(d Directive) {
	parts := splitCommas(d.Value)
	if len(parts) < 2 {
		g.flushPendingRule()
		g.flag(d.line, "malformed layer rule: "+d.Value)
		g.writef("-- TODO: manual review — malformed layerrule on line %d: %s", d.line, d.Value)
		return
	}
	rule := parts[0]
	ns := parts[1]

	preamble := []string{
		fmt.Sprintf("    match = { namespace = %s },", formatValue(ns)),
	}

	var actions []string
	add := func(format string, args ...any) {
		actions = append(actions, fmt.Sprintf(format, args...))
	}

	r := strings.TrimSpace(rule)
	switch {
	case r == "noanim":
		add("    no_anim = true,")
	case r == "blur":
		add("    blur = true,")
	case r == "blurpopups":
		add("    blur_popups = true,")
	case r == "dimaround":
		add("    dim_around = true,")
	case r == "noscreenshare":
		add("    no_screen_share = true,")
	case r == "xray":
		add("    xray = true,")
	case strings.HasPrefix(r, "ignorealpha "):
		add("    ignore_alpha = %s,", formatValue(strings.TrimSpace(r[12:])))
	case strings.HasPrefix(r, "animation "):
		add("    animation = %s,", quoteLuaString(strings.TrimSpace(r[10:])))
	case strings.HasPrefix(r, "order "):
		add("    order = %s,", formatValue(strings.TrimSpace(r[6:])))
	default:
		add("    -- TODO: manual review — unmapped layer rule: %q", r)
		g.flag(d.line, "layer rule: "+r)
	}

	g.coalesceRule("layer_rule", "namespace="+ns, preamble, actions)
	g.translated(1)
}

// emitEnv: 'env = KEY,VALUE'. envd similarly but propagates to systemd/dbus.
func (g *generator) emitEnv(d Directive, dbus bool) {
	parts := splitCommas(d.Value)
	if len(parts) != 2 {
		g.flag(d.line, "malformed env: "+d.Value)
		g.writef("-- TODO: manual review — malformed env on line %d: %s", d.line, d.Value)
		return
	}
	if dbus {
		g.writef("-- Note: 'envd' (systemd/D-Bus-propagated) becomes a regular hl.env in Lua. If you relied on D-Bus propagation, set the variable via systemctl --user import-environment as a separate step.")
	}
	g.writef("hl.env(%s, %s)", quoteLuaString(parts[0]), quoteLuaString(parts[1]))
	g.translated(1)
}

// emitSource: 'source = path.conf'.
//
// Decision: we emit a 'require("...")' call with the path stripped of its
// extension and reduced to a single module name; we also include a comment
// explaining that the sourced .conf file must itself be converted to Lua.
//
// Reasoning: Lua's natural include mechanism is require(); it integrates
// with package.path and gives users the same modularity they had in hyprlang.
// We do *not* inline-expand because it bloats output and loses the user's
// modular organization. We do not use dofile() because it doesn't go through
// package.path and would force the user to deal with relative paths.
func (g *generator) emitSource(d Directive) {
	p := strings.TrimSpace(d.Value)
	if p == "" {
		g.flag(d.line, "empty source")
		return
	}
	mod := moduleNameFor(p)
	g.writef("-- Source: %s — convert this file to Lua and ensure it is on Lua's package.path.", p)
	g.writef("require(%s)", quoteLuaString(mod))
	g.translated(1)
}

// moduleNameFor turns a hyprlang 'source =' path into a Lua module name
// suitable for require(). Lua's package.path resolves dotted names against
// the same directory tree as filesystem paths ('themes.dark' → 'themes/dark.lua'),
// so we map slashes to dots rather than throwing them away.
//
// Examples:
//   colors.conf                                -> "colors"
//   themes/dark.conf                           -> "themes.dark"
//   ~/.config/hypr/mocha.conf                  -> "mocha"
//   ~/.config/hypr/conf/monitor.conf           -> "conf.monitor"
//   ~/.config/hypr/conf/animations/default.conf -> "conf.animations.default"
//   ~/.cache/wal/colors-hyprland.conf          -> "colors-hyprland"  (no hypr/ segment)
//
// Hyprland's Lua runtime starts with cwd set to the user's Hyprland config
// root (typically ~/.config/hypr/), so anything under that root can be
// addressed as a relative path via package.path's './?.lua' pattern.
//
// For absolute paths we look for the last '/hypr/' segment and treat what
// follows as a path relative to the Hyprland config root. Stripping to just
// the basename — the previous behavior — collapses subdirectories, which
// breaks modular configs that organize sources under conf/, themes/, etc.
// and makes per-subdir 'default.conf' files all collide on require("default").
//
// For absolute paths outside any 'hypr' directory (e.g. ~/.cache/wal/foo.conf)
// the directory tree above is meaningless to package.path, so we fall back
// to basename — the caller will need package.path tweaks regardless.
//
// Relative paths (including the './' prefix) are preserved with '/' → '.'
// since they already address into package.path's ./?.lua lookup naturally.
func moduleNameFor(path string) string {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "\\", "/")
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, "~") {
		// Reduce absolute paths to a path relative to the Hyprland config
		// root when possible; otherwise fall back to basename.
		if i := strings.LastIndex(path, "/hypr/"); i >= 0 {
			path = path[i+len("/hypr/"):]
		} else if i := strings.LastIndex(path, "/"); i >= 0 {
			path = path[i+1:]
		}
	} else {
		path = strings.TrimPrefix(path, "./")
	}
	path = strings.TrimSuffix(path, ".conf")
	path = strings.TrimSuffix(path, ".lua")
	// Split on '/' so each component can be sanitized independently; then
	// re-join with '.' to form the Lua-style module name.
	parts := strings.Split(path, "/")
	clean := parts[:0]
	for _, p := range parts {
		if p == "" {
			continue
		}
		clean = append(clean, sanitizeModuleComponent(p))
	}
	if len(clean) == 0 {
		return "sourced_file"
	}
	return strings.Join(clean, ".")
}

// sanitizeModuleComponent maps one path component to a Lua-safe identifier:
// alphanumerics, '_' and '-' survive; anything else becomes '_'; a leading
// digit is prefixed with '_'.
func sanitizeModuleComponent(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i == 0 && r >= '0' && r <= '9' {
			b.WriteByte('_')
		}
		switch {
		case r == '_', r == '-', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

// emitSubmap: 'submap = NAME' opens a submap; 'submap = reset' closes it.
//
// In hyprlang, binds between 'submap = name' and 'submap = reset' belong to
// that submap. We don't have side-by-side ordering with bind directives in
// this simple top-level approach, so we emit a TODO that points users at
// hl.define_submap. The Lua API supports a more elegant form anyway:
//
//   hl.define_submap("resize", function()
//       hl.bind(...)
//   end)
//
// We can't easily fold raw inline submap state at this point without doing a
// preprocessing pass — defer that to a future improvement and flag instead,
// so users see exactly what the right rewrite is.
func (g *generator) emitSubmap(d Directive) {
	name := strings.TrimSpace(d.Value)
	if strings.EqualFold(name, "reset") {
		g.writeln("-- (end of submap block)")
		g.passthrough()
		return
	}
	g.flag(d.line, "submap requires manual conversion")
	g.writef("-- TODO: manual review — wrap the following binds in hl.define_submap(%s, function() ... end). The next 'submap = reset' closes the block.", quoteLuaString(name))
}

// emitAnimation: 'animation = NAME, ON, SPEED, BEZIER[, STYLE]'
func (g *generator) emitAnimation(d Directive) {
	parts := splitCommas(d.Value)
	if len(parts) < 2 {
		g.flag(d.line, "malformed animation: "+d.Value)
		return
	}
	g.writeln("hl.animation({")
	g.writef("    leaf = %s,", quoteLuaString(parts[0]))
	if len(parts) > 1 {
		enabled := "true"
		if parts[1] == "0" || strings.EqualFold(parts[1], "false") {
			enabled = "false"
		}
		g.writef("    enabled = %s,", enabled)
	}
	if len(parts) > 2 && parts[2] != "" {
		g.writef("    speed = %s,", formatValue(parts[2]))
	}
	if len(parts) > 3 && parts[3] != "" {
		g.writef("    bezier = %s,", quoteLuaString(parts[3]))
	}
	if len(parts) > 4 && parts[4] != "" {
		g.writef("    style = %s,", quoteLuaString(parts[4]))
	}
	g.writeln("})")
	g.translated(1)
}

// emitBezier: 'bezier = NAME, x0, y0, x1, y1'
func (g *generator) emitBezier(d Directive) {
	parts := splitCommas(d.Value)
	if len(parts) != 5 {
		g.flag(d.line, "malformed bezier: "+d.Value)
		return
	}
	g.writef("hl.curve(%s, { type = \"bezier\", points = { { %s, %s }, { %s, %s } } })",
		quoteLuaString(parts[0]), formatValue(parts[1]), formatValue(parts[2]),
		formatValue(parts[3]), formatValue(parts[4]))
	g.translated(1)
}

func (g *generator) emitGesture(d Directive) {
	// 'gesture = FINGERS, DIRECTION, ACTION[, ...]'
	parts := splitCommas(d.Value)
	if len(parts) < 3 {
		g.flag(d.line, "malformed gesture: "+d.Value)
		return
	}
	g.writeln("hl.gesture({")
	g.writef("    fingers = %s,", formatValue(parts[0]))
	g.writef("    direction = %s,", quoteLuaString(parts[1]))
	g.writef("    action = %s,", quoteLuaString(parts[2]))
	for _, extra := range parts[3:] {
		g.writef("    -- TODO: manual review — extra gesture field %q", extra)
	}
	g.writeln("})")
	g.translated(1)
}

func (g *generator) emitPermission(d Directive) {
	// 'permission = REGEX, TYPE, ALLOW|DENY'
	parts := splitCommas(d.Value)
	if len(parts) != 3 {
		g.flag(d.line, "malformed permission: "+d.Value)
		return
	}
	g.writef("hl.permission({ binary = %s, type = %s, allow = %s })",
		quoteLuaString(parts[0]), quoteLuaString(parts[1]), quoteLuaString(parts[2]))
	g.translated(1)
}

// splitColon splits "key:value" once. Returns (key, value, ok).
func splitColon(s string) (string, string, bool) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
}
