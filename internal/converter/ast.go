package converter

// node is implemented by every AST node. Each node carries its 1-based source line
// so codegen can preserve rough source ordering and report TODOs with location.
type node interface {
	srcLine() int
}

type baseNode struct{ line int }

func (b baseNode) srcLine() int { return b.line }

// File is the root: a flat slice of top-level nodes (sections, assignments, directives,
// comments, blank lines, variables, etc.) in source order.
type File struct {
	Nodes []node
}

// Comment — preserved as a passthrough in output. Text is the entire
// comment line including its sigil ("#..." or "--...") as it appeared in
// the source, so the generator can echo it back verbatim. Tool-generated
// comments synthesised by the parser or codegen embed the sigil they want
// in Text directly (always "--" since those appear in Lua-syntax positions).
type Comment struct {
	baseNode
	Text string
}

// Blank — preserved as a blank line in output for readability.
type Blank struct{ baseNode }

// VarDecl — '$name = value'.
// Trailing is a same-line comment (e.g. '$mod = SUPER # main modifier'); it
// is "" when no comment followed the value on the source line.
type VarDecl struct {
	baseNode
	Name     string
	Value    string
	Trailing string
}

// Section — 'name { ... }' or 'name:sub { ... }' (device:foo, plugin:bar).
// Body is a flat slice of child nodes. Nested sections are nested Section nodes.
// EndLine is the 1-based source line of the closing '}'; equal to baseNode.line
// for a same-line empty section.
type Section struct {
	baseNode
	Name    string // e.g. "general", "decoration", "device", "plugin"
	Sub     string // e.g. for "device:epic-mouse-v1", Sub = "epic-mouse-v1"; "" otherwise
	Body    []node
	EndLine int
}

// KeyValue — generic 'key = value' or 'key:sub = value' (general:gaps_in = 5).
// Sub is empty for plain 'key = value'. Trailing carries a same-line comment.
type KeyValue struct {
	baseNode
	Key      string
	Sub      string
	Value    string
	Trailing string
}

// Directive — a top-level directive whose semantics need codegen-aware handling.
// Used for 'bind*', 'exec[-once]', 'monitor', 'windowrule[v2]', 'workspace',
// 'layerrule', 'env', 'envd', 'source', 'submap'.
//
// Bare Value preserves the raw RHS exactly as-written so the codegen can do
// directive-specific parsing (split commas, honor backslash-escaped commas).
type Directive struct {
	baseNode
	Name     string
	Value    string
	Trailing string
}
