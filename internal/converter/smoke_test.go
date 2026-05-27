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
	src := `bind = shift, PRINT, exec, hyprshot
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
