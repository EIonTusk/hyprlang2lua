package converter

import "testing"

// TestInterpolate covers the $var → Lua-local rewrite inside exec strings
// and other places hyprlang values can carry mixed text + variable refs.
//
// The tricky cases live inside exec payloads where the same '$' sigil
// belongs to a different language — most often awk's positional $2 or
// shell's $1. Hyprlang identifiers may not start with a digit, so $<digit>
// must pass through unmodified rather than become a (digit-prefixed)
// Lua local like '_2'.
func TestInterpolate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		// Real hyprlang refs still rewrite.
		{"single var", "$mainMod", "mainMod", true},
		{"trailing text", "$mainMod + SHIFT", `mainMod .. " + SHIFT"`, true},
		{"leading text", "echo $mainMod", `"echo " .. mainMod`, true},
		{"both sides", "echo $mainMod here", `"echo " .. mainMod .. " here"`, true},

		// Shell/awk positionals must NOT be rewritten — they're literal
		// text inside an exec arg, not Hyprlang variables.
		{"awk $2", "{print $2 * 1.1}", "", false},
		{"shell $1", "echo $1", "", false},
		{"mixed real var and awk positional",
			"awk '{print $2}' $mainMod",
			`"awk '{print $2}' " .. mainMod`,
			true,
		},

		// Edge: bare $ with no follow-up char (legal as text, no var ref).
		{"bare dollar at end", "echo $", "", false},

		// Edge: $_underscore is a legal hyprlang identifier; should rewrite.
		{"underscore-led", "echo $_foo", `"echo " .. _foo`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Passing nil for `declared` preserves the legacy "rewrite every
			// $X as a Lua local" behaviour these cases were written against.
			got, ok := interpolate(tc.in, nil)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (got expr %q)", ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Errorf("interpolate(%q):\n  got:  %s\n  want: %s", tc.in, got, tc.want)
			}
		})
	}
}

// TestInterpolate_DeclaredSet exercises the declared-aware path: only refs
// in the set rewrite to a Lua local, undeclared refs survive as literal
// text in the surrounding string (so a downstream /bin/sh -c gets the raw
// $HOME / $XDG_* sigil and expands it at runtime).
func TestInterpolate_DeclaredSet(t *testing.T) {
	_, decls := newVarScopes([]string{"mainMod", "terminal"})
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"declared rewrites", "$mainMod + SHIFT",
			`mainMod .. " + SHIFT"`, true},
		{"undeclared preserved", "echo $HOME",
			``, false},
		{"mix declared and env", "echo $HOME from $mainMod",
			`"echo $HOME from " .. mainMod`, true},
		{"declared then env tail", "$terminal --workdir=$HOME",
			`terminal .. " --workdir=$HOME"`, true},
		{"only env refs → no rewrite",
			"systemctl --user start $XDG_CURRENT_DESKTOP",
			``, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := interpolate(tc.in, decls)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (got expr %q)", ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Errorf("interpolate(%q, decls):\n  got:  %s\n  want: %s", tc.in, got, tc.want)
			}
		})
	}
}

// TestInterpolate_HyprlangNames pins hyprlang's own variable lookup: a name
// is everything between '$' and '=' at declaration, and a reference is the
// longest declared name the text continues with — not an identifier scan.
// `$looking-glass` must resolve whole, and `$center-float-large` must win
// over `$center-float`.
func TestInterpolate_HyprlangNames(t *testing.T) {
	values, shell := newVarScopes([]string{
		"looking-glass", "gnome-schema", "center-float", "center-float-large", "mainMod",
	})
	cases := []struct {
		name  string
		scope *varScope
		in    string
		want  string
		ok    bool
	}{
		{"hyphenated whole value", shell, "$looking-glass", "looking_glass", true},
		{"hyphenated mid-string", values,
			"gsettings set $gnome-schema gtk-theme Adwaita",
			`"gsettings set " .. gnome_schema .. " gtk-theme Adwaita"`, true},
		{"longest name wins", values, "float, $center-float-large",
			`"float, " .. center_float_large`, true},
		{"shorter name still matches alone", values, "$center-float, x",
			`center_float .. ", x"`, true},
		// hyprlang substitutes by substring, so a declared name is replaced
		// even when more identifier characters follow it.
		{"substring match like hyprlang", shell, "$mainModX",
			`mainMod .. "X"`, true},
		{"undeclared hyphenated prefix stays shell text", shell, "echo $looking-for",
			``, false},
		{"undeclared value ref still fails fast", values, "$typo-here",
			`typo .. "-here"`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := interpolate(tc.in, tc.scope)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (got expr %q)", ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Errorf("interpolate(%q):\n  got:  %s\n  want: %s", tc.in, got, tc.want)
			}
		})
	}
}

// TestVarScopeIdents covers the hyprlang name → Lua local mapping: distinct
// names that spell the same Lua identifier must not alias one another, and a
// name that is a Lua keyword must not become one.
func TestVarScopeIdents(t *testing.T) {
	values, _ := newVarScopes([]string{"a-b", "a_b", "a.b", "end", "a-b"})
	want := map[string]string{"a-b": "a_b", "a_b": "a_b_2", "a.b": "a_b_3", "end": "end_"}
	for name, id := range want {
		if got := values.ident(name); got != id {
			t.Errorf("ident(%q) = %q, want %q", name, got, id)
		}
	}
}
