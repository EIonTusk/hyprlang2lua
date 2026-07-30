package converter

import (
	"strings"
	"testing"
)

const smokeInput = `# my hyprland config
$mainMod = SUPER

monitor = ,preferred,auto,1

general {
    gaps_in = 5
    gaps_out = 20
    border_size = 2
    col.active_border = rgba(33ccffee)
    layout = dwindle
}

decoration {
    rounding = 10
    blur {
        enabled = true
        size = 3
    }
}

input {
    kb_layout = us
    follow_mouse = 1
    touchpad {
        natural_scroll = false
    }
}

exec-once = waybar
exec-once = hyprpaper

env = XCURSOR_SIZE,24

bind = $mainMod, Q, exec, kitty
bind = $mainMod, C, killactive
bind = $mainMod, M, exit
bind = $mainMod, V, togglefloating
bind = $mainMod, left, movefocus, l
bind = $mainMod, 1, workspace, 1
bind = $mainMod SHIFT, 1, movetoworkspace, 1
bindm = $mainMod, mouse:272, movewindow
bindl = , XF86AudioMute, exec, wpctl set-mute @DEFAULT_AUDIO_SINK@ toggle
binde = , XF86AudioRaiseVolume, exec, wpctl set-volume @DEFAULT_AUDIO_SINK@ 5%+

windowrulev2 = float, class:^(pavucontrol)$
windowrulev2 = workspace 2 silent, class:^(firefox)$
workspace = 1, persistent:true, default:true
layerrule = noanim, waybar
layerrule = blur, gtk-layer-shell
`

func TestSmoke(t *testing.T) {
	out, rpt, err := Convert(smokeInput)
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	// Loose checks — the goal here is to confirm the high-level shape, not
	// the exact byte-for-byte output (golden files do that).
	mustContain := []string{
		`local mainMod = "SUPER"`,
		`hl.config({`,
		`general = {`,
		`gaps_in = 5,`,
		`layout = "dwindle",`,
		`decoration = {`,
		`blur = {`,
		`enabled = true,`,
		`size = 3,`,
		`input = {`,
		`kb_layout = "us",`,
		`touchpad = {`,
		`natural_scroll = false,`,
		`hl.monitor({`,
		`hl.env("XCURSOR_SIZE", "24")`,
		`hl.bind(mainMod .. " + Q", hl.dsp.exec_cmd("kitty"))`,
		`hl.bind(mainMod .. " + C", hl.dsp.window.close())`,
		`hl.bind(mainMod .. " + V", hl.dsp.window.float({ action = "toggle" }))`,
		`hl.bind(mainMod .. " + left", hl.dsp.focus({ direction = "left" }))`,
		`hl.bind(mainMod .. " + 1", hl.dsp.focus({ workspace = 1 }))`,
		`hl.bind(mainMod .. " + SHIFT + 1", hl.dsp.window.move({ workspace = 1 }))`,
		// bindm: no HL.BindOptions field exists for 'mouse'; the key string
		// and dispatcher carry the mouse semantics, so the opts table is omitted.
		`hl.bind(mainMod .. " + mouse:272", hl.dsp.window.drag())`,
		`hl.window_rule({`,
		`class = "^(pavucontrol)$"`,
		`hl.workspace_rule({`,
		`hl.layer_rule({`,
		`hl.on("hyprland.start", function()`,
		`hl.exec_cmd("waybar")`,
	}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Errorf("output missing expected fragment:\n  want: %s\n  full output:\n%s", want, out)
		}
	}
	if rpt.Flagged != 0 {
		t.Logf("flagged=%d notes=%+v", rpt.Flagged, rpt.Notes)
	}
	if rpt.Translated < 10 {
		t.Errorf("expected >= 10 translated directives, got %d", rpt.Translated)
	}
}

// TestModifierCaseNormalization guards against a real bug found in the wild:
// hyprlang's legacy parser accepted modifier names case-insensitively, but
// Hyprland's Lua hl.bind() only accepts uppercase. Mixed-case input must be
// normalized in the generated bind call.
func TestModifierCaseNormalization(t *testing.T) {
	// $mainMod is declared so the mixed-case + variable case below still
	// resolves to a Lua local concat; undeclared $X would now be preserved
	// as literal text per the env-var fallback rule (see TestEnvVarFallback).
	src := `$mainMod = SUPER
bind = shift, PRINT, exec, hyprshot
bindel = shift SUPER, 201, exec, foo
bind = ctrl alt, t, exec, kitty
bind = $mainMod shift, X, exec, bar
`
	out, _, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, lower := range []string{`"shift +`, `+ shift +`, `"ctrl +`, `+ alt +`} {
		if strings.Contains(out, lower) {
			t.Errorf("lowercase modifier leaked into output: %q\nfull output:\n%s", lower, out)
		}
	}
	for _, want := range []string{
		`hl.bind("SHIFT + PRINT",`,
		`hl.bind("SHIFT + SUPER + code:201",`,
		`hl.bind("CTRL + ALT + t",`,
		`hl.bind(mainMod .. " + SHIFT + X",`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
// TestWindowRuleEffects exhaustively covers every keyword in Hyprland's
// EFFECT_STRINGS table:
//
//   - The underscored spellings (no_focus, stay_focused, …) — the only
//     forms current Hyprland actually accepts.
//   - Each no-underscore folk variant (nofocus, noshadow, …) — kept as a
//     legacy alias for older configs and stale wiki examples.
//   - Every argument-taking effect (move, opacity, idle_inhibit, …)
//     including the new ones (max_size, group, content, idle_inhibit, …).
//   - 'tile' / 'tile 1' / 'tile 0', whose value is the inverse of float.
//
// The intent is twofold: (1) nothing in this set surfaces as an
// unmapped-action TODO, and (2) numeric args stay numeric while string
// args get quoted.
func TestWindowRuleEffects(t *testing.T) {
	src := `windowrulev2 = float, class:^(a)$
windowrulev2 = tile, class:^(b)$
windowrulev2 = tile 0, class:^(b2)$
windowrulev2 = pin, class:^(c)$
windowrulev2 = fullscreen, class:^(d)$
windowrulev2 = maximize, class:^(e)$
windowrulev2 = center, class:^(f)$
windowrulev2 = immediate, class:^(g)$
windowrulev2 = pseudo, class:^(h)$
windowrulev2 = persistent_size, class:^(i)$
windowrulev2 = allows_input, class:^(j)$
windowrulev2 = dim_around, class:^(k)$
windowrulev2 = decorate, class:^(l)$
windowrulev2 = focus_on_activate, class:^(m)$
windowrulev2 = keep_aspect_ratio, class:^(n)$
windowrulev2 = nearest_neighbor, class:^(o)$
windowrulev2 = opaque, class:^(p)$
windowrulev2 = force_rgbx, class:^(q)$
windowrulev2 = xray, class:^(r)$
windowrulev2 = render_unfocused, class:^(s)$
windowrulev2 = confine_pointer, class:^(t)$
windowrulev2 = no_focus, class:^(u)$
windowrulev2 = no_initial_focus, class:^(v)$
windowrulev2 = no_anim, class:^(w)$
windowrulev2 = no_blur, class:^(x)$
windowrulev2 = no_shadow, class:^(y)$
windowrulev2 = no_screen_share, class:^(z)$
windowrulev2 = no_dim, class:^(aa)$
windowrulev2 = no_follow_mouse, class:^(ab)$
windowrulev2 = no_max_size, class:^(ac)$
windowrulev2 = no_shortcuts_inhibit, class:^(ad)$
windowrulev2 = no_vrr, class:^(ae)$
windowrulev2 = no_auto_hdr, class:^(af)$
windowrulev2 = stay_focused, class:^(ag)$
windowrulev2 = sync_fullscreen, class:^(ah)$

# Legacy folk spellings.
windowrulev2 = nofocus, class:^(legacy1)$
windowrulev2 = noinitialfocus, class:^(legacy2)$
windowrulev2 = noanim, class:^(legacy3)$
windowrulev2 = noblur, class:^(legacy4)$
windowrulev2 = noshadow, class:^(legacy5)$
windowrulev2 = noscreenshare, class:^(legacy6)$
windowrulev2 = stayfocused, class:^(legacy7)$
windowrulev2 = syncfullscreen, class:^(legacy8)$

# Argument-taking effects.
windowrulev2 = move 100 200, class:^(a1)$
windowrulev2 = size 800 600, class:^(a2)$
windowrulev2 = min_size 400 300, class:^(a3)$
windowrulev2 = max_size 1920 1080, class:^(a4)$
windowrulev2 = workspace 3 silent, class:^(a5)$
windowrulev2 = opacity 0.95, class:^(a6)$
windowrulev2 = rounding 8, class:^(a7)$
windowrulev2 = rounding_power 2.5, class:^(a8)$
windowrulev2 = border_size 3, class:^(a9)$
windowrulev2 = border_color rgba(ff0000ff), class:^(a10)$
windowrulev2 = monitor DP-1, class:^(a11)$
windowrulev2 = tag work, class:^(a12)$
windowrulev2 = animation popin, class:^(a13)$
windowrulev2 = suppress_event fullscreen, class:^(a14)$
windowrulev2 = group new, class:^(a15)$
windowrulev2 = content video, class:^(a16)$
windowrulev2 = fullscreen_state 1 0, class:^(a17)$
windowrulev2 = no_close_for 5, class:^(a18)$
windowrulev2 = scrolling_width 12.5, class:^(a19)$
windowrulev2 = scroll_mouse 1.5, class:^(a20)$
windowrulev2 = scroll_touchpad 0.8, class:^(a21)$
windowrulev2 = idle_inhibit focus, class:^(a22)$

# Legacy folk spellings of arg-taking effects.
windowrulev2 = bordersize 3, class:^(legacy9)$
windowrulev2 = bordercolor rgba(00ff00ff), class:^(legacy10)$
windowrulev2 = suppressevent maximize, class:^(legacy11)$
`
	out, rpt, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	wantFlags := []string{
		`float = true,`,
		`float = false,`, // tile
		`pin = true,`,
		`fullscreen = true,`,
		`maximize = true,`,
		`center = true,`,
		`immediate = true,`,
		`pseudo = true,`,
		`persistent_size = true,`,
		`allows_input = true,`,
		`dim_around = true,`,
		`decorate = true,`,
		`focus_on_activate = true,`,
		`keep_aspect_ratio = true,`,
		`nearest_neighbor = true,`,
		`opaque = true,`,
		`force_rgbx = true,`,
		`xray = true,`,
		`render_unfocused = true,`,
		`confine_pointer = true,`,
		`no_focus = true,`,
		`no_initial_focus = true,`,
		`no_anim = true,`,
		`no_blur = true,`,
		`no_shadow = true,`,
		`no_screen_share = true,`,
		`no_dim = true,`,
		`no_follow_mouse = true,`,
		`no_max_size = true,`,
		`no_shortcuts_inhibit = true,`,
		`no_vrr = true,`,
		`no_auto_hdr = true,`,
		`stay_focused = true,`,
		`sync_fullscreen = true,`,
	}
	wantArgs := []string{
		`move = "100 200",`,
		`size = "800 600",`,
		`min_size = "400 300",`,
		`max_size = "1920 1080",`,
		`workspace = "3 silent",`,
		`opacity = 0.95,`,
		`rounding = 8,`,
		`rounding_power = 2.5,`,
		`border_size = 3,`,
		`border_color = "rgba(ff0000ff)",`,
		`monitor = "DP-1",`,
		`tag = "work",`,
		`animation = "popin",`,
		`suppress_event = "fullscreen",`,
		`group = "new",`,
		`content = "video",`,
		`fullscreen_state = "1 0",`,
		`no_close_for = 5,`,
		`scrolling_width = 12.5,`,
		`scroll_mouse = 1.5,`,
		`scroll_touchpad = 0.8,`,
		`idle_inhibit = "focus",`,
		`border_color = "rgba(00ff00ff)",`, // legacy bordercolor alias
	}
	for _, want := range append(wantFlags, wantArgs...) {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	if strings.Contains(out, "unmapped window rule action") {
		t.Errorf("output contains TODO for an action that should be recognized:\n%s", out)
	}
	if rpt.Flagged != 0 {
		t.Errorf("expected no flagged actions, got %d: %+v", rpt.Flagged, rpt.Notes)
	}
}

// TestBindSuffixCodes locks in the correct semantics for every letter
// suffix on `bind<letters>` directives, per Hyprland v0.54.0
// src/config/ConfigManager.cpp:2526-2550. Three letters used to be
// mapped wrong (bindd→drag, bindp→long_press, bindo→TODO) and three

// TestWindowRulePhantomKeywords confirms that 'noborder' and 'norounding'
// — which used to be silently translated to bogus Lua fields — now
// surface as unmapped-action TODOs. Neither exists in Hyprland's
// EFFECT_STRINGS; both are user errors, usually from confusing a window
// rule with a workspace rule.
func TestWindowRulePhantomKeywords(t *testing.T) {
	src := `windowrulev2 = noborder, class:^(a)$
windowrulev2 = norounding, class:^(b)$
`
	out, rpt, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{
		`unmapped window rule action: "noborder"`,
		`unmapped window rule action: "norounding"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing TODO %q in:\n%s", want, out)
		}
	}
	if rpt.Flagged != 2 {
		t.Errorf("expected 2 flagged actions, got %d: %+v", rpt.Flagged, rpt.Notes)
	}
}
// TestBindSuffixCodes locks in the correct semantics for every letter
// suffix on `bind<letters>` directives, per Hyprland v0.54.0
// src/config/ConfigManager.cpp:2526-2550. Three letters used to be
// mapped wrong (bindd→drag, bindp→long_press, bindo→TODO) and three
// were missing entirely (bindg, binds, bindu).
func TestBindSuffixCodes(t *testing.T) {
	src := `bindo = SUPER, X, exec, foo
bindp = SUPER, Y, exec, foo
bindg = SUPER, mouse:272, movewindow
bindc = SUPER, mouse:273, exec, foo
bindu = SUPER, Z, exec, foo
bindd = SUPER, A, open kitty, exec, kitty
binds = , XF86AudioMute & XF86AudioMicMute, exec, mute-all
`
	out, _, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{
		`{ long_press = true }`,
		`{ dont_inhibit = true }`,
		`{ release = true, drag = true }`,
		`{ release = true, click = true }`,
		`{ submap_universal = true }`,
		`{ description = "open kitty" }`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// 'binds' is a key-string semantic — no opts should be emitted, but
	// the bind should still translate.
	if !strings.Contains(out, `XF86AudioMute & XF86AudioMicMute`) {
		t.Errorf("multi-key bind lost the '&'-joined key string:\n%s", out)
	}
	if strings.Contains(out, "unknown bind flag") {
		t.Errorf("a bind suffix that should be recognized produced an unknown-flag TODO:\n%s", out)
	}
}

// TestWindowRuleMatchers covers the matcher key set: every modern
// underscored spelling, every legacy folk variant, and the pre-0.54
// query-filter keys (monitor/pid/mapped) that survived for backward


// TestBindSuffixCombinations covers multi-letter suffixes (e.g. 'bindel',
// 'bindrl') and the unknown-letter fallback path. The single-letter cases
// are already in TestBindSuffixCodes.
func TestBindSuffixCombinations(t *testing.T) {
	src := `bindel = , XF86AudioRaiseVolume, exec, foo
bindrl = , XF86AudioPlay, exec, bar
bindz = SUPER, X, exec, baz
`
	out, rpt, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if !strings.Contains(out, `{ locked = true, repeating = true }`) {
		t.Errorf("bindel did not produce { locked + repeating }:\n%s", out)
	}
	if !strings.Contains(out, `{ locked = true, release = true }`) {
		t.Errorf("bindrl did not produce { locked + release }:\n%s", out)
	}
	if !strings.Contains(out, `unknown bind flag(s) "z"`) {
		t.Errorf("bindz did not surface as an unknown-flag TODO:\n%s", out)
	}
	if rpt.Flagged == 0 {
		t.Errorf("expected at least one flag for the unknown suffix")
	}
}

// TestWindowRuleBlockFormMatcherNormalization confirms that the block
// form (`windowrule { match:KEY = VALUE, … }`) routes its matcher keys
// through normalizeMatchKey, the same way the directive `KEY:VALUE`
// form does. Legacy block-form configs that use 'initialclass',
// 'floating', etc. must end up with the modern Lua-side spellings.

// TestWindowRuleMatchers covers the matcher key set: every modern
// underscored spelling, every legacy folk variant, and the pre-0.54
// query-filter keys (monitor/pid/mapped) that survived for backward
// compatibility.
func TestWindowRuleMatchers(t *testing.T) {
	src := `windowrulev2 = float, initial_class:^foo$
windowrulev2 = float, initialclass:^bar$
windowrulev2 = float, initial_title:^baz$
windowrulev2 = float, initialtitle:^qux$
windowrulev2 = float, float:1
windowrulev2 = float, floating:1
windowrulev2 = float, pin:1
windowrulev2 = float, pinned:1
windowrulev2 = float, group:active
windowrulev2 = float, modal:1
windowrulev2 = float, content:video
windowrulev2 = float, xdg_tag:work
windowrulev2 = float, fullscreen_state_internal:1
windowrulev2 = float, fullscreen_state_client:1
windowrulev2 = float, fullscreenstate:1
windowrulev2 = float, onworkspace:2
windowrulev2 = float, monitor:DP-1
windowrulev2 = float, pid:1234
windowrulev2 = float, mapped:1
`
	out, _, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{
		`initial_class = "^foo$"`,
		`initial_class = "^bar$"`, // initialclass legacy → initial_class
		`initial_title = "^baz$"`,
		`initial_title = "^qux$"`, // initialtitle → initial_title
		`float = 1`,                       // formatValue: numeric stays numeric
		`pin = 1`,
		`group = "active"`,
		`modal = 1`,
		`content = "video"`,
		`xdg_tag = "work"`,
		`fullscreen_state_internal = 1`,
		`fullscreen_state_client = 1`,
		`monitor = "DP-1"`, // pre-0.54 query-filter key, preserved
		`pid = 1234`,
		`mapped = 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// 'onworkspace' is a legacy alias for 'workspace' — should normalize.
	if !strings.Contains(out, `workspace = 2`) {
		t.Errorf("'onworkspace:2' did not normalize to workspace = 2:\n%s", out)
	}
	// 'fullscreenstate' (legacy single-form) defaults to _internal.
	matches := strings.Count(out, `fullscreen_state_internal = 1`)
	if matches < 2 {
		t.Errorf("expected at least 2 fullscreen_state_internal matches (modern + legacy fullscreenstate); got %d:\n%s", matches, out)
	}
}

// TestLayerRuleEffects covers every layer-rule effect in v0.54.0's
// EFFECT_STRINGS, including the modern underscored spellings and their

// TestWindowRuleBlockFormMatcherNormalization confirms that the block
// form (`windowrule { match:KEY = VALUE, … }`) routes its matcher keys
// through normalizeMatchKey, the same way the directive `KEY:VALUE`
// form does. Legacy block-form configs that use 'initialclass',
// 'floating', etc. must end up with the modern Lua-side spellings.
func TestWindowRuleBlockFormMatcherNormalization(t *testing.T) {
	src := `windowrule {
    match:initialclass = ^picker$
    match:floating = 1
    match:pinned = 1
    match:onworkspace = 2
    no_focus = true
}
`
	out, _, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{
		`initial_class = "^picker$"`,
		`float = 1`,
		`pin = 1`,
		`workspace = 2`, // onworkspace → workspace
		`no_focus = true`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing normalized %q in block-form output:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{
		"initialclass = ",
		"floating = ",
		"pinned = ",
		"onworkspace = ",
	} {
		if strings.Contains(out, unwanted) {
			t.Errorf("block form emitted unnormalized key %q:\n%s", unwanted, out)
		}
	}
}

// TestWindowRuleUnknownMatcher confirms that a colon-form field whose
// key isn't a recognized matcher falls through to the action handler
// (where it'll surface as an unmapped-action TODO) rather than being
// silently emitted as a matcher.
func TestWindowRuleUnknownMatcher(t *testing.T) {
	src := `windowrulev2 = float, bogus:value, class:^foo$
`
	out, _, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if !strings.Contains(out, `unmapped window rule action: "bogus:value"`) {
		t.Errorf("unknown matcher 'bogus:value' did not surface as a TODO:\n%s", out)
	}
	// Real matchers in the same rule still work.
	if !strings.Contains(out, `class = "^foo$"`) {
		t.Errorf("the real matcher 'class' did not survive alongside the unknown one:\n%s", out)
	}
}

// TestMonitorV2LuminanceFields confirms that monitorv2 block fields
// for HDR/SDR luminance and brightness emit as numbers, not strings —

// TestLayerRuleEffects covers every layer-rule effect in v0.54.0's
// EFFECT_STRINGS, including the modern underscored spellings and their
// pre-0.54 folk aliases, plus the new 'above_lock' argument.
func TestLayerRuleEffects(t *testing.T) {
	src := `layerrule = no_anim, waybar
layerrule = noanim, waybar2
layerrule = blur, waybar3
layerrule = blur_popups, waybar4
layerrule = blurpopups, waybar5
layerrule = dim_around, waybar6
layerrule = dimaround, waybar7
layerrule = no_screen_share, waybar8
layerrule = noscreenshare, waybar9
layerrule = xray, waybar10
layerrule = ignore_alpha 0.5, waybar11
layerrule = ignorealpha 0.7, waybar12
layerrule = animation popin, waybar13
layerrule = order 1, waybar14
layerrule = above_lock 2, waybar15
`
	out, rpt, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{
		`no_anim = true,`,
		`blur = true,`,
		`blur_popups = true,`,
		`dim_around = true,`,
		`no_screen_share = true,`,
		`xray = true,`,
		`ignore_alpha = 0.5,`,
		`ignore_alpha = 0.7,`,
		`animation = "popin",`,
		`order = 1,`,
		`above_lock = 2,`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "unmapped layer rule") {
		t.Errorf("a layer rule that should be recognized produced a TODO:\n%s", out)
	}
	if rpt.Flagged != 0 {
		t.Errorf("expected no flagged layer rules, got %d: %+v", rpt.Flagged, rpt.Notes)
	}
}

// TestMonitorSpecialForms covers the three 2-arg special forms accepted
// TestMonitorSpecialForms covers the three 2-arg special forms accepted
// at parts[1] (disable/disabled, transform N, addreserved …) plus the
// expanded tail-param set (cm, sdrbrightness, sdrsaturation, workspace).
func TestMonitorSpecialForms(t *testing.T) {
	src := `monitor = DP-1, disable
monitor = DP-2, disabled
monitor = DP-3, transform, 3
monitor = DP-4, addreserved, 10, 20, 0, 0
monitor = DP-5, 1920x1080, 0x0, 1, cm, srgb, sdrbrightness, 1.2, sdrsaturation, 1.0, workspace, 2
monitor = HDMI-1, preferred, auto-right, 1, vrr, 1, bitdepth, 10
`
	out, rpt, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{
		`output = "DP-1",`,
		`output = "DP-2",`,
		`disabled = true,`,                                                // both DP-1 and DP-2
		`output = "DP-3",`,
		`transform = 3,`,                                                  // DP-3 special form
		`output = "DP-4",`,
		`addreserved = { top = 10, bottom = 20, left = 0, right = 0 },`,  // DP-4 special form
		`output = "DP-5",`,
		`cm = "srgb",`,
		`sdrbrightness = 1.2,`,
		`sdrsaturation = 1.0,`,
		`workspace = "2",`,
		`mode = "preferred",`,
		`position = "auto-right",`,
		`vrr = 1,`,
		`bitdepth = 10,`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "extra field") {
		t.Errorf("a monitor tail param that should be recognized produced a TODO:\n%s", out)
	}
	if rpt.Flagged != 0 {
		t.Errorf("expected no flagged monitor params, got %d: %+v", rpt.Flagged, rpt.Notes)
	}
}

// TestMonitorV2LuminanceFields confirms that monitorv2 block fields
// for HDR/SDR luminance and brightness emit as numbers, not strings —
// per Hyprland v0.54.0's parse table for monitorv2.
func TestMonitorV2LuminanceFields(t *testing.T) {
	src := `monitorv2 {
    output = HDMI-1
    mode = 3840x2160@60
    sdrbrightness = 1.2
    sdrsaturation = 0.95
    sdr_min_luminance = 0.005
    sdr_max_luminance = 80
    min_luminance = 0.001
    max_luminance = 1000
    max_avg_luminance = 400
    supports_hdr = 1
    supports_wide_color = 1
}
`
	out, _, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{
		`sdrbrightness = 1.2,`,
		`sdrsaturation = 0.95,`,
		`sdr_min_luminance = 0.005,`,
		`sdr_max_luminance = 80,`,
		`min_luminance = 0.001,`,
		`max_luminance = 1000,`,
		`max_avg_luminance = 400,`,
		`supports_hdr = 1,`,
		`supports_wide_color = 1,`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing numeric field %q in:\n%s", want, out)
		}
	}
}

// TestHyphenatedConfigKeys covers the hyprlang config routes that contain a
// '-'. Hyprland registers Lua config names through
// CConfigManager::luaConfigValueName, which rewrites ':' → '.' and '-' → '_',
// so the hyphenated source spelling is unreachable from hl.config — emitting
// it verbatim makes Hyprland report "unknown config key" at load. Covers both
// the nested section form and the flattened 'section:key = value' form.
func TestHyphenatedConfigKeys(t *testing.T) {
	src := `input {
    touchpad {
        tap-to-click = true
        tap-and-drag = false
    }
}

input-capture {
    capture_modifiers = true
}

input-capture:enforce_barriers = false
`
	out, _, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{
		"tap_to_click = true,",
		"tap_and_drag = false,",
		"input_capture = {",
		"capture_modifiers = true,",
		"enforce_barriers = false,",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// The source spelling must not survive in any form — neither as a bare
	// key (invalid Lua) nor bracket-quoted (valid Lua, unknown to Hyprland).
	for _, bad := range []string{"tap-to-click", "tap-and-drag", "input-capture ="} {
		if strings.Contains(out, bad) {
			t.Errorf("hyphenated config key %q leaked into output:\n%s", bad, out)
		}
	}
	if strings.Contains(out, "unknown section") {
		t.Errorf("input-capture section was not recognized:\n%s", out)
	}
}

// TestHyprland056Additions covers the three conversion surfaces Hyprland 0.56
// added on the hyprlang side, each of which needs a typed Lua counterpart:
// the 'x' bind flag (allow_input_capture), the 'releaseinputcapture'
// dispatcher, and the bindm resize ratio modes — which only became
// expressible in Lua once 0.56 added resize({ keep_aspect_ratio }).
func TestHyprland056Additions(t *testing.T) {
	src := `bindx = SUPER, X, exec, foo
bindxl = SUPER, Y, exec, bar
bind = SUPER, Z, releaseinputcapture
bindm = ALT, mouse:273, resizewindow 1
bindm = ALT, mouse:274, resizewindow 2
bindm = ALT, mouse:272, movewindow
`
	out, rpt, err := Convert(src)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{
		`hl.bind("SUPER + X", hl.dsp.exec_cmd("foo"), { allow_input_capture = true })`,
		`{ locked = true, allow_input_capture = true }`,
		`hl.bind("SUPER + Z", hl.dsp.release_input_capture())`,
		`hl.bind("ALT + mouse:273", hl.dsp.window.resize({ keep_aspect_ratio = true }))`,
		`hl.bind("ALT + mouse:274", hl.dsp.window.resize({ keep_aspect_ratio = false }))`,
		`hl.bind("ALT + mouse:272", hl.dsp.window.drag())`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if rpt.Flagged != 0 {
		t.Errorf("expected no flags, got %d:\n%s", rpt.Flagged, out)
	}
}

// TestPermissionModeField pins the HL.PermissionSpec field name. hlPermission
// reads binary/type/**mode** and rejects the whole table with
// "hl.permission: expected { binary, type, mode }" if any is missing, so an
// `allow =` key silently drops the mode and fails the call at config load.
// This was wrong for every permission directive until it was caught by running
// generated output through a real Hyprland's --verify-config.
func TestPermissionModeField(t *testing.T) {
	for _, mode := range []string{"allow", "deny", "ask"} {
		src := "permission = /usr/bin/grim, screencopy, " + mode + "\n"
		out, rpt, err := Convert(src)
		if err != nil {
			t.Fatalf("Convert: %v", err)
		}
		want := `hl.permission({ binary = "/usr/bin/grim", type = "screencopy", mode = ` +
			`"` + mode + `" })`
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
		if strings.Contains(out, "allow = ") {
			t.Errorf("emitted an 'allow =' key; HL.PermissionSpec declares 'mode':\n%s", out)
		}
		if rpt.Flagged != 0 {
			t.Errorf("unexpected flags for mode %q: %d", mode, rpt.Flagged)
		}
	}
}
