package tools

import (
	"strings"
	"testing"

	"github.com/mattoddie/octopus-mcp/internal/config"
	"github.com/mattoddie/octopus-mcp/internal/octopus/octopustest"
)

func TestRawGraphQLGating(t *testing.T) {
	readOnly := config.AllTools()
	readOnly.AllowWrites = false
	env := newEnv(t, readOnly)
	env.mock.OnGraphQL(`.`, octopustest.Data(Obj{"ok": true}))

	res := env.obj("octopus_graphql", map[string]any{"query": `query { account(accountNumber: "A-1234ABCD") { balance } }`})
	if res["ok"] != true {
		t.Errorf("query: %v", res)
	}
	for _, q := range []string{
		`mutation { updateBoostCharge(input: {deviceId: "x", action: BOOST}) { id } }`,
		"# a comment\n  mutation Boost { updateBoostCharge(input: {deviceId: \"x\", action: BOOST}) { id } }",
		`query A { x } mutation B { y }`,
	} {
		if msg := env.callErr("octopus_graphql", map[string]any{"query": q}); !strings.Contains(msg, "write access is disabled") {
			t.Errorf("%q: %s", q, msg)
		}
	}
	// The word "mutation" inside a string or comment isn't a mutation.
	env.obj("octopus_graphql", map[string]any{"query": "query { search(term: \"mutation\") { id } } # not a mutation"})
	if msg := env.callErr("octopus_graphql", map[string]any{"query": `mutation { obtainKrakenToken(input: {APIKey: "x"}) { token } }`}); !strings.Contains(msg, "tokens") {
		t.Errorf("token mutation: %s", msg)
	}

	writer := newEnv(t)
	writer.mock.OnGraphQL(`.`, octopustest.Data(Obj{"ok": true}))
	if res := writer.obj("octopus_graphql", map[string]any{"query": `mutation { updateBoostCharge(input: {deviceId: "x", action: BOOST}) { id } }`, "endpoint": "backend"}); res["ok"] != true {
		t.Errorf("mutation with writes: %v", res)
	}
	if writer.mock.LastGraphQL("updateBoostCharge").Endpoint != "backend" {
		t.Error("endpoint=backend")
	}
	if msg := writer.callErr("octopus_graphql", map[string]any{"query": `mutation { regenerateSecretKey { key } }`}); !strings.Contains(msg, "API keys") {
		t.Errorf("secret mutation: %s", msg)
	}
}

// Regression tests for ways around the read-only gate that a regular
// expression missed: GraphQL treats commas and a byte order mark as white
// space, and a document can hold several operations.
func TestRawGraphQLGateBypasses(t *testing.T) {
	readOnly := config.AllTools()
	readOnly.AllowWrites = false
	env := newEnv(t, readOnly)
	env.mock.OnGraphQL(`.`, octopustest.Data(Obj{"ok": true}))
	writer := newEnv(t)
	writer.mock.OnGraphQL(`.`, octopustest.Data(Obj{"ok": true}))

	for _, q := range []string{
		`,mutation { updateBoostCharge(input: {deviceId: "x", action: BOOST}) { id } }`,
		"\uFEFFmutation { updateBoostCharge(input: {deviceId: \"x\", action: BOOST}) { id } }",
		`query{a},mutation{b}`,
		`{a}mutation{b}`,
		"query { a }\r\nmutation { b }",
		`fragment F on Query { a } mutation { b }`,
	} {
		if msg := env.callErr("octopus_graphql", map[string]any{"query": q}); !strings.Contains(msg, "write access is disabled") {
			t.Errorf("%q: %s", q, msg)
		}
	}
	if n := len(env.mock.GraphQLRequests()); n != 0 {
		t.Errorf("refused documents reached Octopus: %d requests", n)
	}

	// Subscriptions are refused even with writes.
	for _, e := range []*testEnv{env, writer} {
		if msg := e.callErr("octopus_graphql", map[string]any{"query": `,subscription { a }`}); !strings.Contains(msg, "subscriptions") {
			t.Errorf("subscription: %s", msg)
		}
	}

	// Credential operations are refused with writes off and on, however they are spelled.
	for _, q := range []string{
		`,mutation { regenerateSecretKey { key } }`,
		`mutation { regenerateSecretKey { key } }`,
		`query { viewer { api_key } }`,
		`query { a: obtainKrakenToken(input: {}) { token } }`,
		`query { viewer { Password } }`,
	} {
		for _, e := range []*testEnv{env, writer} {
			if msg := e.callErr("octopus_graphql", map[string]any{"query": q}); !strings.Contains(msg, "API keys") {
				t.Errorf("%q: %s", q, msg)
			}
		}
	}

	// Documents that can't be lexed are refused rather than guessed at.
	for _, q := range []string{"query { a(s: \"unterminated) }", "{ a } \x00 mutation { b }", "query { a } garbage"} {
		if msg := env.callErr("octopus_graphql", map[string]any{"query": q}); !strings.Contains(msg, "could not read the GraphQL document") {
			t.Errorf("%q: %s", q, msg)
		}
	}

	// Mutation keywords inside strings, block strings and comments don't count, and fragments are fine.
	for _, q := range []string{
		`query { a(s: "} mutation { b") }`,
		"query { a(s: \"\"\"\n} mutation { b }\"\"\") }",
		"query { a } # } mutation { b }",
		`query { ...F } fragment F on Query { a }`,
	} {
		if res := env.obj("octopus_graphql", map[string]any{"query": q}); res["ok"] != true {
			t.Errorf("%q: %v", q, res)
		}
	}
}

func TestRawREST(t *testing.T) {
	env := newEnv(t)
	env.mock.On("GET", "/electricity-meter-points/1000000000001/", octopustest.JSON(Obj{"gsp": "_C", "mpan": "1000000000001", "profile_class": 1}))
	res := env.obj("octopus_rest_get", map[string]any{"path": "/v1/electricity-meter-points/1000000000001"})
	if res["gsp"] != "_C" || env.mock.Last("/electricity-meter-points/").BasicUser != octopustest.APIKey {
		t.Errorf("rest: %v", res)
	}
	for _, p := range []string{"accounts/x", "/../x", "/products/?x=1", "/graphql/"} {
		if msg := env.callErr("octopus_rest_get", map[string]any{"path": p}); !strings.Contains(msg, "path must") {
			t.Errorf("%s: %s", p, msg)
		}
	}
}

func TestRawRESTRejectsDoubleEncoding(t *testing.T) {
	env := newEnv(t)
	for _, p := range []string{"/%252e%252e/graphql/", "/products/%2525", "/%2e%2e/graphql/", "/GraphQL/", "/products/.", "/products/%0aX"} {
		msg := env.callErr("octopus_rest_get", map[string]any{"path": p})
		if !strings.Contains(msg, "path must") {
			t.Errorf("%s: %s", p, msg)
		}
	}
	if n := len(env.mock.Requests()); n != 0 {
		t.Errorf("rejected paths reached Octopus: %d requests", n)
	}
}
