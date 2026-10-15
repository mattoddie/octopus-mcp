# Changelog

All notable changes to this project are documented here. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For octopus-mcp, the "public API" is the set of tool names, their arguments, the prompts and the configuration variables. Renaming or removing any of these is a breaking change.

## [Unreleased]

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

[Unreleased]: https://github.com/mattoddie/octopus-mcp/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/mattoddie/octopus-mcp/releases/tag/v0.1.0
