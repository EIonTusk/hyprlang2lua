package converter

import (
	"sort"
	"strconv"
	"strings"
)

// varScope is the set of hyprlang $variables declared in one file, plus the
// policy for a `$X` that names none of them.
//
// hyprlang has no identifier grammar for variable names: `$name = value`
// declares everything between '$' and '=' (CConfig::parseVariable stores
// lhs.substr(1)), and later lines are expanded by replacing each "$"+name
// substring, longest name first — the variable list is kept sorted by name
// length (src/config.cpp). So `$looking-glass` is a variable, and
// `$center-float-large` must win over `$center-float`. [varScope.match]
// reproduces that lookup; a scanner that stops at the first non-identifier
// character would read `$looking-glass` as `$looking` followed by "-glass".
//
// A nil *varScope is valid and declares nothing: every identifier-shaped
// `$X` rewrites to a Lua local (the legacy behaviour tests and one-shot
// helpers rely on).
type varScope struct {
	names  []string            // declared names without '$', longest first
	idents map[string]string   // declared name → its Lua local
	defs   map[string][]varDef // declared name → its declarations, in source order

	// passUndeclared picks what happens to a `$X` naming no declared
	// variable. true: keep it as literal text, for values a shell or an
	// exec'd program expands at runtime ($HOME, $XDG_*). false: rewrite it
	// to a Lua local anyway, so a typo fails loudly at config load with a
	// nil-concat error instead of producing a silently wrong value.
	passUndeclared bool
}

// varDef is one `$name = value` declaration: name without '$', raw value
// text, and the source line it sits on.
type varDef struct {
	name, value string
	line        int
}

// newVarScopes builds the two scopes a generator needs from the
// declarations in source order: one for plain values (undeclared refs fail
// fast) and one for shell-bound values (undeclared refs pass through). Both
// share the same name → Lua local mapping and declaration table.
//
// Lua locals are assigned in declaration order. Distinct hyprlang names can
// map to the same Lua spelling (`$a-b` and `$a_b` both become a_b), so a
// later name that collides gets a numeric suffix rather than silently
// aliasing the earlier variable.
func newVarScopes(decls []varDef) (values, shell *varScope) {
	idents := map[string]string{}
	taken := map[string]bool{}
	defs := map[string][]varDef{}
	var names []string
	for _, d := range decls {
		n := d.name
		if n == "" {
			continue
		}
		defs[n] = append(defs[n], d)
		if _, seen := idents[n]; seen {
			continue // redeclaration reuses its local
		}
		base := luaIdent(n)
		id := base
		for k := 2; taken[id]; k++ {
			id = base + "_" + strconv.Itoa(k)
		}
		taken[id] = true
		idents[n] = id
		names = append(names, n)
	}
	// Longest first, as hyprlang matches; ties broken lexically so the
	// order (and so the output) never depends on declaration order.
	sort.Slice(names, func(i, j int) bool {
		if len(names[i]) != len(names[j]) {
			return len(names[i]) > len(names[j])
		}
		return names[i] < names[j]
	})
	values = &varScope{names: names, idents: idents, defs: defs}
	shell = &varScope{names: names, idents: idents, defs: defs, passUndeclared: true}
	return values, shell
}

// valueAt returns the raw value `$name` had when hyprlang reached line: the
// latest declaration above it. A name only declared further down resolves
// to its first declaration, the same forward-reference leniency the rest of
// the converter applies (see gatherDecls).
func (v *varScope) valueAt(name string, line int) (string, bool) {
	if v == nil || len(v.defs[name]) == 0 {
		return "", false
	}
	ds := v.defs[name]
	val := ds[0].value
	for _, d := range ds {
		if d.line >= line {
			break
		}
		val = d.value
	}
	return val, true
}

// Limits for [varScope.expandFields]. maxRuleVarDepth bounds nesting
// (`$a = $b`, `$b = class:x`) so a self-referential `$a = $a` terminates;
// hyprlang caps its own expansion loop the same way. maxRuleFields bounds
// fan-out: `$a = $b, $b` nested a dozen levels deep would otherwise expand
// to thousands of fields, and the converter runs on untrusted input in the
// browser. No real rule comes near either limit; past them, references are
// left unexpanded and surface as TODOs.
const (
	maxRuleVarDepth = 16
	maxRuleFields   = 256
)

// expandFields applies hyprlang's textual variable expansion to the
// comma-separated fields of a rule line. hyprlang substitutes variables
// into the raw line before the rule parser sees it, so a variable can carry
// rule syntax — a matcher (`$center-float = class:^(pavucontrol)$`), an
// effect, or several comma-separated fields at once. A field that is
// exactly one declared reference is replaced by that variable's value,
// re-split on commas and expanded again. Everything else is left for value
// formatting, so `class:$cls` still becomes a Lua local reference.
func (v *varScope) expandFields(parts []string, line int) []string {
	var out []string
	for _, f := range parts {
		v.expandField(f, line, 0, &out)
	}
	return out
}

func (v *varScope) expandField(f string, line, depth int, out *[]string) {
	if depth < maxRuleVarDepth && len(*out) < maxRuleFields && strings.HasPrefix(f, "$") {
		if n, ok := v.match(f[1:]); ok && len(n) == len(f)-1 {
			if val, ok := v.valueAt(n, line); ok {
				for _, p := range splitCommas(val) {
					v.expandField(p, line, depth+1, out)
				}
				return
			}
		}
	}
	*out = append(*out, f)
}

// match returns the longest declared name that s starts with, where s is
// the text right after a '$'.
func (v *varScope) match(s string) (string, bool) {
	if v == nil {
		return "", false
	}
	for _, n := range v.names {
		if strings.HasPrefix(s, n) {
			return n, true
		}
	}
	return "", false
}

// ident returns the Lua local for a variable name (with or without its '$').
// Declared names use the collision-free mapping; anything else falls back to
// [luaIdent].
func (v *varScope) ident(name string) string {
	name = strings.TrimPrefix(name, "$")
	if v != nil {
		if id, ok := v.idents[name]; ok {
			return id
		}
	}
	return luaIdent(name)
}

// ref resolves tok as exactly one variable reference ("$name" and nothing
// else) and returns its Lua local. An identifier-shaped undeclared ref
// resolves too unless the scope passes undeclared refs through.
func (v *varScope) ref(tok string) (string, bool) {
	if !strings.HasPrefix(tok, "$") {
		return "", false
	}
	if n, ok := v.match(tok[1:]); ok && len(n) == len(tok)-1 {
		return v.ident(n), true
	}
	if isDollarRef(tok) && (v == nil || !v.passUndeclared) {
		return luaIdent(tok), true
	}
	return "", false
}
