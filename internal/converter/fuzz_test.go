package converter

import "testing"

// FuzzConvert exercises Convert on arbitrary byte sequences to catch panics or
// hangs in the lexer/parser. The converter is required to either return an
// error or a non-empty (possibly heavily-flagged) Lua output — but never crash.
//
// Run with:
//   go test ./internal/converter -fuzz FuzzConvert -fuzztime 30s
func FuzzConvert(f *testing.F) {
	for _, seed := range []string{
		"",
		"# only a comment\n",
		"general { gaps_in = 5 }\n",
		"$m = SUPER\nbind = $m, Q, exec, kitty\n",
		"bind = , XF86AudioMute, exec, true\n",
		"windowrulev2 = float, class:^kitty$\n",
		"monitor = ,preferred,auto,1\n",
		"source = ~/.config/hypr/colors.conf\n",
		"general:gaps_in = 7\n",
		"plugin { foo { bar = 1 } }\n",
		"device:my-device { sensitivity = 0.5 }\n",
		"# unterminated section\ngeneral {\n",
		"\\\nbind = SUPER, A, exec, \\\necho ok\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		// Bound input size — fuzzing huge strings tells us little new.
		if len(s) > 64*1024 {
			t.Skip()
		}
		_, _, _ = Convert(s)
	})
}
