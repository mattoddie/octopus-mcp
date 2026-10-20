package tools

// A small GraphQL lexer, following the "Lexical Tokens" section of the GraphQL
// specification (https://spec.graphql.org/October2021/#sec-Language.Source-Text),
// used by octopus_graphql to work out which operations a document contains.
//
// It is deliberately strict: anything the specification doesn't allow, or that
// GraphQL servers have disagreed about over the years (control characters,
// unknown escapes, numbers followed by names), is an error, and the raw tool
// refuses documents it can't lex. That way the lexer can't be tricked into
// seeing a string or comment where the server sees an operation.

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type gqlTokenKind int

const (
	gqlPunct gqlTokenKind = iota
	gqlName
	gqlInt
	gqlFloat
	gqlString
	gqlBlockString
)

// gqlToken is one lexical token. Value is the token's source text (for strings,
// including the quotes and with escapes left as written).
type gqlToken struct {
	Kind  gqlTokenKind
	Value string
	Pos   int // byte offset in the source
}

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func isNameContinue(c byte) bool { return isNameStart(c) || isDigit(c) }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// gqlLex splits a GraphQL document into tokens, dropping the ignored tokens:
// the byte order mark, white space (tab and space), line terminators, commas
// and comments.
func gqlLex(src string) ([]gqlToken, error) {
	if !utf8.ValidString(src) {
		return nil, fmt.Errorf("the document is not valid UTF-8")
	}
	var toks []gqlToken
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',':
			i++
		case strings.HasPrefix(src[i:], "\uFEFF"):
			i += len("\uFEFF")
		case c == '#':
			// A comment runs to the end of the line.
			for i < len(src) && src[i] != '\n' && src[i] != '\r' {
				if err := checkSourceChar(src, i); err != nil {
					return nil, err
				}
				i++
			}
		case strings.IndexByte("!$&()[]{}:=@|", c) >= 0:
			toks = append(toks, gqlToken{gqlPunct, src[i : i+1], i})
			i++
		case c == '.':
			if !strings.HasPrefix(src[i:], "...") {
				return nil, fmt.Errorf("unexpected '.' at offset %d (only '...' is allowed)", i)
			}
			toks = append(toks, gqlToken{gqlPunct, "...", i})
			i += 3
		case isNameStart(c):
			j := i + 1
			for j < len(src) && isNameContinue(src[j]) {
				j++
			}
			toks = append(toks, gqlToken{gqlName, src[i:j], i})
			i = j
		case c == '-' || isDigit(c):
			tok, err := lexNumber(src, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, tok)
			i += len(tok.Value)
		case strings.HasPrefix(src[i:], `"""`):
			tok, err := lexBlockString(src, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, tok)
			i += len(tok.Value)
		case c == '"':
			tok, err := lexString(src, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, tok)
			i += len(tok.Value)
		default:
			r, _ := utf8.DecodeRuneInString(src[i:])
			return nil, fmt.Errorf("unexpected character %q at offset %d", r, i)
		}
	}
	return toks, nil
}

// checkSourceChar rejects control characters other than tab, which GraphQL
// source text can't contain (line terminators are handled by the callers).
func checkSourceChar(src string, i int) error {
	if c := src[i]; c < 0x20 && c != '\t' || c == 0x7f {
		return fmt.Errorf("control character %#x at offset %d", c, i)
	}
	return nil
}

// lexNumber reads an IntValue or FloatValue starting at i.
func lexNumber(src string, start int) (gqlToken, error) {
	i := start
	bad := func(what string) (gqlToken, error) {
		return gqlToken{}, fmt.Errorf("invalid number at offset %d: %s", start, what)
	}
	if src[i] == '-' {
		i++
	}
	digits := func() int {
		j := i
		for i < len(src) && isDigit(src[i]) {
			i++
		}
		return i - j
	}
	if i < len(src) && src[i] == '0' {
		i++
		if i < len(src) && isDigit(src[i]) {
			return bad("unexpected digit after 0")
		}
	} else if digits() == 0 {
		return bad("expected a digit")
	}
	kind := gqlInt
	if i < len(src) && src[i] == '.' {
		i++
		if digits() == 0 {
			return bad("expected a digit after '.'")
		}
		kind = gqlFloat
	}
	if i < len(src) && (src[i] == 'e' || src[i] == 'E') {
		i++
		if i < len(src) && (src[i] == '+' || src[i] == '-') {
			i++
		}
		if digits() == 0 {
			return bad("expected a digit in the exponent")
		}
		kind = gqlFloat
	}
	// A number can't be followed directly by '.' or a name (as in 1.2.3 or 0x1F).
	if i < len(src) && (src[i] == '.' || isNameStart(src[i])) {
		return bad(fmt.Sprintf("unexpected %q", src[i]))
	}
	return gqlToken{kind, src[start:i], start}, nil
}

// lexString reads a "quoted string" starting at i.
func lexString(src string, start int) (gqlToken, error) {
	for i := start + 1; i < len(src); {
		switch c := src[i]; {
		case c == '"':
			return gqlToken{gqlString, src[start : i+1], start}, nil
		case c == '\n' || c == '\r':
			return gqlToken{}, fmt.Errorf("unterminated string at offset %d", start)
		case c == '\\':
			if i+1 >= len(src) {
				return gqlToken{}, fmt.Errorf("unterminated string at offset %d", start)
			}
			switch e := src[i+1]; e {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				i += 2
			case 'u':
				if i+6 > len(src) || !isHex(src[i+2]) || !isHex(src[i+3]) || !isHex(src[i+4]) || !isHex(src[i+5]) {
					return gqlToken{}, fmt.Errorf("invalid \\u escape at offset %d (use \\uXXXX)", i)
				}
				i += 6
			default:
				return gqlToken{}, fmt.Errorf("invalid escape \\%c at offset %d", e, i)
			}
		default:
			if err := checkSourceChar(src, i); err != nil {
				return gqlToken{}, err
			}
			i++
		}
	}
	return gqlToken{}, fmt.Errorf("unterminated string at offset %d", start)
}

// lexBlockString reads a """block string""" starting at i. Its only escape is \""".
func lexBlockString(src string, start int) (gqlToken, error) {
	for i := start + 3; i < len(src); {
		switch {
		case strings.HasPrefix(src[i:], `\"""`):
			i += 4
		case strings.HasPrefix(src[i:], `"""`):
			return gqlToken{gqlBlockString, src[start : i+3], start}, nil
		case src[i] == '\n' || src[i] == '\r':
			i++
		default:
			if err := checkSourceChar(src, i); err != nil {
				return gqlToken{}, err
			}
			i++
		}
	}
	return gqlToken{}, fmt.Errorf("unterminated block string at offset %d", start)
}

// gqlDocument is what octopus_graphql needs to know about a document.
type gqlDocument struct {
	// Operations lists the type of each operation: "query", "mutation" or "subscription".
	Operations []string
	// Names lists every Name token: fields, aliases, arguments, types, variables and so on.
	Names []string
}

var closers = map[string]string{"(": ")", "[": "]", "{": "}"}

// skipBalanced returns the index just past the bracket that closes toks[i].
func skipBalanced(toks []gqlToken, i int) (int, error) {
	var stack []string
	for ; i < len(toks); i++ {
		t := toks[i]
		if t.Kind != gqlPunct {
			continue
		}
		switch t.Value {
		case "(", "[", "{":
			stack = append(stack, closers[t.Value])
		case ")", "]", "}":
			if len(stack) == 0 || stack[len(stack)-1] != t.Value {
				return 0, fmt.Errorf("unbalanced %q at offset %d", t.Value, t.Pos)
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return i + 1, nil
			}
		}
	}
	return 0, fmt.Errorf("unclosed bracket at the end of the document")
}

// gqlParse lexes a document and finds its top-level definitions. A definition
// is a selection set on its own (shorthand for a query), an operation starting
// with query, mutation or subscription, or a fragment. Anything else at the top
// level, such as a type-system definition, is an error.
func gqlParse(src string) (*gqlDocument, error) {
	toks, err := gqlLex(src)
	if err != nil {
		return nil, err
	}
	doc := &gqlDocument{}
	for _, t := range toks {
		if t.Kind == gqlName {
			doc.Names = append(doc.Names, t.Value)
		}
	}
	for i := 0; i < len(toks); {
		t := toks[i]
		switch {
		case t.Kind == gqlPunct && t.Value == "{":
			doc.Operations = append(doc.Operations, "query")
		case t.Kind == gqlName && (t.Value == "query" || t.Value == "mutation" || t.Value == "subscription"):
			doc.Operations = append(doc.Operations, t.Value)
			i++
		case t.Kind == gqlName && t.Value == "fragment":
			i++
		default:
			return nil, fmt.Errorf("unexpected %q at offset %d: expected query, mutation, subscription, fragment or {", t.Value, t.Pos)
		}
		// Skip the name, variable definitions and directives to the selection set.
		for {
			if i >= len(toks) {
				return nil, fmt.Errorf("definition at offset %d has no selection set", t.Pos)
			}
			tk := toks[i]
			if tk.Kind == gqlPunct && tk.Value == "{" {
				break
			}
			if tk.Kind == gqlPunct && (tk.Value == "(" || tk.Value == "[") {
				if i, err = skipBalanced(toks, i); err != nil {
					return nil, err
				}
				continue
			}
			if tk.Kind == gqlPunct && (tk.Value == ")" || tk.Value == "]" || tk.Value == "}") {
				return nil, fmt.Errorf("unbalanced %q at offset %d", tk.Value, tk.Pos)
			}
			i++
		}
		if i, err = skipBalanced(toks, i); err != nil {
			return nil, err
		}
	}
	return doc, nil
}
