# Troubleshooting

Start with the logs: `docker compose logs octopus-mcp`. Configuration errors stop the server at startup with a clear message. Octopus errors show up in the tool results the assistant sees, and you can usually ask it *"what error did you get?"*. To check the API key, run `docker compose exec octopus-mcp /octopus-mcp status`.

- [The container keeps restarting](#the-container-keeps-restarting)
- [The client can't connect to the server](#the-client-cant-connect-to-the-server)
- [API key and account errors](#api-key-and-account-errors)
- [No consumption data](#no-consumption-data)
- [Costs look wrong](#costs-look-wrong)
- [Prices and tariffs](#prices-and-tariffs)
- [Intelligent Octopus](#intelligent-octopus)
- [Saving Sessions](#saving-sessions)
- [Tools are missing](#tools-are-missing)
- [Still stuck?](#still-stuck)

## The container keeps restarting

Run `docker compose logs octopus-mcp`. The last line names the problem, for example:

| Message | Fix |
|---|---|
| `OCTOPUS_API_KEY is required: …` | Set `OCTOPUS_API_KEY` in `.env`. See [Getting started](getting-started.md#1-get-your-api-key). |
| `OCTOPUS_ACCOUNT must be an account number like A-1234ABCD, got "…"` | Use the account number from your Octopus dashboard, including the `A-`. |
| `OCTOPUS_ALLOW_WRITES must be true or false, got "…"` | Fix the value. Booleans accept `true`/`false`, `1`/`0`, `yes`/`no` and `on`/`off`. |
| `unknown toolset "x" in OCTOPUS_TOOLSETS; valid: …` | Fix the typo. The message lists the valid names. |
| `MCP_TRANSPORT must be stdio or http, got "…"` | Fix the value. |
| `OCTOPUS_API_URL must use https:// (or http://), got …` | Include the scheme, or remove the variable to use the default. |
| `MCP_HTTP_PORT must be a positive integer, got "…"` | Fix the value. The same applies to `OCTOPUS_TIMEOUT_MS`. |
| `could not read OCTOPUS_API_KEY_FILE (…)` | The secret file isn't mounted where the variable points. |

## The client can't connect to the server

1. **Is it running?** `curl http://<docker-host>:8080/health` should return `{"status":"ok"}`.
2. **Right host?** The port is published on the machine running Docker. If Docker runs on another machine (for example with `DOCKER_HOST`), `localhost` won't work.
3. **Right path?** The endpoint is `/mcp`, so the URL is `http://<docker-host>:8080/mcp`.
4. **`401 Unauthorized`:** the client isn't sending `Authorization: Bearer <MCP_AUTH_TOKEN>`, or the token doesn't match. Watch for stray quotes or spaces in `.env`.
5. **`403 Host … is not allowed`:** add the hostname the client uses to `MCP_ALLOWED_HOSTS`, or unset it.
6. **The client expects SSE:** the server is stateless and answers `POST` requests with JSON. Make sure the client is set to *Streamable HTTP* (often called `http`), not `sse`.
7. **stdio clients show nothing:** check that the command has `-i` and not `-t`, and that `MCP_TRANSPORT=stdio` is set when using the image, which defaults to HTTP.

## API key and account errors

**`could not get a Kraken token with OCTOPUS_API_KEY (…): … Invalid data. (KT-CT-1135)`**

Octopus didn't accept the API key. Copy it again from [Personal details → API access](https://octopus.energy/dashboard/new/accounts/personal-details/api-access). If you've regenerated the key, the old one stopped working. Watch for spaces or quotes around it in `.env`.

**`… failed (HTTP 401): Authentication credentials were not provided. Octopus rejected the API key. …`**

The same problem on the REST API: the key is missing or wrong.

**`… Account user API access is not enabled (KT-CT-11334)`**

Octopus has turned off API access for your login. Contact Octopus.

**`this API key can see several accounts (A-…, A-…); pass account_number or set OCTOPUS_ACCOUNT`**

Your login has more than one account. Set `OCTOPUS_ACCOUNT` to the one to use by default, or ask the assistant to use a particular one.

**`… /accounts/A-…/ failed (HTTP 404) …` or `(HTTP 403)`**

The account number isn't one the API key can see. Check `OCTOPUS_ACCOUNT` against `octopus-mcp status`.

**`… (KT-CT-1199)` or `HTTP 429`**

You've hit Octopus's rate limit. octopus-mcp retries a few times; if it still fails, wait a while. Asking for very long periods of half-hourly data, or many tariffs at once, uses up the allowance fastest.

## No consumption data

**`No readings for this period.`**

- **Readings arrive late.** Smart meters usually send yesterday's half-hours overnight. Try a period ending yesterday.
- **The meter isn't smart**, or isn't sending half-hourly readings to Octopus. Check the Octopus app: if it shows no half-hourly usage, octopus-mcp can't either.
- **A new meter.** After a meter exchange, readings are under the new serial number. octopus-mcp tries each serial listed on the meter point and uses the one with readings, or you can pass `serial`.
- **Wrong meter point.** If you have export or more than one property, pass `direction`, `fuel` or `meter_point`.

**`… -meter-points/…/consumption/ failed (HTTP 404) …`**

Octopus doesn't recognise the MPAN/MPRN and serial combination. Check them with `octopus_get_account`.

**`No live readings. Live data needs an Octopus Home Mini …`**

`octopus_get_live_usage` needs a Home Mini paired with your smart meter and online. Check it in the Octopus app.

## Costs look wrong

- **Gas is about 11 times too high or too low.** octopus-mcp assumed the wrong units for your gas meter. SMETS2 meters report m³, and SMETS1 meters kWh. Pass `gas_units: "kwh"` or `"m3"`, and see [Gas readings in cubic metres](usage.md#gas-readings-in-cubic-metres).
- **A little different from your bill.** Estimates don't include discounts, credits, Octoplus rewards or Intelligent Octopus Go's extra cheap slots, use a standard gas calorific value (39.5 MJ/m³ unless you pass `calorific_value`), and skip missing half-hours. Check `half_hours_with_data` against `half_hours_in_period`.
- **Economy 7 is off.** Octopus doesn't publish which hours your meter treats as night, so octopus-mcp assumes 00:30 to 07:30 GMT.
- **`half_hours_without_a_rate`.** No published rate covered those half-hours, usually because they fall outside your tariff's agreement dates. They're left out of the cost.

## Prices and tariffs

**`No rates were published for this period.`**

Agile prices for tomorrow usually appear around 4pm. Otherwise, check the tariff code and region: a product that's no longer sold may have stopped publishing new rates.

**`… /products/…/ failed (HTTP 404) …`**

The product or tariff code doesn't exist. Find current codes with `octopus_list_products`, and the tariff codes for each region with `octopus_get_product`.

**`"…" is not a tariff code; they look like E-1R-AGILE-24-10-01-C …`**

A tariff code has the fuel (`E` or `G`), the registers (`1R`, or `2R` for day/night), the product code and the region letter. Pass `product_code` with `region` instead if you don't have the full code.

## Intelligent Octopus

**`no car or charger is connected to Intelligent Octopus on this account`**

The car or charger hasn't been set up in the Octopus app yet, or it's been retired.

**`… Unable to trigger boost charge. (KT-CT-4357) Reasons: [BC_DEVICE_DISCONNECTED]`**

Octopus refused the bump charge and said why: the car is unplugged (`BC_DEVICE_DISCONNECTED`), away from home (`BC_DEVICE_NOT_AT_HOME`), full (`BC_DEVICE_FULLY_CHARGED`), already boosting (`BC_BOOST_CHARGE_IN_PROGRESS`), suspended (`BC_DEVICE_SUSPENDED`) or not set up yet.

**`… An error occurred while trying to set your device preferences. (KT-CT-4374)`**

Octopus didn't accept the target or ready-by time. Use the values the Octopus app offers (ready-by between 04:00 and 11:00, on the hour or half-hour).

**`Unable to fetch planned dispatches. (KT-CT-4340)`** in `errors`

Octopus couldn't produce a charging plan, often because the car isn't plugged in or isn't on an Intelligent tariff. Completed dispatches are still shown.

## Saving Sessions

**`This account hasn't signed up to Saving Sessions.`**

Sign up through Octoplus in the Octopus app first. octopus-mcp can join sessions, but not the campaign itself.

**`no current session has the code "…"`**

The session has finished, or the code is wrong. Use a code from `octopus_get_saving_sessions`.

## Tools are missing

Tools are only registered when they're allowed:

- Write tools need `OCTOPUS_ALLOW_WRITES=true`.
- Each tool's toolset must be listed in `OCTOPUS_TOOLSETS`.
- Restart the container after changing `.env` with `docker compose up -d`, and reconnect or restart your MCP client so it fetches the new tool list.
- Check the first log line: it shows whether writes are enabled and how many toolsets are loaded.

## Still stuck?

Search the [existing issues](https://github.com/mattoddie/octopus-mcp/issues) or [open a bug report](https://github.com/mattoddie/octopus-mcp/issues/new?template=bug_report.yml). Include the octopus-mcp version (the first log line), the transport, the tool involved and the exact error. **Remove your API key, account number, MPAN/MPRN, meter serial numbers, device IDs and address first.**
