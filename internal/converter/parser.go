package converter

import (
	"fmt"
	"strings"
)

// directiveSet is the closed set of names that don't follow the generic
// 'key=value goes into hl.config' rule. Sections override this — e.g. inside
// 'general { }', a 'bind' line would be unusual but we still treat it as a
// directive at the section level. In practice all of these only appear at
// top level in real configs.
var directiveSet = map[string]bool{
	"bind":         true,
	"bindm":        true,
	"binde":        true,
	"bindr":        true,
	"bindl":        true,
	"bindn":        true,
	"bindo":        true,
	"bindt":        true,
	"bindi":        true,
	"bindp":        true,
	"bindc":        true,
	"bindd":        true,
	"exec":         true,
	"exec-once":    true,
	"execr-once":   true,
	"exec-shutdown": true,
	"monitor":      true,
	"workspace":    true,
	"windowrule":   true,
	"windowrulev2": true,
	"layerrule":    true,
	"env":          true,
	"envd":         true,
	"source":       true,
	"submap":       true,
	"unbind":       true,
	"animation":    true,
	"bezier":       true,
	"gesture":      true,
	"permission":   true,
}

type parser struct {
	toks []token
	pos  int
}

func parse(toks []token) (*File, error) {
	p := &parser{toks: toks}
	body, _, err := p.parseBlock(false)
	if err != nil {
		return nil, err
	}
	return &File{Nodes: body}, nil
}

func (p *parser) peek() token  { return p.toks[p.pos] }
func (p *parser) at(k tokenKind) bool { return p.peek().kind == k }

func (p *parser) consume() token {
	t := p.toks[p.pos]
	if t.kind != tokEOF {
		p.pos++
	}
	return t
}

// trailingComment returns and consumes a comment token that sits on the same
// source line as `line` (i.e. immediately followed the statement we just
// parsed). Empty string when there is no trailing comment.
func (p *parser) trailingComment(line int) string {
	if p.at(tokComment) && p.peek().line == line {
		return p.consume().text
	}
	return ""
}

// parseBlock parses statements until either EOF (top-level) or a matching '}'
// (when inSection is true). The returned endLine is the source line of the
// terminating token — the closing '}' inside a section, or the EOF line at
// top level.
func (p *parser) parseBlock(inSection bool) ([]node, int, error) {
	var out []node
	consecutiveBlanks := 0
	for {
		t := p.peek()
		switch t.kind {
		case tokEOF:
			if inSection {
				return nil, 0, fmt.Errorf("line %d: unexpected EOF inside section", t.line)
			}
			return out, t.line, nil
		case tokRBrace:
			if !inSection {
				// Stray '}' at top level — skip with a TODO. Don't error out so we
				// stay tolerant on weird inputs.
				p.consume()
				continue
			}
			p.consume()
			return out, t.line, nil
		case tokNewline:
			p.consume()
			consecutiveBlanks++
			// One tokNewline terminates the previous content line and is
			// not a separator. Two consecutive newlines mean the source had
			// an actual blank line — emit a single Blank node for it. Three
			// or more collapse to that same one Blank, so output never has
			// more than one consecutive blank line between statements.
			if consecutiveBlanks == 2 && len(out) > 0 {
				out = append(out, Blank{baseNode{line: t.line}})
			}
			continue
		case tokComment:
			p.consume()
			out = append(out, Comment{baseNode{line: t.line}, t.text})
			consecutiveBlanks = 0
			// Leave the comment's line-terminating newline for the loop to
			// observe. The blank-line counter treats it as the first of two
			// newlines, so 'comment + blank line + content' correctly yields
			// a Blank node, matching how every other statement is parsed.
			continue
		case tokDollarRef:
			n, err := p.parseVarDecl()
			if err != nil {
				return nil, 0, err
			}
			out = append(out, n)
			consecutiveBlanks = 0
			continue
		case tokIdent:
			n, err := p.parseIdentStatement()
			if err != nil {
				return nil, 0, err
			}
			out = append(out, n)
			consecutiveBlanks = 0
			continue
		default:
			// Unknown leading token — consume to recover.
			p.consume()
		}
	}
}

func (p *parser) parseVarDecl() (node, error) {
	tok := p.consume() // $name
	name := tok.text
	if !p.at(tokEquals) {
		return Comment{baseNode{line: tok.line}, "-- TODO: manual review — malformed variable: " + name}, nil
	}
	p.consume() // '='
	if !p.at(tokValue) {
		return VarDecl{baseNode{line: tok.line}, name, "", p.trailingComment(tok.line)}, nil
	}
	val := p.consume().text
	return VarDecl{baseNode{line: tok.line}, name, val, p.trailingComment(tok.line)}, nil
}

// parseIdentStatement handles all forms beginning with an identifier:
//   IDENT '=' VALUE
//   IDENT ':' IDENT '=' VALUE
//   IDENT '{' ... '}'
//   IDENT ':' IDENT '{' ... '}'
func (p *parser) parseIdentStatement() (node, error) {
	head := p.consume() // primary ident
	name := head.text

	var sub string
	if p.at(tokColon) {
		p.consume() // ':'
		if p.at(tokIdent) {
			sub = p.consume().text
		}
	}

	switch p.peek().kind {
	case tokEquals:
		p.consume() // '='
		var val string
		if p.at(tokValue) {
			val = p.consume().text
		}
		trailing := p.trailingComment(head.line)
		// 'submap' is a directive even though syntactically it's a key=value.
		// Also: any 'bind*' name is a directive (combined-flag forms like
		// 'bindel', 'bindle' aren't in the static set).
		if sub == "" && (directiveSet[name] || strings.HasPrefix(name, "bind")) {
			return Directive{baseNode{line: head.line}, name, val, trailing}, nil
		}
		return KeyValue{baseNode{line: head.line}, name, sub, val, trailing}, nil
	case tokLBrace:
		p.consume() // '{'
		body, endLine, err := p.parseBlock(true)
		if err != nil {
			return nil, err
		}
		return Section{baseNode{line: head.line}, name, sub, body, endLine}, nil
	default:
		// Bare identifier on a line — treat as a no-op TODO.
		return Comment{baseNode{line: head.line}, "-- TODO: manual review — unexpected token after '" + name + "'"}, nil
	}
}
