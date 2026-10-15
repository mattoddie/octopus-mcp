# Compatibility

octopus-mcp is an independent, unofficial project. It isn't made, endorsed or supported by Octopus Energy, and it uses Octopus's public customer APIs, which can change.

## The Octopus APIs

| API | Address | Used for |
|---|---|---|
| REST API v1 | `https://api.octopus.energy/v1/` | Products, tariffs, unit rates, standing charges and regions (public); your account and meter readings (with the API key). [Octopus's documentation](https://docs.octopus.energy/rest/guides/endpoints). |
| Kraken GraphQL API | `https://api.octopus.energy/v1/graphql/` | Kraken tokens, balance, bills, transactions, smart meter device details, Home Mini telemetry, Intelligent Octopus devices and dispatches, Octoplus points. [Octopus's GraphQL guide](https://docs.octopus.energy/graphql/guides/basics). |
| Octopus Energy backend GraphQL API | `https://api.backend.octopus.energy/v1/graphql/` | Saving Sessions (joining, weekend happy hours and results). |

The GraphQL operations octopus-mcp uses were checked against the live schemas (which can be explored at the addresses above) when it was written, and avoid fields Octopus has marked deprecated. Kraken announces deprecations in the schema with a removal date. If Octopus removes or renames something, the affected tool returns Octopus's error and the rest keep working.

## Accounts, meters and tariffs

| | Support |
|---|---|
| UK Octopus Energy household accounts | ✅ |
| Several accounts on one login | ✅ Set `OCTOPUS_ACCOUNT` or pass `account_number`. |
| Smart electricity meters (SMETS1 and SMETS2) | ✅ Half-hourly readings, usually a day behind. |
| Smart gas meters | ✅ SMETS2 meters report m³, converted to kWh for costs. See [Gas readings in cubic metres](usage.md#gas-readings-in-cubic-metres). |
| Export meters (solar, batteries) | ✅ With export earnings from your export tariff. |
| Traditional (non-smart) meters | ⚠️ No half-hourly consumption data: there are only the meter readings you submit, which octopus-mcp doesn't cover. Tariffs, balance and bills work. |
| Octopus Home Mini | ✅ Live demand with `octopus_get_live_usage`. Without one, live readings are empty. |
| Single-rate tariffs (Flexible, fixed, Tracker) | ✅ |
| Time-of-use tariffs (Agile, Go, Intelligent Go, Cosy, Flux) | ✅ Priced half-hour by half-hour. |
| Day/night (Economy 7) tariffs | ⚠️ Priced assuming night is 00:30 to 07:30 GMT. Your meter's hours may differ, and the API doesn't say. |
| Intelligent Octopus cars and chargers | ✅ Status, schedules, dispatches, and with writes, preferences, bump charges and smart control. |
| Other smart devices (batteries, heat pumps) | ⚠️ Listed by `octopus_list_devices`; their controls aren't covered. Use `octopus_graphql`. |
| Prepayment, business accounts and Octopus Energy's other brands | ⚠️ Untested. Business products are listed with `include_business`. |
| Octopus Energy outside the UK (for example Japan or Germany) | ❌ They run separate APIs. |

## Limits of the Octopus APIs

These come from Octopus's documentation and from using the APIs. They apply whatever octopus-mcp does:

- **Rate limits.** Octopus limits how much each account can request. GraphQL queries have a complexity limit per request and a points allowance per hour. octopus-mcp retries automatically when Octopus answers `429 Too Many Requests` or `503`, and keeps page sizes within the limits.
- **Readings arrive late.** Smart meter readings usually appear the next day, sometimes later. Recent half-hours are often missing.
- **Agile prices for tomorrow** are usually published around 4pm. Before then, there are no prices to plan with.
- **Bills and statements.** PDF links from `octopus_list_bills` are temporary and expire after a short time.
- **Completed EV dispatches** are reported about 12 hours late.
- **Gas units** aren't stated by the REST API. See above.

## Not covered

- Submitting meter readings, and changing tariffs, payments, direct debits or personal details
- Heat pump and battery controls (readable through `octopus_graphql`)
- Octoplus offers and rewards other than points and Saving Sessions
- Carbon intensity and other non-Octopus data

The `raw` tools can reach anything in the APIs in the meantime. If you'd like one of these as a proper tool, [open a feature request](https://github.com/mattoddie/octopus-mcp/issues/new?template=feature_request.yml).

## Running octopus-mcp

| Component | Requirement |
|---|---|
| Docker image | `linux/amd64` or `linux/arm64` (for example Raspberry Pi 4/5) |
| Go (to build without Docker) | 1.25 or later |
| Operating system for the binary | Linux and macOS. Windows should work, but isn't tested. |
| Network | Outbound HTTPS to `api.octopus.energy` and `api.backend.octopus.energy` |
| MCP clients | Any client supporting Streamable HTTP or stdio. See [Connecting MCP clients](clients.md). |

The server is built on the official [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) and supports tools, prompts and tool annotations.

## Help improve this page

Found an API limit, an error or a gap that isn't listed here? [Open an issue](https://github.com/mattoddie/octopus-mcp/issues/new/choose) or start a [discussion](https://github.com/mattoddie/octopus-mcp/discussions). Please remove your API key, account number, MPAN/MPRN and address first.
