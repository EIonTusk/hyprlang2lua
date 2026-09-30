package converter

import (
	"slices"
	"strconv"
	"testing"
)

// TestExpandFields covers hyprlang's textual expansion of rule fields: a
// field that is exactly one declared $var is replaced by its value, which
// may itself hold several comma-separated fields or another reference.
func TestExpandFields(t *testing.T) {
	values, _ := newVarScopes([]varDef{
		{"center-float", "class:^(pavucontrol)$", 1},
		{"pip", "class:^(mpv)$, title:^(Picture)$", 2},
		{"alias", "$center-float", 3},
		{"loop", "$loop", 4},
		{"re", "class:^(one)$", 5},
		{"re", "class:^(two)$", 10},
		{"late", "class:^(late)$", 50},
	})
	cases := []struct {
		name string
		in   []string
		line int
		want []string
	}{
		{"matcher variable", []string{"float", "$center-float"}, 20,
			[]string{"float", "class:^(pavucontrol)$"}},
		{"multi-field variable", []string{"pin", "$pip"}, 20,
			[]string{"pin", "class:^(mpv)$", "title:^(Picture)$"}},
		{"nested reference", []string{"center", "$alias"}, 20,
			[]string{"center", "class:^(pavucontrol)$"}},
		// Partial references are value formatting's job (→ Lua local).
		{"partial reference untouched", []string{"float", "class:$re"}, 20,
			[]string{"float", "class:$re"}},
		{"undeclared untouched", []string{"float", "$nope"}, 20,
			[]string{"float", "$nope"}},
		{"self reference terminates", []string{"float", "$loop"}, 20,
			[]string{"float", "$loop"}},
		// Redeclaration: the latest declaration above the line wins.
		{"value before redeclaration", []string{"float", "$re"}, 7,
			[]string{"float", "class:^(one)$"}},
		{"value after redeclaration", []string{"float", "$re"}, 11,
			[]string{"float", "class:^(two)$"}},
		// Declared only further down: the first declaration.
		{"forward reference", []string{"float", "$late"}, 20,
			[]string{"float", "class:^(late)$"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := values.expandFields(tc.in, tc.line); !slices.Equal(got, tc.want) {
				t.Errorf("expandFields(%q, line %d) = %q, want %q", tc.in, tc.line, got, tc.want)
			}
		})
	}
}

// TestExpandFieldsBounded feeds a doubling chain ($v0 = $v1, $v1; …) deep
// enough to reach 2^40 fields unbounded; expansion must stop at the cap.
func TestExpandFieldsBounded(t *testing.T) {
	var defs []varDef
	for i := 0; i < 40; i++ {
		ref := "$v" + strconv.Itoa(i+1)
		defs = append(defs, varDef{"v" + strconv.Itoa(i), ref + ", " + ref, i + 1})
	}
	values, _ := newVarScopes(defs)
	got := values.expandFields([]string{"float", "$v0"}, 100)
	if len(got) > maxRuleFields+maxRuleVarDepth+1 {
		t.Fatalf("expanded to %d fields, want at most ~%d", len(got), maxRuleFields)
	}
}
