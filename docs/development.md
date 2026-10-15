# Development

This page is for people working on octopus-mcp itself. Start with [CONTRIBUTING.md](../CONTRIBUTING.md) for the workflow, and use this page for how the code fits together.

- [Setup](#setup)
- [Project layout](#project-layout)
- [How a request flows](#how-a-request-flows)
- [Adding a tool](#adding-a-tool)
- [Tool design guidelines](#tool-design-guidelines)
- [Testing](#testing)
- [Running against a real Octopus account](#running-against-a-real-octopus-account)
- [Docs that are generated](#docs-that-are-generated)
- [The website](#the-website)

## Setup

You need Go 1.25 or later, and Docker for building the image.

```sh
git clone https://github.com/mattoddie/octopus-mcp.git
cd octopus-mcp
make test           # runs all tests against a mock Octopus API
```

| Target | What it does |
|---|---|
| `make build` | Build a static binary to `bin/octopus-mcp`. Pass `VERSION=1.2.3` to stamp a version. |
| `make test` | `go test ./...` |
| `make lint` | Fail if any file needs `gofmt`, then run `go vet ./...` |
| `make docs` | Regenerate [`docs/tools.md`](tools.md) from the tool definitions |
| `make docs-check` | Fail if `docs/tools.md` is out of date (CI runs this) |
| `make image` | Build the Docker image as `octopus-mcp:$(VERSION)` |

The project deliberately has only two direct dependencies: the official MCP Go SDK, [`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk), and [`github.com/google/jsonschema-go`](https://github.com/google/jsonschema-go), which the SDK uses for input schemas. Everything else, including the GraphQL client, is the standard library. Please discuss before adding a dependency.

## Project layout

```text
cmd/
├── octopus-mcp/         # the binary: serve, status, healthcheck and version commands
└── gen-tool-docs/       # writes docs/tools.md from the registered tools and prompts
internal/
├── config/              # environment variables → typed Config (validation lives here)
├── octopus/
│   ├── client.go        # Client: REST with Basic auth, GraphQL with Kraken tokens, retries, errors, redaction
│   ├── tariff.go        # tariff codes, regions, unit rates and standing charges, UK time
│   ├── format.go        # helpers to read API objects and build compact summaries
│   └── octopustest/     # an in-process fake REST API and Kraken/backend GraphQL APIs for tests
├── httpserver/          # stateless Streamable HTTP, bearer auth, Host allow-list, /health
└── tools/
    ├── tools.go         # the registration framework: Def, Gate, add[In](), schemas
    ├── server.go        # NewServer(): server instructions, registers every toolset and the prompts
    ├── helpers.go       # time arguments, the account and its meter points, smart meter device IDs
    ├── prompts.go       # the built-in MCP prompts
    └── <toolset>.go     # one file per toolset: account, consumption, tariffs, intelligent, octoplus, raw
```

## How a request flows

1. An MCP client calls a tool, for example `octopus_get_consumption {from: "2026-10-01", group_by: "day"}`.
2. The MCP SDK validates the arguments against the tool's JSON Schema, which was inferred from its Go input struct, and decodes them into that struct.
3. The handler registered by `add()` runs. For consumption, it loads the account over REST to find the meter point and its tariff agreements, fetches the half-hourly readings, then the unit rates and standing charges for each tariff in the period, and totals everything in UK time.
4. `octopus.Client` sends REST requests with the API key as the Basic auth username (public endpoints get no credentials), and GraphQL requests with a Kraken token. `Token()` gets one with `obtainKrakenToken`, caches it until shortly before its JWT `exp`, and renews it with the refresh token, falling back to the API key. If Kraken rejects a token (`KT-CT-1124` and similar), the request is retried once with a new one. On `429` or `503` it waits and retries up to three times. Paged REST lists follow `next` links on the configured host only.
5. The handler returns a compact summary.
6. `add()` serialises the value to indented JSON text. A returned error becomes `isError: true` with the message, so the model can see what went wrong and adjust. Error text is scrubbed of the API key and tokens.

## Adding a tool

Tools are defined with the generic `add[In]()` in the file for their toolset. Here's a complete example from `account.go`:

```go
type accountNumberArgs struct {
	AccountNumber string `json:"account_number,omitempty" jsonschema:"Account number like A-1234ABCD (default OCTOPUS_ACCOUNT, or the only account the API key can see)"`
}

add(c, Def{
	Toolset: "account", Name: "octopus_get_balance", Title: "Account balance",
	Description: "Get the account balance in pounds (positive means in credit, negative means you owe Octopus), any overdue balance and the account status.",
}, func(ctx context.Context, a accountNumberArgs) (any, error) {
	number, err := cl.Account(ctx, a.AccountNumber)
	if err != nil {
		return nil, err
	}
	var res Obj
	if err := cl.GraphQL(ctx, balanceQuery, Obj{"account": number}, &res); err != nil {
		return nil, err
	}
	acct, _ := res["account"].(Obj)
	return summarizeBalance(acct), nil
})
```

### The input struct

The input struct **is** the tool's argument schema:

- The `json` tag names the argument. Use `snake_case`.
- `omitempty` makes it optional. Without it, the argument is required.
- The `jsonschema` tag is the argument's description, which the model reads. Every argument needs one, and a test checks this.
- Use pointers (`*int`, `*bool`, `*float64`) for optional values where zero means something, so "not given" and "set to zero" differ.
- Time arguments are strings parsed with `parseWhen()`/`parseRange()`, which accept dates (whole UK days), local or offset date-times, and `now`, `today`, `yesterday` and `tomorrow`.
- Embedded structs, such as `MeterArgs` or `TariffArgs`, add their fields to the tool.
- Tools without arguments take `NoArgs`.

### The `Def`

| Field | Effect |
|---|---|
| `Toolset` | Required. The tool is only registered when this toolset is enabled. |
| `Name`, `Title`, `Description` | The tool's name (`octopus_<verb>_<thing>`), a short human title, and the description the model reads. |
| `Gate` | `Read` (the default, always registered) or `Write` (needs `OCTOPUS_ALLOW_WRITES`). |
| `ReadOnly` | Overrides the `readOnlyHint` annotation, which defaults to `Gate == Read`. |
| `Destructive` | Overrides the `destructiveHint` annotation for non-read-only tools, which defaults to `false`. Set it for changes that can't be undone. |
| `Idempotent` | Repeating the call with the same arguments has no further effect. Sets `idempotentHint`. |
| `OpenWorld` | The tool affects something outside the account, such as a car starting to charge. Sets `openWorldHint`. |
| `Enums` | Restricts string arguments (or array items) to fixed values, keyed by argument name. |

### GraphQL

Write queries as constants with **variables** (`$account: String!`), never by pasting values into the query text. Name every operation (`query Bills`, `mutation SetDevicePreferences`): the mock routes on the text, and Octopus's logs are easier to read. Check every field against the live schema (`https://api.octopus.energy/v1/graphql/` for Kraken, `https://api.backend.octopus.energy/v1/graphql/` for the backend), and avoid fields marked deprecated. Use `cl.GraphQL()` for Kraken and `cl.Backend()` for the backend API.

GraphQL can return data and errors together. Both are decoded: `err` is an `octopus.GraphQLErrors` and `out` holds whatever data came back, so a tool can keep a partial result (see `octopus_get_dispatches`).

### Checklist for a new tool

1. Put it in the right `internal/tools/<toolset>.go`. If it needs a new toolset, add it to `Toolsets` in `internal/config/config.go`, describe it in `toolsetInfo` in `cmd/gen-tool-docs/main.go`, and register it in `NewServer()`.
2. Set its `Gate` correctly: `Write` for **anything** that changes state at Octopus or in the home. `OCTOPUS_ALLOW_WRITES` is the only thing standing between the model and those changes, because Octopus API keys aren't scoped.
3. Return a **compact summary** with units in the field names (`_p_per_kwh`, `_gbp`, `_kwh`). Never return the API key or tokens.
4. Write descriptions for the model: what the tool does, when to use it, which tool supplies each ID, units, side effects and Octopus's own limits.
5. Add tests in `internal/tools/<toolset>_test.go` using the mock.
6. Run `make docs` and commit the updated `docs/tools.md`. If it's a notable feature, update the README table and [CHANGELOG](../CHANGELOG.md).

## Tool design guidelines

- **Fewer, broader tools beat many narrow ones.** One consumption tool with grouping and costs is easier for a model than separate tools for each.
- **Be honest about units and estimates.** Say what was assumed (gas units, Economy 7 hours, calorific value) in the output, not just the docs.
- **Use UK time.** Customers think in local days; Octopus aggregates in local time too. Send the API UTC times with a `Z`.
- **Errors should teach.** Return messages that tell the model, or the user, how to fix the call.
- **Respect Octopus's limits.** Keep page sizes and GraphQL `first` arguments within what Octopus allows, and don't poll.

## Testing

Tests use Go's `testing` package against `octopustest.Mock` (`internal/octopus/octopustest`), an `httptest` server that imitates the REST API (under `/v1`), the Kraken GraphQL API (`/v1/graphql/`) and the backend GraphQL API (`/backend/v1/graphql/`):

- Account and meter REST paths need HTTP Basic auth with `octopustest.APIKey`. Public paths don't, and the recorded `BasicUser` lets tests check public requests don't send the key.
- `obtainKrakenToken` issues JWT-shaped tokens (with an `exp`) and refresh tokens for the right API key or refresh token, and answers `KT-CT-1135` otherwise. Other GraphQL requests need a valid token, and `ExpireTokens()` invalidates them all to test renewal.
- `On(method, pattern, handler)` adds a REST route: a regular expression matched against the whole path after `/v1`. `OnGraphQL(pattern, handler)` adds a GraphQL route matched against the query text, usually the operation name. Later routes win. `octopustest.JSON(v)`, `Data(v)` and `Errors(message, code)` are ready-made handlers.
- `Requests()`, `Last(substr)`, `GraphQLRequests()` and `LastGraphQL(substr)` show what was sent, including GraphQL variables and which endpoint was used.

`newEnv(t)` in `helpers_test.go` starts the mock, a client pointed at it with a fixed clock (13:00 UK time on 15 October 2026) and an in-memory MCP session with **every** tool enabled. `env.withAccount()` loads a sample account with import, export and gas meter points. `env.obj()` runs a tool and decodes its JSON (failing on a tool error), and `env.callErr()` returns a tool's error text. A typical test:

```go
func TestBalance(t *testing.T) {
	env := newEnv(t)
	env.mock.OnGraphQL(`query Balance`, octopustest.Data(Obj{"account": Obj{"number": "A-1234ABCD", "balance": 12345}}))
	bal := env.obj("octopus_get_balance", nil)
	if num(bal["balance_gbp"]) != 123.45 || bal["in_credit"] != true {
		t.Errorf("balance: %v", bal)
	}
}
```

To test that a tool is gated, pass different options: `newEnv(t, opts)` or `connect(t, env.client, opts)` with a `config.Tools`, and check the tool isn't listed with `toolNames()`. `TestGatesControlWhichToolsExist` already checks every tool against its `Gate`, and `TestSchemasDescribeEveryArgument` checks that every argument has a description.

Run one package or test with `go test ./internal/tools -run TestFindCheapestSlots -v`.

## Running against a real Octopus account

The public tariff tools work against the real API without any credentials beyond a placeholder key. For account tools you need your own API key. Use read-only mode unless you're testing a write tool, and remember that write tools act on real devices.

With the [MCP Inspector](https://github.com/modelcontextprotocol/inspector):

```sh
make build
export OCTOPUS_API_KEY=sk_live_...
./bin/octopus-mcp status
npx @modelcontextprotocol/inspector ./bin/octopus-mcp
```

Or build the image and point Claude Code at it:

```sh
make image
docker run --rm -p 8080:8080 --env-file .env octopus-mcp:0.0.0-dev
claude mcp add --transport http octopus-dev http://localhost:8080/mcp --header "Authorization: Bearer $MCP_AUTH_TOKEN"
```

`.env` is in `.gitignore`. Keep it that way, and never paste a real API key, account number, MPAN, serial number or real usage data into issues, tests or fixtures.

## Docs that are generated

[`docs/tools.md`](tools.md) is generated by `cmd/gen-tool-docs`. The program builds the server with every tool enabled, records each tool's toolset and gate as it's registered, reads the tools and prompts back over MCP, and writes the reference. Don't edit `docs/tools.md` by hand. Change the tool's title, description or `jsonschema` tags and run `make docs`. CI runs `make docs-check` and fails if the committed file is stale.

## The website

The site at [octopus-mcp.mattoddie.dev](https://octopus-mcp.mattoddie.dev) lives in `website/`. It has a hand-built landing page plus these docs rendered with Astro Starlight. It has its own `package.json`, so its dependencies never reach the server or the Docker image.

The Markdown in `docs/` is the single source: the site copies it in at build time. It turns each page's first `# Heading` into the page title, drops hand-written "Contents" lists, rewrites links between `.md` files to site URLs and converts GitHub alerts (`> [!NOTE]`) to Starlight asides. So write docs as normal GitHub Markdown, start each page with a `# Title`, and link between pages with relative `.md` links.

To preview the site locally:

```sh
cd website
npm ci
npm run dev
```
