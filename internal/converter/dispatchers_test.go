package converter

import "testing"

// TestBuildDispatcher_ResizeMove covers the resize/move dispatcher family
// with polyfill OFF (the default). The hyprlang 'resizeactive X Y' form
// maps to Lua's hl.dsp.window.resize({ x, y, relative? }) and the Lua API
// rejects string args with 'expected no args, or a table'. Percent inputs
// have no numeric Lua equivalent at 0.55, so polyfill-off rejects them via
// reason rather than emit a load-time-broken `x = "10%"` form.
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
		{"resize exact keyword inverts to absolute", "resizeactive", []string{"exact 1024 768"},
			"hl.dsp.window.resize({ x = 1024, y = 768 })", false},
		{"resizewindow alias", "resizewindow", []string{"-10 0"},
			"hl.dsp.window.resize({ x = -10, y = 0, relative = true })", false},
		{"resize no args still works", "resizeactive", nil,
			"hl.dsp.window.resize()", false},

		// Percent: rejected without --polyfill (the typed API only accepts numbers).
		{"resize percent rejected without polyfill", "resizeactive", []string{"10% 5%"}, "", true},
		{"moveactive percent rejected without polyfill", "moveactive", []string{"10% 0"}, "", true},

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
			g := newGenerator(Options{})
			got, reason := g.buildDispatcher(tc.disp, tc.args)
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

// TestBuildDispatcher_ResizeMovePolyfill exercises the --polyfill path: any
// percent input emits a closure that resolves the percent at dispatch time
// against the appropriate runtime reference (active window for *active,
// selected window for *windowpixel, active monitor for 'exact'), then
// hl.dispatch()es the typed call. Pure-numeric inputs are unaffected by the
// flag — they still emit the single typed call.
func TestBuildDispatcher_ResizeMovePolyfill(t *testing.T) {
	cases := []struct {
		name     string
		disp     string
		args     []string
		wantExpr string
		wantFail bool
	}{
		// resizeactive percent → active window's size.
		{"resizeactive percent both", "resizeactive", []string{"10% 5%"},
			`function() local w = hl.get_active_window(); if not w then return end; hl.dispatch(hl.dsp.window.resize({ x = math.floor(w.size.x * 10 / 100), y = math.floor(w.size.y * 5 / 100), relative = true })) end`, false},
		{"resizeactive mixed pixel and percent", "resizeactive", []string{"100 5%"},
			`function() local w = hl.get_active_window(); if not w then return end; hl.dispatch(hl.dsp.window.resize({ x = 100, y = math.floor(w.size.y * 5 / 100), relative = true })) end`, false},

		// moveactive percent → active window's position (matches legacy
		// hyprlang: parseWindowVectorArgsRelative uses relativeTo = window.pos).
		{"moveactive percent", "moveactive", []string{"10% 0"},
			`function() local w = hl.get_active_window(); if not w then return end; hl.dispatch(hl.dsp.window.move({ x = math.floor(w.at.x * 10 / 100), y = 0, relative = true })) end`, false},

		// exact prefix → active monitor's size, absolute (no relative=true).
		{"resizeactive exact percent → monitor", "resizeactive", []string{"exact 50% 50%"},
			`function() local m = hl.get_active_monitor(); if not m then return end; hl.dispatch(hl.dsp.window.resize({ x = math.floor(m.width * 50 / 100), y = math.floor(m.height * 50 / 100) })) end`, false},

		// resizewindowpixel + selector + percent → that window's size.
		{"resizewindowpixel percent with selector", "resizewindowpixel", []string{"50% 50%", "title:Firefox"},
			`function() local w = hl.get_window("title:Firefox"); if not w then return end; hl.dispatch(hl.dsp.window.resize({ x = math.floor(w.size.x * 50 / 100), y = math.floor(w.size.y * 50 / 100), window = "title:Firefox" })) end`, false},

		// Pure-numeric still emits the typed call even with polyfill on —
		// no need for a closure when the typed API accepts the input directly.
		{"resize numeric unchanged by polyfill", "resizeactive", []string{"-20 0"},
			"hl.dsp.window.resize({ x = -20, y = 0, relative = true })", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newGenerator(Options{Polyfill: true})
			got, reason := g.buildDispatcher(tc.disp, tc.args)
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

func TestBuildDispatcher_WorkspaceAndFullscreen(t *testing.T) {
	cases := []struct {
		name     string
		disp     string
		args     []string
		wantExpr string
		wantFail bool
	}{
		// Silent workspace move uses follow=false, not silent=true.
		{"movetoworkspacesilent number", "movetoworkspacesilent", []string{"5"},
			"hl.dsp.window.move({ workspace = 5, follow = false })", false},
		{"movetoworkspacesilent named", "movetoworkspacesilent", []string{"m-1"},
			`hl.dsp.window.move({ workspace = "m-1", follow = false })`, false},
		{"movetoworkspacesilent special", "movetoworkspacesilent", []string{"special:magic"},
			`hl.dsp.window.move({ workspace = "special:magic", follow = false })`, false},

		// fullscreen dispatcher maps numeric arg to mode string.
		// fullscreen now always emits action="toggle" explicitly because
		// 0.54's default for `fullscreen` was toggle, and 0.55's default
		// isn't documented in the wiki — preserving the 0.54 behaviour
		// avoids a silent semantic shift.
		{"fullscreen no arg", "fullscreen", nil,
			`hl.dsp.window.fullscreen({ mode = "fullscreen", action = "toggle" })`, false},
		{"fullscreen 0 = fullscreen", "fullscreen", []string{"0"},
			`hl.dsp.window.fullscreen({ mode = "fullscreen", action = "toggle" })`, false},
		{"fullscreen 1 = maximized", "fullscreen", []string{"1"},
			`hl.dsp.window.fullscreen({ mode = "maximized", action = "toggle" })`, false},
		// togglefullscreen now passes action="toggle" so the typed API
		// flips the state instead of unconditionally entering fullscreen.
		{"togglefullscreen", "togglefullscreen", nil,
			`hl.dsp.window.fullscreen({ mode = "fullscreen", action = "toggle" })`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newGenerator(Options{})
			got, reason := g.buildDispatcher(tc.disp, tc.args)
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
