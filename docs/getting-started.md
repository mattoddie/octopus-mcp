# Getting started

This guide takes you from nothing to asking your first question about your Octopus Energy account. It takes about ten minutes.

- [What you need](#what-you-need)
- [1. Get your API key](#1-get-your-api-key)
- [2. Set up the server](#2-set-up-the-server)
- [3. Start the server](#3-start-the-server)
- [4. Connect your assistant](#4-connect-your-assistant)
- [5. Ask something](#5-ask-something)
- [Next steps](#next-steps)

## What you need

- **An Octopus Energy account** in the UK that you can log in to at [octopus.energy](https://octopus.energy). Consumption data needs a **smart meter** that sends half-hourly readings.
- **A machine that runs Docker.** This could be a NAS, a home server, a Raspberry Pi, a small VM or your laptop. The image supports both `amd64` and `arm64`. If you'd rather not use Docker, there's a single binary too. See [Running without Docker](deployment.md#running-without-docker).
- **An MCP client**, such as [Claude Code](https://claude.com/claude-code), Claude Desktop, VS Code, Cursor or Codex.

You don't need an account at all for the public tariff tools (products, prices and Agile rates), but the server still needs an API key to start.

> **Using Claude Desktop on a Mac or Windows PC?** You don't need Docker. Do step 1, then install the [extension bundle](clients.md#claude-desktop) from the releases page and enter your API key in its settings, instead of steps 2 to 4.

## 1. Get your API key

Octopus gives every customer a personal API key.

1. Log in to your Octopus account in a browser.
2. Open **[Personal details → API access](https://octopus.energy/dashboard/new/accounts/personal-details/api-access)**. You'll see your API key, which starts `sk_live_`, and your account number, which looks like `A-1234ABCD`.
3. Copy both.

> [!WARNING]
> The API key works like a password for your account's data. Keep it out of chats, screenshots and Git repositories. If it leaks, regenerate it on the same page, which makes the old key stop working.

## 2. Set up the server

Create a folder and download the compose file and the example settings:

```sh
mkdir octopus-mcp && cd octopus-mcp
curl -fsSLO https://raw.githubusercontent.com/mattoddie/octopus-mcp/main/compose.yaml
curl -fsSL https://raw.githubusercontent.com/mattoddie/octopus-mcp/main/example.env -o .env
```

Edit `.env` and set at least:

```sh
OCTOPUS_API_KEY=sk_live_...              # from step 1
OCTOPUS_ACCOUNT=A-1234ABCD               # optional if the key can only see one account
MCP_AUTH_TOKEN=a-long-random-string      # generate one with: openssl rand -hex 32
```

Leave everything else for now. The server starts **read-only**, which is the right way to begin.

## 3. Start the server

```sh
docker compose up -d
docker compose logs -f octopus-mcp
```

You should see lines like these:

```text
octopus-mcp 1.0.0: https://api.octopus.energy, account A-1234ABCD, writes disabled, toolsets 6, transport http
octopus-mcp listening on http://0.0.0.0:8080/mcp (bearer auth enabled)
```

Check that the API key works:

```sh
docker compose exec octopus-mcp /octopus-mcp status
```

```json
{
  "accounts": ["A-1234ABCD"],
  "api_key": "accepted",
  "default_account": "A-1234ABCD",
  "graphql": "https://api.octopus.energy/v1/graphql/",
  "rest": "https://api.octopus.energy/v1"
}
```

And that the server is reachable from another machine:

```sh
curl http://<docker-host>:8080/health
# {"status":"ok"}
```

> [!TIP]
> `/health` only shows that the server is running. It doesn't contact Octopus. `status` is the real check of your API key.

## 4. Connect your assistant

For **Claude Code**:

```sh
claude mcp add --transport http octopus http://<docker-host>:8080/mcp \
  --header "Authorization: Bearer <MCP_AUTH_TOKEN>"
```

Run `claude mcp list` to check that `octopus` shows as connected. For other clients, see [Connecting MCP clients](clients.md).

## 5. Ask something

Start a new conversation and try:

- *"What tariff am I on, and what's my electricity price right now?"*
- *"How much electricity did I use last week, and what did it cost?"*
- *"When's the cheapest two hours to run the dishwasher tonight?"*

The assistant calls tools like `octopus_get_account`, `octopus_get_consumption` and `octopus_find_cheapest_slots` and summarises the results. In Claude Code you can also run the built-in prompts as slash commands, for example `/mcp__octopus__energy_report`.

If something goes wrong, the tool's error message usually says what to fix. [Troubleshooting](troubleshooting.md) covers the common errors.

## Next steps

- **Let it change things.** With `OCTOPUS_ALLOW_WRITES=true` the assistant can change Intelligent Octopus charging settings, start a bump charge and join Saving Sessions. Read [Security](security.md) first.
- **Expose fewer tools** with `OCTOPUS_TOOLSETS` for a faster, more focused assistant. See [Configuration](configuration.md#keeping-the-tool-list-small).
- **Put it behind HTTPS** if it's reachable outside your LAN. See [Deployment](deployment.md#tls-with-a-reverse-proxy).
- **Learn what it can do** in [Using octopus-mcp](usage.md) and the [tool reference](tools.md).
