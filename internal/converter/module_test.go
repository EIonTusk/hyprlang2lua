package converter

import "testing"

// TestModuleNameFor exercises the source-path → require()-module mapping.
// The behavior matters because modular Hyprland configs source files from
// subdirectories of the config root (~/.config/hypr/conf/...), and a flat
// basename mapping collapses 'conf/animations/default.conf' and
// 'conf/keybindings/default.conf' into the same require("default"), which
// then resolves to whichever the Lua VM finds first on package.path — at
// best surprising, at worst silently wrong.
func TestModuleNameFor(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// Top-level files under the Hyprland root: basename is correct.
		{"colors.conf", "colors"},
		{"~/.config/hypr/colors.conf", "colors"},
		{"/home/user/.config/hypr/mocha.conf", "mocha"},

		// Subdirectories under the Hyprland root must preserve structure
		// so per-subdir files (e.g. multiple default.conf) stay distinct.
		{"~/.config/hypr/conf/monitor.conf", "conf.monitor"},
		{"~/.config/hypr/conf/animations/default.conf", "conf.animations.default"},
		{"~/.config/hypr/conf/keybindings/default.conf", "conf.keybindings.default"},
		{"/home/user/.config/hypr/themes/dark.conf", "themes.dark"},

		// Absolute paths outside any 'hypr/' segment fall back to basename:
		// the user's package.path is the only thing that could rescue them,
		// and we can't predict it from the converter.
		{"~/.cache/wal/colors-hyprland.conf", "colors-hyprland"},
		{"/etc/hypr-extra/foo.conf", "foo"},

		// Relative paths preserve structure with '/' → '.'.
		{"themes/dark.conf", "themes.dark"},
		{"./mocha.conf", "mocha"},
		{"conf/monitor.conf", "conf.monitor"},

		// Filename sanitization carries through.
		{"~/.config/hypr/conf/123/foo.conf", "conf._123.foo"},

		// Empty / degenerate input maps to a recognizable fallback.
		{"", "sourced_file"},
		{"/", "sourced_file"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := moduleNameFor(tc.in)
			if got != tc.want {
				t.Errorf("moduleNameFor(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
