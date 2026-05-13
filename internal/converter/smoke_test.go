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
		`hl.bind(mainMod .. " + mouse:272", hl.dsp.window.drag(), { mouse = true })`,
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
		`hl.bind("SHIFT + SUPER + 201",`,
		`hl.bind("CTRL + ALT + t",`,
		`hl.bind(mainMod .. " + SHIFT + X",`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
