# Configuration

octopus-mcp is configured entirely through environment variables. With Docker Compose, put them in `.env`. Copy [`example.env`](../example.env) to get started.

- [Octopus Energy](#octopus-energy)
- [How authentication works](#how-authentication-works)
- [What the assistant may do](#what-the-assistant-may-do)
- [Audit log](#audit-log)
- [MCP transport](#mcp-transport)
- [Docker secrets (`_FILE` variables)](#docker-secrets-_file-variables)
- [Keeping the tool list small](#keeping-the-tool-list-small)
- [Example configurations](#example-configurations)

The server checks the configuration at startup. If something is wrong, it exits with a clear message such as `OCTOPUS_API_KEY is required`. If the container keeps restarting, check `docker compose logs`.

## Octopus Energy

| Variable | Default | Description |
|---|---|---|
| `OCTOPUS_API_KEY` | **required** | Your personal API key, from **[Personal details → API access](https://octopus.energy/dashboard/new/accounts/personal-details/api-access)** in your Octopus account. It starts `sk_live_`. |
| `OCTOPUS_ACCOUNT` | the only account the key can see | Your account number, like `A-1234ABCD`. If the key can see more than one account, set this, or pass `account_number` to the tools. |
| `OCTOPUS_TIMEOUT_MS` | `30000` | Timeout for each request to Octopus, in milliseconds. |
| `OCTOPUS_API_URL` | `https://api.octopus.energy` | The REST API, without `/v1`. You shouldn't need to change it. The API URLs must use `https://` (plain `http://` is only accepted for `localhost` and loopback addresses) and can't include a user name or password. |
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
| `OCTOPUS_ALLOW_WRITES` | `false` | Register the tools that change things: Intelligent Octopus charging target and ready-by time, bump charges, suspending or resuming smart charging, and joining Saving Sessions. Also lets the raw GraphQL tool run mutations. To keep a record of what changed, set [`OCTOPUS_AUDIT_LOG`](#audit-log). |
| `OCTOPUS_TOOLSETS` | `all` | Comma-separated list of [toolsets](#toolsets) to expose. See [Keeping the tool list small](#keeping-the-tool-list-small). |

Boolean variables accept `true`/`false`, `1`/`0`, `yes`/`no` and `on`/`off`. Anything else stops the server with an error.

A variable set to an empty value counts as unset and gets its default. So does one set to exactly `${user_config.<key>}`, which is how Claude Desktop passes an [extension setting](clients.md#claude-desktop) that was left empty.

The [tool reference](tools.md) labels every tool with the setting it needs:

| Label | Registered when |
|---|---|
| 🟢 read | Always |
| 🟠 write | `OCTOPUS_ALLOW_WRITES=true` |

Tools that aren't allowed are **not registered at all**. The assistant can't see them, so it can't call them or be talked into calling them.

> [!IMPORTANT]
> Octopus API keys aren't scoped: the same key can read your data and, through GraphQL, make the changes above. `OCTOPUS_ALLOW_WRITES` is enforced by octopus-mcp, not by Octopus. See [Security](security.md).

After changing these settings, restart the container (`docker compose up -d`) and reconnect or restart your MCP client so it fetches the new tool list.

## Audit log

| Variable | Default | Description |
|---|---|---|
| `OCTOPUS_AUDIT_LOG` | (off) | Record every call of a tool that changes things. A file path appends JSON Lines to that file, which is created with mode `0600` if it doesn't exist. `stderr` writes the lines to standard error, next to the server's other logs. |

Each line is one call of a 🟠 write tool, or of `octopus_graphql` with a mutation in it (only possible with writes on). Calls that failed or that the server refused are logged too. Reads aren't logged:

```json
{"time":"2026-10-21T09:14:03.120Z","tool":"octopus_set_charge_preferences","arguments":{"target_percent":90,"ready_by":"07:00"},"outcome":"ok"}
```

| Field | Meaning |
|---|---|
| `time` | When the call finished, in UTC (RFC 3339). |
| `tool` | The tool name, such as `octopus_bump_charge`. |
| `arguments` | The arguments the tool was called with. Any value whose name mentions a token, secret, API key, password or authorisation is `[redacted]`, at any depth (for example in GraphQL `variables`). |
| `outcome` | `ok` or `error`. |
| `error` | The error message, when `outcome` is `error`. |

Nothing from Octopus's responses is logged. The server checks at startup that it can write to the file, and refuses to start if the directory doesn't exist or isn't writable. A write that fails later doesn't stop the tool call: the server logs one warning to standard error and carries on.

The file is opened for each entry, so it can be rotated by moving it away; the next entry creates a new one.

The Docker image has a read-only file system, so mount a directory for the log:

```yaml
services:
  octopus-mcp:
    environment:
      OCTOPUS_AUDIT_LOG: /audit/octopus-mcp.jsonl
    volumes:
      - ./audit:/audit
```

Create the directory first and let UID 65532 write to it, for example `mkdir audit && sudo chown 65532 audit`. `OCTOPUS_AUDIT_LOG=stderr` needs no volume: the lines appear in `docker compose logs`.

## MCP transport

| Variable | Default | Description |
|---|---|---|
| `MCP_TRANSPORT` | `http` in the Docker image (`stdio` for the binary) | `http` for Streamable HTTP, or `stdio`. |
| `MCP_HTTP_HOST` | `127.0.0.1` (`0.0.0.0` in the Docker image) | Address to listen on (HTTP only). The `healthcheck` command connects to this address, or to `127.0.0.1` when it is `0.0.0.0` or `::`. |
| `MCP_HTTP_PORT` | `8080` | Port to listen on (HTTP only). |
| `MCP_AUTH_TOKEN` | (required for http) | Every request to `/mcp` must send `Authorization: Bearer <token>`. The server refuses to start in http mode without it, or if it is shorter than 16 characters. Generate one with `openssl rand -hex 32`. |
| `MCP_ALLOW_UNAUTHENTICATED` | `false` | Start the http transport without `MCP_AUTH_TOKEN`. Only for a server that sits behind something else that authenticates every request. It also needs `MCP_ALLOWED_HOSTS`, because without a token the allow-list is what stops web pages reaching the server through DNS rebinding, and the server logs a warning at startup. |
| `MCP_ALLOWED_HOSTS` | | Comma-separated allow-list of hosts, such as `octopus-mcp.lan,localhost`. Requests with any other `Host` header are rejected, and so are requests with an `Origin` header (which browsers send) that isn't on the list, including an empty or `null` one. This protects against DNS-rebinding and cross-site attacks from web pages in your browser. An entry without a port matches a `Host` on any port, but an `Origin` only on the default port (80 for `http://`, 443 for `https://`); to allow a page served on another port, list `host:port`, such as `localhost:3000`. |

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

## Keeping the tool list small

Most MCP clients send every tool's name, description and argument schema to the model with each request. With every toolset enabled that's about 23 KB for the 18 read-only tools, and 27 KB for all 22 tools with writes on. That uses up context you'd rather spend on your conversation and your energy data, and some clients warn about or cap the number of tools.

`OCTOPUS_TOOLSETS` is the way to keep the list small. Enable only the toolsets you need: the model sees fewer tools, picks the right one more easily, and can't reach the rest at all.

Clients with their own tool search, such as Claude Code, load tool definitions only when the model needs them, so they handle the full list fine. You don't need to narrow `OCTOPUS_TOOLSETS` for them, though it still limits what the model can reach.

### Toolsets

| Toolset | Use it for |
|---|---|
| `account` | The account overview (meters, tariffs), balance, bills and transactions. **Almost always include it**: other tools use it to find your meters and tariff. |
| `consumption` | Smart meter readings with cost estimates, and live demand from a Home Mini |
| `tariffs` | Products, prices, Agile and other half-hourly rates, standing charges, cheapest times, regions. Works without account data. |
| `intelligent` | Intelligent Octopus cars and chargers: status, charging slots and, with writes, settings |
| `octoplus` | Octoplus points and Saving Sessions |
| `raw` | Any GraphQL query or REST path. Handy, but gives broad access. |

The [tool reference](tools.md) lists the tools in each toolset.

### Ready-made combinations

The sizes are the length of the server's `tools/list` response. They change a little as tool descriptions change.

| Goal | `OCTOPUS_TOOLSETS` | Read-only | With writes |
|---|---|---|---|
| Everything (default) | `all` | 18 tools, 22.5 KB | 22 tools, 26.9 KB |
| Usage and bills | `account,consumption,tariffs` | 12 tools, 17.0 KB | 12 tools, 17.0 KB |
| Agile price watching | `account,tariffs` | 10 tools, 12.7 KB | 10 tools, 12.7 KB |
| EV charging on Intelligent Octopus Go | `account,tariffs,intelligent` | 12 tools, 14.8 KB | 15 tools, 18.3 KB |
| Usage, bills and EV charging | `account,consumption,tariffs,intelligent` | 14 tools, 19.0 KB | 17 tools, 22.5 KB |
| Saving Sessions helper | `account,consumption,tariffs,octoplus` | 14 tools, 18.5 KB | 15 tools, 19.4 KB |

Each toolset on its own:

| Toolset | Read-only | With writes |
|---|---|---|
| `account` | 4 tools, 3.7 KB | 4 tools, 3.7 KB |
| `consumption` | 2 tools, 4.3 KB | 2 tools, 4.3 KB |
| `tariffs` | 6 tools, 9.0 KB | 6 tools, 9.0 KB |
| `intelligent` | 2 tools, 2.1 KB | 5 tools, 5.6 KB |
| `octoplus` | 2 tools, 1.5 KB | 3 tools, 2.4 KB |
| `raw` | 2 tools, 1.9 KB | 2 tools, 2.0 KB |

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
