# Configuration

octopus-mcp is configured entirely through environment variables. With Docker Compose, put them in `.env`. Copy [`example.env`](../example.env) to get started.

- [Octopus Energy](#octopus-energy)
- [How authentication works](#how-authentication-works)
- [What the assistant may do](#what-the-assistant-may-do)
- [MCP transport](#mcp-transport)
- [Docker secrets (`_FILE` variables)](#docker-secrets-_file-variables)
- [Choosing toolsets](#choosing-toolsets)
- [Example configurations](#example-configurations)

The server checks the configuration at startup. If something is wrong, it exits with a clear message such as `OCTOPUS_API_KEY is required`. If the container keeps restarting, check `docker compose logs`.

## Octopus Energy

| Variable | Default | Description |
|---|---|---|
| `OCTOPUS_API_KEY` | **required** | Your personal API key, from **[Personal details → API access](https://octopus.energy/dashboard/new/accounts/personal-details/api-access)** in your Octopus account. It starts `sk_live_`. |
| `OCTOPUS_ACCOUNT` | the only account the key can see | Your account number, like `A-1234ABCD`. If the key can see more than one account, set this, or pass `account_number` to the tools. |
| `OCTOPUS_TIMEOUT_MS` | `30000` | Timeout for each request to Octopus, in milliseconds. |
| `OCTOPUS_API_URL` | `https://api.octopus.energy` | The REST API, without `/v1`. You shouldn't need to change it. |
| `OCTOPUS_GRAPHQL_URL` | `$OCTOPUS_API_URL/v1/graphql/` | The Kraken GraphQL API. |
| `OCTOPUS_BACKEND_GRAPHQL_URL` | `https://api.backend.octopus.energy/v1/graphql/` | Octopus Energy's own GraphQL API, which serves Saving Sessions. |

## How authentication works

Octopus has two APIs, and octopus-mcp uses both with the one API key:

- **The REST API** (`https://api.octopus.energy/v1/`) has products, tariffs and prices, which are public, and your account and meter readings, which aren't. For those, octopus-mcp sends the API key as the username with HTTP Basic auth, as Octopus documents. Public requests don't send the key at all.
- **The Kraken GraphQL API** (`https://api.octopus.energy/v1/graphql/`) has everything else: balance and bills, Intelligent Octopus devices and dispatches, Octoplus and live Home Mini readings. octopus-mcp swaps the API key for a short-lived **Kraken token** (a JWT that lasts about an hour) with the `obtainKrakenToken` mutation, keeps it in memory, and renews it shortly before it expires, using the refresh token Octopus issues alongside it. If Kraken rejects a token, octopus-mcp gets a new one and retries once.

Nothing is written to disk, so the container needs no volume, and any number of servers or sessions can share the same key.

The API key and Kraken tokens never appear in tool output or logs. Error messages from Octopus are scrubbed of them before they're shown.

The key gives access to every account on your Octopus login. If you manage several (for example a home and a holiday let), set `OCTOPUS_ACCOUNT` to the one the assistant should use by default.

## What the assistant may do

| Variable | Default | Description |
|---|---|---|
| `OCTOPUS_ALLOW_WRITES` | `false` | Register the tools that change things: Intelligent Octopus charging target and ready-by time, bump charges, suspending or resuming smart charging, and joining Saving Sessions. Also lets the raw GraphQL tool run mutations. |
| `OCTOPUS_TOOLSETS` | `all` | Comma-separated list of [toolsets](#choosing-toolsets) to expose. |

Boolean variables accept `true`/`false`, `1`/`0`, `yes`/`no` and `on`/`off`. Anything else stops the server with an error.

The [tool reference](tools.md) labels every tool with the setting it needs:

| Label | Registered when |
|---|---|
| 🟢 read | Always |
| 🟠 write | `OCTOPUS_ALLOW_WRITES=true` |

Tools that aren't allowed are **not registered at all**. The assistant can't see them, so it can't call them or be talked into calling them.

> [!IMPORTANT]
> Octopus API keys aren't scoped: the same key can read your data and, through GraphQL, make the changes above. `OCTOPUS_ALLOW_WRITES` is enforced by octopus-mcp, not by Octopus. See [Security](security.md).

After changing these settings, restart the container (`docker compose up -d`) and reconnect or restart your MCP client so it fetches the new tool list.

## MCP transport

| Variable | Default | Description |
|---|---|---|
| `MCP_TRANSPORT` | `http` in the Docker image (`stdio` for the binary) | `http` for Streamable HTTP, or `stdio`. |
| `MCP_HTTP_HOST` | `127.0.0.1` (`0.0.0.0` in the Docker image) | Address to listen on (HTTP only). |
| `MCP_HTTP_PORT` | `8080` | Port to listen on (HTTP only). |
| `MCP_AUTH_TOKEN` | (required for http) | Every request to `/mcp` must send `Authorization: Bearer <token>`. The server refuses to start in http mode without it. Generate one with `openssl rand -hex 32`. |
| `MCP_ALLOW_UNAUTHENTICATED` | `false` | Start the http transport without `MCP_AUTH_TOKEN`. Only for a server that sits behind something else that authenticates every request. |
| `MCP_ALLOWED_HOSTS` | | Comma-separated allow-list of `Host` header values, such as `octopus-mcp.lan,octopus-mcp.lan:8080`. Requests with any other `Host` are rejected, which protects against DNS-rebinding attacks from web pages in your browser. |

The HTTP server exposes these endpoints:

| Path | Method | Purpose |
|---|---|---|
| `/mcp` | `POST` | The MCP endpoint (stateless Streamable HTTP, JSON responses). Request bodies are limited to 4 MiB. |
| `/health`, `/healthz` | `GET` | Liveness check. Returns `{"status":"ok"}` and doesn't contact Octopus. |

## Docker secrets (`_FILE` variables)

Every variable can also be read from a file: set `<NAME>_FILE` to the file's path. Leading and trailing whitespace is trimmed. This works with [Docker secrets](https://docs.docker.com/compose/how-tos/use-secrets/) and Kubernetes secret volumes:

```yaml
services:
  octopus-mcp:
    image: ghcr.io/mattoddie/octopus-mcp:latest
    environment:
      OCTOPUS_API_KEY_FILE: /run/secrets/octopus_api_key
      OCTOPUS_ACCOUNT: A-1234ABCD
      MCP_AUTH_TOKEN_FILE: /run/secrets/mcp_auth_token
    secrets: [octopus_api_key, mcp_auth_token]
    ports: ["8080:8080"]

secrets:
  octopus_api_key:
    file: ./secrets/octopus_api_key.txt
  mcp_auth_token:
    file: ./secrets/mcp_auth_token.txt
```

If both `NAME` and `NAME_FILE` are set, the file wins.

## Choosing toolsets

Every tool's name, description and argument schema is sent to the model, which uses up context. Exposing only what you need keeps the assistant faster and more focused:

| Toolset | Use it for |
|---|---|
| `account` | The account overview (meters, tariffs), balance, bills and transactions. **Almost always include it**: other tools use it to find your meters and tariff. |
| `consumption` | Smart meter readings with cost estimates, and live demand from a Home Mini |
| `tariffs` | Products, prices, Agile and other half-hourly rates, standing charges, cheapest times, regions. Works without account data. |
| `intelligent` | Intelligent Octopus cars and chargers: status, charging slots and, with writes, settings |
| `octoplus` | Octoplus points and Saving Sessions |
| `raw` | Any GraphQL query or REST path. Handy, but gives broad access. |

The [tool reference](tools.md) lists the tools in each toolset. Some suggested sets:

| Goal | `OCTOPUS_TOOLSETS` |
|---|---|
| Usage and bills | `account,consumption,tariffs` |
| Agile price watching | `account,tariffs` |
| EV charging on Intelligent Octopus Go | `account,tariffs,intelligent` |
| Everything (default) | `all` |

## Example configurations

**Read-only** (the safest setup):

```sh
OCTOPUS_API_KEY=sk_live_...
OCTOPUS_ACCOUNT=A-1234ABCD
MCP_AUTH_TOKEN=...
```

**EV owner on Intelligent Octopus Go.** The assistant can change charging settings and start bump charges:

```sh
OCTOPUS_API_KEY=sk_live_...
MCP_AUTH_TOKEN=...
OCTOPUS_ALLOW_WRITES=true
OCTOPUS_TOOLSETS=account,consumption,tariffs,intelligent
```

**Saving Sessions helper.** Can join sessions, but has no raw API access:

```sh
OCTOPUS_API_KEY=sk_live_...
MCP_AUTH_TOKEN=...
OCTOPUS_ALLOW_WRITES=true
OCTOPUS_TOOLSETS=account,consumption,tariffs,octoplus
```
