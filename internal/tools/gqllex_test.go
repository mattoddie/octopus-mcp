package tools

import (
	"reflect"
	"strings"
	"testing"
)

func lexValues(t *testing.T, src string) []string {
	t.Helper()
	toks, err := gqlLex(src)
	if err != nil {
		t.Fatalf("%q: %v", src, err)
	}
	var out []string
	for _, tk := range toks {
		out = append(out, tk.Value)
	}
	return out
}

func TestGQLLexTokens(t *testing.T) {
	for src, want := range map[string][]string{
		"":                                     {},
		"\uFEFF{ a }":                          {"{", "a", "}"},
		"{a,b\tc\r\nd\re}":                     {"{", "a", "b", "c", "d", "e", "}"},
		"query Q($id: ID! = \"x\") { a }":      {"query", "Q", "(", "$", "id", ":", "ID", "!", "=", `"x"`, ")", "{", "a", "}"},
		"{ ...F ... on T { a } }":              {"{", "...", "F", "...", "on", "T", "{", "a", "}", "}"},
		"@dir [ ] | & =":                       {"@", "dir", "[", "]", "|", "&", "="},
		"# comment, mutation { x }\n{a}":       {"{", "a", "}"},
		"#no newline at the end":               {},
		"{a}#\r{b}":                            {"{", "a", "}", "{", "b", "}"},
		"0 -1 12 1.5 -0.25 1e10 2E-3 6.02e+23": {"0", "-1", "12", "1.5", "-0.25", "1e10", "2E-3", "6.02e+23"},
		`"" "a\"b" "\\" "\/\b\f\n\r\t" "\u00e9\u0041" "tab	ok" "é ☃"`: {`""`, `"a\"b"`, `"\\"`, `"\/\b\f\n\r\t"`, `"\u00e9\u0041"`, "\"tab\tok\"", `"é ☃"`},
		`"""block"""`: {`"""block"""`},
		"\"\"\"two\nlines \\\"\"\" \" \"\" \"\"\"": {"\"\"\"two\nlines \\\"\"\" \" \"\" \"\"\""},
		`""""""`:                 {`""""""`},
		`"""a"""""`:              {`"""a"""`, `""`},
		"_name __typename a1_B2": {"_name", "__typename", "a1_B2"},
	} {
		got := lexValues(t, src)
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q:\n got %q\nwant %q", src, got, want)
		}
	}
}

func TestGQLLexKinds(t *testing.T) {
	toks, err := gqlLex(`a 1 1.0 "s" """b""" {`)
	if err != nil {
		t.Fatal(err)
	}
	want := []gqlTokenKind{gqlName, gqlInt, gqlFloat, gqlString, gqlBlockString, gqlPunct}
	for i, tk := range toks {
		if tk.Kind != want[i] {
			t.Errorf("token %d %q: kind %d, want %d", i, tk.Value, tk.Kind, want[i])
		}
	}
	if toks[1].Pos != 2 {
		t.Errorf("position: %d", toks[1].Pos)
	}
}

func TestGQLLexErrors(t *testing.T) {
	for _, src := range []string{
		"\xff{a}",         // invalid UTF-8
		"{ a. }",          // lone dot
		"{ a .. }",        // two dots
		"{ ; }",           // unknown punctuator
		"{ \u00a0a }",     // non-breaking space is not white space
		"{ é }",           // names are ASCII
		"{ a\x00 }",       // control character
		"# bad \x01 here", // control character in a comment
		"00", "01", "-", "-a", "1.", "1.e5", "1e", "1e+", "1.2.3", "0x1F", "1a", "1.5e3x",
		`"unterminated`,
		"\"line\nbreak\"",
		"\"cr\rbreak\"",
		`"\x"`,        // unknown escape
		`"\u12"`,      // short unicode escape
		`"\u{1F600}"`, // braced escapes aren't accepted
		`"\`,
		"\"ctl\x01\"",
		`"""unterminated`,
		`"""a\"""`, // the escaped quotes don't close it
		"\"\"\"bell\x07\"\"\"",
	} {
		if toks, err := gqlLex(src); err == nil {
			t.Errorf("%q: lexed as %v, want an error", src, toks)
		}
	}
}

func TestGQLParseOperations(t *testing.T) {
	for src, want := range map[string][]string{
		"{ a }":       {"query"},
		"query { a }": {"query"},
		"query Q($a: [Int!] = [1, 2], $b: In = {x: {y: 1}}) @d(a: {b: 1}) { a(x: {y: [1]}) { b } }": {"query"},
		"mutation M { a }":     {"mutation"},
		"subscription { a }":   {"subscription"},
		",mutation { a }":      {"mutation"},
		"\uFEFFmutation { a }": {"mutation"},
		"query{a},mutation{b}": {"query", "mutation"},
		"query{a}mutation{b}":  {"query", "mutation"},
		"{a} {b}":              {"query", "query"},
		"fragment F on Mutation { a } query { ...F }":             {"query"},
		"query mutation { a }":                                    {"query"}, // an operation named "mutation"
		"query { a(s: \"} mutation { b\") }":                      {"query"},
		"query { a(s: \"\"\"\n} mutation { b \\\"\"\" }\"\"\") }": {"query"},
		"query { a } # } mutation { b }":                          {"query"},
		"{ mutation }":                                            {"query"}, // a field called mutation
	} {
		doc, err := gqlParse(src)
		if err != nil {
			t.Errorf("%q: %v", src, err)
			continue
		}
		if !reflect.DeepEqual(doc.Operations, want) {
			t.Errorf("%q: operations %q, want %q", src, doc.Operations, want)
		}
	}
}

func TestGQLParseErrors(t *testing.T) {
	for _, src := range []string{
		"query",                 // no selection set
		"query Q($a: Int)",      // no selection set
		"{ a ",                  // unclosed
		"{ a ) }",               // mismatched
		"query Q($a: Int { a }", // unclosed parenthesis
		"query ) { a }",         // stray closer
		"} { a }",               // stray closer at the top
		"type Query { a: Int }", // type-system definitions aren't executable
		"schema { query: Q }",
		"extend type Q { a: Int }",
		"\"description\" query { a }",
		"query { a } b",
		"fragment F on T",
	} {
		if doc, err := gqlParse(src); err == nil {
			t.Errorf("%q: parsed as %v, want an error", src, doc.Operations)
		}
	}
}

func TestGQLParseNames(t *testing.T) {
	doc, err := gqlParse(`query Q($v: ID) { alias: field(arg: $v, s: "notAName") @dir { ... on T { x } } } # comment`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(doc.Names, " "); got != "query Q v ID alias field arg v s dir on T x" {
		t.Errorf("names: %s", got)
	}
}
