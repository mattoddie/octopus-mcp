# Changelog

All notable changes to this project are documented here. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For octopus-mcp, the "public API" is the set of tool names, their arguments, the prompts and the configuration variables. Renaming or removing any of these is a breaking change.

## [Unreleased]

## [0.4.0] - 2026-11-15

Four new tools, and fixes from a code review. octopus-mcp now has 26 tools in 6 toolsets (22 read and 4 write) and 5 prompts.

### Breaking changes

- **Breaking:** the `raw` toolset (`octopus_graphql` and `octopus_rest_get`) is no longer enabled by default or by `OCTOPUS_TOOLSETS=all`, because it reaches any GraphQL query or REST path. Name it to keep it, for example `OCTOPUS_TOOLSETS=all,raw`.

### Added

- **`octopus_compare_tariffs`** re-prices your actual half-hourly smart meter readings on other tariffs (Agile, Go, Cosy, Tracker, Flux, fixed deals or any product or tariff code) and compares them with the tariffs you were on: unit costs at each half-hour's real rate plus standing charges. Works for electricity import, export (compares earnings) and gas. The `compare_tariffs` prompt uses it.
- **`octopus_get_solar_summary`** puts the import and export meters side by side for homes with solar panels or a battery: kWh imported and exported, import costs and export earnings at each half-hour's real rate, standing charges and the net cost, by day, week or month. Pass `generated_kwh` from your inverter to add self-consumption and self-sufficiency, which Octopus can't see.
- **`octopus_find_greener_slots`** finds the lowest-carbon times to use electricity over the next 48 hours from NESO's official regional carbon intensity forecast, alongside your tariff's prices: the greenest block of a given length, runner-ups, the greenest half-hours and the cheapest block for comparison. It uses a new upstream host, `api.carbonintensity.org.uk` (public, no credentials), set with the new `OCTOPUS_CARBON_INTENSITY_URL`.
- **`octopus_plan_schedule`** plans when to charge a home battery or car, or run a heat pump, on Cosy, Flux, Go, Agile and other time-of-use tariffs: it spreads the energy you need over the cheapest half-hours the device's power allows, across every cheap window, merges them into start and stop times, and lists the tariff's price windows and the saving against starting straight away. It plans only; it doesn't change any device.

### Fixed

- `octopus_get_consumption` with `group_by=hour` keeps the two 01:00 hours apart on the day the clocks go back, instead of merging them.
- `octopus_get_consumption` prices a tariff that comes back in a later agreement (for example Agile, then Go, then Agile again); the later period used to have no rates.
- `yesterday` and `tomorrow` are counted in UK days, so they're right just after midnight on the day the clocks change, whatever the server's time zone.
- The `average_p_per_kwh` from `octopus_get_unit_rates` is weighted by how long each rate applies in the period, instead of being a plain average of the rate records.
- `octopus_find_cheapest_slots` only uses half-hours that end by `to`.
- `octopus_set_charge_preferences` keeps each day's minimum charge (`min`) when it changes the target or ready-by time; it used to clear it. The result now shows the minimum too.

### Security

- `octopus_rest_get` refuses any path with a `graphql` segment, in any case. A leading `/v1` was removed twice, so `/v1/v1/graphql` passed the check and reached the GraphQL endpoint with a GET.
- Successful REST and GraphQL responses are scrubbed of the API key and Kraken tokens before tools see them, not just error messages.
- Audit log entries are scrubbed of the API key and tokens wherever they appear, such as a key pasted into a GraphQL query, not just under secret-looking argument names.

### Changed

- Building from source needs Go 1.26 or later.

## [0.3.2] - 2026-11-13

### Added

- Each release now has plain binaries for macOS, Linux and Windows on `amd64` and `arm64`, with a `SHA256SUMS` file and build provenance attestations, so you can run octopus-mcp without Docker. See [Running without Docker](docs/deployment.md#running-without-docker).

### Security

- Built with Go 1.27.4, which fixes an HTTP/1 server crash in the standard library's `net/http` (GO-2026-6859).

## [0.3.1] - 2026-10-22

### Added

- **Listed in the MCP Registry** as `io.github.mattoddie/octopus-mcp`. Each stable release is published to the [official MCP Registry](https://registry.modelcontextprotocol.io/v0/servers?search=io.github.mattoddie/octopus-mcp) with the Docker image (run over stdio) and the extension bundles, so clients that install from the registry can find it. See [Releasing](docs/releasing.md#the-mcp-registry).

## [0.3.0] - 2026-10-22

### Added

- **Claude Desktop extension bundles.** Each release now has a `.mcpb` bundle for macOS, Windows and Linux on `amd64` and `arm64`, which installs octopus-mcp into Claude Desktop in one click, with a settings form for the API key, account, writes, toolsets and audit log. No Docker or token needed. See [Claude Desktop](docs/clients.md#claude-desktop).
- **Audit log.** `OCTOPUS_AUDIT_LOG` (a file path or `stderr`) records every call of a write tool, and every `octopus_graphql` mutation, as a JSON line with the time, tool, arguments (secrets redacted) and outcome. The file is created with mode `0600`, and the server refuses to start if it can't write to it. See [Audit log](docs/configuration.md#audit-log).
- [Keeping the tool list small](docs/configuration.md#keeping-the-tool-list-small) in the configuration docs: how many tools, and how many kilobytes of `tools/list`, each toolset combination costs, read-only and with writes.

### Changed

- A variable set to exactly `${user_config.<key>}`, which is what Claude Desktop passes for an extension setting left empty, counts as unset, like an empty value.

### Security

- Built with Go 1.27.3, which fixes a `crypto/x509` vulnerability in the standard library (GO-2026-6809).

## [0.2.0] - 2026-10-20

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

[Unreleased]: https://github.com/mattoddie/octopus-mcp/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/mattoddie/octopus-mcp/compare/v0.3.2...v0.4.0
[0.3.2]: https://github.com/mattoddie/octopus-mcp/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/mattoddie/octopus-mcp/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/mattoddie/octopus-mcp/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/mattoddie/octopus-mcp/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/mattoddie/octopus-mcp/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/mattoddie/octopus-mcp/releases/tag/v0.1.0
