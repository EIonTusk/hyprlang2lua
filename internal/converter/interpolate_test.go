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
			got, ok := interpolate(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (got expr %q)", ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Errorf("interpolate(%q):\n  got:  %s\n  want: %s", tc.in, got, tc.want)
			}
		})
	}
}
