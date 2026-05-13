package converter

import (
	"fmt"
	"strings"
)

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokNewline
	tokIdent  // unquoted identifier-ish chunk before '=' or before '{' or ':'
	tokValue  // raw value text after '=' up to newline (with escape handling)
	tokEquals // '='
	tokLBrace // '{'
	tokRBrace // '}'
	tokColon  // ':' — used in 'device:name {' headers
	tokComment
	tokDollarRef // '$name' as a standalone token
)

type token struct {
	kind tokenKind
	text string
	line int
	col  int
}

func (t token) String() string {
	return fmt.Sprintf("%d:%d %s %q", t.line, t.col, kindName(t.kind), t.text)
}

func kindName(k tokenKind) string {
	switch k {
	case tokEOF:
		return "EOF"
	case tokNewline:
		return "NL"
	case tokIdent:
		return "IDENT"
	case tokValue:
		return "VALUE"
	case tokEquals:
		return "="
	case tokLBrace:
		return "{"
	case tokRBrace:
		return "}"
	case tokColon:
		return ":"
	case tokComment:
		return "COMMENT"
	case tokDollarRef:
		return "DOLLAR"
	}
	return "?"
}

type lexer struct {
	src    string
	pos    int
	line   int
	col    int
	tokens []token
}

func newLexer(src string) *lexer {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	return &lexer{src: src, line: 1, col: 1}
}

func (l *lexer) peek() byte {
	if l.pos >= len(l.src) {
		return 0
	}
	return l.src[l.pos]
}

func (l *lexer) peekAt(off int) byte {
	if l.pos+off >= len(l.src) {
		return 0
	}
	return l.src[l.pos+off]
}

func (l *lexer) advance() byte {
	if l.pos >= len(l.src) {
		return 0
	}
	c := l.src[l.pos]
	l.pos++
	if c == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return c
}

func (l *lexer) emit(k tokenKind, text string, line, col int) {
	l.tokens = append(l.tokens, token{kind: k, text: text, line: line, col: col})
}

// Lex performs a full pass.
//
// Hyprlang structure (per upstream parser, simplified):
//   - lines are statements
//   - '#' starts a comment to end of line
//   - statement forms:
//       NAME = VALUE
//       NAME { ... }
//       NAME:SUBNAME { ... }   (device:foo, plugin sub-keys, etc.)
//       NAME:SUBNAME = VALUE   (e.g. general:gaps_in = 5)
//       $var = VALUE
//   - VALUE runs to end of line, with backslash-newline as continuation
//     and a leading whitespace strip.
//   - inside section bodies, the same forms apply.
//   - '}' on its own (possibly indented) closes a section.
//   - blank lines and comments preserve roughly.
func (l *lexer) Lex() ([]token, error) {
	for l.pos < len(l.src) {
		c := l.peek()
		switch {
		case c == '\n':
			startLine, startCol := l.line, l.col
			l.advance()
			l.emit(tokNewline, "\n", startLine, startCol)
		case c == ' ' || c == '\t':
			// Whitespace is usually a token separator. But when it precedes a
			// '#' / '--' on the same line, capture it as part of the comment
			// so codegen can re-emit the comment at its original indent.
			startLine, startCol := l.line, l.col
			wsStart := l.pos
			for l.pos < len(l.src) {
				cc := l.peek()
				if cc != ' ' && cc != '\t' {
					break
				}
				l.advance()
			}
			if l.pos < len(l.src) {
				cc := l.peek()
				if cc == '#' || (cc == '-' && l.peekAt(1) == '-') {
					l.captureComment(string(l.src[wsStart:l.pos]), startLine, startCol)
				}
			}
		case c == '#' || (c == '-' && l.peekAt(1) == '-'):
			// Both '#' (the hyprlang default) and '--' (Lua-style) start a
			// line comment. We capture the entire line including sigil so
			// the codegen can echo it back verbatim.
			l.lexComment()
		case c == '{':
			startLine, startCol := l.line, l.col
			l.advance()
			l.emit(tokLBrace, "{", startLine, startCol)
		case c == '}':
			startLine, startCol := l.line, l.col
			l.advance()
			l.emit(tokRBrace, "}", startLine, startCol)
		default:
			if err := l.lexStatement(); err != nil {
				return nil, err
			}
		}
	}
	l.emit(tokEOF, "", l.line, l.col)
	return l.tokens, nil
}

// lexComment consumes a comment to end of line and emits a tokComment
// whose text is the entire comment line, sigil and all. Caller has already
// peeked the sigil ('#' or '--'); we leave the split to no one — codegen
// echoes the line verbatim.
func (l *lexer) lexComment() {
	l.captureComment("", l.line, l.col)
}

// captureComment reads to end of line and emits a tokComment whose text is
// prefix + sigil + body. prefix is the leading whitespace that preceded the
// sigil on the same line; it is included so codegen can preserve the
// comment's original indent.
func (l *lexer) captureComment(prefix string, startLine, startCol int) {
	var b strings.Builder
	b.WriteString(prefix)
	for l.pos < len(l.src) && l.peek() != '\n' {
		b.WriteByte(l.advance())
	}
	l.emit(tokComment, b.String(), startLine, startCol)
}

// lexStatement consumes one statement: either NAME (possibly with :sub) followed by
// '=' VALUE or '{', or a bare '$var = value' assignment.
func (l *lexer) lexStatement() error {
	startLine, startCol := l.line, l.col

	// Identifier head: read until we see '=', '{', ':' (sub-key), '#' (comment), or newline.
	var head strings.Builder
	for l.pos < len(l.src) {
		c := l.peek()
		if c == '=' || c == '{' || c == ':' || c == '\n' || c == '#' {
			break
		}
		head.WriteByte(l.advance())
	}
	name := strings.TrimSpace(head.String())
	if name == "" {
		// Spurious char or empty; consume to newline to recover.
		for l.pos < len(l.src) && l.peek() != '\n' {
			l.advance()
		}
		return nil
	}

	if strings.HasPrefix(name, "$") {
		l.emit(tokDollarRef, name, startLine, startCol)
	} else {
		l.emit(tokIdent, name, startLine, startCol)
	}

	for l.pos < len(l.src) {
		switch l.peek() {
		case ':':
			colLine, colCol := l.line, l.col
			l.advance()
			l.emit(tokColon, ":", colLine, colCol)
			// Read the sub-identifier.
			var sub strings.Builder
			subLine, subCol := l.line, l.col
			for l.pos < len(l.src) {
				c := l.peek()
				if c == '=' || c == '{' || c == ':' || c == '\n' || c == '#' || c == ' ' || c == '\t' {
					break
				}
				sub.WriteByte(l.advance())
			}
			subName := strings.TrimSpace(sub.String())
			if subName != "" {
				l.emit(tokIdent, subName, subLine, subCol)
			}
			// Skip trailing spaces/tabs.
			for l.pos < len(l.src) && (l.peek() == ' ' || l.peek() == '\t') {
				l.advance()
			}
			continue
		case '=':
			eqLine, eqCol := l.line, l.col
			l.advance()
			l.emit(tokEquals, "=", eqLine, eqCol)
			l.lexValue()
			return nil
		case '{':
			bLine, bCol := l.line, l.col
			l.advance()
			l.emit(tokLBrace, "{", bLine, bCol)
			return nil
		case '#':
			l.lexComment()
			return nil
		case '\n':
			return nil
		case ' ', '\t':
			l.advance()
		default:
			// Unexpected — fall through and treat as continuation of header.
			// Should already have been consumed above; just advance one to avoid spin.
			l.advance()
		}
	}
	return nil
}

// lexValue reads the value after '=' up to a newline that isn't a continuation,
// strips one leading space, and tracks the raw text exactly as the user wrote it
// (including embedded commas, $vars, and escaped commas).
func (l *lexer) lexValue() {
	// Skip a single leading space if present (idiomatic 'key = value').
	for l.pos < len(l.src) && (l.peek() == ' ' || l.peek() == '\t') {
		l.advance()
	}
	startLine, startCol := l.line, l.col
	var b strings.Builder
	for l.pos < len(l.src) {
		c := l.peek()
		if c == '\\' && l.peekAt(1) == '\n' {
			// Line continuation. Consume both and continue.
			l.advance()
			l.advance()
			continue
		}
		if c == '\n' {
			break
		}
		if c == '#' {
			// Comment to end-of-line — stop value here, but don't consume the '#'
			// so the comment lexer can pick it up after we emit.
			break
		}
		b.WriteByte(l.advance())
	}
	text := strings.TrimRight(b.String(), " \t")
	l.emit(tokValue, text, startLine, startCol)
}
