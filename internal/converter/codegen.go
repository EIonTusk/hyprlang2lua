package converter

import (
	"fmt"
	"strconv"
	"strings"
)

type generator struct {
	out    strings.Builder
	report Report

	// Pending exec / exec-once directives are batched into a single
	// hl.on("hyprland.start", function() ... end) block at the end so they
	// run after the rest of the config is applied. execrOnce is kept apart
	// so the start block can flag the raw-spawn variant — Hyprland's hyprlang
	// 'execr-once' bypassed Hyprland's signal-mask inheritance for the child;
	// hl.exec_cmd has no documented analog as of 0.55, so we emit the same
	// call but warn that the raw-spawn semantic isn't preserved.
	execOnce  []string
	execrOnce []string
	exec      []string

	// Pending hl.config sections. We buffer per top-level section (general,
	// decoration, etc.) and build a small tree per section so nested keys
	// like 'decoration.blur.size' render as nested Lua tables (matching the
	// shipped /usr/share/hypr/hyprland.lua idiom) instead of bracketed
	// strings. Flushed at the next non-config statement.
	configPending map[string]*confTree
	configOrder   []string // section names in first-seen order

	// Whether we've emitted the leading header.
	headerWritten bool
	// preambleAnchor is the byte offset in g.out immediately after the
	// generated-by header's trailing blank line. Polyfill helper
	// definitions are spliced in at this point so they appear before the
	// first body statement that references them.
	preambleAnchor int

	// merge folds every hl.X(...) call that supports merging into a single
	// trailing call. It currently powers two coalescers:
	//   1. hl.config — every section is folded into one trailing
	//      hl.config({...}) whose top-level keys are the sections in
	//      first-seen order (mergedTree).
	//   2. Rule-style calls with a natural grouping argument — fold
	//      adjacent calls whose grouping arg is identical so the action
	//      fields union into one call. Currently active for:
	//        - hl.window_rule    grouped by the match table
	//        - hl.layer_rule     grouped by match.namespace
	//        - hl.workspace_rule grouped by the workspace name
	//      The buffer (pendingRule) holds at most one in-flight call and
	//      is flushed the moment any non-coalescable node intervenes.
	// Top-level comments that arrive while mergedTree has content are added
	// as comment entries on the tree so visual grouping survives. Other
	// hl.* APIs (bind, monitor, env, device, animation, bezier, gesture,
	// permission) re-define their target on each call, so re-emitting
	// would override rather than combine — they're left alone.
	merge        bool
	mergedTree   *confTree
	pendingRule  *pendingRule

	// stripComments drops Comment nodes at every level. TODO markers from
	// flagged directives are unaffected.
	stripComments bool

	// hoistVariables collects every VarDecl into a single block at the top
	// of the output and skips emitting them at their source positions.
	hoistVariables bool

	// polyfill turns on runtime helper-code emission for hyprlang features
	// that have no direct equivalent in the typed Lua API. See
	// [Options.Polyfill] for the trade-off and the current feature list.
	polyfill bool

	// declaredVars is the set of `$name` identifiers declared anywhere in
	// the source via a VarDecl. Populated in a pre-pass before emit so
	// references to a variable resolve no matter where they appear, even if
	// the declaration sits later in the file. Used by interpolation to
	// distinguish a real hyprlang variable from an env-var-ish $X that the
	// shell should expand at runtime: declared names rewrite to a Lua local,
	// undeclared names pass through verbatim (preserving the literal `$X`
	// inside the Lua string so /bin/sh expands it when the exec runs).
	declaredVars map[string]bool

	// neededPolyfills tracks which runtime helper closures were emitted by
	// any directive in this file. The corresponding `local function …` block
	// is written once at the top of the output, gated by Options.Polyfill so
	// turning the flag off forces strict mode (and a TODO marker for each
	// feature that would have used a helper).
	neededPolyfills map[string]bool

	// submap is non-nil while emit is inside a hyprlang `submap = NAME`
	// block. All bind / comment / blank output is redirected into its
	// body buffer; on `submap = reset` (or the next non-submap-friendly
	// directive) the buffer is wrapped in `hl.define_submap(NAME, function()
	// ... end)` and flushed to the main output. This trades hyprlang's
	// stateful global-mode model for the Lua API's callback-scoped form.
	submap *submapBuf
}

// submapBuf collects the body of an in-flight submap block. Lines are
// stored already-formatted (without the per-line indent that the
// `function() ... end` body needs); flushSubmap adds the indent when it
// composes the final hl.define_submap call.
type submapBuf struct {
	name string
	body strings.Builder
	line int
}

func newGenerator(opts Options) *generator {
	g := &generator{
		configPending:   map[string]*confTree{},
		merge:           opts.MergeCalls,
		stripComments:   opts.StripComments,
		hoistVariables:  opts.HoistVariables,
		polyfill:        opts.Polyfill,
		declaredVars:    map[string]bool{},
		neededPolyfills: map[string]bool{},
	}
	if opts.MergeCalls {
		g.mergedTree = newConfTree()
	}
	return g
}

// gatherDecls walks the AST once and records every $var declaration in
// g.declaredVars. Recurses into Section bodies even though hyprlang
// doesn't normally allow nested decls — the AST permits them, so we
// stay defensive. Run before the main emit pass so a reference earlier
// in the file resolves against a later declaration.
func (g *generator) gatherDecls(nodes []node) {
	for _, n := range nodes {
		switch x := n.(type) {
		case VarDecl:
			g.declaredVars[strings.TrimPrefix(x.Name, "$")] = true
		case Section:
			g.gatherDecls(x.Body)
		}
	}
}

// confTree is a small ordered tree used to accumulate config keys before
// emitting them as nested Lua tables. Each entry is either a child subtree
// or a verbatim comment; the two are tracked through a single ordered slice
// so insertion order is preserved exactly.
type confTree struct {
	entries  []confEntry
	children map[string]*confTree
	value    string // non-empty for leaf nodes (formatted Lua literal)
	trailing string // same-line source comment after the leaf value (carries its sigil)
}

type confEntry struct {
	key     string // "" when this entry is a comment
	comment string // populated only when key == ""; carries its source sigil
}

func newConfTree() *confTree {
	return &confTree{children: map[string]*confTree{}}
}

func (t *confTree) setLeaf(path []string, formatted, trailing string) {
	// Drop empty path segments — these come from inputs like 'foo:.bar = 1'
	// where the dotted form yields a stray "". They have no meaningful
	// place in the Lua table tree.
	clean := path[:0]
	for _, p := range path {
		if p != "" {
			clean = append(clean, p)
		}
	}
	if len(clean) == 0 {
		t.value = formatted
		t.trailing = trailing
		return
	}
	head := clean[0]
	child, ok := t.children[head]
	if !ok {
		child = newConfTree()
		t.children[head] = child
		t.entries = append(t.entries, confEntry{key: head})
	}
	child.setLeaf(clean[1:], formatted, trailing)
}

func (t *confTree) addComment(text string) {
	t.entries = append(t.entries, confEntry{comment: text})
}

func (g *generator) translated(n int) { g.report.Translated += n }
func (g *generator) passthrough()     { g.report.Passthrough++ }
func (g *generator) flag(line int, text string) {
	g.report.Flagged++
	g.report.Notes = append(g.report.Notes, ReportNote{Line: line, Text: text})
	g.classifyLine(line, "flagged")
}

// classifyLine records a per-line classification for the diff gutter.
// Once a line is marked "flagged", subsequent calls won't downgrade it —
// flagged status outranks both translated and passthrough.
func (g *generator) classifyLine(line int, class string) {
	if line <= 0 {
		return
	}
	if g.report.LineClass == nil {
		g.report.LineClass = map[int]string{}
	}
	if g.report.LineClass[line] == "flagged" {
		return
	}
	g.report.LineClass[line] = class
}

// preClassify walks the AST in a fast pre-pass and seeds LineClass with a
// best-guess category per node. Codegen later upgrades any line to "flagged"
// via flag() if it emits a TODO marker. This pre-pass keeps the classification
// logic separate from the messy emit code rather than threading line numbers
// through every g.translated()/g.passthrough() call site.
func (g *generator) preClassify(nodes []node) {
	for _, n := range nodes {
		switch x := n.(type) {
		case Comment:
			// Comments are restated verbatim (only the leading sigil changes
			// from '#' to '--'). Not a directive translation.
			g.classifyLine(x.srcLine(), "passthrough")
		case VarDecl:
			// '$foo = bar' becomes 'local foo = "bar"' — a real syntactic
			// rewrite, not a verbatim copy. Treat it like any other
			// translated directive in the gutter and stats.
			g.classifyLine(x.srcLine(), "translated")
		case KeyValue:
			g.classifyLine(x.line, "translated")
		case Section:
			g.classifyLine(x.line, "translated")
			g.preClassify(x.Body)
			// The closing '}' belongs to the section; mark it so the gutter
			// doesn't leave a visual hole at the end of every block.
			g.classifyLine(x.EndLine, "translated")
		case Directive:
			g.classifyLine(x.line, "translated")
		}
	}
}

// stripCommentIndent removes any leading spaces or tabs from a comment line
// without touching the sigil. Used when re-emitting a comment at a new
// indent: the source's original indent doesn't apply at the new position.
func stripCommentIndent(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[i:]
}

// formatLuaComment normalizes a source comment line so it is valid Lua.
// Hyprlang uses '#' for line comments; Lua uses '--'. The '#' character
// is only valid as a comment sigil on the very first line of a chunk
// (the shebang exception) — anywhere else, '#' is the length operator
// and produces a syntax error. To keep emit sites trivial and consistent,
// every comment passes through this single rewriter on the way out:
// any leading whitespace is preserved verbatim, '#' is replaced with '--',
// and anything else (notably already-'--' comments) is returned unchanged.
func formatLuaComment(raw string) string {
	i := 0
	for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t') {
		i++
	}
	if i < len(raw) && raw[i] == '#' {
		return raw[:i] + "--" + raw[i+1:]
	}
	return raw
}

func (g *generator) writeln(s string) {
	if g.submap != nil {
		g.submap.body.WriteString(s)
		g.submap.body.WriteByte('\n')
		return
	}
	g.out.WriteString(s)
	g.out.WriteByte('\n')
}

func (g *generator) writef(format string, args ...any) {
	g.writeln(fmt.Sprintf(format, args...))
}

// header is emitted before the first generated statement (but after any
// leading comments). Records the byte offset right after the trailing
// blank so flushPolyfillPreamble can splice in helper definitions once
// the main emit pass has discovered everything that needs one.
func (g *generator) header() {
	if g.headerWritten {
		return
	}
	g.headerWritten = true
	g.writeln("-- Generated by hyprlang2lua. Review TODOs before reloading Hyprland.")
	g.writeln("")
	g.preambleAnchor = g.out.Len()
}

func (g *generator) gen(file *File) {
	// Build the declared-variable set up front so interpolation can tell a
	// real `$mainMod` reference apart from an env-var-ish `$HOME` that the
	// shell should expand when the resulting command runs.
	g.gatherDecls(file.Nodes)
	if g.hoistVariables {
		// Pull every VarDecl up front so the rest of the file's references
		// resolve no matter where they appear. The main emit pass below
		// skips VarDecl nodes when this is on.
		g.emitHoistedVars(file.Nodes)
	}
	// First pass: emit nodes in source order, accumulating config-section
	// fragments and deferred exec lines.
	for _, n := range file.Nodes {
		g.emit(n)
	}
	g.flushConfig()
	g.flushPendingRule()
	g.flushSubmap()
	g.flushMergedConfig()
	g.flushExec()
	g.flushPolyfillPreamble()
}

// flushPolyfillPreamble splices any helper functions that the main emit
// pass marked needed into the output, right after the generated-by
// header. Done as a post-step rather than at header-write time because
// the set of helpers isn't known until every directive has been
// processed (e.g. a loadconfig dispatcher buried in the last bind line
// adds `hl_reload` to the set).
func (g *generator) flushPolyfillPreamble() {
	if len(g.neededPolyfills) == 0 {
		return
	}
	preamble := buildPolyfillPreamble(g.neededPolyfills)
	out := g.out.String()
	g.out.Reset()
	if g.headerWritten && g.preambleAnchor <= len(out) {
		g.out.WriteString(out[:g.preambleAnchor])
		g.out.WriteString(preamble)
		g.out.WriteString(out[g.preambleAnchor:])
		return
	}
	// No header (empty input?) — prepend so referencing code still works.
	g.out.WriteString(preamble)
	g.out.WriteString(out)
}

// buildPolyfillPreamble returns the Lua source for every helper marked
// needed, in a stable order so the output is deterministic. Each helper
// stands alone; including only the ones actually referenced keeps the
// generated file lean.
func buildPolyfillPreamble(needed map[string]bool) string {
	var b strings.Builder
	b.WriteString("-- hyprlang2lua polyfills — runtime helpers reproducing\n")
	b.WriteString("-- hyprlang behaviour the typed Lua API doesn't expose directly.\n")
	// Stable order, declared once here so the output is byte-identical
	// across runs regardless of map iteration order.
	for _, name := range []string{
		"source_glob",
	} {
		if !needed[name] {
			continue
		}
		b.WriteString(polyfillSnippets[name])
	}
	b.WriteString("\n")
	return b.String()
}

// polyfillSnippets holds the Lua source for each polyfill, keyed by the
// short name used in neededPolyfills. Each snippet ends with a single
// newline so concatenation in buildPolyfillPreamble produces a clean,
// blank-line-separated block.
//
// The set is intentionally tiny: nearly every legacy hyprlang dispatcher
// has a direct native form in Hyprland 0.55's typed Lua API. The one
// holdout is `source = path/*.conf` globbing — `require()` and `dofile()`
// can't glob, and there's no native include-by-pattern.
var polyfillSnippets = map[string]string{
	"source_glob": `
local function hl_source_glob(pattern)
    -- 'source = path/*.conf' had hyprlang glob and inline-expand the
    -- matches. require() can't glob, so we shell out to ls (matching
    -- the user's brace-expansion behaviour) and dofile each result.
    -- Paths with spaces or shell metacharacters in the directory
    -- portion will misparse; typical ~/.config/hypr/ layouts don't
    -- hit this. Swap to lfs.dir() or find -name if you need fancier.
    local p = io.popen("ls " .. pattern .. " 2>/dev/null")
    if not p then return end
    for f in p:lines() do
        local chunk, err = loadfile(f)
        if chunk then chunk()
        else io.stderr:write("hl_source_glob: " .. tostring(err) .. "\n") end
    end
    p:close()
end
`,
}

// emitHoistedVars walks the top-level node list once, emitting every
// VarDecl as a single block right after the generated-by header. Order
// is preserved from source so users still see their grouping. Nested
// VarDecls (i.e. inside sections — hyprlang doesn't actually allow this,
// but the AST permits it) are intentionally left alone.
func (g *generator) emitHoistedVars(nodes []node) {
	emittedAny := false
	for _, n := range nodes {
		v, ok := n.(VarDecl)
		if !ok {
			continue
		}
		if !emittedAny {
			g.header()
			emittedAny = true
		}
		line := fmt.Sprintf("local %s = %s", luaIdent(v.Name), g.fmtVal(v.Value))
		if v.Trailing != "" {
			line += " " + formatLuaComment(v.Trailing)
		}
		g.writeln(line)
		g.translated(1)
	}
	if emittedAny {
		// Blank line separating the hoisted vars from the body.
		g.writeln("")
	}
}

func (g *generator) emit(n node) {
	// Any node that isn't itself one of the coalescable rule directives
	// breaks the in-flight grouping stream — flush whatever's pending
	// before emitting it so source order is preserved.
	if !isCoalescableDirective(n) {
		g.flushPendingRule()
	}
	// While a submap block is in flight, allow only bind* directives,
	// Comments and Blanks to land inside its body — every other node type
	// (sections, exec/exec-once, monitor, env, …) flushes the submap first
	// so it doesn't get accidentally captured into the define_submap
	// callback. The submap directive itself is routed through emitDirective
	// → emitSubmap which handles start/reset/swap explicitly.
	if g.submap != nil && breaksSubmap(n) {
		g.flushSubmap()
	}
	switch x := n.(type) {
	case Comment:
		if g.stripComments {
			return
		}
		// In merge mode, comments arriving between sections are folded into
		// the merged tree at the table-indent of the surrounding context.
		// Outside merge mode they print at their source position. Either
		// way they go through formatLuaComment so '#' becomes '--' — '#'
		// is only valid Lua syntax as a line-1 shebang.
		if g.appendMergedComment(formatLuaComment(stripCommentIndent(x.Text))) {
			g.passthrough()
			return
		}
		g.flushConfig()
		g.writeln(formatLuaComment(x.Text))
		g.passthrough()
	case Blank:
		g.writeln("")
	case VarDecl:
		if g.hoistVariables {
			// Already emitted by emitHoistedVars at the top of the file.
			// Skipping here keeps the body free of duplicate declarations
			// without disturbing line counts on the translated tally
			// (emitHoistedVars already counted it).
			return
		}
		g.header()
		g.flushConfig()
		line := fmt.Sprintf("local %s = %s", luaIdent(x.Name), g.fmtVal(x.Value))
		if x.Trailing != "" {
			line += " " + formatLuaComment(x.Trailing)
		}
		g.writeln(line)
		// '$foo = bar' is a real syntactic rewrite to 'local foo = "bar"',
		// so it counts against translated, not passthrough.
		g.translated(1)
	case KeyValue:
		g.header()
		g.emitKeyValue(x, nil)
	case Section:
		g.header()
		g.emitSection(x)
	case Directive:
		g.header()
		g.flushConfig()
		g.emitDirective(x)
	}
}

// emitKeyValue handles 'key = value' and 'key:sub = value' at any level.
// Inside a Section, parentPath is the slice of nested section names (e.g.
// ["general"] for top-level 'general { gaps_in = 5 }').
func (g *generator) emitKeyValue(kv KeyValue, parentPath []string) {
	if len(parentPath) == 0 {
		// Top-level 'general:gaps_in = 5' — flattened form. Treat the head
		// as the section.
		if kv.Sub != "" {
			g.appendConfig(kv.Key, kv.Sub, kv.Value, kv.Trailing)
			g.translated(1)
			return
		}
		// Bare top-level 'key = value' with no section — uncommon. Stash
		// under a synthetic 'global' bucket; emit as raw comment-warning.
		g.writef("-- TODO: manual review — top-level key '%s = %s' has no enclosing section", kv.Key, kv.Value)
		g.flag(kv.line, "top-level key without section: "+kv.Key)
		return
	}
	// Inside a section — append to the config bucket for the outermost section.
	section := parentPath[0]
	dottedKey := strings.Join(append(parentPath[1:], kv.Key), ".")
	if kv.Sub != "" {
		dottedKey = dottedKey + "." + kv.Sub
	}
	g.appendConfig(section, dottedKey, kv.Value, kv.Trailing)
	g.translated(1)
}

// appendConfig adds a single key inside a section's config tree.
// dottedKey is the path inside the section (e.g. "gaps_in" or "blur.size" or
// "col.active_border"). The 'col' family is special-cased so it stays
// reachable as a nested table (matching the shipped example). `trailing`
// carries a same-line source comment (e.g. "# default value") which will be
// appended after the leaf's ", " in the emitted Lua table.
func (g *generator) appendConfig(section, dottedKey, rawValue, trailing string) {
	// fmtConfValue matches on the source spelling (isCssGapField), so resolve
	// the value before normalizing the route.
	value := g.fmtConfValue(section, dottedKey, rawValue)
	section = luaConfigKey(section)
	path := strings.Split(dottedKey, ".")
	for i := range path {
		path[i] = luaConfigKey(path[i])
	}
	if g.merge {
		g.mergedTree.setLeaf(append([]string{section}, path...), value, trailing)
		return
	}
	tree, ok := g.configPending[section]
	if !ok {
		tree = newConfTree()
		g.configPending[section] = tree
		g.configOrder = append(g.configOrder, section)
	}
	tree.setLeaf(path, value, trailing)
}

// sectionTree returns the confTree node that represents the top-level
// `section` (e.g. "misc"). In merge mode the section lives as a child of
// mergedTree; in default mode it's its own entry in configPending. Either
// way the returned tree is the place to attach KeyValues, sub-sections, and
// comments at depth 0 of that section.
func (g *generator) sectionTree(section string) *confTree {
	if g.merge {
		child, ok := g.mergedTree.children[section]
		if !ok {
			child = newConfTree()
			g.mergedTree.children[section] = child
			g.mergedTree.entries = append(g.mergedTree.entries, confEntry{key: section})
		}
		return child
	}
	tree, ok := g.configPending[section]
	if !ok {
		tree = newConfTree()
		g.configPending[section] = tree
		g.configOrder = append(g.configOrder, section)
	}
	return tree
}

// appendMergedComment adds a top-level comment entry to the merged tree, but
// only after some section content has been buffered. Returns true if the
// comment was absorbed; false means the caller should emit it normally.
func (g *generator) appendMergedComment(text string) bool {
	if !g.merge || len(g.mergedTree.entries) == 0 {
		return false
	}
	g.mergedTree.addComment(text)
	return true
}

func (g *generator) flushConfig() {
	if g.merge {
		// In merge mode, intermediate flush points are no-ops; the single
		// merged call is emitted at end-of-file by flushMergedConfig.
		return
	}
	if len(g.configOrder) == 0 {
		return
	}
	for _, section := range g.configOrder {
		tree := g.configPending[section]
		g.writeln("hl.config({")
		g.writef("    %s = {", luaTableKey(section))
		emitConfTree(&g.out, tree, 2)
		g.writeln("    },")
		g.writeln("})")
		g.writeln("")
	}
	g.configPending = map[string]*confTree{}
	g.configOrder = nil
}

// flushMergedConfig emits the single merged hl.config({...}) call at the
// end of the generated output when MergeCalls is set.
func (g *generator) flushMergedConfig() {
	if !g.merge || len(g.mergedTree.entries) == 0 {
		return
	}
	g.writeln("hl.config({")
	emitConfTree(&g.out, g.mergedTree, 1)
	g.writeln("})")
	g.writeln("")
}

// pendingRule buffers one in-flight rule-style call so that an adjacent
// call of the same kind with the same grouping arg can be coalesced into
// it. The buffer is generic over the call name so window_rule, layer_rule
// and workspace_rule all share the same merge path.
//
// Fields:
//   callName — the hl.X function name (e.g. "window_rule").
//   groupKey — a canonical string derived from the grouping arg. Equality
//              on this string is the merge predicate; different grouping
//              args render to different strings and won't merge.
//   preamble — preformatted lines (each without trailing newline, indented
//              for one level of nesting) that render the grouping arg
//              itself. Written verbatim at flush.
//   actions  — preformatted body lines, same convention as preamble. New
//              additions are appended on each merge hit.
type pendingRule struct {
	callName string
	groupKey string
	preamble []string
	actions  []string
}

func (g *generator) flushPendingRule() {
	if g.pendingRule == nil {
		return
	}
	p := g.pendingRule
	g.pendingRule = nil
	g.writef("hl.%s({", p.callName)
	for _, l := range p.preamble {
		g.writeln(l)
	}
	for _, a := range p.actions {
		g.writeln(a)
	}
	g.writeln("})")
	g.writeln("")
}

// coalesceRule is the shared merge-or-flush dispatch every rule emitter
// runs after building its preamble + actions. It either folds the new
// actions onto an in-flight pending of the same (callName, groupKey), or
// flushes that pending and installs the new one. With merge disabled the
// new pending is flushed immediately so behavior collapses to the prior
// one-call-per-directive output.
func (g *generator) coalesceRule(callName, groupKey string, preamble, actions []string) {
	if g.merge && g.pendingRule != nil && g.pendingRule.callName == callName && g.pendingRule.groupKey == groupKey {
		g.pendingRule.actions = append(g.pendingRule.actions, actions...)
		return
	}
	g.flushPendingRule()
	g.pendingRule = &pendingRule{
		callName: callName,
		groupKey: groupKey,
		preamble: preamble,
		actions:  actions,
	}
	if !g.merge {
		g.flushPendingRule()
	}
}

// isCoalescableDirective reports whether a node is a directive that the
// pending-rule coalescer can extend. Anything else flushes the pending
// stream first to preserve source order.
func isCoalescableDirective(n node) bool {
	d, ok := n.(Directive)
	if !ok {
		return false
	}
	switch d.Name {
	case "windowrule", "windowrulev2", "layerrule", "workspace":
		return true
	}
	return false
}

// breaksSubmap reports whether the given node should terminate an
// in-flight submap block before being emitted. Bind* directives,
// Comments, Blanks, and `submap = …` itself stay inside; anything else
// (sections, exec, monitor, …) breaks out so it lands at the file's top
// level where it belongs.
func breaksSubmap(n node) bool {
	switch x := n.(type) {
	case Comment, Blank:
		return false
	case Directive:
		if x.Name == "submap" {
			return false
		}
		return !strings.HasPrefix(x.Name, "bind")
	}
	return true
}

// flushSubmap composes the in-flight submap body into an
// `hl.define_submap(NAME, function() ... end)` call and writes it to
// the main output. Cleared regardless of whether body was empty so a
// stray `submap = name` with no binds in between doesn't leak state.
func (g *generator) flushSubmap() {
	if g.submap == nil {
		return
	}
	sb := g.submap
	g.submap = nil // detach so subsequent writeln() goes to g.out, not body
	g.writef("hl.define_submap(%s, function()", quoteLuaString(sb.name))
	body := strings.TrimRight(sb.body.String(), "\n")
	if body != "" {
		for _, line := range strings.Split(body, "\n") {
			if line == "" {
				g.writeln("")
				continue
			}
			g.writeln("    " + line)
		}
	}
	g.writeln("end)")
	g.writeln("")
}

// emitConfTree writes a confTree's children at the given indent depth
// (one depth unit = 4 spaces). Leaves are 'key = value,' lines; subtrees
// are nested 'key = { ... },' blocks. Comment entries are emitted in
// their recorded position.
func emitConfTree(w *strings.Builder, t *confTree, depth int) {
	indent := strings.Repeat("    ", depth)
	for _, e := range t.entries {
		if e.key == "" {
			// Comments inside a table are re-emitted at the table's indent
			// (the source's leading whitespace is meaningless here) and run
			// through formatLuaComment so the sigil is always Lua-valid.
			fmt.Fprintf(w, "%s%s\n", indent, formatLuaComment(e.comment))
			continue
		}
		child := t.children[e.key]
		if child.value != "" && len(child.children) == 0 {
			if child.trailing != "" {
				fmt.Fprintf(w, "%s%s = %s, %s\n", indent, luaTableKey(e.key), child.value, formatLuaComment(child.trailing))
				continue
			}
			fmt.Fprintf(w, "%s%s = %s,\n", indent, luaTableKey(e.key), child.value)
			continue
		}
		fmt.Fprintf(w, "%s%s = {\n", indent, luaTableKey(e.key))
		emitConfTree(w, child, depth+1)
		fmt.Fprintf(w, "%s},\n", indent)
	}
}

// emitSection dispatches based on the section name. Recognized sections fold
// into hl.config; 'device' and 'plugin' get their own emitter; anything else
// is flagged.
func (g *generator) emitSection(s Section) {
	switch s.Name {
	case "general", "decoration", "input", "animations", "gestures", "misc",
		"binds", "cursor", "debug", "dwindle", "master", "group", "render",
		"xwayland", "opengl", "ecosystem", "experimental", "layout",
		"scrolling", "quirks",
		// 'input-capture' (Hyprland 0.56) is the only top-level section whose
		// hyprlang name is hyphenated; luaConfigKey rewrites it to the
		// 'input_capture' table hl.config expects.
		"input-capture":
		g.emitConfigSection(s, []string{luaConfigKey(s.Name)})
	case "device":
		g.emitDeviceSection(s)
	case "plugin":
		g.emitPluginSection(s)
	case "monitorv2":
		// Block form (hyprland 0.54): `monitorv2 { output = ..., mode = ...,
		// position = ..., scale = ..., ... }`. Hyprland registered this as
		// a special category alongside the legacy comma-separated
		// `monitor = NAME,MODE,POS,SCALE` directive. The Lua API exposes a
		// single hl.monitor(spec) regardless of which form was used.
		g.emitMonitorV2Block(s)
	case "windowrule", "windowrulev2":
		// Block form (hyprland 0.54): `windowrule { name = ..., match:K = V,
		// key = value, ... }`. Routes through the same hl.window_rule path
		// as the directive form, but with the block-shaped body.
		g.emitWindowRuleBlock(s)
	case "workspace":
		// Block form: `workspace { name = 1, monitor = DP-1, ... }`. The
		// 'name' field carries the workspace selector (matches the
		// directive form's leading positional).
		g.emitWorkspaceBlock(s)
	case "layerrule":
		// Block form: `layerrule { name = ..., match:namespace = ...,
		// blur = true, ... }`.
		g.emitLayerRuleBlock(s)
	default:
		g.flag(s.line, "unknown section: "+s.Name)
		g.writef("-- TODO: manual review — unknown section '%s {' on line %d", s.Name, s.line)
		// Still emit a stub so users can see the body.
		for _, child := range s.Body {
			if c, ok := child.(Comment); ok {
				g.writeln(formatLuaComment(stripCommentIndent(c.Text)))
			}
		}
	}
}

// emitConfigSection walks a section's body and accumulates everything into
// the configPending buckets. Nested sections (decoration { blur { } }) yield
// dotted keys.
func (g *generator) emitConfigSection(s Section, path []string) {
	for _, child := range s.Body {
		switch c := child.(type) {
		case KeyValue:
			g.emitKeyValue(c, path)
		case Section:
			g.emitConfigSection(c, append(append([]string{}, path...), luaConfigKey(c.Name)))
		case Comment:
			if g.stripComments {
				continue
			}
			// Comments inside a config section get attached to the current
			// (nested) point in the section's tree so they survive the emit.
			// The source's leading whitespace is dropped (table depth dictates
			// the indent in the output) but the sigil ('#' or '--') is kept.
			text := stripCommentIndent(c.Text)
			cur := g.sectionTree(path[0])
			// Walk to the current nested point.
			for _, p := range path[1:] {
				next, has := cur.children[p]
				if !has {
					next = newConfTree()
					cur.children[p] = next
					cur.entries = append(cur.entries, confEntry{key: p})
				}
				cur = next
			}
			cur.addComment(text)
			g.report.Passthrough++
		case Blank:
			// Skip — config tables look cleaner without internal blanks.
		case Directive:
			// A directive inside a config section is unusual. Flush the
			// section first, then emit the directive at top level.
			g.flushConfig()
			g.emitDirective(c)
		case VarDecl:
			g.flushConfig()
			g.writef("local %s = %s", luaIdent(c.Name), g.fmtVal(c.Value))
		}
	}
}

func (g *generator) emitDeviceSection(s Section) {
	g.flushConfig()
	name := s.Sub
	g.writeln("hl.device({")
	if name != "" {
		g.writef("    name = %s,", quoteLuaString(name))
	}
	for _, child := range s.Body {
		switch c := child.(type) {
		case KeyValue:
			g.writef("    %s = %s,", luaTableKey(c.Key), g.fmtVal(c.Value))
		case Comment:
			if g.stripComments {
				continue
			}
			g.writeln("    " + formatLuaComment(stripCommentIndent(c.Text)))
		}
	}
	g.writeln("})")
	g.writeln("")
	g.translated(1)
}

func (g *generator) emitPluginSection(s Section) {
	g.flushConfig()
	// Per-plugin config is highly plugin-specific — hl.plugin[name] is a
	// dynamic namespace. Emit a TODO with the body preserved.
	g.flag(s.line, "plugin section requires manual review (plugin API is per-plugin)")
	g.writef("-- TODO: manual review — plugin section '%s'. The new Lua API exposes plugins via hl.plugin.<name>(...) — wire up per the plugin's docs.", s.Sub)
	g.writeln("--[[")
	for _, child := range s.Body {
		switch c := child.(type) {
		case Section:
			g.writef("  %s { ... }", c.Name)
		case KeyValue:
			g.writef("  %s = %s", c.Key, c.Value)
		case Comment:
			// Body is already inside '--[[ ]]', so the sigil doesn't have
			// to be Lua-syntactic. Run through formatLuaComment anyway so
			// the output looks consistent and source '#' doesn't show up.
			g.writeln("  " + formatLuaComment(stripCommentIndent(c.Text)))
		}
	}
	g.writeln("]]")
	g.writeln("")
}

// flushExec emits a trailing hl.on("hyprland.start", ...) block for any
// pending exec/exec-once/execr-once directives, and an
// hl.on("config.reloaded", ...) block for plain 'exec'. 'execr-once'
// entries get a per-line TODO note in the start block since their raw-spawn
// semantic (signal-mask isolation) has no documented Lua analog.
func (g *generator) flushExec() {
	if len(g.execOnce) == 0 && len(g.exec) == 0 && len(g.execrOnce) == 0 {
		return
	}
	// Visually separate the closing event blocks from whatever the last
	// emitted statement was — collapseBlanks dedupes if the previous emit
	// already ended on a blank line.
	if g.out.Len() > 0 {
		g.writeln("")
	}
	if len(g.execOnce) > 0 || len(g.execrOnce) > 0 {
		g.writeln("hl.on(\"hyprland.start\", function()")
		for _, cmd := range g.execOnce {
			g.writeln("    " + g.formatExecCall(cmd, false))
		}
		for _, cmd := range g.execrOnce {
			// 'execr-once' = "execute, raw" (no /bin/sh -c wrap), per the
			// 0.55 wiki's hl.dsp.exec_raw entry. The startup hook has no
			// top-level hl.exec_raw, so dispatch the exec_raw dispatcher
			// directly — same effect as if the user had `bind = …, execr`.
			g.writeln("    " + g.formatExecRawCall(cmd))
		}
		g.writeln("end)")
		g.writeln("")
	}
	if len(g.exec) > 0 {
		// 'exec' (without -once) runs on every config reload. The closest
		// Lua hook is config.reloaded, which fires after every reload
		// including the initial one.
		g.writeln("hl.on(\"config.reloaded\", function()")
		for _, cmd := range g.exec {
			g.writeln("    " + g.formatExecCall(cmd, false))
		}
		g.writeln("end)")
		g.writeln("")
	}
}

// formatExecCall renders one hl.exec_cmd call from a raw hyprlang exec value.
// Two transformations beyond plain quoting:
//
//  1. If the value begins with '[RULES] CMD' (hyprlang's spawn-rule prefix),
//     extract RULES into the second argument of hl.exec_cmd as the spec stub
//     declares: rules?: table<string, string|number|boolean>.
//  2. Declared `$var` references in CMD resolve to their Lua local; undeclared
//     `$X` is preserved as literal text so /bin/sh expands it at runtime —
//     the same fallback semantic hyprlang itself relies on.
//
// The `raw` parameter is kept on the signature only as future-proofing;
// callers currently route execr-once through [generator.formatExecRawCall].
func (g *generator) formatExecCall(input string, raw bool) string {
	_ = raw
	input = strings.TrimSpace(input)
	rules := ""
	cmd := input
	if strings.HasPrefix(input, "[") {
		if end := strings.IndexByte(input, ']'); end > 0 {
			rules = formatExecRules(input[1:end])
			cmd = strings.TrimSpace(input[end+1:])
		}
	}
	// Shell context: declared $vars rewrite to Lua locals; undeclared
	// $HOME / $XDG_* survive verbatim so /bin/sh expands them at runtime
	// (matching hyprlang's pass-through semantic). See [generator.fmtShell].
	cmdExpr := g.fmtShell(cmd)
	if rules == "" {
		return fmt.Sprintf("hl.exec_cmd(%s)", cmdExpr)
	}
	return fmt.Sprintf("hl.exec_cmd(%s, %s)", cmdExpr, rules)
}

// formatExecRawCall renders an `execr-once` value through the native
// hl.dsp.exec_raw dispatcher, wrapped in hl.dispatch so it can run from
// the top-level startup hook (the dispatcher namespace lives under
// hl.dsp.*; there is no top-level hl.exec_raw). [RULES] prefixes are
// flagged but not silently dropped — hyprlang's PID-tracking spawn
// rules don't apply to a no-shell raw exec the same way, and we'd
// rather surface the difference than guess.
func (g *generator) formatExecRawCall(input string) string {
	input = strings.TrimSpace(input)
	cmd := input
	hasRules := false
	if strings.HasPrefix(input, "[") {
		if end := strings.IndexByte(input, ']'); end > 0 {
			hasRules = true
			cmd = strings.TrimSpace(input[end+1:])
		}
	}
	cmdExpr := g.fmtShell(cmd)
	expr := fmt.Sprintf("hl.dispatch(hl.dsp.exec_raw(%s))", cmdExpr)
	if hasRules {
		expr += " -- TODO: legacy [RULES] prefix dropped — hl.dsp.exec_raw has no rules arg"
	}
	return expr
}

// formatExecRules turns the contents of '[...]' (semicolon-separated spawn
// rules) into a Lua table literal. Recognized rule shapes:
//
//   workspace N [silent]   -> workspace = "N silent"
//   monitor   NAME         -> monitor   = "NAME"
//   size      W H          -> size      = "W H"
//   move      X Y          -> move      = "X Y"
//   float / tile / pseudo  -> float = true / float = false / pseudo = true
//   fullscreen / maximize  -> fullscreen = true / maximize = true
//
// Anything else becomes a quoted-string entry under its first word so the
// rule isn't lost; users will see the raw form on the right-hand side and
// can adjust.
func formatExecRules(body string) string {
	rules := strings.Split(body, ";")
	parts := []string{}
	for _, r := range rules {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		fields := strings.Fields(r)
		head := strings.ToLower(fields[0])
		rest := strings.TrimSpace(strings.TrimPrefix(r, fields[0]))
		switch head {
		case "float":
			parts = append(parts, "float = true")
		case "tile":
			parts = append(parts, "float = false")
		case "pseudo":
			parts = append(parts, "pseudo = true")
		case "fullscreen":
			parts = append(parts, "fullscreen = true")
		case "maximize":
			parts = append(parts, "maximize = true")
		case "minimize":
			parts = append(parts, "minimize = true")
		case "pin":
			parts = append(parts, "pin = true")
		case "workspace", "monitor", "size", "move", "tag", "title", "class":
			parts = append(parts, fmt.Sprintf("%s = %s", head, quoteLuaString(rest)))
		default:
			// Unknown rule — keep it as a string under the first word so
			// nothing is silently dropped.
			parts = append(parts, fmt.Sprintf("%s = %s", luaTableKey(head), quoteLuaString(rest)))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// luaIdent maps a hyprlang $varname to a safe Lua local identifier.
func luaIdent(name string) string {
	s := strings.TrimPrefix(name, "$")
	// Replace anything Lua wouldn't accept.
	var b strings.Builder
	for i, r := range s {
		if i == 0 && (r >= '0' && r <= '9') {
			b.WriteByte('_')
		}
		switch {
		case r == '_', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

// luaConfigKey normalizes one hl.config route segment to the spelling the Lua
// config registry actually uses. Hyprland registers every config value's Lua
// name through CConfigManager::luaConfigValueName
// (src/config/lua/ConfigManager.cpp), which maps ':' → '.' and '-' → '_'. So
// hyprlang's hyphenated routes are only reachable from Lua under their
// underscored spelling:
//
//	input:touchpad:tap-to-click       → input.touchpad.tap_to_click
//	input-capture:capture_modifiers   → input_capture.capture_modifiers
//
// Emitting the source hyphen instead produces "unknown config key" at load
// time (hlConfig walks the table and looks each dotted path up verbatim), so
// every segment on the hl.config path goes through here.
func luaConfigKey(k string) string {
	return strings.ReplaceAll(k, "-", "_")
}

// luaTableKey returns either an unquoted identifier (preferred) or a
// ['quoted'] form for keys that aren't valid Lua identifiers.
func luaTableKey(k string) string {
	if isLuaIdent(k) {
		return k
	}
	return "[" + quoteLuaString(k) + "]"
}

func isLuaIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 && r >= '0' && r <= '9' {
			return false
		}
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	// Also reject Lua reserved words.
	switch s {
	case "and", "break", "do", "else", "elseif", "end", "false", "for",
		"function", "goto", "if", "in", "local", "nil", "not", "or",
		"repeat", "return", "then", "true", "until", "while":
		return false
	}
	return true
}

// formatValue chooses a Lua literal for a raw hyprlang value:
//
//   - Pure integer or float            → numeric literal
//   - Boolean spelling (true/false/yes/no/on/off/0/1 in context) → literal
//     (we keep true/false only — 0/1 stays numeric; yes/no/on/off map to true/false)
//   - $var (whole value)               → bare local reference
//   - Mixed $var interpolation         → quoted with .. concat
//   - rgb(...) / rgba(...) hex token   → quoted string (Hyprland accepts both
//     literal strings and the integer 0xAARRGGBB; we use strings to stay
//     faithful to the source)
//   - Anything else                    → quoted string
//
// `declared` is the set of $var names that have a corresponding VarDecl
// in this file; a nil set means "treat every $X as declared", which
// preserves the original behaviour for tests and one-shot helpers.
// References whose name is not in `declared` pass through verbatim, so
// an undeclared `$HOME` ends up as the literal text `$HOME` inside the
// emitted Lua string and the shell expands it at exec time.
func formatValue(raw string, declared map[string]bool) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "\"\""
	}
	if isLuaNumber(raw) {
		return raw
	}
	switch strings.ToLower(raw) {
	case "true", "yes", "on":
		return "true"
	case "false", "no", "off":
		return "false"
	}
	// Bare $var (no other content).
	if strings.HasPrefix(raw, "$") && !strings.ContainsAny(raw, " \t,") && isDollarRef(raw) {
		name := strings.TrimPrefix(raw, "$")
		if declared == nil || declared[name] {
			return luaIdent(raw)
		}
		// Undeclared bare ref: preserve as literal text so a downstream
		// shell expansion still sees the `$NAME` sigil.
		return quoteLuaString(raw)
	}
	// Multi-stop color gradient: two or more rgb()/rgba() tokens, optional
	// trailing '<n>deg' angle. Rendered as { colors = {...}, angle = N }.
	if g, ok := formatGradient(raw); ok {
		return g
	}
	// String with embedded $var refs → "literal " .. var .. " more".
	if strings.Contains(raw, "$") {
		if expr, ok := interpolate(raw, declared); ok {
			return expr
		}
	}
	return quoteLuaString(raw)
}

// fmtVal is the generator-aware default for value formatting in
// non-shell contexts (bind key strings, config values, dispatcher table
// fields, …). Passing nil for `declared` keeps the legacy behaviour of
// rewriting every `$X` to a Lua local, so undeclared references fast-fail
// at config load with a clear nil-concat error — pointing the user
// straight at the typo.
//
// Use [generator.fmtShell] instead for values that will end up inside a
// /bin/sh -c command line; those need undeclared `$X` preserved as
// literal text so the shell expands them at exec time.
func (g *generator) fmtVal(raw string) string {
	return formatValue(raw, nil)
}

// fmtShell formats a value destined for a /bin/sh -c command (the cmd
// argument of hl.exec_cmd or the args of an `exec`/`execr` dispatcher).
// Declared `$mainMod`-style refs still rewrite to Lua locals; undeclared
// `$HOME` / `$XDG_*` survive verbatim so the shell expands them — matching
// hyprlang's own pass-through semantic.
func (g *generator) fmtShell(raw string) string {
	return formatValue(raw, g.declaredVars)
}

// formatCssGap parses hyprlang's CSS-shorthand gap value into a typed
// HL.CssGap table literal { top, right, bottom, left }. Returns ok=true
// when the raw value is two, three, or four whitespace-separated integers
// (matching CSS box-shorthand semantics); ok=false otherwise so the caller
// falls back to the default numeric/string emitter.
//
// Why: HL.CssGap is `integer | {top?, right?, bottom?, left?}` — strings
// like "5 10 15 20" are rejected at config load with a type-mismatch
// error. Single-integer inputs are intentionally left to the default
// path: the typed API accepts a bare integer (uniform on all sides),
// and emitting `5` is shorter and clearer than `{top=5, right=5, ...}`.
//
// CSS box-shorthand mapping:
//
//	N           → integer N (not handled here)
//	A B         → top=A, right=B, bottom=A, left=B
//	A B C       → top=A, right=B, bottom=C, left=B
//	A B C D     → top=A, right=B, bottom=C, left=D
func formatCssGap(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	fields := strings.Fields(raw)
	if len(fields) < 2 || len(fields) > 4 {
		return "", false
	}
	vals := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			return "", false
		}
		vals = append(vals, n)
	}
	var top, right, bottom, left int
	switch len(vals) {
	case 2:
		top, right, bottom, left = vals[0], vals[1], vals[0], vals[1]
	case 3:
		top, right, bottom, left = vals[0], vals[1], vals[2], vals[1]
	case 4:
		top, right, bottom, left = vals[0], vals[1], vals[2], vals[3]
	}
	return fmt.Sprintf("{ top = %d, right = %d, bottom = %d, left = %d }",
		top, right, bottom, left), true
}

// isCssGapField reports whether (section, dottedKey) is one of the typed
// HL.CssGap config fields. Per hl.meta.lua's HL.ConfigValueTypes,
// `integer | HL.CssGap` lives at:
//
//	general.gaps_in / gaps_out / float_gaps
//	(monitor.reserved / reserved_area is handled separately in the monitorv2
//	 block emitter, which has its own key set)
//
// Returning false drops the value to the default formatter, which is
// always safe — it just means the typed CssGap branch isn't taken.
func isCssGapField(section, dottedKey string) bool {
	if section != "general" {
		return false
	}
	switch dottedKey {
	case "gaps_in", "gaps_out", "float_gaps":
		return true
	}
	return false
}

// fmtConfValue chooses the right Lua literal for a config-section leaf,
// routing CSS-shorthand gap values through formatCssGap before falling
// back to the generic fmtVal. Other typed shapes (Vec2Like, Gradient)
// already flow through fmtVal's existing detection.
func (g *generator) fmtConfValue(section, dottedKey, raw string) string {
	if isCssGapField(section, dottedKey) {
		if expr, ok := formatCssGap(raw); ok {
			return expr
		}
	}
	return g.fmtVal(raw)
}

// formatGradient detects a Hyprland color-gradient string like
//   "rgba(33ccffee) rgba(00ff99ee) 45deg"
// and renders it as a Lua table literal:
//   { colors = { "rgba(33ccffee)", "rgba(00ff99ee)" }, angle = 45 }
// Requires at least two rgb()/rgba() tokens. The angle is optional. Returns
// ok=false for anything that isn't a multi-stop gradient so the caller falls
// back to plain string quoting.
func formatGradient(raw string) (string, bool) {
	var colors []string
	rest := raw
	for {
		rest = strings.TrimLeft(rest, " \t")
		lower := strings.ToLower(rest)
		var prefix string
		switch {
		case strings.HasPrefix(lower, "rgba("):
			prefix = rest[:5]
		case strings.HasPrefix(lower, "rgb("):
			prefix = rest[:4]
		default:
			goto done
		}
		end := strings.IndexByte(rest, ')')
		if end < 0 {
			return "", false
		}
		colors = append(colors, prefix+rest[len(prefix):end+1])
		rest = rest[end+1:]
	}
done:
	if len(colors) < 2 {
		return "", false
	}
	rest = strings.TrimSpace(rest)
	angle := ""
	if rest != "" {
		// Optional trailing '<number>deg'.
		lower := strings.ToLower(rest)
		if !strings.HasSuffix(lower, "deg") {
			return "", false
		}
		num := strings.TrimSpace(rest[:len(rest)-3])
		if !isLuaNumber(num) {
			return "", false
		}
		angle = num
	}
	var b strings.Builder
	b.WriteString("{ colors = { ")
	for i, c := range colors {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(quoteLuaString(c))
	}
	b.WriteString(" }")
	if angle != "" {
		b.WriteString(", angle = ")
		b.WriteString(angle)
	}
	b.WriteString(" }")
	return b.String(), true
}

func isDollarRef(s string) bool {
	if !strings.HasPrefix(s, "$") || len(s) < 2 {
		return false
	}
	for i, r := range s[1:] {
		if i == 0 && r >= '0' && r <= '9' {
			return false
		}
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func isLuaNumber(s string) bool {
	if s == "" {
		return false
	}
	// Hex literal (0xDEADBEEF) — Lua accepts this directly.
	if strings.HasPrefix(strings.ToLower(s), "0x") {
		for _, r := range s[2:] {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return false
			}
		}
		return len(s) > 2
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		// Don't accept leading '+' (rare in configs, but parseFloat does).
		if strings.HasPrefix(s, "+") {
			return false
		}
		return true
	}
	return false
}

// interpolate turns "foo $x bar" into "\"foo \" .. x .. \" bar\"".
// Returns ok=false if no rewrite was performed (no $X tokens at all, or
// every $X encountered was undeclared), so the caller can fall back to
// plain quoting and the literal `$X` sigil survives in the output for
// the shell to expand at runtime.
//
// `declared` controls which `$X` tokens are real hyprlang variables:
//   - nil       → rewrite every `$X` to a Lua local (legacy behavior;
//                 used by tests and one-shot callers that haven't built
//                 a declared-set).
//   - non-nil   → rewrite only `$X` where declared[X] is true; preserve
//                 the rest as literal text inside the surrounding string.
func interpolate(raw string, declared map[string]bool) (string, bool) {
	var parts []string
	var lit strings.Builder
	flushLit := func() {
		if lit.Len() > 0 {
			parts = append(parts, quoteLuaString(lit.String()))
			lit.Reset()
		}
	}
	i := 0
	found := false
	for i < len(raw) {
		c := raw[i]
		if c == '$' && i+1 < len(raw) {
			// Hyprlang identifiers must not start with a digit, same rule as
			// Lua locals (see luaIdent / isDollarRef). Leading-digit forms
			// like $2 are shell/awk positionals — e.g. inside an exec arg
			// 'awk {print $2}' — and must pass through as literal text, not
			// be rewritten into a (digit-prefixed) Lua local reference.
			first := raw[i+1]
			if first >= '0' && first <= '9' {
				lit.WriteByte(c)
				i++
				continue
			}
			// Read identifier chars.
			j := i + 1
			for j < len(raw) {
				r := raw[j]
				if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
					j++
					continue
				}
				break
			}
			if j > i+1 {
				name := raw[i+1 : j]
				if declared != nil && !declared[name] {
					// Undeclared identifier: keep the `$NAME` sigil in the
					// emitted Lua string so a downstream shell can expand
					// it at runtime (matching hyprlang's own behaviour —
					// hyprlang only resolves declared variables and passes
					// unrecognised `$X` through to /bin/sh -c).
					for k := i; k < j; k++ {
						lit.WriteByte(raw[k])
					}
					i = j
					continue
				}
				flushLit()
				parts = append(parts, luaIdent(raw[i:j]))
				i = j
				found = true
				continue
			}
		}
		lit.WriteByte(c)
		i++
	}
	if !found {
		return "", false
	}
	flushLit()
	if len(parts) == 1 {
		return parts[0], true
	}
	return strings.Join(parts, " .. "), true
}

// quoteLuaString returns a safely-escaped Lua single-line string literal.
// Uses double quotes for consistency with the shipped example config.
func quoteLuaString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\%03d`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
