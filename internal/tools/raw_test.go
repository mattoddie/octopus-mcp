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
