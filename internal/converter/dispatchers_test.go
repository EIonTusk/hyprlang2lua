package converter

import "testing"

// TestBuildDispatcher_ResizeMove covers the resize/move dispatcher family.
// The hyprlang 'resizeactive X Y' form maps to Lua's hl.dsp.window.resize({
// x, y, relative? }) and the Lua API rejects string args with 'expected no
// args, or a table'. Earlier output passed quoteLuaString(joinArgs(args)),
// which failed at config load and silently broke every following bind.
func TestBuildDispatcher_ResizeMove(t *testing.T) {
	cases := []struct {
		name     string
		disp     string
		args     []string
		wantExpr string
		wantFail bool
	}{
		// Resize: relative by default.
		{"resize negative x", "resizeactive", []string{"-20 0"},
			"hl.dsp.window.resize({ x = -20, y = 0, relative = true })", false},
		{"resize positive x", "resizeactive", []string{"20 0"},
			"hl.dsp.window.resize({ x = 20, y = 0, relative = true })", false},
		{"resize negative y", "resizeactive", []string{"0 -20"},
			"hl.dsp.window.resize({ x = 0, y = -20, relative = true })", false},
		{"resize percent", "resizeactive", []string{"10% 5%"},
			`hl.dsp.window.resize({ x = "10%", y = "5%", relative = true })`, false},
		{"resize exact keyword inverts to absolute", "resizeactive", []string{"exact 1024 768"},
			"hl.dsp.window.resize({ x = 1024, y = 768 })", false},
		{"resizewindow alias", "resizewindow", []string{"-10 0"},
			"hl.dsp.window.resize({ x = -10, y = 0, relative = true })", false},
		{"resize no args still works", "resizeactive", nil,
			"hl.dsp.window.resize()", false},

		// resizewindowpixel: absolute by default; trailing ',WINDOW' selector.
		{"resizewindowpixel absolute", "resizewindowpixel", []string{"1024 768"},
			"hl.dsp.window.resize({ x = 1024, y = 768 })", false},
		{"resizewindowpixel with window selector", "resizewindowpixel", []string{"1024 768", "title:Firefox"},
			`hl.dsp.window.resize({ x = 1024, y = 768, window = "title:Firefox" })`, false},

		// Move active: same string-vs-table bug fixed.
		{"moveactive delta", "moveactive", []string{"50 0"},
			"hl.dsp.window.move({ x = 50, y = 0, relative = true })", false},
		{"movewindowpixel absolute", "movewindowpixel", []string{"100 200"},
			"hl.dsp.window.move({ x = 100, y = 200 })", false},

		// Garbage args should fail so they're flagged, not silently mis-emitted.
		{"resize wrong arg count", "resizeactive", []string{"only-one"}, "", true},
		{"resize non-numeric", "resizeactive", []string{"abc def"}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := buildDispatcher(tc.disp, tc.args)
			if tc.wantFail {
				if reason == "" {
					t.Fatalf("expected failure but got expr %q", got)
				}
				return
			}
			if reason != "" {
				t.Fatalf("unexpected failure: %s", reason)
			}
			if got != tc.wantExpr {
				t.Errorf("buildDispatcher(%q, %v):\n  got:  %s\n  want: %s", tc.disp, tc.args, got, tc.wantExpr)
			}
		})
	}
}
