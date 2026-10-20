# Changelog

All notable changes to this project are documented here. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For octopus-mcp, the "public API" is the set of tool names, their arguments, the prompts and the configuration variables. Renaming or removing any of these is a breaking change.

## [Unreleased]

Fixes from a security review.

### Breaking changes

- With `MCP_TRANSPORT=http`, the server refuses to start if `MCP_AUTH_TOKEN` is shorter than 16 characters. Generate one with `openssl rand -hex 32`.
- `MCP_ALLOW_UNAUTHENTICATED=true` now needs `MCP_ALLOWED_HOSTS` too: without a token, the allow-list is the only protection against DNS rebinding.
- When `MCP_ALLOWED_HOSTS` is set, requests with an `Origin` header that isn't on the list are refused. An entry without a port matches an `Origin` only on the scheme's default port, so a browser-based client on another port needs a `host:port` entry.
- `/mcp` only accepts `POST`, with a valid JSON body of at most 4 MiB.
- `OCTOPUS_API_URL`, `OCTOPUS_GRAPHQL_URL` and `OCTOPUS_BACKEND_GRAPHQL_URL` must use `https://` (`http://` only for localhost) and can't contain a user name or password.
- `octopus_graphql` refuses any document that mentions tokens, API keys or passwords even with writes off, refuses subscriptions, and refuses documents it can't parse.

### Security

- Fix a bypass of the read-only switch in `octopus_graphql`: a mutation preceded by a comma or a byte order mark, which GraphQL ignores, was taken for a query, so `,mutation { regenerateSecretKey { key } }` ran with writes off. Documents are now read with a GraphQL lexer that follows the specification, and every operation in them must be a query unless writes are on.
- Check the `Origin` header against `MCP_ALLOWED_HOSTS`, as well as `Host`.
- Don't follow redirects from Octopus at all; same-host redirects, including https to http, were followed before.
- Don't retry a POST (GraphQL queries and mutations) when Octopus answers 503, so a mutation is never sent twice. POSTs are still retried when rate-limited.
- Redact the API key and tokens from the details in GraphQL errors (`errorDescription`, refusal reasons), and keep redacting recent tokens after they are refreshed.
- `octopus_rest_get` refuses paths that are still percent-encoded after decoding, such as `/%252e%252e/graphql/`.
- Error on Octopus responses over 64 MiB instead of reading a truncated body.
- Wait 30 seconds before sending a rejected API key to Octopus again.
- Add read and idle timeouts to the HTTP server.
- Build with Go 1.27 (`toolchain go1.27.2`), pin the Docker base images by digest, and run `govulncheck` in CI.

### Fixed

- Costs for long periods (up to the 732 days the consumption tools allow) no longer silently miss rates: rate history is fetched for the whole period, with an error asking for a shorter one if Octopus has more than expected.
- The rate in force is found even when it started many records earlier, for tariffs whose rate records overlap.
- `cost_if_run_now_gbp` in `octopus_find_cheapest_slots` prices the job over its whole duration from now, rather than at the current half-hour's rate.
- The `healthcheck` command connects to `MCP_HTTP_HOST` when it is a specific address.

## [0.1.1] - 2026-10-16

### Security

- Update `golang.org/x/sys` to v0.44.0 for GO-2026-5024. The advisory is Windows-only and not reachable from this code, so the Linux images weren't affected.
- Build with Go 1.26.9 (`toolchain go1.26.9`), which fixes standard-library advisories in `net/http`, `mime/multipart` and `crypto/tls`.

## [0.1.0] - 2026-10-15

First release, not yet tagged. It hasn't yet been tested against a live Octopus account, so expect rough edges, and please [report](https://github.com/mattoddie/octopus-mcp/issues/new/choose) anything Octopus rejects.

### Added

- MCP server for Octopus Energy (unofficial) with 22 tools in 6 toolsets: account, consumption, tariffs, intelligent, octoplus and raw.
- Account overview (properties, MPAN/MPRN, meter serials, smart meter device IDs, gas meter units, current and previous tariffs), balance, bills with temporary PDF links, and transactions.
- Smart meter consumption for electricity import and export and gas, totalled by half-hour, hour, day, week, month or overall in UK time, with cost estimates from the actual unit rate of every half-hour and the daily standing charges of the tariff in force at the time.
- Honest gas units: SMETS2 readings in m³ are recognised from the meter's details, converted to kWh with the standard formula and a configurable calorific value, and any assumption is reported.
- Live demand from an Octopus Home Mini.
- Public tariff tools: products, prices by region, unit rates (half-hourly for Agile and other time-of-use tariffs, day and night for Economy 7), standing charges, a cheapest-time finder for continuous blocks and individual half-hours, and region lookup by postcode.
- Intelligent Octopus: devices with status, battery level and schedule, and planned and completed dispatches. With writes: charging target and ready-by time, bump charge, and suspending or resuming smart control.
- Octoplus: points balance and Saving Sessions (upcoming in your region, joined status, past results). With writes: joining a session or booking a weekend happy hour.
- Raw `octopus_graphql` (Kraken or backend API; mutations only with writes, and never for tokens, API keys or passwords) and `octopus_rest_get` tools.
- Five built-in prompts: `energy_report`, `plan_cheap_usage`, `compare_tariffs`, `ev_charging_check` and `saving_session_check`.
- Authentication with the API key alone: HTTP Basic for account REST requests (public requests send no credentials), and in-memory Kraken tokens for GraphQL, renewed before expiry with the refresh token and retried once if rejected. The key and tokens are scrubbed from errors and never returned.
- Account number discovery when `OCTOPUS_ACCOUNT` isn't set and the key sees a single account.
- Read-only by default; `OCTOPUS_ALLOW_WRITES` registers the write tools and `OCTOPUS_TOOLSETS` limits the tools exposed. Tools are annotated as read-only, destructive or open-world.
- Automatic retries when Octopus rate-limits requests, and error messages with hints for common Octopus errors.
- Streamable HTTP (stateless) and stdio transports, with bearer-token auth and a `Host` header allow-list for HTTP.
- `serve`, `status`, `healthcheck` and `version` commands.
- `_FILE` variants of every variable for Docker secrets.
- Multi-arch distroless Docker image (`linux/amd64`, `linux/arm64`) published to GHCR on each release, with SBOM and provenance attestation.
- Documentation, including a tool reference generated from the code, and a website.

[Unreleased]: https://github.com/mattoddie/octopus-mcp/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/mattoddie/octopus-mcp/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/mattoddie/octopus-mcp/releases/tag/v0.1.0
