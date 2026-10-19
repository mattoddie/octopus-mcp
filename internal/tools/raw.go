package tools

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/mattoddie/octopus-mcp/internal/octopus"
)

type graphqlArgs struct {
	Query     string         `json:"query" jsonschema:"A GraphQL query (or, with writes enabled, a mutation). Use variables rather than pasting values into the query"`
	Variables map[string]any `json:"variables,omitempty" jsonschema:"Values for the query's variables"`
	Endpoint  string         `json:"endpoint,omitempty" jsonschema:"kraken (default, the main Kraken API) or backend (Octopus Energy's own API, which has Saving Sessions and Octoplus extras)"`
}

type restArgs struct {
	Path  string            `json:"path" jsonschema:"Path relative to https://api.octopus.energy/v1, for example /products/ or /accounts/A-1234ABCD/"`
	Query map[string]string `json:"query,omitempty" jsonschema:"Query string parameters, for example {\"period_from\": \"2026-10-01T00:00Z\"}"`
}

// gqlSecretName matches names of fields, arguments and types that issue or
// reveal credentials, such as obtainKrakenToken, regenerateSecretKey or apiKey.
var gqlSecretName = regexp.MustCompile(`(?i)token|secretkey|apikey|password`)

// checkGraphQL decides whether octopus_graphql may send a document. It lexes the
// document (refusing anything it can't lex) and looks at every operation:
// subscriptions are never allowed, mutations only with writes, and no name in
// the document may refer to tokens, API keys or passwords, whether or not
// writes are on.
func checkGraphQL(query string, allowWrites bool) error {
	doc, err := gqlParse(query)
	if err != nil {
		return fmt.Errorf("could not read the GraphQL document (%v); only well-formed documents are sent", err)
	}
	if len(doc.Operations) == 0 {
		return errors.New("the document has no query in it")
	}
	for _, name := range doc.Names {
		if gqlSecretName.MatchString(strings.ReplaceAll(name, "_", "")) {
			return errors.New("operations that issue, read or change tokens, API keys or passwords aren't allowed through this tool")
		}
	}
	for _, op := range doc.Operations {
		switch {
		case op == "subscription":
			return errors.New("subscriptions aren't supported by this tool")
		case op != "query" && !allowWrites:
			return errors.New("write access is disabled, so only queries are allowed. Set OCTOPUS_ALLOW_WRITES=true to allow mutations")
		}
	}
	return nil
}

func registerRaw(c *Context) {
	cl := c.Client

	desc := "Run any query against Octopus's Kraken GraphQL API (or its backend API), for anything the other tools don't cover. The schema can be explored at https://api.octopus.energy/v1/graphql/. Most queries take accountNumber. "
	if c.AllowWrites {
		desc += "Mutations are allowed because writes are enabled (OCTOPUS_ALLOW_WRITES=true): confirm any change with the user first. "
	} else {
		desc += "Only queries are allowed because writes are disabled (OCTOPUS_ALLOW_WRITES=false). "
	}
	desc += "Subscriptions, and any operation that mentions tokens, API keys or passwords, are always refused."
	add(c, Def{
		Toolset: "raw", Name: "octopus_graphql", Title: "Raw GraphQL query",
		Description: desc,
		ReadOnly:    boolPtr(!c.AllowWrites), Destructive: boolPtr(c.AllowWrites), OpenWorld: c.AllowWrites,
		Enums: map[string][]string{"endpoint": {"kraken", "backend"}},
	}, func(ctx context.Context, a graphqlArgs) (any, error) {
		if strings.TrimSpace(a.Query) == "" {
			return nil, errors.New("give a query")
		}
		if err := checkGraphQL(a.Query, c.AllowWrites); err != nil {
			return nil, err
		}
		var out any
		var err error
		if a.Endpoint == "backend" {
			err = cl.Backend(ctx, a.Query, a.Variables, &out)
		} else {
			err = cl.GraphQL(ctx, a.Query, a.Variables, &out)
		}
		if err != nil {
			var gerr octopus.GraphQLErrors
			if errors.As(err, &gerr) && out != nil {
				return Obj{"data": out, "errors": err.Error()}, nil
			}
			return nil, err
		}
		return out, nil
	})

	add(c, Def{
		Toolset: "raw", Name: "octopus_rest_get", Title: "Raw REST request",
		Description: "GET any path of the Octopus REST API (https://api.octopus.energy/v1), for anything the other tools don't cover, such as /electricity-meter-points/<MPAN>/ or a consumption path with your own parameters. The REST API is read-only; the API key is sent with HTTP Basic auth.",
	}, func(ctx context.Context, a restArgs) (any, error) {
		p, err := url.PathUnescape(a.Path)
		if err != nil {
			return nil, errors.New("path is not validly percent-encoded")
		}
		p = strings.TrimPrefix(strings.TrimSpace(p), "/v1")
		if !strings.HasPrefix(p, "/") || strings.Contains(p, "..") || strings.ContainsAny(p, "?#\\") || strings.Contains(p, "//") || strings.Contains(p, "/./") || strings.HasPrefix(p, "/graphql") {
			return nil, errors.New("path must start with /, must not contain '..', './' or '//' segments, must not include a query string (use query), and can't be the GraphQL endpoint (use octopus_graphql)")
		}
		q := url.Values{}
		for k, v := range a.Query {
			q.Set(k, v)
		}
		var out any
		return out, cl.Get(ctx, p, q, &out)
	})
}
